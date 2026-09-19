package proxy

import (
	"bytes"
	"errors"
	"io"
	"net"
	"net/http"
	"net/url"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"gbf-proxy/cache"
	"gbf-proxy/cert"
	"gbf-proxy/config"
	"gbf-proxy/telemetry"
)

type trackingReadCloser struct {
	io.Reader
	closed atomic.Bool
}

func (t *trackingReadCloser) Read(p []byte) (int, error) {
	return t.Reader.Read(p)
}

func (t *trackingReadCloser) Close() error {
	t.closed.Store(true)
	return nil
}

type roundTripFunc func(req *http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

// TestBug13_HandleStaticAsset_ResponseBodyClosedOnNon200 verifies that when upstream returns
// non-200 status codes (e.g. 404, 500, 502, 503), resp.Body is explicitly closed to prevent stream/socket leaks.
func TestBug13_HandleStaticAsset_ResponseBodyClosedOnNon200(t *testing.T) {
	statuses := []int{
		http.StatusNotFound,
		http.StatusInternalServerError,
		http.StatusBadGateway,
		http.StatusServiceUnavailable,
	}

	for _, statusCode := range statuses {
		t.Run(http.StatusText(statusCode), func(t *testing.T) {
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

			srv := NewProxyServer(cfgMgr, certMgr, cacheMgr, stats)

			tracker := &trackingReadCloser{
				Reader: strings.NewReader("upstream error response"),
			}

			srv.assetClient = &http.Client{
				Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
					return &http.Response{
						StatusCode: statusCode,
						Header:     make(http.Header),
						Body:       tracker,
						Proto:      "HTTP/1.1",
					}, nil
				}),
			}

			req, _ := http.NewRequest(http.MethodGet, "https://game.granbluefantasy.jp/assets/nonexistent.png", nil)
			var buf bytes.Buffer
			srv.handleStaticAsset(&buf, req, "game.granbluefantasy.jp")

			if !tracker.closed.Load() {
				t.Errorf("expected upstream response body to be closed on non-200 status %d", statusCode)
			}
		})
	}

	t.Run("NilResponseAndErrorHandledGracefully", func(t *testing.T) {
		tempDir := t.TempDir()
		cfgPath := filepath.Join(tempDir, "config.json")
		cfgMgr := config.NewManager(cfgPath)
		certMgr, _ := cert.NewManager(filepath.Join(tempDir, "certs"))
		cacheMgr := cache.NewManager(tempDir, 16)
		defer cacheMgr.Close()
		stats := telemetry.NewStats()
		srv := NewProxyServer(cfgMgr, certMgr, cacheMgr, stats)

		srv.assetClient = &http.Client{
			Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
				return nil, errors.New("simulated network failure")
			}),
		}

		req, _ := http.NewRequest(http.MethodGet, "https://game.granbluefantasy.jp/assets/failed.png", nil)
		var buf bytes.Buffer
		srv.handleStaticAsset(&buf, req, "game.granbluefantasy.jp")

		if !strings.Contains(buf.String(), "502 Bad Gateway") {
			t.Errorf("expected 502 Bad Gateway on nil resp, got: %s", buf.String())
		}
	})
}

