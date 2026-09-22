package proxy

import (
	"bufio"
	"bytes"
	"compress/gzip"
	"io"
	"net/http"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"

	"gbf-proxy/cache"
	"gbf-proxy/config"
	"gbf-proxy/telemetry"
)

// fastPathAssetPath is a realistic static asset path used by the cached-asset fast
// path suite.
const fastPathAssetPath = "/assets/js/1000/app_fastpath.js"

// newCachedAssetSerializationServer builds the minimal ProxyServer the cached-asset
// serializers need: the config manager supplies the browser cache policy.
func newCachedAssetSerializationServer(t *testing.T) *ProxyServer {
	t.Helper()
	cfgMgr := config.NewManager(filepath.Join(t.TempDir(), "config.json"))
	return &ProxyServer{cfgMgr: cfgMgr}
}

// sampleCachedAssetItem is a fully populated CacheItem (all optional metadata set).
func sampleCachedAssetItem() *cache.CacheItem {
	payload := []byte("/* cached asset payload for the fast path regression suite */\nvar fastpath = 1;\n")
	return &cache.CacheItem{
		Key:             strings.TrimPrefix(fastPathAssetPath, "/"),
		Data:            payload,
		ContentType:     "application/javascript",
		ContentEncoding: "gzip",
		ETag:            `"fastpath-etag-v1"`,
		LastModified:    "Wed, 21 Oct 2026 07:28:00 GMT",
		Size:            int64(len(payload)),
	}
}

// splitRawResponse returns the status line, the header lines (without the terminating
// blank line) and the payload of a raw HTTP/1.1 response.
func splitRawResponse(t *testing.T, raw []byte) (string, []string, []byte) {
	t.Helper()
	idx := bytes.Index(raw, []byte("\r\n\r\n"))
	if idx == -1 {
		t.Fatalf("raw response has no header terminator:\n%s", raw)
	}
	head := string(raw[:idx])
	sep := strings.Index(head, "\r\n")
	if sep == -1 {
		return head, nil, raw[idx+4:]
	}
	return head[:sep], strings.Split(head[sep+2:], "\r\n"), raw[idx+4:]
}

// sortedHeaderLinesExceptDate returns the sorted header lines of a raw response with
// the Date line removed. Line ORDER is deliberately not compared: the legacy path
// iterated a Go map (random order), so header ordering was never part of the contract.
func sortedHeaderLinesExceptDate(t *testing.T, raw []byte) []string {
	t.Helper()
	_, lines, _ := splitRawResponse(t, raw)
	out := make([]string, 0, len(lines))
	for _, line := range lines {
		if strings.HasPrefix(strings.ToLower(line), "date:") {
			continue
		}
		out = append(out, line)
	}
	sort.Strings(out)
	return out
}

// assertSameResponseAsLegacy compares a fast-path response against the legacy
// sendAssetResponse output: identical status line, identical header name/value pairs
// (ignoring Date, whose timestamp may tick between the two calls) and identical payload.
func assertSameResponseAsLegacy(t *testing.T, label string, legacyRaw, fastRaw []byte) {
	t.Helper()
	legacyStatus, _, legacyBody := splitRawResponse(t, legacyRaw)
	fastStatus, _, fastBody := splitRawResponse(t, fastRaw)
	if legacyStatus != fastStatus {
		t.Errorf("%s: status line mismatch: legacy %q, fast %q", label, legacyStatus, fastStatus)
	}

	legacyLines := sortedHeaderLinesExceptDate(t, legacyRaw)
	fastLines := sortedHeaderLinesExceptDate(t, fastRaw)
	if len(legacyLines) != len(fastLines) {
		t.Fatalf("%s: header line count mismatch: legacy %d, fast %d\nlegacy=%v\nfast=%v",
			label, len(legacyLines), len(fastLines), legacyLines, fastLines)
	}
	for i := range legacyLines {
		if legacyLines[i] != fastLines[i] {
			t.Errorf("%s: header mismatch at %d: legacy %q, fast %q\nlegacy=%v\nfast=%v",
				label, i, legacyLines[i], fastLines[i], legacyLines, fastLines)
		}
	}

	if !bytes.Equal(legacyBody, fastBody) {
		t.Errorf("%s: payload mismatch: legacy %d B, fast %d B", label, len(legacyBody), len(fastBody))
	}
}

