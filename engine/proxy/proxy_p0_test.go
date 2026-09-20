package proxy

import (
	"bytes"
	"net/http"
	"path/filepath"
	"strings"
	"testing"

	"gbf-proxy/cache"
	"gbf-proxy/config"
)

// buildTestResponse constructs an upstream response carrying the headers that P0
// rules care about, so we can assert forwardDynamicResponse reproduces them
// faithfully without any proxy-induced pollution.
func buildTestResponse() *http.Response {
	h := http.Header{}
	h.Add("Set-Cookie", "session=abc; Path=/; HttpOnly")
	h.Add("Set-Cookie", "token=xyz; Path=/; Secure")
	h.Set("Content-Type", "application/json")
	h.Set("X-Upstream-Custom", "keep-me")
	// These must be stripped (zero proxy-fingerprint pollution).
	h.Set("X-Proxy-Cache", "HIT")
	h.Set("X-Cache-Source", "local")
	h.Set("X-Acceleration-Engine", "gbf")
	return &http.Response{
		StatusCode: http.StatusOK,
		Header:     h,
	}
}

func firstLine(raw string) string {
	if idx := strings.Index(raw, "\r\n"); idx >= 0 {
		return raw[:idx]
	}
	return raw
}

// P0-4: Zero Header Pollution & Multi-line Set-Cookie preservation.
func TestForwardDynamicResponse_P0_HeaderIntegrity(t *testing.T) {
	srv := &ProxyServer{}
	resp := buildTestResponse()
	body := []byte(`{"result":"ok"}`)

	var buf bytes.Buffer
	srv.forwardDynamicResponse(&buf, resp, body, false, false)
	out := buf.String()

	// 1. Status line must be exact.
	if firstLine(out) != "HTTP/1.1 200 OK" {
		t.Errorf("unexpected status line: %q", firstLine(out))
	}

	// 2. Each Set-Cookie must appear on its own line, never comma-folded.
	cookieLines := 0
	for _, line := range strings.Split(out, "\r\n") {
		if strings.HasPrefix(line, "Set-Cookie: ") {
			cookieLines++
			if strings.Contains(line, ", token=") || strings.Contains(line, ",session=") {
				t.Errorf("Set-Cookie must not be comma-folded: %q", line)
			}
		}
	}
	if cookieLines != 2 {
		t.Errorf("expected 2 distinct Set-Cookie lines, got %d", cookieLines)
	}

	// 3. Proxy fingerprint headers must be stripped.
	lower := strings.ToLower(out)
	for _, banned := range []string{"x-proxy-cache", "x-cache-source", "x-acceleration-engine"} {
		if strings.Contains(lower, banned) {
			t.Errorf("proxy fingerprint header %q must be stripped", banned)
		}
	}

	// 4. Legitimate upstream headers must be preserved.
	if !strings.Contains(out, "X-Upstream-Custom: keep-me") {
		t.Errorf("expected upstream custom header preserved, got:\n%s", out)
	}

	// 5. Correct Content-Length and body bytes appended.
	if !strings.Contains(out, "Content-Length: 15") {
		t.Errorf("expected Content-Length: 15, got:\n%s", out)
	}
	if !bytes.HasSuffix(buf.Bytes(), body) {
		t.Errorf("expected body bytes appended after header block")
	}
}

// P0-4: Non-canonical lowercase set-cookie headers must also be preserved multi-line.
func TestForwardDynamicResponse_NonCanonicalSetCookie(t *testing.T) {
	srv := &ProxyServer{}
	h := http.Header{}
	// Direct map assignment to simulate non-canonical lowercase key from upstream
	h["set-cookie"] = []string{"foo=1; Path=/", "bar=2; Path=/"}
	resp := &http.Response{
		StatusCode: http.StatusOK,
		Header:     h,
	}
	var buf bytes.Buffer
	srv.forwardDynamicResponse(&buf, resp, []byte("ok"), false, false)
	out := buf.String()

	cookieLines := 0
	for _, line := range strings.Split(out, "\r\n") {
		if strings.HasPrefix(line, "Set-Cookie: ") {
			cookieLines++
		}
	}
	if cookieLines != 2 {
		t.Errorf("expected 2 Set-Cookie lines for non-canonical headers, got %d:\n%s", cookieLines, out)
	}
}