// TestBug1_HandleStaticAsset_AbsoluteURISchemeAllowed verifies that absolute URI schemes sent
// in plain HTTP proxy requests (e.g. http://gbf.game.mbga.jp/...) do not trigger the colon check,
// while path colons (e.g. C:/) and malformed hosts are still strictly rejected.
func TestBug1_HandleStaticAsset_AbsoluteURISchemeAllowed(t *testing.T) {
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

	srv := NewProxyServer(cfgMgr, certMgr, cacheMgr, stats)

	srv.assetClient = &http.Client{
		Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
			h := make(http.Header)
			h.Set("Content-Type", "image/png")
			return &http.Response{
				StatusCode: http.StatusOK,
				Header:     h,
				Body:       io.NopCloser(bytes.NewReader([]byte("\x89PNG\r\n\x1a\n\x00\x00\x00\rIHDR"))),
				Proto:      "HTTP/1.1",
			}, nil
		}),
	}

	// 1. Plain HTTP absolute URI sent through proxy
	req1 := &http.Request{
		Method:     http.MethodGet,
		RequestURI: "http://gbf.game.mbga.jp/assets/icon.png",
		URL:        &url.URL{Path: "/assets/icon.png"},
		Host:       "gbf.game.mbga.jp",
	}
	var buf1 bytes.Buffer
	srv.handleStaticAsset(&buf1, req1, "gbf.game.mbga.jp")
	resp1 := buf1.String()
	if strings.Contains(resp1, "400 Bad Request") {
		t.Errorf("plain HTTP absolute URI http://gbf.game.mbga.jp/assets/icon.png must NOT return 400 Bad Request, got: %s", resp1)
	}
	if !strings.Contains(resp1, "200 OK") {
		t.Errorf("expected 200 OK, got: %s", resp1)
	}

	// 2. Plain HTTP absolute URI with explicit port
	req2 := &http.Request{
		Method:     http.MethodGet,
		RequestURI: "http://gbf.game.mbga.jp:80/assets/icon.png",
		URL:        &url.URL{Path: "/assets/icon.png"},
		Host:       "gbf.game.mbga.jp:80",
	}
	var buf2 bytes.Buffer
	srv.handleStaticAsset(&buf2, req2, "gbf.game.mbga.jp:80")
	resp2 := buf2.String()
	if strings.Contains(resp2, "400 Bad Request") {
		t.Errorf("plain HTTP absolute URI with port must NOT return 400 Bad Request, got: %s", resp2)
	}

	// 3. Path containing colon must be rejected
	req3 := &http.Request{
		Method:     http.MethodGet,
		RequestURI: "http://gbf.game.mbga.jp/assets/c:/evil.png",
		URL:        &url.URL{Path: "/assets/c:/evil.png"},
		Host:       "gbf.game.mbga.jp",
	}
	var buf3 bytes.Buffer
	srv.handleStaticAsset(&buf3, req3, "gbf.game.mbga.jp")
	if !strings.Contains(buf3.String(), "400 Bad Request") {
		t.Errorf("path containing colon MUST return 400 Bad Request, got: %s", buf3.String())
	}

	// 4. Relative path containing colon must be rejected
	req4 := &http.Request{
		Method:     http.MethodGet,
		RequestURI: "/assets/c:/evil.png",
		URL:        &url.URL{Path: "/assets/c:/evil.png"},
		Host:       "gbf.game.mbga.jp",
	}
	var buf4 bytes.Buffer
	srv.handleStaticAsset(&buf4, req4, "gbf.game.mbga.jp")
	if !strings.Contains(buf4.String(), "400 Bad Request") {
		t.Errorf("relative path containing colon MUST return 400 Bad Request, got: %s", buf4.String())
	}

	// 5. Malformed targetHost with colon (not IPv6) must be rejected
	req5 := &http.Request{
		Method:     http.MethodGet,
		RequestURI: "http://bad:host/assets/icon.png",
		URL:        &url.URL{Path: "/assets/icon.png"},
		Host:       "bad:host",
	}
	var buf5 bytes.Buffer
	srv.handleStaticAsset(&buf5, req5, "bad:host")
	if !strings.Contains(buf5.String(), "400 Bad Request") {
		t.Errorf("malformed targetHost with colon MUST return 400 Bad Request, got: %s", buf5.String())
	}

	// 6. Query parameters with colons in absolute URI must NOT be rejected
	req6 := &http.Request{
		Method:     http.MethodGet,
		RequestURI: "http://gbf.game.mbga.jp/assets/icon.png?v=12:34",
		URL:        &url.URL{Path: "/assets/icon.png"},
		Host:       "gbf.game.mbga.jp",
	}
	var buf6 bytes.Buffer
	srv.handleStaticAsset(&buf6, req6, "gbf.game.mbga.jp")
	resp6 := buf6.String()
	if strings.Contains(resp6, "400 Bad Request") {
		t.Errorf("absolute URI with query parameter colon must NOT return 400 Bad Request, got: %s", resp6)
	}
	if !strings.Contains(resp6, "200 OK") {
		t.Errorf("expected 200 OK for query param with colon, got: %s", resp6)
	}

	// 7. Query parameters with colons in relative URI must NOT be rejected
	req7 := &http.Request{
		Method:     http.MethodGet,
		RequestURI: "/assets/icon.png?timestamp=2026-09-19T22:00:00Z",
		URL:        &url.URL{Path: "/assets/icon.png"},
		Host:       "gbf.game.mbga.jp",
	}
	var buf7 bytes.Buffer
	srv.handleStaticAsset(&buf7, req7, "gbf.game.mbga.jp")
	resp7 := buf7.String()
	if strings.Contains(resp7, "400 Bad Request") {
		t.Errorf("relative URI with query parameter colon must NOT return 400 Bad Request, got: %s", resp7)
	}
	if !strings.Contains(resp7, "200 OK") {
		t.Errorf("expected 200 OK for relative URI query param with colon, got: %s", resp7)
	}

	// 8. Colon in path with query parameter must be strictly rejected
	req8 := &http.Request{
		Method:     http.MethodGet,
		RequestURI: "http://gbf.game.mbga.jp/assets/c:/evil.png?v=12:34",
		URL:        &url.URL{Path: "/assets/c:/evil.png"},
		Host:       "gbf.game.mbga.jp",
	}
	var buf8 bytes.Buffer
	srv.handleStaticAsset(&buf8, req8, "gbf.game.mbga.jp")
	if !strings.Contains(buf8.String(), "400 Bad Request") {
		t.Errorf("path colon with query parameter MUST return 400 Bad Request, got: %s", buf8.String())
	}

	// 9. Percent-encoded colon in path must be rejected
	req9 := &http.Request{
		Method:     http.MethodGet,
		RequestURI: "/assets/c%3a/evil.png",
		URL:        &url.URL{Path: "/assets/c:/evil.png"},
		Host:       "gbf.game.mbga.jp",
	}
	var buf9 bytes.Buffer
	srv.handleStaticAsset(&buf9, req9, "gbf.game.mbga.jp")
	if !strings.Contains(buf9.String(), "400 Bad Request") {
		t.Errorf("percent-encoded colon in path MUST return 400 Bad Request, got: %s", buf9.String())
	}

	// 10. TargetHost with port 80 stripped before upstream HTTPS connection
	var capturedUpstreamURL string
	srv.assetClient = &http.Client{
		Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
			capturedUpstreamURL = r.URL.String()
			h := make(http.Header)
			h.Set("Content-Type", "image/png")
			return &http.Response{
				StatusCode: http.StatusOK,
				Header:     h,
				Body:       io.NopCloser(bytes.NewReader([]byte("\x89PNG\r\n\x1a\n\x00\x00\x00\rIHDR"))),
				Proto:      "HTTP/1.1",
			}, nil
		}),
	}
	req10 := &http.Request{
		Method:     http.MethodGet,
		RequestURI: "http://gbf.game.mbga.jp:80/assets/port_test_icon.png",
		URL:        &url.URL{Path: "/assets/port_test_icon.png"},
		Host:       "gbf.game.mbga.jp:80",
	}
	var buf10 bytes.Buffer
	srv.handleStaticAsset(&buf10, req10, "gbf.game.mbga.jp:80")
	if strings.Contains(capturedUpstreamURL, ":80/") {
		t.Errorf("upstream HTTPS URL should not contain :80, got: %s", capturedUpstreamURL)
	}
	if !strings.HasPrefix(capturedUpstreamURL, "https://gbf.game.mbga.jp/") {
		t.Errorf("expected https://gbf.game.mbga.jp/..., got: %s", capturedUpstreamURL)
	}
}

