package proxy

import (
	"bytes"
	"context"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
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

// helper to set up a test ProxyServer and mock TLS upstream
func setupOptionsTestEnv(t *testing.T, upstreamHandler http.HandlerFunc) (*ProxyServer, *httptest.Server, *telemetry.Stats) {
	t.Helper()
	tempDir := t.TempDir()
	cfgMgr := config.NewManager(filepath.Join(tempDir, "config.json"))
	certMgr, err := cert.NewManager(filepath.Join(tempDir, "certs"))
	if err != nil {
		t.Fatalf("failed to create cert manager: %v", err)
	}
	cacheMgr := cache.NewManager(tempDir, 16)
	t.Cleanup(func() { cacheMgr.Close() })
	stats := telemetry.NewStats()

	srv := NewProxyServer(cfgMgr, certMgr, cacheMgr, stats)
	t.Cleanup(func() { srv.Stop() })

	ts := httptest.NewTLSServer(upstreamHandler)
	t.Cleanup(func() { ts.Close() })

	tr := ts.Client().Transport.(*http.Transport).Clone()
	tr.DialContext = func(ctx context.Context, network, addr string) (net.Conn, error) {
		return net.Dial("tcp", ts.Listener.Addr().String())
	}
	tr.TLSClientConfig.InsecureSkipVerify = true

	setTestAPIClient(srv, &http.Client{
		Transport: tr,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			return http.ErrUseLastResponse
		},
	})
	setTestAssetClient(srv, &http.Client{Transport: tr})

	return srv, ts, stats
}