// P0-4: HEAD requests must not emit a body.
func TestForwardDynamicResponse_HeadNoBody(t *testing.T) {
	srv := &ProxyServer{}
	resp := buildTestResponse()
	body := []byte(`{"result":"ok"}`)

	var buf bytes.Buffer
	srv.forwardDynamicResponse(&buf, resp, body, true, false)
	out := buf.String()

	if bytes.Contains(buf.Bytes(), body) {
		t.Errorf("HEAD response must not contain body bytes")
	}
	if !strings.Contains(out, "Content-Length: 15") {
		t.Errorf("HEAD must still advertise correct Content-Length, got:\n%s", out)
	}
}

// P0-4: 204/304 must not carry Content-Length (bodyless statuses).
func TestForwardDynamicResponse_BodylessStatus(t *testing.T) {
	srv := &ProxyServer{}
	resp := buildTestResponse()
	resp.StatusCode = http.StatusNoContent

	var buf bytes.Buffer
	srv.forwardDynamicResponse(&buf, resp, nil, false, false)
	out := buf.String()

	if firstLine(out) != "HTTP/1.1 204 No Content" {
		t.Errorf("unexpected 204 status line: %q", firstLine(out))
	}
	if strings.Contains(out, "Content-Length:") {
		t.Errorf("204 must not include Content-Length, got:\n%s", out)
	}
}

// P0-3: Retry whitelist is strictly read-only idempotent GET paths; write/state
// endpoints must never be retryable.
func TestRetryableAPI_P0_WriteEndpointsExcluded(t *testing.T) {
	writePaths := []string{
		"/rest/multiraid/start.json",
		"/rest/raid/ability_result.json",
		"/rest/quest/result",
		"/rest/gacha/draw",
		"/rest/user/profile",
		"/ob/r",
		"/rest/error/js",
	}
	for _, p := range writePaths {
		if isRetryableAPI(p) {
			t.Errorf("write/state endpoint %q must NOT be retryable (zero-retry rule)", p)
		}
	}
}

// P0-2: Official probe & error-report paths must be classified as dynamic
// (never treated as cacheable static assets).
func TestProbePaths_AreDynamicNotStatic(t *testing.T) {
	probes := []struct{ host, path string }{
		{"game.granbluefantasy.jp", "/ob/r"},
		{"game.granbluefantasy.jp", "/rest/error/js"},
	}
	for _, pr := range probes {
		if isStaticTarget(pr.host, pr.path) {
			t.Errorf("official probe %s%s must pass through as dynamic API, not static", pr.host, pr.path)
		}
	}
}

// sendAssetResponse must emit an RFC-correct reason phrase for non-200 statuses.
func TestSendAssetResponse_StatusPhrase(t *testing.T) {
	tempDir := t.TempDir()
	cfgMgr := config.NewManager(filepath.Join(tempDir, "config.json"))
	srv := &ProxyServer{cfgMgr: cfgMgr}
	item := &cache.CacheItem{
		Key:         "assets/x.png",
		Data:        []byte("\x89PNG\r\n\x1a\n"),
		ContentType: "image/png",
	}

	var buf bytes.Buffer
	srv.sendAssetResponse(&buf, 304, item, true, "/assets/x.png", false)
	if firstLine(buf.String()) != "HTTP/1.1 304 Not Modified" {
		t.Errorf("expected '304 Not Modified' reason phrase, got %q", firstLine(buf.String()))
	}
}

func extractHeader(raw, key string) string {
	prefix := strings.ToLower(key) + ":"
	for _, line := range strings.Split(raw, "\r\n") {
		if strings.HasPrefix(strings.ToLower(line), prefix) {
			return strings.TrimSpace(line[len(prefix):])
		}
	}
	return ""
}

