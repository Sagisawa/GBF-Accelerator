package proxy

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"gbf-proxy/cache"
	"gbf-proxy/cert"
	"gbf-proxy/config"
	"gbf-proxy/telemetry"
)

func setupH2TestProxy(t testing.TB, handler http.Handler) (*ProxyServer, *cert.Manager, *httptest.Server) {
	t.Helper()
	tempDir := t.TempDir()
	cfgMgr := config.NewManager(filepath.Join(tempDir, "config.json"))
	_ = cfgMgr.Update(func(c *config.Config) {
		c.ListenHost = "127.0.0.1"
		c.ListenPort = 0
		c.EnablePrefetch = false
	})
	certMgr, err := cert.NewManager(filepath.Join(tempDir, "certs"))
	if err != nil {
		t.Fatalf("cert.NewManager failed: %v", err)
	}
	cacheMgr := cache.NewManager(tempDir, 32)
	t.Cleanup(func() { cacheMgr.Close() })
	stats := telemetry.NewStats()

	srv := NewProxyServer(cfgMgr, certMgr, cacheMgr, stats)

	ts := httptest.NewTLSServer(handler)
	t.Cleanup(ts.Close)

	tsAddr := ts.Listener.Addr().String()
	dialUpstream := func(ctx context.Context, network, addr string) (net.Conn, error) {
		var d net.Dialer
		return d.DialContext(ctx, "tcp", tsAddr)
	}
	upClient := &http.Client{
		Transport: &http.Transport{
			DialContext:       dialUpstream,
			TLSClientConfig:   &tls.Config{InsecureSkipVerify: true},
			ForceAttemptHTTP2: false,
		},
		Timeout: 10 * time.Second,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
	srv.apiClient.Store(upClient)
	srv.assetClient.Store(upClient)

	if err := srv.Start(); err != nil {
		t.Fatalf("srv.Start failed: %v", err)
	}
	t.Cleanup(func() { srv.Stop() })

	return srv, certMgr, ts
}

func newDownstreamProxyClient(t testing.TB, proxyAddr string, certMgr *cert.Manager, forceH2 bool) *http.Client {
	t.Helper()
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(certMgr.GetCAPEM()) {
		t.Fatal("failed to append CA PEM")
	}
	proxyURL, err := url.Parse("http://" + proxyAddr)
	if err != nil {
		t.Fatalf("invalid proxy addr: %v", err)
	}
	tr := &http.Transport{
		Proxy:               http.ProxyURL(proxyURL),
		TLSClientConfig:     &tls.Config{RootCAs: pool, ClientSessionCache: tls.NewLRUClientSessionCache(16)},
		ForceAttemptHTTP2:   forceH2,
		MaxIdleConns:        64,
		MaxIdleConnsPerHost: 32,
	}
	if !forceH2 {
		tr.TLSNextProto = make(map[string]func(authority string, c *tls.Conn) http.RoundTripper)
	}
	t.Cleanup(tr.CloseIdleConnections)
	return &http.Client{Transport: tr, Timeout: 10 * time.Second}
}

func TestDownstreamHTTP2_MultiplexedAssetsAndDynamicAPI(t *testing.T) {
	largeStreamPayload := append([]byte("\x89PNG\r\n\x1a\n\x00\x00\x00\rIHDR\x00\x00\x00\x01\x00\x00\x00\x01\x08\x06\x00\x00\x00\x1f\x15\xc4\x89"), bytes.Repeat([]byte("A"), 32*1024)...)
	smallPNG := []byte("\x89PNG\r\n\x1a\n\x00\x00\x00\rIHDR\x00\x00\x00\x01\x00\x00\x00\x01\x08\x06\x00\x00\x00\x1f\x15\xc4\x89small")
	var postCount atomic.Int32
	var smallHits atomic.Int32

	srv, certMgr, _ := setupH2TestProxy(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/assets/1795000000/img/small.png":
			smallHits.Add(1)
			w.Header().Set("Content-Type", "image/png")
			w.Header().Set("ETag", `"small-etag-1"`)
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write(smallPNG)
		case r.URL.Path == "/assets/1795000000/img/large.png":
			w.Header().Set("Content-Type", "image/png")
			w.Header().Set("ETag", `"large-etag-1"`)
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write(largeStreamPayload)
		case r.URL.Path == "/rest/battle/attack":
			postCount.Add(1)
			w.Header().Add("Set-Cookie", "sid=alpha; Path=/; HttpOnly")
			w.Header().Add("Set-Cookie", "csrf=beta; Path=/; Secure")
			w.Header().Set("X-Proxy-Cache", "SHOULD-BE-STRIPPED")
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"ok":true}`))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))

	client := newDownstreamProxyClient(t, srv.ListenerAddr(), certMgr, true)

	// 1. Cold miss + H2 negotiation check
	resp, err := client.Get("https://prd-game-a-granbluefantasy.akamaized.net/assets/1795000000/img/small.png")
	if err != nil {
		t.Fatalf("H2 GET small.png failed: %v", err)
	}
	body, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if resp.Proto != "HTTP/2.0" {
		t.Fatalf("expected HTTP/2.0 response from proxy, got %s", resp.Proto)
	}
	if resp.StatusCode != http.StatusOK || !bytes.Equal(body, smallPNG) {
		t.Fatalf("unexpected small.png response: status=%d len=%d", resp.StatusCode, len(body))
	}

	// 2. Concurrent burst over multiplexed HTTP/2 RAM cache fast path
	const burst = 24
	var wg sync.WaitGroup
	errCh := make(chan error, burst)
	for i := 0; i < burst; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			r, gErr := client.Get("https://prd-game-a-granbluefantasy.akamaized.net/assets/1795000000/img/small.png")
			if gErr != nil {
				errCh <- gErr
				return
			}
			b, _ := io.ReadAll(r.Body)
			_ = r.Body.Close()
			if r.Proto != "HTTP/2.0" || r.StatusCode != http.StatusOK || !bytes.Equal(b, smallPNG) {
				errCh <- fmt.Errorf("unexpected H2 burst resp: proto=%s status=%d len=%d", r.Proto, r.StatusCode, len(b))
			}
		}()
	}
	wg.Wait()
	close(errCh)
	for e := range errCh {
		t.Fatal(e)
	}
	if smallHits.Load() != 1 {
		t.Fatalf("expected exactly 1 upstream hit for small.png, got %d", smallHits.Load())
	}

	// 3. Conditional 304 over HTTP/2
	condReq, _ := http.NewRequest(http.MethodGet, "https://prd-game-a-granbluefantasy.akamaized.net/assets/1795000000/img/small.png", nil)
	condReq.Header.Set("If-None-Match", `"small-etag-1"`)
	condResp, err := client.Do(condReq)
	if err != nil {
		t.Fatalf("H2 conditional GET failed: %v", err)
	}
	condBody, _ := io.ReadAll(condResp.Body)
	_ = condResp.Body.Close()
	if condResp.Proto != "HTTP/2.0" || condResp.StatusCode != http.StatusNotModified || len(condBody) != 0 {
		t.Fatalf("expected H2 304 empty body, got proto=%s status=%d len=%d", condResp.Proto, condResp.StatusCode, len(condBody))
	}

	// 4. Range 206 over HTTP/2
	rangeReq, _ := http.NewRequest(http.MethodGet, "https://prd-game-a-granbluefantasy.akamaized.net/assets/1795000000/img/small.png", nil)
	rangeReq.Header.Set("Range", "bytes=0-7")
	rangeResp, err := client.Do(rangeReq)
	if err != nil {
		t.Fatalf("H2 range GET failed: %v", err)
	}
	rangeBody, _ := io.ReadAll(rangeResp.Body)
	_ = rangeResp.Body.Close()
	if rangeResp.Proto != "HTTP/2.0" || rangeResp.StatusCode != http.StatusPartialContent || !bytes.Equal(rangeBody, smallPNG[:8]) {
		t.Fatalf("unexpected H2 range response: proto=%s status=%d body=%x", rangeResp.Proto, rangeResp.StatusCode, rangeBody)
	}

	// 5. Stream-Through >16KB asset over HTTP/2
	largeResp, err := client.Get("https://prd-game-a-granbluefantasy.akamaized.net/assets/1795000000/img/large.png")
	if err != nil {
		t.Fatalf("H2 stream-through GET failed: %v", err)
	}
	largeBody, _ := io.ReadAll(largeResp.Body)
	_ = largeResp.Body.Close()
	if largeResp.Proto != "HTTP/2.0" || largeResp.StatusCode != http.StatusOK || !bytes.Equal(largeBody, largeStreamPayload) {
		t.Fatalf("unexpected H2 stream-through resp: proto=%s status=%d len=%d", largeResp.Proto, largeResp.StatusCode, len(largeBody))
	}

	// 6. Dynamic API POST over HTTP/2: multi-line Set-Cookie & zero proxy header pollution
	apiResp, err := client.Post("https://game.granbluefantasy.jp/rest/battle/attack", "application/json", bytes.NewBufferString(`{"turn":1}`))
	if err != nil {
		t.Fatalf("H2 POST dynamic API failed: %v", err)
	}
	apiBody, _ := io.ReadAll(apiResp.Body)
	_ = apiResp.Body.Close()
	if apiResp.Proto != "HTTP/2.0" || apiResp.StatusCode != http.StatusOK || string(apiBody) != `{"ok":true}` {
		t.Fatalf("unexpected H2 API resp: proto=%s status=%d body=%s", apiResp.Proto, apiResp.StatusCode, string(apiBody))
	}
	if cookies := apiResp.Header.Values("Set-Cookie"); len(cookies) != 2 {
		t.Fatalf("expected 2 separate Set-Cookie headers on H2 API resp, got %v", cookies)
	}
	if apiResp.Header.Get("X-Proxy-Cache") != "" {
		t.Fatalf("expected X-Proxy-Cache to be stripped on H2 API resp")
	}
	if postCount.Load() != 1 {
		t.Fatalf("expected strictly 1 upstream POST, got %d", postCount.Load())
	}
}

func BenchmarkDownstreamH1vsH2_RAMCacheBurst(b *testing.B) {
	payload := append([]byte("\x89PNG\r\n\x1a\n\x00\x00\x00\rIHDR\x00\x00\x00\x01\x00\x00\x00\x01\x08\x06\x00\x00\x00\x1f\x15\xc4\x89"), bytes.Repeat([]byte("B"), 16*1024)...)
	srv, certMgr, _ := setupH2TestProxy(b, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "image/png")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(payload)
	}))

	warmURL := "https://prd-game-a-granbluefantasy.akamaized.net/assets/1795000000/img/bench.png"
	warmClient := newDownstreamProxyClient(b, srv.ListenerAddr(), certMgr, true)
	r, err := warmClient.Get(warmURL)
	if err != nil {
		b.Fatalf("warmup failed: %v", err)
	}
	_, _ = io.Copy(io.Discard, r.Body)
	_ = r.Body.Close()

	for _, forceH2 := range []bool{false, true} {
		name := "HTTP1.1"
		if forceH2 {
			name = "HTTP2"
		}
		b.Run(name, func(b *testing.B) {
			cli := newDownstreamProxyClient(b, srv.ListenerAddr(), certMgr, forceH2)
			b.ResetTimer()
			b.RunParallel(func(pb *testing.PB) {
				for pb.Next() {
					resp, err := cli.Get(warmURL)
					if err != nil {
						b.Error(err)
						return
					}
					_, _ = io.Copy(io.Discard, resp.Body)
					_ = resp.Body.Close()
				}
			})
		})
	}
}