// TestProxy_Options_ForwardedToUpstream verifies that:
// 1. OPTIONS requests actually reach the upstream server (not answered locally).
// 2. Preflight request headers (Origin, Access-Control-Request-Method, Access-Control-Request-Headers) are forwarded.
// 3. Upstream CORS headers are returned to the client.
// 4. Local synthetic wildcard CORS headers (Access-Control-Allow-Origin: *) are not returned.
// 5. Proxy fingerprint headers are stripped.
func TestProxy_Options_ForwardedToUpstream(t *testing.T) {
	var upstreamReceived atomic.Int64
	var capturedMethod string
	var capturedURI string
	var capturedOrigin string
	var capturedReqMethod string
	var capturedReqHeaders string

	srv, _, _ := setupOptionsTestEnv(t, func(w http.ResponseWriter, r *http.Request) {
		upstreamReceived.Add(1)
		capturedMethod = r.Method
		capturedURI = r.URL.RequestURI()
		capturedOrigin = r.Header.Get("Origin")
		capturedReqMethod = r.Header.Get("Access-Control-Request-Method")
		capturedReqHeaders = r.Header.Get("Access-Control-Request-Headers")

		// Upstream returns specific CORS headers (NOT wildcard)
		w.Header().Set("Access-Control-Allow-Origin", "https://game.granbluefantasy.jp")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type, X-Requested-With")
		w.Header().Set("Access-Control-Max-Age", "3600")
		w.Header().Set("X-Upstream-Auth", "verified")
		// Proxy fingerprint header from upstream must be stripped
		w.Header().Set("X-Proxy-Cache", "MISS")
		w.WriteHeader(http.StatusNoContent)
	})

	targetHost := "game.granbluefantasy.jp"
	req, err := http.NewRequest(http.MethodOptions, "https://"+targetHost+"/rest/multiraid/start.json", nil)
	if err != nil {
		t.Fatalf("failed to create OPTIONS request: %v", err)
	}
	req.Header.Set("Origin", "https://game.granbluefantasy.jp")
	req.Header.Set("Access-Control-Request-Method", "POST")
	req.Header.Set("Access-Control-Request-Headers", "content-type, x-requested-with")
	req.Header.Set("User-Agent", "GranblueBrowser/1.0")

	var buf bytes.Buffer
	keepAlive := srv.handleDecryptedRequest(&buf, req, targetHost)
	if !keepAlive {
		t.Errorf("expected keepAlive == true for OPTIONS request")
	}

	// 1. Upstream must have received the request
	if upstreamReceived.Load() != 1 {
		t.Fatalf("expected upstream to receive exactly 1 request, got %d (proxy answered locally!)", upstreamReceived.Load())
	}
	if capturedMethod != http.MethodOptions {
		t.Errorf("expected upstream method %q, got %q", http.MethodOptions, capturedMethod)
	}
	if capturedURI != "/rest/multiraid/start.json" {
		t.Errorf("expected upstream URI %q, got %q", "/rest/multiraid/start.json", capturedURI)
	}

	// 2. Original preflight headers forwarded to upstream
	if capturedOrigin != "https://game.granbluefantasy.jp" {
		t.Errorf("expected Origin forwarded, got %q", capturedOrigin)
	}
	if capturedReqMethod != "POST" {
		t.Errorf("expected Access-Control-Request-Method forwarded, got %q", capturedReqMethod)
	}
	if capturedReqHeaders != "content-type, x-requested-with" {
		t.Errorf("expected Access-Control-Request-Headers forwarded, got %q", capturedReqHeaders)
	}

	// 3. Verify proxy output response
	rawResp := buf.String()
	if !strings.HasPrefix(rawResp, "HTTP/1.1 204 No Content") {
		t.Fatalf("expected status 'HTTP/1.1 204 No Content', got: %s", firstLine(rawResp))
	}

	// 4. Must NOT have local wildcard CORS
	allowOrigin := extractHeader(rawResp, "Access-Control-Allow-Origin")
	if allowOrigin == "*" {
		t.Errorf("CRITICAL: response still contains fake wildcard Access-Control-Allow-Origin: *")
	}
	if allowOrigin != "https://game.granbluefantasy.jp" {
		t.Errorf("expected Access-Control-Allow-Origin %q, got %q", "https://game.granbluefantasy.jp", allowOrigin)
	}

	// Max age must match upstream (3600), not the old hardcoded 604800
	maxAge := extractHeader(rawResp, "Access-Control-Max-Age")
	if maxAge == "604800" {
		t.Errorf("CRITICAL: response contains old fake Access-Control-Max-Age: 604800")
	}
	if maxAge != "3600" {
		t.Errorf("expected Access-Control-Max-Age 3600, got %q", maxAge)
	}

	// 5. Upstream custom header preserved
	if custom := extractHeader(rawResp, "X-Upstream-Auth"); custom != "verified" {
		t.Errorf("expected X-Upstream-Auth: verified, got %q", custom)
	}

	// 6. Proxy fingerprint headers stripped
	lower := strings.ToLower(rawResp)
	if strings.Contains(lower, "x-proxy-cache") {
		t.Errorf("proxy fingerprint header X-Proxy-Cache was not stripped")
	}
}