// TestBug2_HandlePlainHTTP_ProxyRequestsForInternalAssets verifies that requests sent via proxy
// (with URL.IsAbs() = true) or on LAN IPs correctly hit the internal endpoints rather than looping.
func TestBug2_HandlePlainHTTP_ProxyRequestsForInternalAssets(t *testing.T) {
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

	srv := NewProxyServer(cfgMgr, certMgr, cacheMgr, stats)

	testCases := []struct {
		name        string
		req         *http.Request
		wantStatus  string
		wantBodySub string
	}{
		{
			name: "Proxied request for 127.0.0.1 ca.crt (URL.IsAbs=true)",
			req: &http.Request{
				Method: http.MethodGet,
				Host:   "127.0.0.1:8124",
				URL:    &url.URL{Scheme: "http", Host: "127.0.0.1:8124", Path: "/ca.crt"},
			},
			wantStatus:  "200 OK",
			wantBodySub: "application/x-x509-ca-cert",
		},
		{
			name: "Proxied request for localhost ca.crt (URL.IsAbs=true)",
			req: &http.Request{
				Method: http.MethodGet,
				Host:   "localhost:8124",
				URL:    &url.URL{Scheme: "http", Host: "localhost:8124", Path: "/ca.crt"},
			},
			wantStatus:  "200 OK",
			wantBodySub: "application/x-x509-ca-cert",
		},
		{
			name: "Proxied request for localhost proxy.pac (URL.IsAbs=true)",
			req: &http.Request{
				Method: http.MethodGet,
				Host:   "localhost:8124",
				URL:    &url.URL{Scheme: "http", Host: "localhost:8124", Path: "/proxy.pac"},
			},
			wantStatus:  "200 OK",
			wantBodySub: "application/x-ns-proxy-autoconfig",
		},
		{
			name: "Proxied request for multi-NIC LAN IP ca.crt (URL.IsAbs=true)",
			req: &http.Request{
				Method: http.MethodGet,
				Host:   "192.168.10.55:8124",
				URL:    &url.URL{Scheme: "http", Host: "192.168.10.55:8124", Path: "/ca.crt"},
			},
			wantStatus:  "200 OK",
			wantBodySub: "application/x-x509-ca-cert",
		},
		{
			name: "Proxied request for multi-NIC LAN IP landing page (URL.IsAbs=true)",
			req: &http.Request{
				Method: http.MethodGet,
				Host:   "192.168.10.55:8124",
				URL:    &url.URL{Scheme: "http", Host: "192.168.10.55:8124", Path: "/"},
			},
			wantStatus:  "200 OK",
			wantBodySub: "text/html",
		},
		{
			name: "Loop prevention: unknown path to proxy itself returns 404 instead of looping",
			req: &http.Request{
				Method: http.MethodGet,
				Host:   "127.0.0.1:8124",
				URL:    &url.URL{Scheme: "http", Host: "127.0.0.1:8124", Path: "/some_nonexistent_local_endpoint"},
			},
			wantStatus: "404 Not Found",
		},
		{
			name: "Proxied request for private router http://192.168.1.1/ on port 80 must NOT return landing page (forwarded upstream)",
			req: &http.Request{
				Method: http.MethodGet,
				Host:   "192.168.1.1",
				URL:    &url.URL{Scheme: "http", Host: "192.168.1.1", Path: "/"},
			},
			wantStatus:  "200 OK",
			wantBodySub: "upstream-response-from-router",
		},
		{
			name: "Proxied request for private LAN server http://10.0.0.5/api on port 80 must NOT return 404 (forwarded upstream)",
			req: &http.Request{
				Method: http.MethodGet,
				Host:   "10.0.0.5",
				URL:    &url.URL{Scheme: "http", Host: "10.0.0.5", Path: "/api"},
			},
			wantStatus:  "200 OK",
			wantBodySub: "upstream-response-from-intranet",
		},
	}

	srv.apiClient = &http.Client{
		Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
			body := "upstream-ok"
			if r.Host == "192.168.1.1" {
				body = "upstream-response-from-router"
			} else if r.Host == "10.0.0.5" {
				body = "upstream-response-from-intranet"
			}
			return &http.Response{
				StatusCode: http.StatusOK,
				Header:     make(http.Header),
				Body:       io.NopCloser(strings.NewReader(body)),
				Proto:      "HTTP/1.1",
			}, nil
		}),
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			conn := &dummyConn{}
			srv.handlePlainHTTP(conn, tc.req)
			out := conn.writeBuf.String()
			if !strings.Contains(out, tc.wantStatus) {
				t.Errorf("expected status %s, got: %s", tc.wantStatus, out)
			}
			if tc.wantBodySub != "" && !strings.Contains(out, tc.wantBodySub) {
				t.Errorf("expected body to contain %q, got: %s", tc.wantBodySub, out)
			}
		})
	}
}

