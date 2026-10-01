package proxy

import (
	"bufio"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"gbf-proxy/cache"
	"gbf-proxy/cert"
	"gbf-proxy/config"
	"gbf-proxy/telemetry"
)

func TestTelemetryToggleSkipsTraceWhenDisabled(t *testing.T) {
	cfgMgr := config.NewManager("")
	cfgMgr.Update(func(c *config.Config) {
		c.EnableAPITelemetry = false
	})
	mgr := cache.NewManager(t.TempDir(), 16)
	defer mgr.Close()
	stats := telemetry.NewStats()
	defer stats.Close()

	srv := NewProxyServer(cfgMgr, &cert.Manager{}, mgr, stats)
	req, err := http.NewRequest(http.MethodGet, "https://example.com/", nil)
	if err != nil {
		t.Fatal(err)
	}
	if got := srv.tracedRequest(req); got != req {
		t.Fatal("disabled telemetry should leave request untouched")
	}

	cfgMgr.Update(func(c *config.Config) { c.EnableAPITelemetry = true })
	got := srv.tracedRequest(req)
	if got == req {
		t.Fatal("enabled telemetry should attach a trace request clone")
	}
}

// TestTelemetryDomainsPassthroughNotBlocked verifies that former telemetry domains
// (e.g. smbeat.jp, datadoghq.com, smrtbeat.com, etc.) and official telemetry endpoints
// are no longer blocked with 403 Forbidden, and instead pass through standard proxy channels
// (plain HTTP forwarding & CONNECT tunnel).
func TestTelemetryDomainsPassthroughNotBlocked(t *testing.T) {
	tempDir := t.TempDir()
	cfgPath := filepath.Join(tempDir, "config.json")
	cfgMgr := config.NewManager(cfgPath)
	certMgr, err := cert.NewManager(filepath.Join(tempDir, "certs"))
	if err != nil {
		t.Fatalf("failed to create cert manager: %v", err)
	}
	cacheMgr := cache.NewManager(tempDir, 16)
	defer cacheMgr.Close()
	stats := telemetry.NewStats()
	defer stats.Close()

	srv := NewProxyServer(cfgMgr, certMgr, cacheMgr, stats)

	testDomains := []string{
		"smbeat.jp",
		"smrtbeat.com",
		"rcv.a-i-ad.com",
		"datadoghq-browser-agent.com",
		"datadoghq.com",
		"spdmg-backend.i-mobile.co.jp",
		"creativecdn.com",
	}

	// 1. Plain HTTP: must NOT return 403 Forbidden, must forward via apiClient
	for _, domain := range testDomains {
		t.Run("PlainHTTP/"+domain, func(t *testing.T) {
			var forwardedHost string
			srv.apiClient.Store(&http.Client{
				Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
					forwardedHost = r.Host
					return &http.Response{
						StatusCode: http.StatusOK,
						Header:     make(http.Header),
						Body:       io.NopCloser(strings.NewReader("upstream-ok")),
						Proto:      "HTTP/1.1",
					}, nil
				}),
			})

			conn := &dummyConn{}
			req := &http.Request{
				Method: http.MethodGet,
				Host:   domain,
				URL:    &url.URL{Scheme: "http", Host: domain, Path: "/collect"},
			}
			keepAlive := srv.handlePlainHTTP(conn, req)
			out := conn.writeBuf.String()

			if strings.Contains(out, "403 Forbidden") {
				t.Errorf("domain %s must NOT be blocked with 403 Forbidden in handlePlainHTTP", domain)
			}
			if !strings.Contains(out, "200 OK") || !strings.Contains(out, "upstream-ok") {
				t.Errorf("expected 200 OK forwarded response for %s, got: %s", domain, out)
			}
			if forwardedHost != domain {
				t.Errorf("expected apiClient to receive request for %s, got %s", domain, forwardedHost)
			}
			if !keepAlive {
				t.Errorf("expected keepAlive=true for plain proxy request to %s", domain)
			}
		})
	}

	// Also verify official telemetry paths on GBF domains are forwarded rather than blocked
	officialTargets := []struct {
		host string
		path string
	}{
		{"sp.mbga.jp", "/telemetry"},
		{"log.granbluefantasy.jp", "/log/collect"},
	}
	for _, ot := range officialTargets {
		t.Run("PlainHTTP/Official/"+ot.host, func(t *testing.T) {
			var forwardedHost string
			srv.apiClient.Store(&http.Client{
				Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
					forwardedHost = r.Host
					return &http.Response{
						StatusCode: http.StatusOK,
						Header:     make(http.Header),
						Body:       io.NopCloser(strings.NewReader("upstream-ok")),
						Proto:      "HTTP/1.1",
					}, nil
				}),
			})

			conn := &dummyConn{}
			req := &http.Request{
				Method: http.MethodGet,
				Host:   ot.host,
				URL:    &url.URL{Scheme: "http", Host: ot.host, Path: ot.path},
			}
			keepAlive := srv.handlePlainHTTP(conn, req)
			out := conn.writeBuf.String()

			if strings.Contains(out, "403 Forbidden") {
				t.Errorf("target %s%s must NOT be blocked with 403 Forbidden in handlePlainHTTP", ot.host, ot.path)
			}
			if !strings.Contains(out, "200 OK") || !strings.Contains(out, "upstream-ok") {
				t.Errorf("expected 200 OK forwarded response for %s%s, got: %s", ot.host, ot.path, out)
			}
			if forwardedHost != ot.host {
				t.Errorf("expected apiClient to receive request for %s, got %s", ot.host, forwardedHost)
			}
			if !keepAlive {
				t.Errorf("expected keepAlive=true for request to %s", ot.host)
			}
		})
	}

	// 2. CONNECT tunnel: must NOT return 403 Forbidden, must route to passthrough tunnel
	for _, domain := range testDomains {
		t.Run("CONNECT/"+domain, func(t *testing.T) {
			target := domain + ":443"

			upstreamLn, lErr := net.Listen("tcp", "127.0.0.1:0")
			if lErr != nil {
				t.Fatalf("failed to start mock upstream: %v", lErr)
			}
			defer upstreamLn.Close()

			connectReceived := make(chan string, 1)
			go func(ln net.Listener) {
				c, aErr := ln.Accept()
				if aErr != nil {
					return
				}
				defer c.Close()
				br := bufio.NewReader(c)
				upReq, rErr := http.ReadRequest(br)
				if rErr != nil {
					return
				}
				if upReq.Method == http.MethodConnect {
					connectReceived <- upReq.Host
					_, _ = c.Write([]byte("HTTP/1.1 200 Connection Established\r\n\r\n"))
					_, _ = io.Copy(io.Discard, c)
				}
			}(upstreamLn)

			cfgMgr.Update(func(c *config.Config) {
				c.UpstreamProxy = "http://" + upstreamLn.Addr().String()
			})

			clientSide, serverSide := net.Pipe()
			br := bufio.NewReader(serverSide)

			connectDone := make(chan struct{})
			go func() {
				defer close(connectDone)
				defer serverSide.Close()
				req, rErr := http.ReadRequest(br)
				if rErr != nil {
					return
				}
				srv.handleConnect(serverSide, br, req)
			}()

			// Write CONNECT request from client side
			connectReq := fmt.Sprintf("CONNECT %s HTTP/1.1\r\nHost: %s\r\n\r\n", target, target)
			_, _ = clientSide.Write([]byte(connectReq))

			clientBr := bufio.NewReader(clientSide)
			respLine, rErr := clientBr.ReadString('\n')

			if strings.Contains(respLine, "403") {
				t.Errorf("domain %s must NOT be blocked with 403 Forbidden in handleConnect", domain)
			}
			if !strings.Contains(respLine, "200") {
				t.Errorf("expected 200 Connection Established for %s, got: %s (err: %v)", domain, respLine, rErr)
			}

			select {
			case gotTarget := <-connectReceived:
				if gotTarget != target {
					t.Errorf("expected mock upstream to receive CONNECT for %s, got %s", target, gotTarget)
				}
			case <-time.After(2 * time.Second):
				t.Errorf("timed out waiting for upstream to receive CONNECT for %s", target)
			}

			_ = clientSide.Close()
			<-connectDone
		})
	}
}