// TestProxy_Options_UpstreamStatusCodes verifies that the proxy accurately forwards
// various upstream status codes (204, 403, 200) without coercing them to 200.
func TestProxy_Options_UpstreamStatusCodes(t *testing.T) {
	testCases := []struct {
		name               string
		upstreamStatus     int
		upstreamBody       []byte
		upstreamHeaders    map[string]string
		expectedStatusLine string
		expectBody         bool
	}{
		{
			name:           "204 No Content with CORS",
			upstreamStatus: http.StatusNoContent,
			upstreamHeaders: map[string]string{
				"Access-Control-Allow-Origin":  "https://game.granbluefantasy.jp",
				"Access-Control-Allow-Methods": "GET, POST, OPTIONS",
			},
			expectedStatusLine: "HTTP/1.1 204 No Content",
			expectBody:         false,
		},
		{
			name:           "403 Forbidden with CORS rejection",
			upstreamStatus: http.StatusForbidden,
			upstreamBody:   []byte(`{"error":"cors_origin_disallowed"}`),
			upstreamHeaders: map[string]string{
				"Content-Type": "application/json",
			},
			expectedStatusLine: "HTTP/1.1 403 Forbidden",
			expectBody:         true,
		},
		{
			name:           "200 OK with OPTIONS description",
			upstreamStatus: http.StatusOK,
			upstreamBody:   []byte(`{"status":"ok"}`),
			upstreamHeaders: map[string]string{
				"Content-Type":                 "application/json",
				"Access-Control-Allow-Origin":  "https://game.granbluefantasy.jp",
				"Access-Control-Allow-Methods": "OPTIONS, GET, HEAD",
			},
			expectedStatusLine: "HTTP/1.1 200 OK",
			expectBody:         true,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			srv, _, _ := setupOptionsTestEnv(t, func(w http.ResponseWriter, r *http.Request) {
				for k, v := range tc.upstreamHeaders {
					w.Header().Set(k, v)
				}
				w.WriteHeader(tc.upstreamStatus)
				if len(tc.upstreamBody) > 0 {
					_, _ = w.Write(tc.upstreamBody)
				}
			})

			targetHost := "game.granbluefantasy.jp"
			req, err := http.NewRequest(http.MethodOptions, "https://"+targetHost+"/rest/multiraid/start.json", nil)
			if err != nil {
				t.Fatalf("failed to create OPTIONS request: %v", err)
			}
			req.Header.Set("Origin", "https://game.granbluefantasy.jp")

			var buf bytes.Buffer
			srv.handleDecryptedRequest(&buf, req, targetHost)

			raw := buf.String()
			if firstLine(raw) != tc.expectedStatusLine {
				t.Errorf("status line mismatch: expected %q, got %q", tc.expectedStatusLine, firstLine(raw))
			}

			// Verify body presence / suppression
			body := extractHTTPResponseBody(buf.Bytes())
			if tc.expectBody {
				if !bytes.Equal(body, tc.upstreamBody) {
					t.Errorf("body mismatch: expected %q, got %q", tc.upstreamBody, body)
				}
			} else {
				if len(body) != 0 {
					t.Errorf("expected no body for %s, got %d bytes: %q", tc.name, len(body), body)
				}
				// RFC 7230: Content-Length must not be sent for 204
				if tc.upstreamStatus == http.StatusNoContent {
					if cl := extractHeader(raw, "Content-Length"); cl != "" {
						t.Errorf("RFC 7230 violation: Content-Length must be omitted for 204 No Content, got %q", cl)
					}
				}
			}

			// Verify upstream headers preserved
			for k, v := range tc.upstreamHeaders {
				if got := extractHeader(raw, k); got != v {
					t.Errorf("header %s mismatch: expected %q, got %q", k, v, got)
				}
			}
		})
	}
}

// TestProxy_Options_StrictZeroRetry verifies that an OPTIONS request is NEVER retried
// if the upstream connection drops or aborts (maxAttempts = 1).
func TestProxy_Options_StrictZeroRetry(t *testing.T) {
	var upstreamAttempts atomic.Int64
	srv, _, stats := setupOptionsTestEnv(t, func(w http.ResponseWriter, r *http.Request) {
		upstreamAttempts.Add(1)
		// Simulate network drop by hijacking and closing the connection immediately
		hj, ok := w.(http.Hijacker)
		if !ok {
			http.Error(w, "hijack not supported", http.StatusInternalServerError)
			return
		}
		conn, _, err := hj.Hijack()
		if err != nil {
			return
		}
		_ = conn.Close()
	})

	targetHost := "game.granbluefantasy.jp"
	req, err := http.NewRequest(http.MethodOptions, "https://"+targetHost+"/rest/multiraid/start.json", nil)
	if err != nil {
		t.Fatalf("failed to create OPTIONS request: %v", err)
	}

	var buf bytes.Buffer
	srv.handleDecryptedRequest(&buf, req, targetHost)

	// Client must receive 502 Bad Gateway
	respStr := buf.String()
	if !strings.HasPrefix(respStr, "HTTP/1.1 502 Bad Gateway") {
		t.Fatalf("expected HTTP/1.1 502 Bad Gateway, got: %s", firstLine(respStr))
	}

	// Strictly 1 attempt, zero retries
	if attempts := upstreamAttempts.Load(); attempts != 1 {
		t.Fatalf("P0 VIOLATION: expected strictly 1 attempt on OPTIONS request (zero retries), got %d", attempts)
	}
	if retries := stats.APIRetries.Load(); retries != 0 {
		t.Fatalf("expected 0 api_retries in stats, got %d", retries)
	}
}