// expectedCachedAssetHeaders is the exact cached-asset header contract: optional
// metadata is present only when the CacheItem field is non-empty, while
// Access-Control-Allow-Origin, Cache-Control and Content-Length are always present.
func expectedCachedAssetHeaders(t *testing.T, srv *ProxyServer, item *cache.CacheItem, urlPath string) map[string]string {
	t.Helper()
	expected := map[string]string{
		"Access-Control-Allow-Origin": "*",
		"Cache-Control":               srv.getCacheControlHeader(urlPath),
		"Content-Length":              strconv.Itoa(len(item.Data)),
	}
	if item.ContentType != "" {
		expected["Content-Type"] = item.ContentType
	}
	if item.ETag != "" {
		expected["ETag"] = item.ETag
	}
	if item.LastModified != "" {
		expected["Last-Modified"] = item.LastModified
	}
	if item.ContentEncoding != "" {
		expected["Content-Encoding"] = item.ContentEncoding
	}
	return expected
}

// assertCachedAssetHeaderContract asserts the emitted header block exactly: no missing
// header, no extra header, no duplicated header line, exactly one valid RFC 1123 Date
// line and Connection as the final header line.
func assertCachedAssetHeaderContract(t *testing.T, label string, raw []byte, expected map[string]string, wantConnection string) {
	t.Helper()
	_, lines, _ := splitRawResponse(t, raw)

	present := make(map[string]string, len(lines))
	for _, line := range lines {
		name, value, ok := strings.Cut(line, ": ")
		if !ok {
			t.Errorf("%s: malformed header line %q", label, line)
			continue
		}
		if _, dup := present[name]; dup {
			t.Errorf("%s: duplicated header line for %q", label, name)
		}
		present[name] = value
	}

	if countHeaderLines(string(raw), "Date") != 1 {
		t.Errorf("%s: expected exactly 1 Date header line, got %d", label, countHeaderLines(string(raw), "Date"))
	}
	if dateVal := extractHeader(string(raw), "Date"); dateVal == "" {
		t.Errorf("%s: expected a Date header, got none:\n%s", label, raw)
	} else if _, err := http.ParseTime(dateVal); err != nil {
		t.Errorf("%s: expected RFC 1123 Date header, got %q (err: %v)", label, dateVal, err)
	}

	for name, want := range expected {
		got, ok := present[name]
		if !ok {
			t.Errorf("%s: missing header %q (want %q)", label, name, want)
			continue
		}
		if got != want {
			t.Errorf("%s: header %q = %q, want %q", label, name, got, want)
		}
	}
	for name, got := range present {
		if name == "Date" || name == "Connection" {
			continue
		}
		if _, want := expected[name]; !want {
			t.Errorf("%s: unexpected header %q = %q", label, name, got)
		}
	}

	if len(lines) == 0 {
		t.Fatalf("%s: response carries no header lines:\n%s", label, raw)
	}
	if lines[len(lines)-1] != "Connection: "+wantConnection {
		t.Errorf("%s: expected Connection to be the final header line, got %q", label, lines[len(lines)-1])
	}
	if countHeaderLines(string(raw), "Connection") != 1 {
		t.Errorf("%s: expected exactly 1 Connection header line, got %d", label, countHeaderLines(string(raw), "Connection"))
	}
}

