package proxy

import (
	"bytes"
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
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

func setTestAssetClient(s *ProxyServer, client *http.Client) {
	s.assetClient.Store(client)
}

func setTestAPIClient(s *ProxyServer, client *http.Client) {
	s.apiClient.Store(client)
}

// extractHTTPResponseBody extracts the response payload after the HTTP header separator (\r\n\r\n).
func extractHTTPResponseBody(raw []byte) []byte {
	idx := bytes.Index(raw, []byte("\r\n\r\n"))
	if idx == -1 {
		return nil
	}
	return raw[idx+4:]
}

// TestProxy_FullLifecycleCacheIntegration exercises the complete asset cache lifecycle:
// Step 1: Cold Cache MISS -> Upstream fetch, 200 OK, Date header, ETag/Last-Modified/Content-Type/Cache-Control,
//         and verified async persistence of both payload file AND .ext metadata to disk and RAM.
// Step 2: RAM Cache HIT -> Served from memory, 200 OK, identical bytes, preserved metadata,
//         atomic RAMHits increment, zero additional upstream hits.
// Step 3: Conditional 304 -> Matching ETag / If-Modified-Since returns 304 Not Modified, Cache-Control & Date,
//         no Content-Length, no body, zero upstream hits.
// Step 4: Disk Cache HIT after RAM eviction -> Memory wiped, loaded from disk, re-cached in RAM with complete
//         metadata (ETag, Last-Modified, Content-Type), 200 OK, atomic DiskHits increment, zero upstream hits throughout.
func TestProxy_FullLifecycleCacheIntegration(t *testing.T) {
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
	defer srv.Stop()

	// Mock upstream HTTP server serving static asset with ETag and Last-Modified
	var upstreamHitCount atomic.Int64
	sampleBytes := []byte("\x89PNG\r\n\x1a\n\x00\x00\x00\rIHDR\x00\x00\x00\x01\x00\x00\x00\x01\x08\x06\x00\x00\x00\x1f\x15\xc4\x89full-lifecycle-sample")
	sampleETag := "\"sample-etag-v1\""
	sampleLastModified := "Wed, 21 Oct 2026 07:28:00 GMT"

	ts := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		upstreamHitCount.Add(1)
		w.Header().Set("Content-Type", "image/png")
		w.Header().Set("ETag", sampleETag)
		w.Header().Set("Last-Modified", sampleLastModified)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(sampleBytes)
	}))
	defer ts.Close()

	targetHost := "prd-game-a-granbluefantasy.akamaized.net"
	assetPath := "/assets/app/sample.png"

	// Configure asset client to route to mock upstream TLS server
	tr := ts.Client().Transport.(*http.Transport).Clone()
	tr.DialContext = func(ctx context.Context, network, addr string) (net.Conn, error) {
		return net.Dial("tcp", ts.Listener.Addr().String())
	}
	tr.TLSClientConfig.InsecureSkipVerify = true
	setTestAssetClient(srv, &http.Client{Transport: tr})

	// -------------------------------------------------------------
	// Step 1 (Cold Cache MISS):
	// Send GET request.
	// Verify client gets 200 OK with correct bytes, Date, ETag, Last-Modified,
	// Content-Type, and Cache-Control headers; upstream hit count == 1;
	// async save flushes both data file AND .ext metadata to disk and RAM.
	// -------------------------------------------------------------
	req1, err := http.NewRequest(http.MethodGet, "https://"+targetHost+assetPath, nil)
	if err != nil {
		t.Fatalf("failed to create req1: %v", err)
	}
	var buf1 bytes.Buffer
	srv.handleDecryptedRequest(&buf1, req1, targetHost)

	res1 := buf1.String()
	if !strings.HasPrefix(res1, "HTTP/1.1 200 OK") {
		t.Fatalf("Step 1 (Cold Cache MISS): expected HTTP/1.1 200 OK, got: %s", firstLine(res1))
	}
	dateVal1 := extractHeader(res1, "Date")
	if dateVal1 == "" {
		t.Fatalf("Step 1 (Cold Cache MISS): expected Date header in response, got:\n%s", res1)
	}
	if _, pErr := http.ParseTime(dateVal1); pErr != nil {
		t.Fatalf("Step 1 (Cold Cache MISS): invalid Date header %q: %v", dateVal1, pErr)
	}
	if ct1 := extractHeader(res1, "Content-Type"); ct1 != "image/png" {
		t.Fatalf("Step 1 (Cold Cache MISS): expected Content-Type image/png, got %q", ct1)
	}
	if et1 := extractHeader(res1, "ETag"); et1 != sampleETag {
		t.Fatalf("Step 1 (Cold Cache MISS): expected ETag %s, got %q", sampleETag, et1)
	}
	if lm1 := extractHeader(res1, "Last-Modified"); lm1 != sampleLastModified {
		t.Fatalf("Step 1 (Cold Cache MISS): expected Last-Modified %s, got %q", sampleLastModified, lm1)
	}
	if cc1 := extractHeader(res1, "Cache-Control"); cc1 != srv.getCacheControlHeader(assetPath) {
		t.Fatalf("Step 1 (Cold Cache MISS): expected Cache-Control %s, got %q", srv.getCacheControlHeader(assetPath), cc1)
	}
	body1 := extractHTTPResponseBody(buf1.Bytes())
	if !bytes.Equal(body1, sampleBytes) {
		t.Fatalf("Step 1 (Cold Cache MISS): body mismatch: expected %d bytes, got %d bytes", len(sampleBytes), len(body1))
	}
	if hits := upstreamHitCount.Load(); hits != 1 {
		t.Fatalf("Step 1 (Cold Cache MISS): expected upstream hit count == 1, got %d", hits)
	}

	// Verify async save flushes data file AND .ext metadata to disk and RAM
	diskPath := filepath.Join(tempDir, "assets", "app", "sample.png")
	extPath := diskPath + ".ext"
	var diskData []byte
	var extData []byte
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if d, rErr := os.ReadFile(diskPath); rErr == nil && len(d) > 0 {
			diskData = d
		}
		if ed, rErr := os.ReadFile(extPath); rErr == nil && len(ed) > 0 {
			extData = ed
		}
		if len(diskData) > 0 && len(extData) > 0 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if !bytes.Equal(diskData, sampleBytes) {
		t.Fatalf("Step 1 (Cold Cache MISS): expected flushed disk cache file to match sample bytes, got len %d", len(diskData))
	}
	if len(extData) == 0 {
		t.Fatalf("Step 1 (Cold Cache MISS): expected flushed .ext metadata file at %s, but file is missing or empty", extPath)
	}
	var meta map[string]interface{}
	if err := json.Unmarshal(extData, &meta); err != nil {
		t.Fatalf("Step 1 (Cold Cache MISS): failed to unmarshal .ext metadata: %v", err)
	}
	if meta["ETag"] != sampleETag {
		t.Fatalf("Step 1 (Cold Cache MISS): expected .ext ETag %s, got %v", sampleETag, meta["ETag"])
	}
	if meta["LastModified"] != sampleLastModified {
		t.Fatalf("Step 1 (Cold Cache MISS): expected .ext LastModified %s, got %v", sampleLastModified, meta["LastModified"])
	}
	if meta["ct"] != "image/png" {
		t.Fatalf("Step 1 (Cold Cache MISS): expected .ext ct image/png, got %v", meta["ct"])
	}

	ramItem, hitSrc := cacheMgr.GetWithNamespace("gbf", assetPath)
	if ramItem == nil || hitSrc != "RAM" || !bytes.Equal(ramItem.Data, sampleBytes) {
		t.Fatalf("Step 1 (Cold Cache MISS): expected RAM cache hit with sample bytes, got src=%q, item=%v", hitSrc, ramItem)
	}
	if ramItem.ETag != sampleETag {
		t.Fatalf("Step 1 (Cold Cache MISS): expected RAM item ETag %s, got %q", sampleETag, ramItem.ETag)
	}
	if ramItem.LastModified != sampleLastModified {
		t.Fatalf("Step 1 (Cold Cache MISS): expected RAM item LastModified %s, got %q", sampleLastModified, ramItem.LastModified)
	}

	// -------------------------------------------------------------
	// Step 2 (RAM Cache HIT):
	// Send GET request again.
	// Verify client gets 200 OK with identical bytes and preserved metadata;
	// RAM hit counter increments; upstream hit count stays at 1 (no re-fetch).
	// -------------------------------------------------------------
	ramHitsBefore := stats.RAMHits.Load()

	req2, err := http.NewRequest(http.MethodGet, "https://"+targetHost+assetPath, nil)
	if err != nil {
		t.Fatalf("failed to create req2: %v", err)
	}
	var buf2 bytes.Buffer
	srv.handleDecryptedRequest(&buf2, req2, targetHost)

	res2 := buf2.String()
	if !strings.HasPrefix(res2, "HTTP/1.1 200 OK") {
		t.Fatalf("Step 2 (RAM Cache HIT): expected HTTP/1.1 200 OK, got: %s", firstLine(res2))
	}
	dateVal2 := extractHeader(res2, "Date")
	if dateVal2 == "" {
		t.Fatalf("Step 2 (RAM Cache HIT): expected Date header in response, got:\n%s", res2)
	}
	if _, pErr := http.ParseTime(dateVal2); pErr != nil {
		t.Fatalf("Step 2 (RAM Cache HIT): invalid Date header %q: %v", dateVal2, pErr)
	}
	if et2 := extractHeader(res2, "ETag"); et2 != sampleETag {
		t.Fatalf("Step 2 (RAM Cache HIT): expected ETag %s, got %q", sampleETag, et2)
	}
	if lm2 := extractHeader(res2, "Last-Modified"); lm2 != sampleLastModified {
		t.Fatalf("Step 2 (RAM Cache HIT): expected Last-Modified %s, got %q", sampleLastModified, lm2)
	}
	if ct2 := extractHeader(res2, "Content-Type"); ct2 != "image/png" {
		t.Fatalf("Step 2 (RAM Cache HIT): expected Content-Type image/png, got %q", ct2)
	}
	body2 := extractHTTPResponseBody(buf2.Bytes())
	if !bytes.Equal(body2, sampleBytes) {
		t.Fatalf("Step 2 (RAM Cache HIT): body mismatch: expected %d bytes, got %d bytes", len(sampleBytes), len(body2))
	}
	if hits := upstreamHitCount.Load(); hits != 1 {
		t.Fatalf("Step 2 (RAM Cache HIT): upstream was re-fetched, hit count is %d (expected 1)", hits)
	}
	if ramHitsAfter := stats.RAMHits.Load(); ramHitsAfter != ramHitsBefore+1 {
		t.Fatalf("Step 2 (RAM Cache HIT): expected RAMHits to increase by 1, before=%d, after=%d", ramHitsBefore, ramHitsAfter)
	}

	// -------------------------------------------------------------
	// Step 3 (Conditional 304):
	// Send GET request with If-None-Match matching ETag.
	// Verify client gets 304 Not Modified with Cache-Control and Date,
	// no Content-Length, no body; upstream hit count stays at 1.
	// Also test If-Modified-Since matching Last-Modified.
	// -------------------------------------------------------------
	req3, err := http.NewRequest(http.MethodGet, "https://"+targetHost+assetPath, nil)
	if err != nil {
		t.Fatalf("failed to create req3: %v", err)
	}
	req3.Header.Set("If-None-Match", sampleETag)
	var buf3 bytes.Buffer
	srv.handleDecryptedRequest(&buf3, req3, targetHost)

	res3 := buf3.String()
	if !strings.HasPrefix(res3, "HTTP/1.1 304 Not Modified") {
		t.Fatalf("Step 3 (Conditional 304 - ETag): expected HTTP/1.1 304 Not Modified, got: %s", firstLine(res3))
	}
	if cc := extractHeader(res3, "Cache-Control"); cc == "" {
		t.Fatalf("Step 3 (Conditional 304 - ETag): expected Cache-Control header, got none:\n%s", res3)
	}
	d3 := extractHeader(res3, "Date")
	if d3 == "" {
		t.Fatalf("Step 3 (Conditional 304 - ETag): expected Date header, got none:\n%s", res3)
	}
	if _, pErr := http.ParseTime(d3); pErr != nil {
		t.Fatalf("Step 3 (Conditional 304 - ETag): invalid Date header %q: %v", d3, pErr)
	}
	if strings.Contains(res3, "Content-Length:") {
		t.Fatalf("Step 3 (Conditional 304 - ETag): 304 response must not carry Content-Length, got:\n%s", res3)
	}
	body3 := extractHTTPResponseBody(buf3.Bytes())
	if len(body3) > 0 {
		t.Fatalf("Step 3 (Conditional 304 - ETag): 304 response must have no body, got %d bytes", len(body3))
	}
	if hits := upstreamHitCount.Load(); hits != 1 {
		t.Fatalf("Step 3 (Conditional 304 - ETag): upstream was re-fetched, hit count is %d (expected 1)", hits)
	}

	// Also verify If-Modified-Since returns 304 Not Modified
	req3IMS, err := http.NewRequest(http.MethodGet, "https://"+targetHost+assetPath, nil)
	if err != nil {
		t.Fatalf("failed to create req3IMS: %v", err)
	}
	req3IMS.Header.Set("If-Modified-Since", sampleLastModified)
	var buf3IMS bytes.Buffer
	srv.handleDecryptedRequest(&buf3IMS, req3IMS, targetHost)
	res3IMS := buf3IMS.String()
	if !strings.HasPrefix(res3IMS, "HTTP/1.1 304 Not Modified") {
		t.Fatalf("Step 3 (Conditional 304 - IMS): expected HTTP/1.1 304 Not Modified, got: %s", firstLine(res3IMS))
	}
	if hits := upstreamHitCount.Load(); hits != 1 {
		t.Fatalf("Step 3 (Conditional 304 - IMS): upstream was re-fetched, hit count is %d (expected 1)", hits)
	}

	// -------------------------------------------------------------
	// Step 4 (Disk Cache HIT after RAM eviction):
	// Invalidate/evict the item from the RAM cache (simulate memory eviction
	// or reboot while disk remains intact). Send GET request again.
	// Verify asset is loaded from disk, re-cached into RAM with complete
	// metadata (ETag, Last-Modified, Content-Type); client gets 200 OK
	// with identical bytes; upstream hit count stays strictly at 1 throughout.
	// -------------------------------------------------------------
	diskHitsBefore := stats.DiskHits.Load()
	cacheMgr.ClearRAM()

	req4, err := http.NewRequest(http.MethodGet, "https://"+targetHost+assetPath, nil)
	if err != nil {
		t.Fatalf("failed to create req4: %v", err)
	}
	var buf4 bytes.Buffer
	srv.handleDecryptedRequest(&buf4, req4, targetHost)

	res4 := buf4.String()
	if !strings.HasPrefix(res4, "HTTP/1.1 200 OK") {
		t.Fatalf("Step 4 (Disk Cache HIT): expected HTTP/1.1 200 OK, got: %s", firstLine(res4))
	}
	dateVal4 := extractHeader(res4, "Date")
	if dateVal4 == "" {
		t.Fatalf("Step 4 (Disk Cache HIT): expected Date header in response, got:\n%s", res4)
	}
	if _, pErr := http.ParseTime(dateVal4); pErr != nil {
		t.Fatalf("Step 4 (Disk Cache HIT): invalid Date header %q: %v", dateVal4, pErr)
	}
	if et4 := extractHeader(res4, "ETag"); et4 != sampleETag {
		t.Fatalf("Step 4 (Disk Cache HIT): expected preserved ETag %s from disk, got %q", sampleETag, et4)
	}
	if lm4 := extractHeader(res4, "Last-Modified"); lm4 != sampleLastModified {
		t.Fatalf("Step 4 (Disk Cache HIT): expected preserved Last-Modified %s from disk, got %q", sampleLastModified, lm4)
	}
	if ct4 := extractHeader(res4, "Content-Type"); ct4 != "image/png" {
		t.Fatalf("Step 4 (Disk Cache HIT): expected preserved Content-Type image/png from disk, got %q", ct4)
	}
	body4 := extractHTTPResponseBody(buf4.Bytes())
	if !bytes.Equal(body4, sampleBytes) {
		t.Fatalf("Step 4 (Disk Cache HIT): body mismatch: expected %d bytes, got %d bytes", len(sampleBytes), len(body4))
	}

	diskHitsAfter := stats.DiskHits.Load()
	if diskHitsAfter != diskHitsBefore+1 {
		t.Fatalf("Step 4 (Disk Cache HIT): expected DiskHits to increase by 1, before=%d, after=%d", diskHitsBefore, diskHitsAfter)
	}

	// Verify asset was re-cached into RAM with all metadata
	itemAfter, srcAfter := cacheMgr.GetWithNamespace("gbf", assetPath)
	if itemAfter == nil || srcAfter != "RAM" || !bytes.Equal(itemAfter.Data, sampleBytes) {
		t.Fatalf("Step 4 (Disk Cache HIT): expected asset to be re-cached in RAM, got src=%q, item=%v", srcAfter, itemAfter)
	}
	if itemAfter.ETag != sampleETag {
		t.Fatalf("Step 4 (Disk Cache HIT): expected re-cached RAM item ETag %s, got %q", sampleETag, itemAfter.ETag)
	}
	if itemAfter.LastModified != sampleLastModified {
		t.Fatalf("Step 4 (Disk Cache HIT): expected re-cached RAM item LastModified %s, got %q", sampleLastModified, itemAfter.LastModified)
	}

	if hits := upstreamHitCount.Load(); hits != 1 {
		t.Fatalf("Step 4 (Disk Cache HIT): upstream hit count is %d (must strictly stay at 1)", hits)
	}

	// Verify conditional request after disk reload still returns 304 Not Modified with zero upstream hits
	req4Cond, err := http.NewRequest(http.MethodGet, "https://"+targetHost+assetPath, nil)
	if err != nil {
		t.Fatalf("failed to create req4Cond: %v", err)
	}
	req4Cond.Header.Set("If-None-Match", sampleETag)
	var buf4Cond bytes.Buffer
	srv.handleDecryptedRequest(&buf4Cond, req4Cond, targetHost)
	if !strings.HasPrefix(buf4Cond.String(), "HTTP/1.1 304 Not Modified") {
		t.Fatalf("Step 4 (Post-Disk Conditional 304): expected 304 Not Modified, got: %s", firstLine(buf4Cond.String()))
	}
	if hits := upstreamHitCount.Load(); hits != 1 {
		t.Fatalf("Step 4 (Post-Disk Conditional 304): upstream was contacted, count is %d (expected 1)", hits)
	}
}