// TestProxy_Options_StaticAssetHost verifies that OPTIONS requests targeting static asset
// domains (e.g. prd-game-a-granbluefantasy.akamaized.net) are forwarded to upstream CDN,
// not answered locally with fake wildcard CORS.
func TestProxy_Options_StaticAssetHost(t *testing.T) {
	var upstreamReceived atomic.Int64
	var capturedHost string
	var capturedURI string

	srv, _, _ := setupOptionsTestEnv(t, func(w http.ResponseWriter, r *http.Request) {
		upstreamReceived.Add(1)
		capturedHost = r.Host
		capturedURI = r.URL.RequestURI()

		// Akamai CDN returning CORS headers for static font / css
		w.Header().Set("Access-Control-Allow-Origin", "https://game.granbluefantasy.jp")
		w.Header().Set("Access-Control-Allow-Methods", "GET, HEAD, OPTIONS")
		w.Header().Set("Access-Control-Max-Age", "86400")
		w.WriteHeader(http.StatusOK)
	})

	targetHost := "prd-game-a-granbluefantasy.akamaized.net"
	assetPath := "/assets_en/css/common.css"
	req, err := http.NewRequest(http.MethodOptions, "https://"+targetHost+assetPath, nil)
	if err != nil {
		t.Fatalf("failed to create request: %v", err)
	}
	req.Header.Set("Origin", "https://game.granbluefantasy.jp")

	var buf bytes.Buffer
	srv.handleDecryptedRequest(&buf, req, targetHost)

	// Must have reached upstream
	if upstreamReceived.Load() != 1 {
		t.Fatalf("expected upstream to receive 1 request, got %d", upstreamReceived.Load())
	}
	if capturedHost != targetHost {
		t.Errorf("expected upstream Host %q, got %q", targetHost, capturedHost)
	}
	if capturedURI != assetPath {
		t.Errorf("expected upstream URI %q, got %q", assetPath, capturedURI)
	}

	raw := buf.String()
	if !strings.HasPrefix(raw, "HTTP/1.1 200 OK") {
		t.Fatalf("expected 200 OK from upstream CDN, got: %s", firstLine(raw))
	}
	allowOrigin := extractHeader(raw, "Access-Control-Allow-Origin")
	if allowOrigin == "*" {
		t.Errorf("CRITICAL: response contains fake wildcard Access-Control-Allow-Origin: *")
	}
	if allowOrigin != "https://game.granbluefantasy.jp" {
		t.Errorf("expected Access-Control-Allow-Origin %q, got %q", "https://game.granbluefantasy.jp", allowOrigin)
	}
}