// The fast path must reproduce the legacy GET response exactly - status line, every
// header name/value, Content-Length, Connection and payload bytes - for both browser
// cache policy branches (unversioned max-age=3600 and versioned immutable).
func TestCachedAssetResponseFast_MatchesLegacyGET(t *testing.T) {
	srv := newCachedAssetSerializationServer(t)
	item := sampleCachedAssetItem()

	for _, urlPath := range []string{fastPathAssetPath, "/assets/1000/app_fastpath.js"} {
		var legacy, fast bytes.Buffer
		srv.sendAssetResponse(&legacy, http.StatusOK, item, false, urlPath, false)
		srv.sendCachedAssetResponseFast(&fast, item, false, urlPath, false)

		if got := firstLine(fast.String()); got != "HTTP/1.1 200 OK" {
			t.Errorf("%s: expected 'HTTP/1.1 200 OK', got %q", urlPath, got)
		}
		assertSameResponseAsLegacy(t, "GET "+urlPath, legacy.Bytes(), fast.Bytes())
		assertCachedAssetHeaderContract(t, "fast GET "+urlPath, fast.Bytes(),
			expectedCachedAssetHeaders(t, srv, item, urlPath), "keep-alive")

		if _, _, body := splitRawResponse(t, fast.Bytes()); !bytes.Equal(body, item.Data) {
			t.Errorf("%s: payload mismatch: got %d B, want %d B", urlPath, len(body), len(item.Data))
		}

		// A real HTTP client must be able to consume the fast-path bytes unchanged.
		resp, err := http.ReadResponse(bufio.NewReader(bytes.NewReader(fast.Bytes())), nil)
		if err != nil {
			t.Fatalf("%s: http.ReadResponse rejected the fast-path response: %v", urlPath, err)
		}
		readBody, readErr := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		if readErr != nil {
			t.Fatalf("%s: failed to read fast-path body: %v", urlPath, readErr)
		}
		if resp.StatusCode != http.StatusOK {
			t.Errorf("%s: status code = %d, want 200", urlPath, resp.StatusCode)
		}
		if resp.ContentLength != int64(len(item.Data)) {
			t.Errorf("%s: ContentLength = %d, want %d", urlPath, resp.ContentLength, len(item.Data))
		}
		if !bytes.Equal(readBody, item.Data) {
			t.Errorf("%s: parsed body mismatch: got %d B, want %d B", urlPath, len(readBody), len(item.Data))
		}
		if got := resp.Header.Get("ETag"); got != item.ETag {
			t.Errorf("%s: ETag = %q, want %q", urlPath, got, item.ETag)
		}
		if got := resp.Header.Get("Last-Modified"); got != item.LastModified {
			t.Errorf("%s: Last-Modified = %q, want %q", urlPath, got, item.LastModified)
		}
		if got := resp.Header.Get("Content-Encoding"); got != item.ContentEncoding {
			t.Errorf("%s: Content-Encoding = %q, want %q", urlPath, got, item.ContentEncoding)
		}
		if got := resp.Header.Get("Content-Type"); got != item.ContentType {
			t.Errorf("%s: Content-Type = %q, want %q", urlPath, got, item.ContentType)
		}
	}
}

// HEAD must keep the legacy contract: identical headers, Content-Length announcing the
// payload size and no payload bytes on the wire.
func TestCachedAssetResponseFast_MatchesLegacyHEAD(t *testing.T) {
	srv := newCachedAssetSerializationServer(t)
	item := sampleCachedAssetItem()
	urlPath := fastPathAssetPath

	var legacy, fast bytes.Buffer
	srv.sendAssetResponse(&legacy, http.StatusOK, item, true, urlPath, false)
	srv.sendCachedAssetResponseFast(&fast, item, true, urlPath, false)

	if got := firstLine(fast.String()); got != "HTTP/1.1 200 OK" {
		t.Errorf("expected 'HTTP/1.1 200 OK', got %q", got)
	}
	assertSameResponseAsLegacy(t, "HEAD", legacy.Bytes(), fast.Bytes())
	assertCachedAssetHeaderContract(t, "fast HEAD", fast.Bytes(),
		expectedCachedAssetHeaders(t, srv, item, urlPath), "keep-alive")

	if got := extractHeader(fast.String(), "Content-Length"); got != strconv.Itoa(len(item.Data)) {
		t.Errorf("HEAD Content-Length = %q, want %d", got, len(item.Data))
	}
	if _, _, body := splitRawResponse(t, fast.Bytes()); len(body) != 0 {
		t.Errorf("HEAD response must not carry a payload, got %d B", len(body))
	}

	resp, err := http.ReadResponse(bufio.NewReader(bytes.NewReader(fast.Bytes())), &http.Request{Method: http.MethodHead})
	if err != nil {
		t.Fatalf("http.ReadResponse rejected the fast-path HEAD response: %v", err)
	}
	readBody, readErr := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if readErr != nil {
		t.Fatalf("failed to read fast-path HEAD body: %v", readErr)
	}
	if len(readBody) != 0 {
		t.Errorf("HEAD response body must be empty, got %d B", len(readBody))
	}
	if resp.ContentLength != int64(len(item.Data)) {
		t.Errorf("HEAD ContentLength = %d, want %d", resp.ContentLength, len(item.Data))
	}
}