func countHeaderLines(raw, key string) int {
	cnt := 0
	prefix := strings.ToLower(key) + ":"
	for _, line := range strings.Split(raw, "\r\n") {
		if strings.HasPrefix(strings.ToLower(line), prefix) {
			cnt++
		}
	}
	return cnt
}

func TestWriteHTTPResponse_DateHeader_GeneratedWhenMissing(t *testing.T) {
	var buf bytes.Buffer
	writeHTTPResponse(&buf, http.StatusOK, nil, []byte("ok"), false, false)
	out := buf.String()

	dateVal := extractHeader(out, "Date")
	if dateVal == "" {
		t.Fatalf("expected Date header in response, got:\n%s", out)
	}
	parsedTime, err := http.ParseTime(dateVal)
	if err != nil {
		t.Fatalf("expected Date header to follow RFC 1123 GMT format, parse error: %v (val: %q)", err, dateVal)
	}
	if parsedTime.IsZero() {
		t.Fatalf("parsed Date header is zero time")
	}
	if countHeaderLines(out, "Date") != 1 {
		t.Errorf("expected exactly 1 Date header line, got %d", countHeaderLines(out, "Date"))
	}
}

func TestWriteHTTPResponse_DateHeader_PreserveUpstream(t *testing.T) {
	const upstreamDate = "Tue, 15 Nov 1994 08:12:31 GMT"
	hdr := make(http.Header)
	hdr.Set("Date", upstreamDate)

	var buf bytes.Buffer
	writeHTTPResponse(&buf, http.StatusOK, hdr, []byte("ok"), false, false)
	out := buf.String()

	dateVal := extractHeader(out, "Date")
	if dateVal != upstreamDate {
		t.Errorf("expected upstream Date %q preserved, got %q", upstreamDate, dateVal)
	}
	if countHeaderLines(out, "Date") != 1 {
		t.Errorf("expected exactly 1 Date header line, got %d", countHeaderLines(out, "Date"))
	}
}

func TestSendAssetResponse_DateHeader(t *testing.T) {
	tempDir := t.TempDir()
	cfgMgr := config.NewManager(filepath.Join(tempDir, "config.json"))
	srv := &ProxyServer{cfgMgr: cfgMgr}
	item := &cache.CacheItem{
		Key:         "assets/x.png",
		Data:        []byte("\x89PNG\r\n\x1a\n"),
		ContentType: "image/png",
	}

	var buf bytes.Buffer
	srv.sendAssetResponse(&buf, http.StatusOK, item, false, "/assets/x.png", false)
	out := buf.String()

	dateVal := extractHeader(out, "Date")
	if dateVal == "" {
		t.Fatalf("expected Date header in sendAssetResponse, got:\n%s", out)
	}
	if _, err := http.ParseTime(dateVal); err != nil {
		t.Errorf("expected valid RFC 1123 Date header in sendAssetResponse, got: %q (err: %v)", dateVal, err)
	}
}

func TestSendNotModifiedResponse_DateHeader(t *testing.T) {
	tempDir := t.TempDir()
	cfgMgr := config.NewManager(filepath.Join(tempDir, "config.json"))
	srv := &ProxyServer{cfgMgr: cfgMgr}
	item := &cache.CacheItem{
		Key:          "assets/x.png",
		ETag:         `"asset-etag"`,
		LastModified: "Wed, 21 Oct 2026 07:28:00 GMT",
	}

	var buf bytes.Buffer
	srv.sendNotModifiedResponse(&buf, item, "/assets/x.png", false)
	out := buf.String()

	dateVal := extractHeader(out, "Date")
	if dateVal == "" {
		t.Fatalf("expected Date header in sendNotModifiedResponse, got:\n%s", out)
	}
	if _, err := http.ParseTime(dateVal); err != nil {
		t.Errorf("expected valid RFC 1123 Date header in sendNotModifiedResponse, got: %q (err: %v)", dateVal, err)
	}
	if !strings.Contains(out, "ETag: \"asset-etag\"") {
		t.Errorf("expected ETag preserved in 304 response, got:\n%s", out)
	}
}