// TestProxy_P0_PostWriteRequestsStrictZeroRetry validates the P0 safety rule:
// Write requests (POST/PUT/DELETE) that modify game state must NEVER be automatically
// retried under any circumstances, even when encountering connection aborts/resets.
// Only whitelisted read-only idempotent GET requests may attempt a single reconnect.
func TestProxy_P0_PostWriteRequestsStrictZeroRetry(t *testing.T) {
	tempDir := t.TempDir()
	cfgMgr := config.NewManager(filepath.Join(tempDir, "config.json"))
	certMgr, err := cert.NewManager(filepath.Join(tempDir, "certs"))
	if err != nil {
		t.Fatalf("failed to create cert manager: %v", err)
	}
	cacheMgr := cache.NewManager(tempDir, 16)
	defer cacheMgr.Close()
	stats := telemetry.NewStats()

	srv := NewProxyServer(cfgMgr, certMgr, cacheMgr, stats)
	defer srv.Stop()

	// Mock upstream HTTP server handling game write endpoints
	var upstreamAttempts atomic.Int64
	ts := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		upstreamAttempts.Add(1)
		// Upstream deliberately drops/aborts the connection
		hj, ok := w.(http.Hijacker)
		if !ok {
			http.Error(w, "hijack not supported", http.StatusInternalServerError)
			return
		}
		conn, _, hErr := hj.Hijack()
		if hErr != nil {
			return
		}
		_ = conn.Close() // immediately drop connection to simulate network abort
	}))
	defer ts.Close()

	targetHost := "game.granbluefantasy.jp"
	tr := ts.Client().Transport.(*http.Transport).Clone()
	tr.DialContext = func(ctx context.Context, network, addr string) (net.Conn, error) {
		return net.Dial("tcp", ts.Listener.Addr().String())
	}
	tr.TLSClientConfig.InsecureSkipVerify = true
	setTestAPIClient(srv, &http.Client{Transport: tr})

	// 1. Send POST write request with payload (e.g. /rest/multiraid/start.json)
	postPayload := []byte(`{"action":"start_raid","raid_id":"12345678"}`)
	req, err := http.NewRequest(http.MethodPost, "https://"+targetHost+"/rest/multiraid/start.json", bytes.NewReader(postPayload))
	if err != nil {
		t.Fatalf("failed to create POST request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")

	var buf bytes.Buffer
	srv.handleDecryptedRequest(&buf, req, targetHost)

	// Verify client receives 502 Bad Gateway
	respStr := buf.String()
	if !strings.HasPrefix(respStr, "HTTP/1.1 502 Bad Gateway") {
		t.Fatalf("expected HTTP/1.1 502 Bad Gateway, got: %s", firstLine(respStr))
	}

	// Crucially assert: Upstream server request counter is strictly 1 (upstreamAttempts == 1)
	// Zero retries occurred on the write request (physical validation of P0 zero retry rule).
	if attempts := upstreamAttempts.Load(); attempts != 1 {
		t.Fatalf("CRITICAL P0 VIOLATION: expected strictly 1 attempt on write request (zero retries), got %d attempts", attempts)
	}

	// Verify zero retries recorded in telemetry stats
	if retries := stats.APIRetries.Load(); retries != 0 {
		t.Fatalf("expected 0 api_retries in stats, got %d", retries)
	}

	// 2. Also verify another critical write endpoint: /rest/raid/ability_result.json
	upstreamAttempts.Store(0)
	reqAbility, err := http.NewRequest(http.MethodPost, "https://"+targetHost+"/rest/raid/ability_result.json", bytes.NewReader([]byte(`{"ability_id":1}`)))
	if err != nil {
		t.Fatalf("failed to create ability request: %v", err)
	}
	reqAbility.Header.Set("Content-Type", "application/json")

	var bufAbility bytes.Buffer
	srv.handleDecryptedRequest(&bufAbility, reqAbility, targetHost)

	if !strings.HasPrefix(bufAbility.String(), "HTTP/1.1 502 Bad Gateway") {
		t.Fatalf("expected HTTP/1.1 502 Bad Gateway for ability request, got: %s", firstLine(bufAbility.String()))
	}
	if attempts := upstreamAttempts.Load(); attempts != 1 {
		t.Fatalf("CRITICAL P0 VIOLATION: expected strictly 1 attempt on ability result write (zero retries), got %d attempts", attempts)
	}
	if retries := stats.APIRetries.Load(); retries != 0 {
		t.Fatalf("expected 0 api_retries in stats after ability POST, got %d", retries)
	}

	// 3. P0 verification on PUT and DELETE write methods
	upstreamAttempts.Store(0)
	reqPut, err := http.NewRequest(http.MethodPut, "https://"+targetHost+"/rest/user/status_update.json", bytes.NewReader([]byte(`{"status":"online"}`)))
	if err != nil {
		t.Fatalf("failed to create PUT request: %v", err)
	}
	var bufPut bytes.Buffer
	srv.handleDecryptedRequest(&bufPut, reqPut, targetHost)
	if !strings.HasPrefix(bufPut.String(), "HTTP/1.1 502 Bad Gateway") {
		t.Fatalf("expected HTTP/1.1 502 Bad Gateway for PUT write, got: %s", firstLine(bufPut.String()))
	}
	if attempts := upstreamAttempts.Load(); attempts != 1 {
		t.Fatalf("CRITICAL P0 VIOLATION: expected strictly 1 attempt on PUT write (zero retries), got %d attempts", attempts)
	}
	if retries := stats.APIRetries.Load(); retries != 0 {
		t.Fatalf("expected 0 api_retries in stats after PUT write, got %d", retries)
	}

	upstreamAttempts.Store(0)
	reqDel, err := http.NewRequest(http.MethodDelete, "https://"+targetHost+"/rest/party/deck_delete.json", nil)
	if err != nil {
		t.Fatalf("failed to create DELETE request: %v", err)
	}
	var bufDel bytes.Buffer
	srv.handleDecryptedRequest(&bufDel, reqDel, targetHost)
	if !strings.HasPrefix(bufDel.String(), "HTTP/1.1 502 Bad Gateway") {
		t.Fatalf("expected HTTP/1.1 502 Bad Gateway for DELETE write, got: %s", firstLine(bufDel.String()))
	}
	if attempts := upstreamAttempts.Load(); attempts != 1 {
		t.Fatalf("CRITICAL P0 VIOLATION: expected strictly 1 attempt on DELETE write (zero retries), got %d attempts", attempts)
	}
	if retries := stats.APIRetries.Load(); retries != 0 {
		t.Fatalf("expected 0 api_retries in stats after DELETE write, got %d", retries)
	}

	// 4. Contrast check: Prove that a whitelisted retryable GET request to /rest/multiraid/condition.json
	// DOES retry when a connection is dropped on the first attempt, confirming the test harness detects retries,
	// AND verify that stats.APIRetries accurately increments to 1.
	var getAttempts atomic.Int64
	tsGet := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		att := getAttempts.Add(1)
		if att == 1 {
			// Drop first attempt
			if hj, ok := w.(http.Hijacker); ok {
				if conn, _, err := hj.Hijack(); err == nil {
					_ = conn.Close()
					return
				}
			}
		}
		// Second attempt succeeds
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"result":"condition_ok"}`))
	}))
	defer tsGet.Close()

	trGet := tsGet.Client().Transport.(*http.Transport).Clone()
	trGet.DialContext = func(ctx context.Context, network, addr string) (net.Conn, error) {
		return net.Dial("tcp", tsGet.Listener.Addr().String())
	}
	trGet.TLSClientConfig.InsecureSkipVerify = true
	setTestAPIClient(srv, &http.Client{Transport: trGet})

	reqGet, err := http.NewRequest(http.MethodGet, "https://"+targetHost+"/rest/multiraid/condition.json", nil)
	if err != nil {
		t.Fatalf("failed to create GET request: %v", err)
	}
	var bufGet bytes.Buffer
	srv.handleDecryptedRequest(&bufGet, reqGet, targetHost)

	if !strings.HasPrefix(bufGet.String(), "HTTP/1.1 200 OK") {
		t.Fatalf("expected retryable GET to succeed with 200 OK on second attempt, got: %s", firstLine(bufGet.String()))
	}
	if att := getAttempts.Load(); att != 2 {
		t.Fatalf("expected retryable GET to perform exactly 2 attempts, got %d", att)
	}
	if retries := stats.APIRetries.Load(); retries != 1 {
		t.Fatalf("expected stats.APIRetries == 1 after successful GET retry, got %d", retries)
	}
}

// TestProxy_SingleFlight_HighConcurrencyCacheMissStampede verifies that when
// 50 concurrent client goroutines simultaneously request the exact same cold un-cached
// static asset, SingleFlight coalescing at the proxy layer coalesces them into
// strictly 1 upstream fetch, eliminating cache stampede (thundering herd), and all
// 50 callers receive 200 OK with byte-for-byte identical content.
func TestProxy_SingleFlight_HighConcurrencyCacheMissStampede(t *testing.T) {
	tempDir := t.TempDir()
	cfgMgr := config.NewManager(filepath.Join(tempDir, "config.json"))
	certMgr, err := cert.NewManager(filepath.Join(tempDir, "certs"))
	if err != nil {
		t.Fatalf("failed to create cert manager: %v", err)
	}
	cacheMgr := cache.NewManager(tempDir, 16)
	defer cacheMgr.Close()
	stats := telemetry.NewStats()

	srv := NewProxyServer(cfgMgr, certMgr, cacheMgr, stats)
	defer srv.Stop()

	// Upstream mock server: introduces simulated processing latency (30ms)
	// so concurrent requests overlap in-flight.
	var upstreamRequests atomic.Int64
	sampleAssetData := []byte("\x89PNG\r\n\x1a\n\x00\x00\x00\rIHDR\x00\x00\x00\x01\x00\x00\x00\x01\x08\x06\x00\x00\x00\x1f\x15\xc4\x89stampede-payload-50-clients")

	ts := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		upstreamRequests.Add(1)
		time.Sleep(30 * time.Millisecond) // simulated latency
		w.Header().Set("Content-Type", "image/png")
		w.Header().Set("ETag", "\"stampede-etag-v1\"")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(sampleAssetData)
	}))
	defer ts.Close()

	targetHost := "prd-game-a-granbluefantasy.akamaized.net"
	assetPath := "/assets/app/concurrent_stampede.png"

	tr := ts.Client().Transport.(*http.Transport).Clone()
	tr.DialContext = func(ctx context.Context, network, addr string) (net.Conn, error) {
		return net.Dial("tcp", ts.Listener.Addr().String())
	}
	tr.TLSClientConfig.InsecureSkipVerify = true
	setTestAssetClient(srv, &http.Client{Transport: tr})

	// Launch 50 concurrent client goroutines simultaneously requesting
	// the exact same cold un-cached static asset
	const concurrency = 50
	var wg sync.WaitGroup
	var readyWg sync.WaitGroup
	startBarrier := make(chan struct{})

	type clientResp struct {
		statusLine string
		body       []byte
		err        error
	}
	results := make([]clientResp, concurrency)

	readyWg.Add(concurrency)
	wg.Add(concurrency)
	for i := 0; i < concurrency; i++ {
		idx := i
		go func() {
			defer wg.Done()
			readyWg.Done() // Signal that this goroutine is running and waiting at the barrier
			<-startBarrier // synchronize start of all 50 goroutines

			req, rErr := http.NewRequest(http.MethodGet, "https://"+targetHost+assetPath, nil)
			if rErr != nil {
				results[idx] = clientResp{err: rErr}
				return
			}
			var buf bytes.Buffer
			srv.handleDecryptedRequest(&buf, req, targetHost)

			respStr := buf.String()
			results[idx] = clientResp{
				statusLine: firstLine(respStr),
				body:       extractHTTPResponseBody(buf.Bytes()),
			}
		}()
	}

	// Ensure all 50 goroutines are scheduled and waiting at startBarrier before releasing
	readyWg.Wait()
	// Release all 50 goroutines at once
	close(startBarrier)
	wg.Wait()

	// Crucially assert: Upstream mock server was contacted strictly once
	// confirming SingleFlight coalescing at the proxy layer eliminates thundering herd.
	if reqCount := upstreamRequests.Load(); reqCount != 1 {
		t.Fatalf("CRITICAL: SingleFlight coalescing failed: upstream was contacted %d times (expected strictly 1)", reqCount)
	}

	// Verify all 50 goroutines received 200 OK and byte-for-byte identical content
	for i, res := range results {
		if res.err != nil {
			t.Fatalf("client %d encountered error: %v", i, res.err)
		}
		if !strings.HasPrefix(res.statusLine, "HTTP/1.1 200 OK") {
			t.Fatalf("client %d expected HTTP/1.1 200 OK, got: %q", i, res.statusLine)
		}
		if !bytes.Equal(res.body, sampleAssetData) {
			t.Fatalf("client %d received mismatched body (got %d bytes, expected %d bytes)", i, len(res.body), len(sampleAssetData))
		}
	}

	// Verify asset is saved in cache manager
	item, _ := cacheMgr.GetWithNamespace("gbf", assetPath)
	if item == nil || !bytes.Equal(item.Data, sampleAssetData) {
		t.Fatalf("expected asset to be cached in cache manager after stampede coalescing")
	}
}

// TestProxy_SingleFlight_HighConcurrencyCacheMissStampede_UpstreamError verifies that
// when 50 concurrent requests experience an upstream failure (e.g. 502 Bad Gateway), SingleFlight
// still executes only 1 upstream request, fans out 502 Bad Gateway to all 50 clients without
// panic or deadlock, and properly clears the flight key so subsequent attempts can re-fetch.
func TestProxy_SingleFlight_HighConcurrencyCacheMissStampede_UpstreamError(t *testing.T) {
	tempDir := t.TempDir()
	cfgMgr := config.NewManager(filepath.Join(tempDir, "config.json"))
	certMgr, err := cert.NewManager(filepath.Join(tempDir, "certs"))
	if err != nil {
		t.Fatalf("failed to create cert manager: %v", err)
	}
	cacheMgr := cache.NewManager(tempDir, 16)
	defer cacheMgr.Close()
	stats := telemetry.NewStats()

	srv := NewProxyServer(cfgMgr, certMgr, cacheMgr, stats)
	defer srv.Stop()

	var upstreamRequests atomic.Int64
	ts := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reqNum := upstreamRequests.Add(1)
		time.Sleep(25 * time.Millisecond) // simulated latency
		if reqNum == 1 {
			// First flight encounters 502 Bad Gateway
			w.WriteHeader(http.StatusBadGateway)
			_, _ = w.Write([]byte("Upstream Gateway Error"))
			return
		}
		// Subsequent request succeeds
		w.Header().Set("Content-Type", "image/png")
		w.WriteHeader(http.StatusOK)
		validRecoveryPNG := []byte("\x89PNG\r\n\x1a\n\x00\x00\x00\rIHDR\x00\x00\x00\x01\x00\x00\x00\x01\x08\x06\x00\x00\x00\x1f\x15\xc4\x89recovered-asset-bytes")
		_, _ = w.Write(validRecoveryPNG)
	}))
	defer ts.Close()

	targetHost := "prd-game-a-granbluefantasy.akamaized.net"
	assetPath := "/assets/app/stampede_error_recovery.png"

	tr := ts.Client().Transport.(*http.Transport).Clone()
	tr.DialContext = func(ctx context.Context, network, addr string) (net.Conn, error) {
		return net.Dial("tcp", ts.Listener.Addr().String())
	}
	tr.TLSClientConfig.InsecureSkipVerify = true
	setTestAssetClient(srv, &http.Client{Transport: tr})

	const concurrency = 50
	var wg sync.WaitGroup
	var readyWg sync.WaitGroup
	startBarrier := make(chan struct{})

	results := make([]string, concurrency)
	readyWg.Add(concurrency)
	wg.Add(concurrency)
	for i := 0; i < concurrency; i++ {
		idx := i
		go func() {
			defer wg.Done()
			readyWg.Done()
			<-startBarrier

			req, _ := http.NewRequest(http.MethodGet, "https://"+targetHost+assetPath, nil)
			var buf bytes.Buffer
			srv.handleDecryptedRequest(&buf, req, targetHost)
			results[idx] = firstLine(buf.String())
		}()
	}

	readyWg.Wait()
	close(startBarrier)
	wg.Wait()

	// Upstream should have been contacted exactly once during the 50-client stampede
	if reqCount := upstreamRequests.Load(); reqCount != 1 {
		t.Fatalf("expected strictly 1 upstream attempt during error stampede, got %d", reqCount)
	}

	// All 50 clients must receive 502 Bad Gateway
	for i, status := range results {
		if !strings.HasPrefix(status, "HTTP/1.1 502 Bad Gateway") {
			t.Fatalf("client %d expected 502 Bad Gateway, got: %q", i, status)
		}
	}

	// Verify that the flight key was properly cleared by SingleFlight:
	// A subsequent request must be able to try upstream again and succeed!
	reqRetry, _ := http.NewRequest(http.MethodGet, "https://"+targetHost+assetPath, nil)
	var bufRetry bytes.Buffer
	srv.handleDecryptedRequest(&bufRetry, reqRetry, targetHost)
	if !strings.HasPrefix(bufRetry.String(), "HTTP/1.1 200 OK") {
		t.Fatalf("expected subsequent request after error flight to succeed with 200 OK, got: %s", firstLine(bufRetry.String()))
	}
	if reqCount := upstreamRequests.Load(); reqCount != 2 {
		t.Fatalf("expected upstreamRequests to be 2 after recovery fetch, got %d", reqCount)
	}
}

func TestDynamicAPIFailoverOnNonRetryableGET(t *testing.T) {
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
	defer srv.Stop()

	// Closed listener for primary to immediately cause connection refused
	deadListener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("failed to listen: %v", err)
	}
	deadAddr := "http://" + deadListener.Addr().String()
	_ = deadListener.Close() // close immediately so connect fails

	// Update config with failover enabled
	cfg := cfgMgr.Get()
	cfg.EnableUpstreamFailover = true
	cfg.UpstreamProxy = deadAddr
	cfg.BackupUpstreamProxy = "http://127.0.0.1:8123"
	cfg.UpstreamFailoverConsecutiveFailures = 2
	cfg.UpstreamFailoverThresholdMS = 2000
	srv.updateClients(&cfg)

	// Non-retryable dynamic GET API path
	targetHost := "game.granbluefantasy.jp"
	nonRetryablePath := "/rest/user/status"
	if isRetryableAPI(nonRetryablePath) {
		t.Fatalf("%s must NOT be in retryable API whitelist", nonRetryablePath)
	}

	// Request 1: fails via primary (dead, connection refused) — hard errors
	// bypass the consecutive counter and trigger immediate failover.
	req1, _ := http.NewRequest(http.MethodGet, "https://"+targetHost+nonRetryablePath, nil)
	var buf1 bytes.Buffer
	srv.handleDecryptedRequest(&buf1, req1, targetHost)
	if !strings.HasPrefix(buf1.String(), "HTTP/1.1 502 Bad Gateway") {
		t.Fatalf("expected 502 Bad Gateway, got: %s", firstLine(buf1.String()))
	}
	st1 := srv.GetUpstreamStatus()
	if st1.Active != "backup" {
		t.Fatalf("expected immediate failover to backup on hard connection error, got active=%s", st1.Active)
	}
	if st1.Reason != "connection_error" {
		t.Fatalf("expected reason connection_error, got %s", st1.Reason)
	}
}

func TestDynamicAPIInFlightWatchdogFailover(t *testing.T) {
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
	defer srv.Stop()

	// Slow primary proxy: hangs for 300ms
	slowPrimary := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(300 * time.Millisecond)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("slow ok"))
	}))
	defer slowPrimary.Close()

	// Configure proxy with 60ms threshold and consecutive=1
	cfg := cfgMgr.Get()
	cfg.EnableUpstreamFailover = true
	cfg.UpstreamProxy = slowPrimary.URL
	cfg.BackupUpstreamProxy = "http://127.0.0.1:8123"
	cfg.UpstreamFailoverConsecutiveFailures = 1
	cfg.UpstreamFailoverThresholdMS = 60
	srv.updateClients(&cfg)

	targetHost := "game.granbluefantasy.jp"
	nonRetryablePath := "/rest/user/status"

	reqDone := make(chan struct{})
	go func() {
		defer close(reqDone)
		req, _ := http.NewRequest(http.MethodGet, "https://"+targetHost+nonRetryablePath, nil)
		var buf bytes.Buffer
		srv.handleDecryptedRequest(&buf, req, targetHost)
	}()

	// Wait 120ms (more than threshold 60ms, less than server response 300ms)
	time.Sleep(120 * time.Millisecond)

	// In-flight watchdog MUST have fired and switched active route to backup while request is still pending!
	st := srv.GetUpstreamStatus()
	if st.Active != "backup" {
		t.Fatalf("expected active backup while request in-flight, got active=%s", st.Active)
	}
	if st.Reason != "latency_threshold" {
		t.Fatalf("expected reason latency_threshold, got %s", st.Reason)
	}

	// Wait for the slow request to finish
	select {
	case <-reqDone:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for slow request to complete")
	}

	// Route must remain backup
	stAfter := srv.GetUpstreamStatus()
	if stAfter.Active != "backup" {
		t.Fatalf("expected active to remain backup, got %s", stAfter.Active)
	}
}