// TestBug3_HandlePassthroughTunnel_BidirectionalClose verifies that closing either side of the
// passthrough tunnel immediately terminates both sides, preventing goroutine or connection leaks.
func TestBug3_HandlePassthroughTunnel_BidirectionalClose(t *testing.T) {
	tempDir := t.TempDir()
	cfgPath := filepath.Join(tempDir, "config.json")
	cfgMgr := config.NewManager(cfgPath)
	certMgr, _ := cert.NewManager(filepath.Join(tempDir, "certs"))
	cacheMgr := cache.NewManager(tempDir, 16)
	defer cacheMgr.Close()
	stats := telemetry.NewStats()
	srv := NewProxyServer(cfgMgr, certMgr, cacheMgr, stats)

	t.Run("UpstreamCloses_BothClosedWithoutDeadlock", func(t *testing.T) {
		ln, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatalf("failed to listen: %v", err)
		}
		defer ln.Close()

		clientSide, serverSide := net.Pipe()
		defer clientSide.Close()

		done := make(chan struct{})
		go func() {
			srv.handlePassthroughTunnel(serverSide, serverSide, ln.Addr().String())
			close(done)
		}()

		upConn, err := ln.Accept()
		if err != nil {
			t.Fatalf("failed to accept upstream conn: %v", err)
		}

		// Read the 200 Connection Established
		buf := make([]byte, 128)
		n, err := clientSide.Read(buf)
		if err != nil || !strings.Contains(string(buf[:n]), "200 Connection Established") {
			t.Fatalf("expected 200 Connection Established, got: %s, err: %v", string(buf[:n]), err)
		}

		// Upstream closes connection
		_ = upConn.Close()

		select {
		case <-done:
			// Terminated cleanly
		case <-time.After(2 * time.Second):
			t.Fatal("handlePassthroughTunnel timed out / deadlocked when upstream closed")
		}
	})

	t.Run("ClientCloses_BothClosedWithoutDeadlock", func(t *testing.T) {
		ln, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatalf("failed to listen: %v", err)
		}
		defer ln.Close()

		clientSide, serverSide := net.Pipe()

		done := make(chan struct{})
		go func() {
			srv.handlePassthroughTunnel(serverSide, serverSide, ln.Addr().String())
			close(done)
		}()

		upConn, err := ln.Accept()
		if err != nil {
			t.Fatalf("failed to accept upstream conn: %v", err)
		}
		defer upConn.Close()

		buf := make([]byte, 128)
		_, _ = clientSide.Read(buf)

		// Client closes connection
		_ = clientSide.Close()

		select {
		case <-done:
			// Terminated cleanly
		case <-time.After(2 * time.Second):
			t.Fatal("handlePassthroughTunnel timed out / deadlocked when client closed")
		}
	})

	t.Run("MissingPortDefaultsTo443", func(t *testing.T) {
		clientSide, serverSide := net.Pipe()
		defer clientSide.Close()

		done := make(chan struct{})
		go func() {
			srv.handlePassthroughTunnel(serverSide, serverSide, "127.0.0.1")
			close(done)
		}()

		buf := make([]byte, 128)
		n, _ := clientSide.Read(buf)
		if !strings.Contains(string(buf[:n]), "502 Bad Gateway") {
			t.Errorf("expected 502 Bad Gateway for unconnected default port 443, got: %s", string(buf[:n]))
		}
		select {
		case <-done:
		case <-time.After(2 * time.Second):
			t.Fatal("handlePassthroughTunnel hung on connection failure")
		}
	})
}