// CacheItems without optional metadata must omit exactly those header lines, keeping
// the header set identical to the legacy serializer.
func TestCachedAssetResponseFast_OmitsEmptyOptionalMetadata(t *testing.T) {
	srv := newCachedAssetSerializationServer(t)
	urlPath := "/assets/js/1000/minimal.js"
	item := &cache.CacheItem{
		Key:  "assets/js/1000/minimal.js",
		Data: []byte("var minimal = true;\n"),
	}

	var legacy, fast bytes.Buffer
	srv.sendAssetResponse(&legacy, http.StatusOK, item, false, urlPath, false)
	srv.sendCachedAssetResponseFast(&fast, item, false, urlPath, false)

	assertSameResponseAsLegacy(t, "GET minimal metadata", legacy.Bytes(), fast.Bytes())
	assertCachedAssetHeaderContract(t, "fast minimal metadata", fast.Bytes(),
		expectedCachedAssetHeaders(t, srv, item, urlPath), "keep-alive")

	for _, name := range []string{"Content-Type", "ETag", "Last-Modified", "Content-Encoding"} {
		if n := countHeaderLines(fast.String(), name); n != 0 {
			t.Errorf("expected no %s header for an empty field, got %d line(s):\n%s", name, n, fast.String())
		}
	}
}

// Connection handling is driven by req.Close and must stay identical to the legacy path.
func TestCachedAssetResponseFast_ConnectionClose(t *testing.T) {
	srv := newCachedAssetSerializationServer(t)
	item := sampleCachedAssetItem()
	urlPath := fastPathAssetPath

	var legacy, fast bytes.Buffer
	srv.sendAssetResponse(&legacy, http.StatusOK, item, false, urlPath, true)
	srv.sendCachedAssetResponseFast(&fast, item, false, urlPath, true)

	assertSameResponseAsLegacy(t, "GET connection close", legacy.Bytes(), fast.Bytes())
	assertCachedAssetHeaderContract(t, "fast connection close", fast.Bytes(),
		expectedCachedAssetHeaders(t, srv, item, urlPath), "close")
}