// TestProxy_NoRegression_DynamicAndStaticPaths verifies that regular dynamic GET/POST,
// official probes (/ob/r, /rest/error/js), and static asset caching paths are completely
// unaffected and continue to work as expected.
func TestProxy_NoRegression_DynamicAndStaticPaths(t *testing.T) {
	var dynCalls atomic.Int64
	var probeCalls atomic.Int64
	var errCalls atomic.Int64
	var assetCalls atomic.Int64

	samplePng := []byte("\x89PNG\r\n\x1a\n\x00\x00\x00\rIHDR\x00\x00\x00\x01\x00\x00\x00\x01\x08\x06\x00\x00\x00\x1f\x15c4\x00\x00\x00\nIDATx\x9cc\x00\x01\x00\x00\x05\x00\x01\r\n-\xb4\x00\x00\x00\x00IEND\xaeB`\x82")

	srv, _, _ := setupOptionsTestEnv(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/rest/user/status.json":
			dynCalls.Add(1)
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"status":"ok"}`))
		case "/rest/multiraid/start.json":
			dynCalls.Add(1)
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"raid_id":"123"}`))
		case "/ob/r":
			probeCalls.Add(1)
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`ok`))
		case "/rest/error/js":
			errCalls.Add(1)
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`recorded`))
		case "/assets_en/img/sp/ui/icon.png":
			assetCalls.Add(1)
			w.Header().Set("Content-Type", "image/png")
			w.Header().Set("ETag", `"icon-v1"`)
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write(samplePng)
		default:
			http.NotFound(w, r)
		}
	})

	apiHost := "game.granbluefantasy.jp"
	assetHost := "prd-game-a-granbluefantasy.akamaized.net"

	// 1. Dynamic GET
	reqGet, _ := http.NewRequest(http.MethodGet, "https://"+apiHost+"/rest/user/status.json", nil)
	var bufGet bytes.Buffer
	srv.handleDecryptedRequest(&bufGet, reqGet, apiHost)
	if !strings.HasPrefix(bufGet.String(), "HTTP/1.1 200 OK") {
		t.Errorf("dynamic GET failed: %s", firstLine(bufGet.String()))
	}

	// 2. Dynamic POST
	reqPost, _ := http.NewRequest(http.MethodPost, "https://"+apiHost+"/rest/multiraid/start.json", bytes.NewReader([]byte(`{}`)))
	var bufPost bytes.Buffer
	srv.handleDecryptedRequest(&bufPost, reqPost, apiHost)
	if !strings.HasPrefix(bufPost.String(), "HTTP/1.1 200 OK") {
		t.Errorf("dynamic POST failed: %s", firstLine(bufPost.String()))
	}

	// 3. Official probe /ob/r
	reqProbe, _ := http.NewRequest(http.MethodGet, "https://"+apiHost+"/ob/r", nil)
	var bufProbe bytes.Buffer
	srv.handleDecryptedRequest(&bufProbe, reqProbe, apiHost)
	if !strings.HasPrefix(bufProbe.String(), "HTTP/1.1 200 OK") {
		t.Errorf("probe /ob/r failed: %s", firstLine(bufProbe.String()))
	}

	// 4. Error report /rest/error/js
	reqErr, _ := http.NewRequest(http.MethodPost, "https://"+apiHost+"/rest/error/js", bytes.NewReader([]byte(`err`)))
	var bufErr bytes.Buffer
	srv.handleDecryptedRequest(&bufErr, reqErr, apiHost)
	if !strings.HasPrefix(bufErr.String(), "HTTP/1.1 200 OK") {
		t.Errorf("error reporter /rest/error/js failed: %s", firstLine(bufErr.String()))
	}

	// 5. Static asset GET (first request: MISS, second request: HIT)
	reqAsset1, _ := http.NewRequest(http.MethodGet, "https://"+assetHost+"/assets_en/img/sp/ui/icon.png", nil)
	var bufAsset1 bytes.Buffer
	srv.handleDecryptedRequest(&bufAsset1, reqAsset1, assetHost)
	if !strings.HasPrefix(bufAsset1.String(), "HTTP/1.1 200 OK") {
		t.Errorf("static asset GET 1 failed: %s", firstLine(bufAsset1.String()))
	}

	// Wait briefly for async cache write if needed
	time.Sleep(50 * time.Millisecond)

	reqAsset2, _ := http.NewRequest(http.MethodGet, "https://"+assetHost+"/assets_en/img/sp/ui/icon.png", nil)
	var bufAsset2 bytes.Buffer
	srv.handleDecryptedRequest(&bufAsset2, reqAsset2, assetHost)
	if !strings.HasPrefix(bufAsset2.String(), "HTTP/1.1 200 OK") {
		t.Errorf("static asset GET 2 failed: %s", firstLine(bufAsset2.String()))
	}

	if dynCalls.Load() != 2 {
		t.Errorf("expected 2 dynamic calls, got %d", dynCalls.Load())
	}
	if probeCalls.Load() != 1 {
		t.Errorf("expected 1 probe call, got %d", probeCalls.Load())
	}
	if errCalls.Load() != 1 {
		t.Errorf("expected 1 err call, got %d", errCalls.Load())
	}
	// Asset was requested twice: first was MISS (reaches upstream), second is HIT (served from RAM SLRU)
	if assetCalls.Load() != 1 {
		t.Errorf("expected exactly 1 upstream asset call (cache hit on second), got %d", assetCalls.Load())
	}
}