// TestBug16_HandlePlainHTTP_NonGBFStaticRequestsNotForcedHTTPS verifies that plain HTTP static
// requests for non-GBF domains (e.g. http://example.com/logo.png) are not forced to HTTPS via
// handleStaticAsset, but transparently forwarded via forwardPlainProxy.
func TestBug16_HandlePlainHTTP_NonGBFStaticRequestsNotForcedHTTPS(t *testing.T) {
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

	srv := NewProxyServer(cfgMgr, certMgr, cacheMgr, stats)

	var assetClientCalled atomic.Bool
	var apiClientCalled atomic.Bool

	srv.assetClient = &http.Client{
		Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
			assetClientCalled.Store(true)
			return nil, errors.New("assetClient should not be called for non-GBF")
		}),
	}

	srv.apiClient = &http.Client{
		Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
			apiClientCalled.Store(true)
			h := make(http.Header)
			h.Set("Content-Type", "image/png")
			return &http.Response{
				StatusCode: http.StatusOK,
				Header:     h,
				Body:       io.NopCloser(bytes.NewReader([]byte("external-image-bytes"))),
				Proto:      "HTTP/1.1",
			}, nil
		}),
	}

	// 1. Non-GBF request for static file (e.g. http://example.com/logo.png)
	conn1 := &dummyConn{}
	req1 := &http.Request{
		Method:     http.MethodGet,
		Host:       "example.com",
		RequestURI: "http://example.com/logo.png",
		URL:        &url.URL{Scheme: "http", Host: "example.com", Path: "/logo.png"},
	}
	srv.handlePlainHTTP(conn1, req1)

	if assetClientCalled.Load() {
		t.Error("non-GBF static request was routed to handleStaticAsset (assetClient) - forced HTTPS!")
	}
	if !apiClientCalled.Load() {
		t.Error("non-GBF static request should have been forwarded via forwardPlainProxy (apiClient)")
	}
	if !strings.Contains(conn1.writeBuf.String(), "external-image-bytes") {
		t.Errorf("expected external image bytes forwarded, got: %s", conn1.writeBuf.String())
	}

	// 2. GBF request for static file (e.g. http://gbf.game.mbga.jp/assets/ui.png)
	assetClientCalled.Store(false)
	apiClientCalled.Store(false)

	srv.assetClient = &http.Client{
		Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
			assetClientCalled.Store(true)
			h := make(http.Header)
			h.Set("Content-Type", "image/png")
			return &http.Response{
				StatusCode: http.StatusOK,
				Header:     h,
				Body:       io.NopCloser(bytes.NewReader([]byte("\x89PNG\r\n\x1a\n\x00\x00\x00\rIHDR"))),
				Proto:      "HTTP/1.1",
			}, nil
		}),
	}

	conn2 := &dummyConn{}
	req2 := &http.Request{
		Method:     http.MethodGet,
		Host:       "gbf.game.mbga.jp",
		RequestURI: "http://gbf.game.mbga.jp/assets/ui.png",
		URL:        &url.URL{Scheme: "http", Host: "gbf.game.mbga.jp", Path: "/assets/ui.png"},
	}
	srv.handlePlainHTTP(conn2, req2)

	if !assetClientCalled.Load() {
		t.Error("GBF static request should have been routed to handleStaticAsset (assetClient)")
	}
	if apiClientCalled.Load() {
		t.Error("GBF static request should not be forwarded through apiClient")
	}
}