// End-to-end regression for the RAM hit path: the cached-asset fast path must serve the
// response without any upstream fetch, while the conditional 304 branch keeps using the
// untouched sendNotModifiedResponse serializer.
func TestCachedAssetFastPath_EndToEndRAMHit(t *testing.T) {
	tempDir := t.TempDir()
	cfgMgr := config.NewManager(filepath.Join(tempDir, "config.json"))
	cacheMgr := cache.NewManager(tempDir, 16)
	defer cacheMgr.Close()
	stats := telemetry.NewStats()
	defer stats.Close()

	// No asset client is wired on purpose: any upstream fetch attempt would surface as a
	// 502 response and fail the assertions below.
	srv := &ProxyServer{cfgMgr: cfgMgr, cacheMgr: cacheMgr, stats: stats}

	plain := []byte("/* gbf fast path e2e payload */\nvar e2e = true;\n")
	var gz bytes.Buffer
	zw := gzip.NewWriter(&gz)
	if _, err := zw.Write(plain); err != nil {
		t.Fatalf("failed to gzip the sample payload: %v", err)
	}
	if err := zw.Close(); err != nil {
		t.Fatalf("failed to finish the gzip sample payload: %v", err)
	}
	payload := gz.Bytes()

	headers := map[string]string{
		"content-type":     "application/javascript",
		"content-encoding": "gzip",
		"etag":             `"e2e-etag-v1"`,
		"last-modified":    "Wed, 21 Oct 2026 07:28:00 GMT",
	}
	savedItem, ok := cacheMgr.SaveRAM(fastPathAssetPath, headers, payload)
	if !ok || savedItem == nil {
		t.Fatalf("failed to seed the RAM cache with %s", fastPathAssetPath)
	}

	targetHost := "prd-game-a-granbluefantasy.akamaized.net"
	ramHitsBefore := stats.RAMHits.Load()

	// 1. Regular GET is served from RAM through the fast path.
	getReq, err := http.NewRequest(http.MethodGet, "https://"+targetHost+fastPathAssetPath, nil)
	if err != nil {
		t.Fatalf("failed to build the GET request: %v", err)
	}
	getReq.RequestURI = fastPathAssetPath
	var getBuf bytes.Buffer
	srv.handleDecryptedRequest(&getBuf, getReq, targetHost)

	if got := firstLine(getBuf.String()); got != "HTTP/1.1 200 OK" {
		t.Fatalf("RAM hit GET: expected 'HTTP/1.1 200 OK', got %q\n%s", got, getBuf.String())
	}
	assertCachedAssetHeaderContract(t, "E2E RAM hit GET", getBuf.Bytes(),
		expectedCachedAssetHeaders(t, srv, savedItem, fastPathAssetPath), "keep-alive")
	if _, _, body := splitRawResponse(t, getBuf.Bytes()); !bytes.Equal(body, payload) {
		t.Errorf("RAM hit GET: payload mismatch: got %d B, want %d B", len(body), len(payload))
	}

	// 2. HEAD advertises the cached length without sending a payload.
	headReq, err := http.NewRequest(http.MethodHead, "https://"+targetHost+fastPathAssetPath, nil)
	if err != nil {
		t.Fatalf("failed to build the HEAD request: %v", err)
	}
	headReq.RequestURI = fastPathAssetPath
	var headBuf bytes.Buffer
	srv.handleDecryptedRequest(&headBuf, headReq, targetHost)

	if got := firstLine(headBuf.String()); got != "HTTP/1.1 200 OK" {
		t.Fatalf("RAM hit HEAD: expected 'HTTP/1.1 200 OK', got %q\n%s", got, headBuf.String())
	}
	assertCachedAssetHeaderContract(t, "E2E RAM hit HEAD", headBuf.Bytes(),
		expectedCachedAssetHeaders(t, srv, savedItem, fastPathAssetPath), "keep-alive")
	if _, _, body := splitRawResponse(t, headBuf.Bytes()); len(body) != 0 {
		t.Errorf("RAM hit HEAD: response must not carry a payload, got %d B", len(body))
	}
	if got := extractHeader(headBuf.String(), "Content-Length"); got != strconv.Itoa(len(payload)) {
		t.Errorf("RAM hit HEAD: Content-Length = %q, want %d", got, len(payload))
	}

	// 3. Conditional GET still returns 304 through sendNotModifiedResponse.
	condReq, err := http.NewRequest(http.MethodGet, "https://"+targetHost+fastPathAssetPath, nil)
	if err != nil {
		t.Fatalf("failed to build the conditional GET request: %v", err)
	}
	condReq.RequestURI = fastPathAssetPath
	condReq.Header.Set("If-None-Match", `"e2e-etag-v1"`)
	var condBuf bytes.Buffer
	srv.handleDecryptedRequest(&condBuf, condReq, targetHost)

	condOut := condBuf.String()
	if got := firstLine(condOut); got != "HTTP/1.1 304 Not Modified" {
		t.Errorf("conditional GET: expected 'HTTP/1.1 304 Not Modified', got %q\n%s", got, condOut)
	}
	if n := countHeaderLines(condOut, "Content-Length"); n != 0 {
		t.Errorf("conditional GET: 304 must not carry Content-Length, got %d line(s):\n%s", n, condOut)
	}
	if _, _, body := splitRawResponse(t, condBuf.Bytes()); len(body) != 0 {
		t.Errorf("conditional GET: 304 must not carry a payload, got %d B", len(body))
	}
	if got := extractHeader(condOut, "ETag"); got != `"e2e-etag-v1"` {
		t.Errorf("conditional GET: ETag = %q, want %q", got, `"e2e-etag-v1"`)
	}

	// All three requests were answered locally from RAM (no upstream fetch).
	if ramHits := stats.RAMHits.Load(); ramHits != ramHitsBefore+3 {
		t.Errorf("expected 3 additional RAM hits, before=%d after=%d", ramHitsBefore, ramHits)
	}
}