// TestProxy_Options_WithRequestBody verifies that an OPTIONS request with a body
// (e.g. non-standard preflight body or diagnostic payload) is correctly read,
// forwarded to upstream without corruption or blocking, and answered.
func TestProxy_Options_WithRequestBody(t *testing.T) {
	var upstreamReceived atomic.Int64
	var capturedBody []byte

	srv, _, _ := setupOptionsTestEnv(t, func(w http.ResponseWriter, r *http.Request) {
		upstreamReceived.Add(1)
		var err error
		capturedBody, err = io.ReadAll(r.Body)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Access-Control-Allow-Origin", "https://game.granbluefantasy.jp")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"received":true}`))
	})

	targetHost := "game.granbluefantasy.jp"
	testPayload := []byte(`{"ping":"options_body_test"}`)
	req, err := http.NewRequest(http.MethodOptions, "https://"+targetHost+"/rest/multiraid/start.json", bytes.NewReader(testPayload))
	if err != nil {
		t.Fatalf("failed to create OPTIONS request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Origin", "https://game.granbluefantasy.jp")

	var buf bytes.Buffer
	srv.handleDecryptedRequest(&buf, req, targetHost)

	if upstreamReceived.Load() != 1 {
		t.Fatalf("expected upstream to receive 1 request, got %d", upstreamReceived.Load())
	}
	if !bytes.Equal(capturedBody, testPayload) {
		t.Errorf("expected body %q, got %q", testPayload, capturedBody)
	}
	raw := buf.String()
	if !strings.HasPrefix(raw, "HTTP/1.1 200 OK") {
		t.Fatalf("expected 200 OK, got: %s", firstLine(raw))
	}
}

// TestProxy_Options_RedirectResponse verifies that when the upstream responds
// with a redirect (e.g. 302 Found) to an OPTIONS request, the proxy does NOT
// follow the redirect (matching production CheckRedirect policy) and preserves
// the redirect status line and Location header.
func TestProxy_Options_RedirectResponse(t *testing.T) {
	var upstreamCalls atomic.Int64
	redirectTarget := "https://game.granbluefantasy.jp/rest/new_endpoint"

	srv, _, _ := setupOptionsTestEnv(t, func(w http.ResponseWriter, r *http.Request) {
		upstreamCalls.Add(1)
		w.Header().Set("Location", redirectTarget)
		w.Header().Set("Access-Control-Allow-Origin", "https://game.granbluefantasy.jp")
		w.WriteHeader(http.StatusFound)
	})

	targetHost := "game.granbluefantasy.jp"
	req, err := http.NewRequest(http.MethodOptions, "https://"+targetHost+"/rest/multiraid/start.json", nil)
	if err != nil {
		t.Fatalf("failed to create OPTIONS request: %v", err)
	}
	req.Header.Set("Origin", "https://game.granbluefantasy.jp")

	var buf bytes.Buffer
	srv.handleDecryptedRequest(&buf, req, targetHost)

	if upstreamCalls.Load() != 1 {
		t.Errorf("expected strictly 1 upstream call (no redirect following), got %d", upstreamCalls.Load())
	}
	raw := buf.String()
	if !strings.HasPrefix(raw, "HTTP/1.1 302 Found") {
		t.Fatalf("expected 302 Found, got: %s", firstLine(raw))
	}
	if loc := extractHeader(raw, "Location"); loc != redirectTarget {
		t.Errorf("expected Location %q, got %q", redirectTarget, loc)
	}
}

// TestProxy_Options_MultipleSetCookies verifies that multiple Set-Cookie headers
// returned on an upstream OPTIONS response are preserved as separate lines
// without comma-folding (per P0 rule).
func TestProxy_Options_MultipleSetCookies(t *testing.T) {
	srv, _, _ := setupOptionsTestEnv(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Add("Set-Cookie", "c1=v1; Path=/; HttpOnly")
		w.Header().Add("Set-Cookie", "c2=v2; Path=/; Secure")
		w.Header().Set("Access-Control-Allow-Origin", "https://game.granbluefantasy.jp")
		w.WriteHeader(http.StatusOK)
	})

	targetHost := "game.granbluefantasy.jp"
	req, err := http.NewRequest(http.MethodOptions, "https://"+targetHost+"/rest/multiraid/start.json", nil)
	if err != nil {
		t.Fatalf("failed to create OPTIONS request: %v", err)
	}

	var buf bytes.Buffer
	srv.handleDecryptedRequest(&buf, req, targetHost)

	raw := buf.String()
	cookieLines := 0
	for _, line := range strings.Split(raw, "\r\n") {
		if strings.HasPrefix(line, "Set-Cookie: ") {
			cookieLines++
		}
	}
	if cookieLines != 2 {
		t.Errorf("expected 2 separate Set-Cookie lines, got %d", cookieLines)
	}
}

