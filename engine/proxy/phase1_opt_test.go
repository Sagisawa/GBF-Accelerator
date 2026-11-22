package proxy

import (
	"bufio"
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"gbf-proxy/cache"
	"gbf-proxy/cert"
	"gbf-proxy/config"
	"gbf-proxy/telemetry"
)

func TestDynamicAPI_StreamThroughEmitsFirstChunkBeforeUpstreamFinishes(t *testing.T) {
	tempDir := t.TempDir()
	cfgMgr := config.NewManager(filepath.Join(tempDir, "config.json"))
	certMgr, err := cert.NewManager(filepath.Join(tempDir, "certs"))
	if err != nil {
		t.Fatalf("cert.NewManager: %v", err)
	}
	cacheMgr := cache.NewManager(tempDir, 16)
	defer cacheMgr.Close()
	stats := telemetry.NewStats()
	defer stats.Close()

	srv := NewProxyServer(cfgMgr, certMgr, cacheMgr, stats)
	defer func() {
		srv.Stop()
		srv.WaitPrefetchWorkers(2 * time.Second)
	}()

	releaseSecond := make(chan struct{})
	pr, pw := io.Pipe()

	srv.apiClient.Store(&http.Client{
		Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
			h := http.Header{}
			h.Set("Content-Type", "application/json")
			h.Add("Set-Cookie", "a=1; Path=/")
			h.Add("Set-Cookie", "b=2; Path=/")
			h.Set("X-Proxy-Cache", "MUST-STRIP")
			return &http.Response{
				StatusCode:    http.StatusOK,
				Proto:         "HTTP/1.1",
				Header:        h,
				ContentLength: -1,
				Body:          pr,
			}, nil
		}),
	})

	go func() {
		defer pw.Close()
		_, _ = pw.Write([]byte(`{"chunk":1}`))
		<-releaseSecond
		_, _ = pw.Write([]byte(`{"chunk":2}`))
	}()

	clientSide, serverSide := net.Pipe()
	defer clientSide.Close()
	defer serverSide.Close()

	req := &http.Request{
		Method: http.MethodGet,
		Host:   "game.granbluefantasy.jp",
		URL:    &url.URL{Path: "/rest/party/deck_info"},
		Header: http.Header{},
	}

	done := make(chan bool, 1)
	go func() {
		done <- srv.handleDynamicAPI(serverSide, req, "game.granbluefantasy.jp")
	}()

	// Client must receive headers AND chunk 1 BEFORE releaseSecond is closed!
	_ = clientSide.SetReadDeadline(time.Now().Add(2 * time.Second))
	br := bufio.NewReader(clientSide)
	resp, err := http.ReadResponse(br, req)
	if err != nil {
		t.Fatalf("http.ReadResponse failed before upstream EOF: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 OK, got %d", resp.StatusCode)
	}
	if gotCookies := resp.Header.Values("Set-Cookie"); len(gotCookies) != 2 {
		t.Fatalf("expected 2 distinct Set-Cookie headers, got %v", gotCookies)
	}
	if resp.Header.Get("X-Proxy-Cache") != "" {
		t.Fatal("proxy fingerprint header X-Proxy-Cache must be stripped")
	}

	firstBuf := make([]byte, len(`{"chunk":1}`))
	if _, err := io.ReadFull(resp.Body, firstBuf); err != nil {
		t.Fatalf("failed reading first streamed chunk before upstream close: %v", err)
	}
	if string(firstBuf) != `{"chunk":1}` {
		t.Fatalf("unexpected first chunk: %q", string(firstBuf))
	}

	// Now allow upstream to finish chunk 2
	close(releaseSecond)
	rest, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("failed reading remainder of streamed body: %v", err)
	}
	if string(rest) != `{"chunk":2}` {
		t.Fatalf("unexpected second chunk: %q", string(rest))
	}

	if keepAlive := <-done; !keepAlive {
		t.Fatal("expected keepAlive=true after clean streamed dynamic response")
	}
}

func TestDynamicAPI_StreamThroughCapturesGameDataForPrefetch(t *testing.T) {
	tempDir := t.TempDir()
	cfgMgr := config.NewManager(filepath.Join(tempDir, "config.json"))
	cfgMgr.Update(func(c *config.Config) {
		c.EnablePrefetch = true
	})
	certMgr, err := cert.NewManager(filepath.Join(tempDir, "certs"))
	if err != nil {
		t.Fatalf("cert.NewManager: %v", err)
	}
	cacheMgr := cache.NewManager(tempDir, 16)
	defer cacheMgr.Close()
	stats := telemetry.NewStats()
	defer stats.Close()

	srv := NewProxyServer(cfgMgr, certMgr, cacheMgr, stats)
	defer func() {
		srv.Stop()
		srv.WaitPrefetchWorkers(2 * time.Second)
	}()

	var prefetchedManifest atomic.Bool
	srv.assetClient.Store(&http.Client{
		Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
			if strings.Contains(r.URL.Path, "manifest/stream_hero.js") {
				prefetchedManifest.Store(true)
			}
			body := "console.log('ok');"
			return &http.Response{
				StatusCode:    http.StatusOK,
				Proto:         "HTTP/2.0",
				Header:        http.Header{"Content-Type": []string{"application/javascript"}},
				ContentLength: int64(len(body)),
				Body:          io.NopCloser(strings.NewReader(body)),
			}, nil
		}),
	})

	payload := `{"player":{"param":[{"cjs":"stream_hero"}]}}`
	srv.apiClient.Store(&http.Client{
		Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
			return &http.Response{
				StatusCode:    http.StatusOK,
				Proto:         "HTTP/1.1",
				Header:        http.Header{"Content-Type": []string{"application/json"}},
				ContentLength: int64(len(payload)),
				Body:          io.NopCloser(strings.NewReader(payload)),
			}, nil
		}),
	})

	req := &http.Request{
		Method: http.MethodPost,
		Host:   "game.granbluefantasy.jp",
		URL:    &url.URL{Path: "/rest/multiraid/start.json"},
		Header: http.Header{
			"X-Version": []string{"1795000001"},
			"Cookie":    []string{"language_type=1"},
		},
		Body: io.NopCloser(strings.NewReader(`{}`)),
	}

	var out strings.Builder
	if !srv.handleDynamicAPI(&out, req, "game.granbluefantasy.jp") {
		t.Fatal("expected keepAlive=true")
	}
	if !strings.Contains(out.String(), payload) {
		t.Fatalf("expected streamed body to contain payload, got: %s", out.String())
	}

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if prefetchedManifest.Load() {
			return
		}
		time.Sleep(15 * time.Millisecond)
	}
	t.Fatal("expected streamed /rest/multiraid/start.json to tee body into Game-Data prefetch")
}

func TestDNSCache_StaleWhileRevalidateAndSingleFlight(t *testing.T) {
	c := newDNSCacheWithSWR(50*time.Millisecond, 5*time.Second, 8)
	var calls atomic.Int32
	var slowRefresh atomic.Bool
	refreshDone := make(chan struct{}, 4)

	resolveFn := func(ctx context.Context, host string) ([]net.IP, error) {
		n := calls.Add(1)
		if slowRefresh.Load() {
			time.Sleep(120 * time.Millisecond)
			refreshDone <- struct{}{}
			return []net.IP{net.ParseIP("127.0.0.2")}, nil
		}
		time.Sleep(30 * time.Millisecond)
		if n == 1 {
			return []net.IP{net.ParseIP("127.0.0.1")}, nil
		}
		return []net.IP{net.ParseIP("127.0.0.2")}, nil
	}

	// 1. Concurrent cold lookups must coalesce to 1 resolver call via SingleFlight
	var wg sync.WaitGroup
	for i := 0; i < 6; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			ips, err := c.lookup(context.Background(), "akamai.example.net", resolveFn)
			if err != nil || len(ips) == 0 {
				t.Errorf("concurrent lookup failed: %v", err)
			}
		}()
	}
	wg.Wait()
	if calls.Load() != 1 {
		t.Fatalf("expected SingleFlight to coalesce 6 concurrent lookups into 1 call, got %d", calls.Load())
	}

	// 2. Wait past softTTL (50ms) but well within hardTTL (5s) -> must return stale IP immediately!
	time.Sleep(70 * time.Millisecond)
	slowRefresh.Store(true)
	start := time.Now()
	ips, err := c.lookup(context.Background(), "akamai.example.net", resolveFn)
	elapsed := time.Since(start)
	if err != nil || len(ips) != 1 || !ips[0].Equal(net.ParseIP("127.0.0.1")) {
		t.Fatalf("expected immediate stale IP 127.0.0.1, got ips=%v err=%v", ips, err)
	}
	if elapsed > 60*time.Millisecond {
		t.Fatalf("SWR lookup blocked for %v instead of returning stale entry immediately", elapsed)
	}

	// Wait for background refresh to finish and verify updated IP is now served
	select {
	case <-refreshDone:
	case <-time.After(1 * time.Second):
		t.Fatal("background SWR refresh did not execute")
	}
	time.Sleep(15 * time.Millisecond)
	ipsAfter, err := c.lookup(context.Background(), "akamai.example.net", resolveFn)
	if err != nil || len(ipsAfter) != 1 || !ipsAfter[0].Equal(net.ParseIP("127.0.0.2")) {
		t.Fatalf("expected refreshed IP 127.0.0.2 after SWR, got ips=%v err=%v", ipsAfter, err)
	}
}