// TestProxy_Options_ConnectionClose verifies that an OPTIONS request with Connection: close
// causes the proxy to output Connection: close and return keepAlive == false.
func TestProxy_Options_ConnectionClose(t *testing.T) {
	srv, _, _ := setupOptionsTestEnv(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "https://game.granbluefantasy.jp")
		w.WriteHeader(http.StatusNoContent)
	})

	targetHost := "game.granbluefantasy.jp"
	req, err := http.NewRequest(http.MethodOptions, "https://"+targetHost+"/rest/multiraid/start.json", nil)
	if err != nil {
		t.Fatalf("failed to create OPTIONS request: %v", err)
	}
	req.Close = true

	var buf bytes.Buffer
	keepAlive := srv.handleDecryptedRequest(&buf, req, targetHost)
	if keepAlive {
		t.Errorf("expected keepAlive == false when req.Close is true")
	}

	raw := buf.String()
	if connHdr := extractHeader(raw, "Connection"); !strings.EqualFold(connHdr, "close") {
		t.Errorf("expected Connection: close, got %q", connHdr)
	}
}

// TestProxy_Options_QueryParameters verifies that query parameters on OPTIONS
// are preserved in the forwarded RequestURI.
func TestProxy_Options_QueryParameters(t *testing.T) {
	var capturedURI string
	srv, _, _ := setupOptionsTestEnv(t, func(w http.ResponseWriter, r *http.Request) {
		capturedURI = r.URL.RequestURI()
		w.Header().Set("Access-Control-Allow-Origin", "https://game.granbluefantasy.jp")
		w.WriteHeader(http.StatusOK)
	})

	targetHost := "game.granbluefantasy.jp"
	targetURI := "/rest/multiraid/start.json?foo=bar&category=123"
	req, err := http.NewRequest(http.MethodOptions, "https://"+targetHost+targetURI, nil)
	if err != nil {
		t.Fatalf("failed to create OPTIONS request: %v", err)
	}

	var buf bytes.Buffer
	srv.handleDecryptedRequest(&buf, req, targetHost)

	if capturedURI != targetURI {
		t.Errorf("expected upstream URI %q, got %q", targetURI, capturedURI)
	}
}