func TestDNSCache_FailedIPDemotionAndRecovery(t *testing.T) {
	c := newDNSCacheWithSWR(1*time.Minute, 5*time.Minute, 8)
	ip1 := net.ParseIP("198.51.100.1")
	ip2 := net.ParseIP("198.51.100.2")

	resolveFn := func(ctx context.Context, host string) ([]net.IP, error) {
		return []net.IP{ip1, ip2}, nil
	}

	ips, err := c.lookup(context.Background(), "multi.example.net", resolveFn)
	if err != nil || !ips[0].Equal(ip1) {
		t.Fatalf("expected ip1 first initially, got %v", ips)
	}

	c.recordDialResult(ip1, errors.New("dial timeout"))
	ipsRanked, err := c.lookup(context.Background(), "multi.example.net", resolveFn)
	if err != nil || !ipsRanked[0].Equal(ip2) || !ipsRanked[1].Equal(ip1) {
		t.Fatalf("expected failed ip1 demoted behind ip2, got %v", ipsRanked)
	}

	c.recordDialResult(ip1, nil)
	ipsRecovered, err := c.lookup(context.Background(), "multi.example.net", resolveFn)
	if err != nil || !ipsRecovered[0].Equal(ip1) {
		t.Fatalf("expected recovered ip1 restored to original order, got %v", ipsRecovered)
	}
}

func TestUpstreamTransportTuning_P0AndHTTP2PingConfigured(t *testing.T) {
	cfg := config.DefaultConfig()
	pair := buildUpstreamClients(&cfg, "")

	apiTr := pair.api.Transport.(*http.Transport)
	assetTr := pair.asset.Transport.(*http.Transport)

	if apiTr.ForceAttemptHTTP2 {
		t.Fatal("P0 violation: apiTr must keep ForceAttemptHTTP2=false (HTTP/1.1 Keep-Alive baseline)")
	}
	if apiTr.ReadBufferSize != 32*1024 || apiTr.WriteBufferSize != 32*1024 {
		t.Fatalf("expected 32KiB buffers on apiTr, got read=%d write=%d", apiTr.ReadBufferSize, apiTr.WriteBufferSize)
	}
	if assetTr.ReadBufferSize != 32*1024 || assetTr.WriteBufferSize != 32*1024 {
		t.Fatalf("expected 32KiB buffers on assetTr, got read=%d write=%d", assetTr.ReadBufferSize, assetTr.WriteBufferSize)
	}
	if assetTr.HTTP2 == nil || assetTr.HTTP2.SendPingTimeout != 15*time.Second || assetTr.HTTP2.PingTimeout != 5*time.Second {
		t.Fatalf("expected assetTr HTTP/2 PING keepalive (15s/5s), got %+v", assetTr.HTTP2)
	}

	// Ensure unused httptest import isn't needed; quick smoke check via local server
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	defer ts.Close()
	resp, err := pair.api.Get(ts.URL)
	if err != nil {
		t.Fatalf("api.Get smoke failed: %v", err)
	}
	_ = resp.Body.Close()
}
