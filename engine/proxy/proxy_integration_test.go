package proxy

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
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
	if client != nil {
		if tr, ok := client.Transport.(*http.Transport); ok && tr != nil {
			wrapAPITransportSingleAttempt(tr)
		}
	}
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

func TestProxy_P0_TransportReusedConnZeroImplicitReplay(t *testing.T) {
	tempDir := t.TempDir()
	cfgMgr := config.NewManager(filepath.Join(tempDir, "config.json"))
	certMgr, err := cert.NewManager(filepath.Join(tempDir, "certs"))
	if err != nil {
		t.Fatalf("failed to create cert manager: %v", err)
	}
	cacheMgr := cache.NewManager(tempDir, 16)
	defer cacheMgr.Close()
	stats := telemetry.NewStats()
	defer stats.Close()

	srv := NewProxyServer(cfgMgr, certMgr, cacheMgr, stats)
	defer srv.Stop()

	var targetAttempts atomic.Int64
	var dropTarget atomic.Bool
	var lastMethod atomic.Value
	var lastContentLength atomic.Int64
	var lastChunked atomic.Bool
	var lastBody atomic.Value

	ts := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/rest/warmup" {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"warmup":true}`))
			return
		}

		att := targetAttempts.Add(1)
		b, _ := io.ReadAll(r.Body)
		lastMethod.Store(r.Method)
		lastContentLength.Store(r.ContentLength)
		lastChunked.Store(len(r.TransferEncoding) > 0)
		lastBody.Store(string(b))

		if dropTarget.Load() && att == 1 {
			if hj, ok := w.(http.Hijacker); ok {
				if conn, _, hErr := hj.Hijack(); hErr == nil {
					_ = conn.Close()
					return
				}
			}
		}

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer ts.Close()

	targetHost := "game.granbluefantasy.jp"
	tr := ts.Client().Transport.(*http.Transport).Clone()
	tr.MaxIdleConns = 10
	tr.MaxIdleConnsPerHost = 10
	tr.DialContext = func(ctx context.Context, network, addr string) (net.Conn, error) {
		return net.Dial("tcp", ts.Listener.Addr().String())
	}
	tr.TLSClientConfig.InsecureSkipVerify = true
	setTestAPIClient(srv, &http.Client{Transport: tr})

	warmIdleConn := func() {
		t.Helper()
		reqWarm, _ := http.NewRequest(http.MethodGet, "https://"+targetHost+"/rest/warmup", nil)
		var bufWarm bytes.Buffer
		srv.handleDecryptedRequest(&bufWarm, reqWarm, targetHost)
		if !strings.HasPrefix(bufWarm.String(), "HTTP/1.1 200 OK") {
			t.Fatalf("warmup request failed: %s", firstLine(bufWarm.String()))
		}
	}

	cases := []struct {
		name  string
		build func() *http.Request
		wantM string
		wantL int64
		wantB string
	}{
		{
			name: "empty_body_nil_post",
			build: func() *http.Request {
				r, _ := http.NewRequest(http.MethodPost, "https://"+targetHost+"/rest/raid/ability_result.json", nil)
				return r
			},
			wantM: http.MethodPost,
			wantL: 0,
			wantB: "",
		},
		{
			name: "empty_body_http_nobody_post",
			build: func() *http.Request {
				r, _ := http.NewRequest(http.MethodPost, "https://"+targetHost+"/rest/raid/ability_result.json", http.NoBody)
				return r
			},
			wantM: http.MethodPost,
			wantL: 0,
			wantB: "",
		},
		{
			name: "empty_body_idempotency_key_post",
			build: func() *http.Request {
				r, _ := http.NewRequest(http.MethodPost, "https://"+targetHost+"/rest/raid/ability_result.json", http.NoBody)
				r.Header.Set("Idempotency-Key", "idem-123")
				r.Header.Set("X-Idempotency-Key", "x-idem-123")
				return r
			},
			wantM: http.MethodPost,
			wantL: 0,
			wantB: "",
		},
		{
			name: "payload_idempotency_key_post",
			build: func() *http.Request {
				r, _ := http.NewRequest(http.MethodPost, "https://"+targetHost+"/rest/raid/ability_result.json", strings.NewReader(`{"ability_id":7}`))
				r.Header.Set("Idempotency-Key", "idem-456")
				r.Header.Set("Content-Type", "application/json")
				return r
			},
			wantM: http.MethodPost,
			wantL: int64(len(`{"ability_id":7}`)),
			wantB: `{"ability_id":7}`,
		},
		{
			name: "delete_empty_body",
			build: func() *http.Request {
				r, _ := http.NewRequest(http.MethodDelete, "https://"+targetHost+"/rest/party/deck_delete.json", nil)
				return r
			},
			wantM: http.MethodDelete,
			wantL: 0,
			wantB: "",
		},
		{
			name: "non_whitelisted_get_on_reused_conn",
			build: func() *http.Request {
				r, _ := http.NewRequest(http.MethodGet, "https://"+targetHost+"/rest/user/status", nil)
				return r
			},
			wantM: http.MethodGet,
			wantL: 0,
			wantB: "",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			warmIdleConn()
			targetAttempts.Store(0)
			dropTarget.Store(true)

			req := tc.build()
			var buf bytes.Buffer
			srv.handleDecryptedRequest(&buf, req, targetHost)

			if !strings.HasPrefix(buf.String(), "HTTP/1.1 502 Bad Gateway") {
				t.Fatalf("expected 502 Bad Gateway on broken reused connection, got: %s", firstLine(buf.String()))
			}
			if got := targetAttempts.Load(); got != 1 {
				t.Fatalf("P0 violation (%s): expected strictly 1 upstream attempt on reused connection failure, got %d", tc.name, got)
			}
			if gotM, _ := lastMethod.Load().(string); gotM != tc.wantM {
				t.Fatalf("expected method %s, got %s", tc.wantM, gotM)
			}
			if gotL := lastContentLength.Load(); gotL != tc.wantL {
				t.Fatalf("expected ContentLength %d, got %d", tc.wantL, gotL)
			}
			if lastChunked.Load() {
				t.Fatalf("unexpected chunked Transfer-Encoding on %s", tc.name)
			}
			if gotB, _ := lastBody.Load().(string); gotB != tc.wantB {
				t.Fatalf("expected body %q, got %q", tc.wantB, gotB)
			}
		})
	}
}

func TestProxy_StaticAssetRangeRequests_CacheMissAndHit(t *testing.T) {
	tempDir := t.TempDir()
	cfgMgr := config.NewManager(filepath.Join(tempDir, "config.json"))
	certMgr, err := cert.NewManager(filepath.Join(tempDir, "certs"))
	if err != nil {
		t.Fatalf("failed to create cert manager: %v", err)
	}
	cacheMgr := cache.NewManager(tempDir, 16)
	defer cacheMgr.Close()
	stats := telemetry.NewStats()
	defer stats.Close()

	srv := NewProxyServer(cfgMgr, certMgr, cacheMgr, stats)
	defer srv.Stop()

	fullAsset := []byte("\x89PNG\r\n\x1a\n0123456789abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ")
	const assetETag = `"range-etag-v1"`

	var upstreamRangeHits atomic.Int64
	var upstreamFullHits atomic.Int64
	var lastSeenIfRange atomic.Value

	ts := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "image/png")
		w.Header().Set("ETag", assetETag)
		w.Header().Set("Accept-Ranges", "bytes")
		if ir := r.Header.Get("If-Range"); ir != "" {
			lastSeenIfRange.Store(ir)
		}

		rangeHdr := r.Header.Get("Range")
		if rangeHdr != "" {
			upstreamRangeHits.Add(1)
			start, end, status := parseSingleByteRange(rangeHdr, int64(len(fullAsset)))
			if status != http.StatusPartialContent {
				w.Header().Set("Content-Range", fmt.Sprintf("bytes */%d", len(fullAsset)))
				w.WriteHeader(http.StatusRequestedRangeNotSatisfiable)
				return
			}
			slice := fullAsset[start : end+1]
			w.Header().Set("Content-Range", fmt.Sprintf("bytes %d-%d/%d", start, end, len(fullAsset)))
			w.Header().Set("Content-Length", fmt.Sprintf("%d", len(slice)))
			w.WriteHeader(http.StatusPartialContent)
			if r.Method != http.MethodHead {
				_, _ = w.Write(slice)
			}
			return
		}

		upstreamFullHits.Add(1)
		w.Header().Set("Content-Length", fmt.Sprintf("%d", len(fullAsset)))
		w.WriteHeader(http.StatusOK)
		if r.Method != http.MethodHead {
			_, _ = w.Write(fullAsset)
		}
	}))
	defer ts.Close()

	targetHost := "prd-game-a-granbluefantasy.akamaized.net"
	assetPath := "/assets/img/sp/range_test_asset.png"

	tr := ts.Client().Transport.(*http.Transport).Clone()
	tr.DialContext = func(ctx context.Context, network, addr string) (net.Conn, error) {
		return net.Dial("tcp", ts.Listener.Addr().String())
	}
	tr.TLSClientConfig.InsecureSkipVerify = true
	setTestAssetClient(srv, &http.Client{Transport: tr})

	doRangeReq := func(method, rangeVal, ifRangeVal string) *http.Response {
		t.Helper()
		req, rErr := http.NewRequest(method, "https://"+targetHost+assetPath, nil)
		if rErr != nil {
			t.Fatalf("NewRequest failed: %v", rErr)
		}
		if rangeVal != "" {
			req.Header.Set("Range", rangeVal)
		}
		if ifRangeVal != "" {
			req.Header.Set("If-Range", ifRangeVal)
		}
		var buf bytes.Buffer
		srv.handleDecryptedRequest(&buf, req, targetHost)
		resp, pErr := http.ReadResponse(bufio.NewReader(&buf), req)
		if pErr != nil {
			t.Fatalf("ReadResponse failed: %v (raw=%q)", pErr, buf.String())
		}
		return resp
	}

	// 1. Cold cache miss with Range: bytes=0-15 -> must return 206 Partial Content (NOT 502)
	resp1 := doRangeReq(http.MethodGet, "bytes=0-15", assetETag)
	body1, _ := io.ReadAll(resp1.Body)
	_ = resp1.Body.Close()
	if resp1.StatusCode != http.StatusPartialContent {
		t.Fatalf("cold miss Range expected 206 Partial Content, got %d", resp1.StatusCode)
	}
	wantCR1 := fmt.Sprintf("bytes 0-15/%d", len(fullAsset))
	if gotCR := resp1.Header.Get("Content-Range"); gotCR != wantCR1 {
		t.Fatalf("expected Content-Range %q, got %q", wantCR1, gotCR)
	}
	if !bytes.Equal(body1, fullAsset[:16]) {
		t.Fatalf("cold miss Range body mismatch: got %q, want %q", body1, fullAsset[:16])
	}
	if gotIR, _ := lastSeenIfRange.Load().(string); gotIR != assetETag {
		t.Fatalf("expected If-Range %q forwarded to upstream, got %q", assetETag, gotIR)
	}

	// Partial response must NOT pollute full-asset cache
	if item, _ := cacheMgr.GetWithNamespace("gbf", assetPath); item != nil {
		t.Fatalf("partial 206 response must not be stored as full asset in cache")
	}

	// 2. Cold cache miss HEAD with Range: bytes=0-15 -> 200 OK with full Content-Length and empty body (per RFC 9110, Range MUST be ignored on HEAD)
	respHeadMiss := doRangeReq(http.MethodHead, "bytes=0-15", "")
	bodyHeadMiss, _ := io.ReadAll(respHeadMiss.Body)
	_ = respHeadMiss.Body.Close()
	if respHeadMiss.StatusCode != http.StatusOK {
		t.Fatalf("cold miss HEAD Range expected 200, got %d", respHeadMiss.StatusCode)
	}
	if respHeadMiss.ContentLength != int64(len(fullAsset)) || len(bodyHeadMiss) != 0 {
		t.Fatalf("cold miss HEAD Range expected Content-Length=%d and empty body, got CL=%d bodyLen=%d", len(fullAsset), respHeadMiss.ContentLength, len(bodyHeadMiss))
	}
	cacheMgr.ClearRAM()

	// 3. Cold cache miss with out-of-bounds Range -> 416 Range Not Satisfiable
	resp416Miss := doRangeReq(http.MethodGet, "bytes=9999-10000", "")
	_ = resp416Miss.Body.Close()
	if resp416Miss.StatusCode != http.StatusRequestedRangeNotSatisfiable {
		t.Fatalf("cold miss invalid Range expected 416, got %d", resp416Miss.StatusCode)
	}

	// 4. Full GET populates the cache with the complete asset
	respFull := doRangeReq(http.MethodGet, "", "")
	bodyFull, _ := io.ReadAll(respFull.Body)
	_ = respFull.Body.Close()
	if respFull.StatusCode != http.StatusOK || !bytes.Equal(bodyFull, fullAsset) {
		t.Fatalf("full GET expected 200 OK with full asset (%d bytes), got status=%d len=%d", len(fullAsset), respFull.StatusCode, len(bodyFull))
	}
	rangeHitsBeforeCacheHit := upstreamRangeHits.Load()
	fullHitsBeforeCacheHit := upstreamFullHits.Load()

	// 5. Cache hit with Range: bytes=8-23 and matching If-Range -> served locally with 206
	respHitSlice := doRangeReq(http.MethodGet, "bytes=8-23", assetETag)
	bodyHitSlice, _ := io.ReadAll(respHitSlice.Body)
	_ = respHitSlice.Body.Close()
	if respHitSlice.StatusCode != http.StatusPartialContent {
		t.Fatalf("cache hit Range expected 206, got %d", respHitSlice.StatusCode)
	}
	wantCRHit := fmt.Sprintf("bytes 8-23/%d", len(fullAsset))
	if gotCR := respHitSlice.Header.Get("Content-Range"); gotCR != wantCRHit {
		t.Fatalf("cache hit expected Content-Range %q, got %q", wantCRHit, gotCR)
	}
	if respHitSlice.Header.Get("Accept-Ranges") != "bytes" {
		t.Fatalf("cache hit 206 expected Accept-Ranges: bytes, got %q", respHitSlice.Header.Get("Accept-Ranges"))
	}
	if !bytes.Equal(bodyHitSlice, fullAsset[8:24]) {
		t.Fatalf("cache hit Range slice mismatch: got %q, want %q", bodyHitSlice, fullAsset[8:24])
	}

	// 6. Cache hit with suffix Range: bytes=-10
	respSuffix := doRangeReq(http.MethodGet, "bytes=-10", "")
	bodySuffix, _ := io.ReadAll(respSuffix.Body)
	_ = respSuffix.Body.Close()
	if respSuffix.StatusCode != http.StatusPartialContent || !bytes.Equal(bodySuffix, fullAsset[len(fullAsset)-10:]) {
		t.Fatalf("cache hit suffix Range failed: status=%d body=%q", respSuffix.StatusCode, bodySuffix)
	}

	// 7. Cache hit with mismatched If-Range -> RFC 9110 requires 200 OK with full asset
	respMismatchIR := doRangeReq(http.MethodGet, "bytes=0-7", `"stale-etag"`)
	bodyMismatchIR, _ := io.ReadAll(respMismatchIR.Body)
	_ = respMismatchIR.Body.Close()
	if respMismatchIR.StatusCode != http.StatusOK || !bytes.Equal(bodyMismatchIR, fullAsset) {
		t.Fatalf("cache hit mismatched If-Range expected 200 OK full asset, got status=%d len=%d", respMismatchIR.StatusCode, len(bodyMismatchIR))
	}

	// 8. Cache hit with HEAD + Range: bytes=0-7 -> 200 OK with full Content-Length and empty body (per RFC 9110, Range MUST be ignored on HEAD)
	respHeadHit := doRangeReq(http.MethodHead, "bytes=0-7", "")
	bodyHeadHit, _ := io.ReadAll(respHeadHit.Body)
	_ = respHeadHit.Body.Close()
	if respHeadHit.StatusCode != http.StatusOK || respHeadHit.ContentLength != int64(len(fullAsset)) || len(bodyHeadHit) != 0 {
		t.Fatalf("cache hit HEAD Range expected 200 CL=%d empty body, got status=%d CL=%d len=%d", len(fullAsset), respHeadHit.StatusCode, respHeadHit.ContentLength, len(bodyHeadHit))
	}

	// 9. Cache hit with out-of-bounds Range -> 416 Range Not Satisfiable
	resp416Hit := doRangeReq(http.MethodGet, "bytes=5000-6000", "")
	_ = resp416Hit.Body.Close()
	if resp416Hit.StatusCode != http.StatusRequestedRangeNotSatisfiable {
		t.Fatalf("cache hit invalid Range expected 416, got %d", resp416Hit.StatusCode)
	}
	wantCR416 := fmt.Sprintf("bytes */%d", len(fullAsset))
	if gotCR := resp416Hit.Header.Get("Content-Range"); gotCR != wantCR416 {
		t.Fatalf("cache hit 416 expected Content-Range %q, got %q", wantCR416, gotCR)
	}

	// Confirm steps 5-9 were served entirely from cache without extra upstream calls
	if upstreamRangeHits.Load() != rangeHitsBeforeCacheHit || upstreamFullHits.Load() != fullHitsBeforeCacheHit {
		t.Fatalf("expected zero upstream calls during cache-hit Range tests")
	}
}

func TestProxy_SingleFlight_PrefetchAndCommitFailureFollowerResilience(t *testing.T) {
	t.Run("prefetch_late_foreground_follower_and_ram_commit_failure", func(t *testing.T) {
		tempDir := t.TempDir()
		cfgMgr := config.NewManager(filepath.Join(tempDir, "config.json"))
		certMgr, err := cert.NewManager(filepath.Join(tempDir, "certs"))
		if err != nil {
			t.Fatalf("failed to create cert manager: %v", err)
		}
		cacheMgr := cache.NewManager(tempDir, 16)
		defer cacheMgr.Close()
		stats := telemetry.NewStats()
		defer stats.Close()

		srv := NewProxyServer(cfgMgr, certMgr, cacheMgr, stats)
		defer srv.Stop()

		payload := []byte("\x89PNG\r\n\x1a\n\x00\x00\x00\rIHDR\x00\x00\x00\x01\x00\x00\x00\x01\x08\x06\x00\x00\x00\x1f\x15\xc4\x89prefetch-coalesce-payload")
		upstreamStarted := make(chan struct{})
		releaseUpstream := make(chan struct{})

		ts := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			select {
			case <-upstreamStarted:
			default:
				close(upstreamStarted)
			}
			<-releaseUpstream
			w.Header().Set("Content-Type", "image/png")
			w.Header().Set("Content-Length", fmt.Sprintf("%d", len(payload)))
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write(payload)
		}))
		defer ts.Close()

		targetHost := "prd-game-a-granbluefantasy.akamaized.net"
		assetPath := "/assets/img/sp/prefetch_follower_race.png"

		tr := ts.Client().Transport.(*http.Transport).Clone()
		tr.DialContext = func(ctx context.Context, network, addr string) (net.Conn, error) {
			return net.Dial("tcp", ts.Listener.Addr().String())
		}
		tr.TLSClientConfig.InsecureSkipVerify = true
		setTestAssetClient(srv, &http.Client{Transport: tr})

		// Start prefetch worker directly so it enters sf.Do while Waiters() == 0
		var wg sync.WaitGroup
		wg.Add(1)
		go func() {
			defer wg.Done()
			srv.prefetch.processFetchItem(prefetchItem{
				host: targetHost,
				path: assetPath,
				prio: 1,
			})
		}()

		<-upstreamStarted

		// Disable RAM cache while prefetch is in-flight so Save* validated RAM write returns false
		cacheMgr.SetRAMEnabled(false)

		// Now launch a foreground request that joins the same SingleFlight key
		var fgBuf bytes.Buffer
		fgDone := make(chan struct{})
		go func() {
			defer close(fgDone)
			req, _ := http.NewRequest(http.MethodGet, "https://"+targetHost+assetPath, nil)
			srv.handleDecryptedRequest(&fgBuf, req, targetHost)
		}()

		// Give foreground request a moment to join SingleFlight, then release upstream
		time.Sleep(20 * time.Millisecond)
		close(releaseUpstream)

		<-fgDone
		wg.Wait()

		if !strings.HasPrefix(fgBuf.String(), "HTTP/1.1 200 OK") {
			t.Fatalf("foreground follower joined to prefetch expected 200 OK, got: %s", firstLine(fgBuf.String()))
		}
		if gotBody := extractHTTPResponseBody(fgBuf.Bytes()); !bytes.Equal(gotBody, payload) {
			t.Fatalf("foreground follower body mismatch: got %d bytes, want %d bytes", len(gotBody), len(payload))
		}
	})

	t.Run("disk_stream_commit_generation_change_follower_fallback", func(t *testing.T) {
		tempDir := t.TempDir()
		cfgMgr := config.NewManager(filepath.Join(tempDir, "config.json"))
		certMgr, err := cert.NewManager(filepath.Join(tempDir, "certs"))
		if err != nil {
			t.Fatalf("failed to create cert manager: %v", err)
		}
		cacheMgr := cache.NewManager(tempDir, 1) // 64KB per-file RAM threshold -> >64KB spills to disk temp file
		defer cacheMgr.Close()
		stats := telemetry.NewStats()
		defer stats.Close()

		srv := NewProxyServer(cfgMgr, certMgr, cacheMgr, stats)
		defer srv.Stop()

		const totalSize = 128 * 1024
		payload := make([]byte, totalSize)
		copy(payload, []byte("\x89PNG\r\n\x1a\n"))
		for i := 8; i < len(payload); i++ {
			payload[i] = byte(i % 251)
		}

		var upstreamCalls atomic.Int64
		leaderStreaming := make(chan struct{})
		allowFinishStream := make(chan struct{})

		ts := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			callNum := upstreamCalls.Add(1)
			w.Header().Set("Content-Type", "image/png")
			w.Header().Set("ETag", `"disk-commit-fallback-etag"`)
			w.Header().Set("Content-Length", fmt.Sprintf("%d", len(payload)))
			w.WriteHeader(http.StatusOK)

			if callNum == 1 {
				// Write first half so leader starts streaming to client and spills to disk temp file
				_, _ = w.Write(payload[:totalSize/2])
				if f, ok := w.(http.Flusher); ok {
					f.Flush()
				}
				close(leaderStreaming)
				<-allowFinishStream
				_, _ = w.Write(payload[totalSize/2:])
				return
			}
			_, _ = w.Write(payload)
		}))
		defer ts.Close()

		targetHost := "prd-game-a-granbluefantasy.akamaized.net"
		assetPath := "/assets/img/sp/disk_commit_gen_race.png"

		tr := ts.Client().Transport.(*http.Transport).Clone()
		tr.DialContext = func(ctx context.Context, network, addr string) (net.Conn, error) {
			return net.Dial("tcp", ts.Listener.Addr().String())
		}
		tr.TLSClientConfig.InsecureSkipVerify = true
		setTestAssetClient(srv, &http.Client{Transport: tr})

		var leaderBuf, followerBuf bytes.Buffer
		var wg sync.WaitGroup
		wg.Add(2)

		go func() {
			defer wg.Done()
			req, _ := http.NewRequest(http.MethodGet, "https://"+targetHost+assetPath, nil)
			srv.handleDecryptedRequest(&leaderBuf, req, targetHost)
		}()

		<-leaderStreaming

		// Follower joins SingleFlight while leader is mid-stream
		go func() {
			defer wg.Done()
			req, _ := http.NewRequest(http.MethodGet, "https://"+targetHost+assetPath, nil)
			srv.handleDecryptedRequest(&followerBuf, req, targetHost)
		}()

		time.Sleep(20 * time.Millisecond)
		// Trigger ClearAll while leader's disk temp file is open so CommitDiskTempFile fails due to clearGen mismatch!
		_, _ = cacheMgr.ClearAll()
		close(allowFinishStream)

		wg.Wait()

		// Both leader and follower must succeed with 200 OK and exact payload (no false 502)
		if !strings.HasPrefix(leaderBuf.String(), "HTTP/1.1 200 OK") {
			t.Fatalf("leader expected 200 OK, got: %s", firstLine(leaderBuf.String()))
		}
		if gotL := extractHTTPResponseBody(leaderBuf.Bytes()); !bytes.Equal(gotL, payload) {
			t.Fatalf("leader body mismatch: got %d bytes, want %d bytes", len(gotL), len(payload))
		}

		if !strings.HasPrefix(followerBuf.String(), "HTTP/1.1 200 OK") {
			t.Fatalf("follower expected 200 OK via fallback after CommitDiskTempFile gen mismatch, got: %s", firstLine(followerBuf.String()))
		}
		if gotF := extractHTTPResponseBody(followerBuf.Bytes()); !bytes.Equal(gotF, payload) {
			t.Fatalf("follower body mismatch: got %d bytes, want %d bytes", len(gotF), len(payload))
		}
		if upstreamCalls.Load() != 2 {
			t.Fatalf("expected 2 upstream calls (1 leader + 1 follower fallback), got %d", upstreamCalls.Load())
		}
	})

	t.Run("ram_stream_commit_failure_follower_gets_transient_item", func(t *testing.T) {
		tempDir := t.TempDir()
		cfgMgr := config.NewManager(filepath.Join(tempDir, "config.json"))
		certMgr, err := cert.NewManager(filepath.Join(tempDir, "certs"))
		if err != nil {
			t.Fatalf("failed to create cert manager: %v", err)
		}
		cacheMgr := cache.NewManager(tempDir, 16)
		defer cacheMgr.Close()
		stats := telemetry.NewStats()
		defer stats.Close()

		srv := NewProxyServer(cfgMgr, certMgr, cacheMgr, stats)
		defer srv.Stop()

		payload := make([]byte, 8192)
		copy(payload, []byte("\x89PNG\r\n\x1a\n"))
		for i := 8; i < len(payload); i++ {
			payload[i] = byte(i % 251)
		}

		var upstreamCalls atomic.Int64
		leaderStreaming := make(chan struct{})
		allowFinishStream := make(chan struct{})

		ts := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			upstreamCalls.Add(1)
			w.Header().Set("Content-Type", "image/png")
			w.Header().Set("ETag", `"ram-commit-fallback-etag"`)
			w.Header().Set("Content-Length", fmt.Sprintf("%d", len(payload)))
			w.WriteHeader(http.StatusOK)

			_, _ = w.Write(payload[:4096])
			if f, ok := w.(http.Flusher); ok {
				f.Flush()
			}
			select {
			case <-leaderStreaming:
			default:
				close(leaderStreaming)
			}
			<-allowFinishStream
			_, _ = w.Write(payload[4096:])
		}))
		defer ts.Close()

		targetHost := "prd-game-a-granbluefantasy.akamaized.net"
		assetPath := "/assets/img/sp/ram_commit_fail_race.png"

		tr := ts.Client().Transport.(*http.Transport).Clone()
		tr.DialContext = func(ctx context.Context, network, addr string) (net.Conn, error) {
			return net.Dial("tcp", ts.Listener.Addr().String())
		}
		tr.TLSClientConfig.InsecureSkipVerify = true
		setTestAssetClient(srv, &http.Client{Transport: tr})

		var leaderBuf, followerBuf bytes.Buffer
		var wg sync.WaitGroup
		wg.Add(2)

		go func() {
			defer wg.Done()
			req, _ := http.NewRequest(http.MethodGet, "https://"+targetHost+assetPath, nil)
			srv.handleDecryptedRequest(&leaderBuf, req, targetHost)
		}()

		<-leaderStreaming

		go func() {
			defer wg.Done()
			req, _ := http.NewRequest(http.MethodGet, "https://"+targetHost+assetPath, nil)
			srv.handleDecryptedRequest(&followerBuf, req, targetHost)
		}()

		time.Sleep(20 * time.Millisecond)
		// Disable RAM cache mid-stream so SaveRAMValidatedWithNamespace returns false after leader finishes streaming
		cacheMgr.SetRAMEnabled(false)
		close(allowFinishStream)

		wg.Wait()

		if !strings.HasPrefix(leaderBuf.String(), "HTTP/1.1 200 OK") {
			t.Fatalf("leader expected 200 OK, got: %s", firstLine(leaderBuf.String()))
		}
		if gotL := extractHTTPResponseBody(leaderBuf.Bytes()); !bytes.Equal(gotL, payload) {
			t.Fatalf("leader body mismatch: got %d bytes, want %d bytes", len(gotL), len(payload))
		}

		if !strings.HasPrefix(followerBuf.String(), "HTTP/1.1 200 OK") {
			t.Fatalf("follower expected 200 OK via transient CacheItem, got: %s", firstLine(followerBuf.String()))
		}
		if gotF := extractHTTPResponseBody(followerBuf.Bytes()); !bytes.Equal(gotF, payload) {
			t.Fatalf("follower body mismatch: got %d bytes, want %d bytes", len(gotF), len(payload))
		}
		if upstreamCalls.Load() != 1 {
			t.Fatalf("expected strictly 1 upstream call (follower served from in-memory transient CacheItem), got %d", upstreamCalls.Load())
		}
	})

	t.Run("large_file_clearall_multi_follower_coalesced_and_conditional_stream", func(t *testing.T) {
		tempDir := t.TempDir()
		cfgMgr := config.NewManager(filepath.Join(tempDir, "config.json"))
		certMgr, err := cert.NewManager(filepath.Join(tempDir, "certs"))
		if err != nil {
			t.Fatalf("failed to create cert manager: %v", err)
		}
		cacheMgr := cache.NewManager(tempDir, 1) // 64 KiB MaxRAMItemSize
		defer cacheMgr.Close()
		stats := telemetry.NewStats()
		defer stats.Close()

		srv := NewProxyServer(cfgMgr, certMgr, cacheMgr, stats)
		defer srv.Stop()

		const totalSize = 256 * 1024 // 4x MaxRAMItemSize
		payload := make([]byte, totalSize)
		copy(payload, []byte("\x89PNG\r\n\x1a\n"))
		for i := 8; i < len(payload); i++ {
			payload[i] = byte((i * 7) % 251)
		}

		var upstreamCalls atomic.Int64
		var condBodyBytesRead atomic.Int64
		leaderStreaming := make(chan struct{})
		allowFinishStream := make(chan struct{})

		ts := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == "/assets/img/sp/cond_304_test.png" {
				w.Header().Set("Content-Type", "image/png")
				w.Header().Set("ETag", `"cond-match-etag"`)
				w.Header().Set("Content-Length", fmt.Sprintf("%d", len(payload)))
				w.WriteHeader(http.StatusOK)
				if f, ok := w.(http.Flusher); ok {
					f.Flush()
				}
				// Wait briefly; if client closes body immediately on 304, avoid counting full body write
				time.Sleep(20 * time.Millisecond)
				n, wErr := w.Write(payload)
				if wErr == nil {
					condBodyBytesRead.Add(int64(n))
				}
				return
			}

			callNum := upstreamCalls.Add(1)
			w.Header().Set("Content-Type", "image/png")
			w.Header().Set("ETag", `"large-multi-follower-etag"`)
			w.Header().Set("Content-Length", fmt.Sprintf("%d", len(payload)))
			w.WriteHeader(http.StatusOK)

			if callNum == 1 {
				_, _ = w.Write(payload[:32*1024])
				if f, ok := w.(http.Flusher); ok {
					f.Flush()
				}
				close(leaderStreaming)
				<-allowFinishStream
				_, _ = w.Write(payload[32*1024:])
				return
			}
			_, _ = w.Write(payload)
		}))
		defer ts.Close()

		targetHost := "prd-game-a-granbluefantasy.akamaized.net"
		assetPath := "/assets/img/sp/large_multi_follower_gen_race.png"

		tr := ts.Client().Transport.(*http.Transport).Clone()
		tr.DialContext = func(ctx context.Context, network, addr string) (net.Conn, error) {
			return net.Dial("tcp", ts.Listener.Addr().String())
		}
		tr.TLSClientConfig.InsecureSkipVerify = true
		setTestAssetClient(srv, &http.Client{Transport: tr})

		const numFollowers = 8
		var leaderBuf bytes.Buffer
		followerBufs := make([]bytes.Buffer, numFollowers)
		var wg sync.WaitGroup
		wg.Add(1 + numFollowers)

		go func() {
			defer wg.Done()
			req, _ := http.NewRequest(http.MethodGet, "https://"+targetHost+assetPath, nil)
			srv.handleDecryptedRequest(&leaderBuf, req, targetHost)
		}()

		<-leaderStreaming

		for i := 0; i < numFollowers; i++ {
			idx := i
			go func() {
				defer wg.Done()
				req, _ := http.NewRequest(http.MethodGet, "https://"+targetHost+assetPath, nil)
				srv.handleDecryptedRequest(&followerBufs[idx], req, targetHost)
			}()
		}

		time.Sleep(25 * time.Millisecond)
		// ClearAll changes generation while leader is streaming -> CommitDiskTempFile fails
		_, _ = cacheMgr.ClearAll()
		close(allowFinishStream)

		wg.Wait()

		if !strings.HasPrefix(leaderBuf.String(), "HTTP/1.1 200 OK") {
			t.Fatalf("leader expected 200 OK, got: %s", firstLine(leaderBuf.String()))
		}
		if gotL := extractHTTPResponseBody(leaderBuf.Bytes()); !bytes.Equal(gotL, payload) {
			t.Fatalf("leader body mismatch: got %d, want %d", len(gotL), len(payload))
		}

		for i := 0; i < numFollowers; i++ {
			if !strings.HasPrefix(followerBufs[i].String(), "HTTP/1.1 200 OK") {
				t.Fatalf("follower %d expected 200 OK, got: %s", i, firstLine(followerBufs[i].String()))
			}
			if gotF := extractHTTPResponseBody(followerBufs[i].Bytes()); !bytes.Equal(gotF, payload) {
				t.Fatalf("follower %d body mismatch: got %d, want %d", i, len(gotF), len(payload))
			}
		}

		// All 8 followers must coalesce into 1 retry SingleFlight call (total upstream calls == 2, never 1+8=9)
		if upstreamCalls.Load() != 2 {
			t.Fatalf("expected 8 followers to coalesce into 1 retry upstream call (total=2), got %d", upstreamCalls.Load())
		}

		// Also verify conditional GET on cache miss: matching ETag returns 304 immediately
		var cond304Buf bytes.Buffer
		req304, _ := http.NewRequest(http.MethodGet, "https://"+targetHost+"/assets/img/sp/cond_304_test.png", nil)
		req304.Header.Set("If-None-Match", `"cond-match-etag"`)
		srv.handleDecryptedRequest(&cond304Buf, req304, targetHost)
		if !strings.HasPrefix(cond304Buf.String(), "HTTP/1.1 304 Not Modified") {
			t.Fatalf("expected 304 Not Modified on conditional cache miss with matching ETag, got: %s", firstLine(cond304Buf.String()))
		}
		if gotBody := extractHTTPResponseBody(cond304Buf.Bytes()); len(gotBody) != 0 {
			t.Fatalf("expected empty body on 304 response, got %d bytes", len(gotBody))
		}

		// Conditional GET on cache miss with non-matching ETag for large file (>MaxRAMItemSize) must stream & spill to disk
		_, _ = cacheMgr.ClearAll()
		var cond200Buf bytes.Buffer
		req200, _ := http.NewRequest(http.MethodGet, "https://"+targetHost+assetPath, nil)
		req200.Header.Set("If-None-Match", `"stale-etag"`)
		srv.handleDecryptedRequest(&cond200Buf, req200, targetHost)
		if !strings.HasPrefix(cond200Buf.String(), "HTTP/1.1 200 OK") {
			t.Fatalf("expected 200 OK on conditional cache miss with stale ETag, got: %s", firstLine(cond200Buf.String()))
		}
		if gotBody := extractHTTPResponseBody(cond200Buf.Bytes()); !bytes.Equal(gotBody, payload) {
			t.Fatalf("conditional GET 200 body mismatch: got %d, want %d", len(gotBody), len(payload))
		}
	})
}

type trackingRequestBody struct {
	io.Reader
	closed atomic.Bool
}

func (b *trackingRequestBody) Close() error {
	b.closed.Store(true)
	return nil
}

type failNextWriteConn struct {
	net.Conn
	failWrite *atomic.Bool
}

func (c *failNextWriteConn) Write(b []byte) (int, error) {
	if c.failWrite != nil && c.failWrite.Swap(false) {
		_ = c.Conn.Close()
		return 0, io.ErrClosedPipe
	}
	return c.Conn.Write(b)
}

// TestProxy_ZeroReplay_MultiIdleConnAndEmptyBodyPost verifies that under Go 1.27.1:
// 1. Empty-body POST (including with Idempotency-Key) is never replayed after WroteHeaders when a reused connection drops,
//    even when a second warm idle connection is sitting ready in the pool.
// 2. When multiple idle connections exist in the pool and the first idle connection fails before write (nothingWrittenError),
//    wrapAPITransportSingleAttempt aborts iteration 2 before getConn can pop the second idle connection.
// 3. Normal request semantics (URL, query, headers, Content-Length, body) and req.Body.Close() are preserved.
func TestProxy_ZeroReplay_MultiIdleConnAndEmptyBodyPost(t *testing.T) {
	tempDir := t.TempDir()
	cfgMgr := config.NewManager(filepath.Join(tempDir, "config.json"))
	certMgr, err := cert.NewManager(filepath.Join(tempDir, "certs"))
	if err != nil {
		t.Fatalf("failed to create cert manager: %v", err)
	}
	cacheMgr := cache.NewManager(tempDir, 16)
	defer cacheMgr.Close()
	stats := telemetry.NewStats()
	defer stats.Close()

	srv := NewProxyServer(cfgMgr, certMgr, cacheMgr, stats)
	defer srv.Stop()

	var dropNextAfterRead atomic.Bool
	var failNextConnWrite atomic.Bool
	var upstreamReceived atomic.Int64
	var warmBarrierGate atomic.Bool
	warmEntered := make(chan struct{}, 4)
	releaseWarm := make(chan struct{})

	var mu sync.Mutex
	var lastMethod, lastURI, lastIdempKey string
	var lastContentLen int64
	var lastBody []byte

	ts := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		_ = r.Body.Close()

		if r.URL.Path == "/rest/warmup" {
			if warmBarrierGate.Load() {
				warmEntered <- struct{}{}
				<-releaseWarm
			}
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"warmup":true}`))
			return
		}

		upstreamReceived.Add(1)
		mu.Lock()
		lastMethod = r.Method
		lastURI = r.URL.RequestURI()
		lastIdempKey = r.Header.Get("Idempotency-Key")
		lastContentLen = r.ContentLength
		lastBody = append([]byte(nil), body...)
		mu.Unlock()

		if dropNextAfterRead.Swap(false) {
			if hj, ok := w.(http.Hijacker); ok {
				if conn, _, hErr := hj.Hijack(); hErr == nil {
					_ = conn.Close()
					return
				}
			}
		}

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer ts.Close()

	apiHost := "game.granbluefantasy.jp"
	tr := ts.Client().Transport.(*http.Transport).Clone()
	tr.MaxIdleConns = 8
	tr.MaxIdleConnsPerHost = 4
	tr.IdleConnTimeout = 30 * time.Second
	tr.DialContext = func(ctx context.Context, network, addr string) (net.Conn, error) {
		c, dErr := net.Dial("tcp", ts.Listener.Addr().String())
		if dErr != nil {
			return nil, dErr
		}
		return &failNextWriteConn{Conn: c, failWrite: &failNextConnWrite}, nil
	}
	tr.TLSClientConfig.InsecureSkipVerify = true
	setTestAPIClient(srv, &http.Client{Transport: tr})

	warmTwoIdleConns := func() {
		t.Helper()
		releaseWarm = make(chan struct{})
		warmBarrierGate.Store(true)
		var wg sync.WaitGroup
		for i := 0; i < 2; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				req, _ := http.NewRequest(http.MethodGet, "https://"+apiHost+"/rest/warmup", nil)
				var buf bytes.Buffer
				srv.handleDecryptedRequest(&buf, req, apiHost)
			}()
		}
		<-warmEntered
		<-warmEntered
		warmBarrierGate.Store(false)
		close(releaseWarm)
		wg.Wait()
	}

	warmTwoIdleConns()

	// Verify normal request semantics and req.Body.Close()
	trackBody := &trackingRequestBody{Reader: bytes.NewReader([]byte(`{"normal":true}`))}
	reqNorm, _ := http.NewRequest(http.MethodPost, "https://"+apiHost+"/rest/raid/normal.json?foo=bar", trackBody)
	reqNorm.Header.Set("Idempotency-Key", "idemp-normal-1")
	var bufNorm bytes.Buffer
	srv.handleDecryptedRequest(&bufNorm, reqNorm, apiHost)
	if !trackBody.closed.Load() {
		t.Fatalf("expected incoming req.Body.Close() to be called by handleDynamicAPI")
	}
	if !strings.HasPrefix(bufNorm.String(), "HTTP/1.1 200 OK") {
		t.Fatalf("expected normal POST to succeed with 200 OK, got: %s", firstLine(bufNorm.String()))
	}
	mu.Lock()
	if lastMethod != http.MethodPost || lastURI != "/rest/raid/normal.json?foo=bar" || lastIdempKey != "idemp-normal-1" || string(lastBody) != `{"normal":true}` || lastContentLen != 15 {
		t.Fatalf("normal request semantics altered: method=%s uri=%s idemp=%s cl=%d body=%q", lastMethod, lastURI, lastIdempKey, lastContentLen, string(lastBody))
	}
	mu.Unlock()

	// Case 1: Post-write drop (after WroteHeaders) with 2 warm idle connections in pool:
	// Empty-body POST with Idempotency-Key must be sent at most once (never replayed onto 2nd idle conn).
	warmTwoIdleConns()
	beforeEmptyPost := upstreamReceived.Load()
	dropNextAfterRead.Store(true)
	reqEmptyPost, _ := http.NewRequest(http.MethodPost, "https://"+apiHost+"/rest/raid/empty_post.json", http.NoBody)
	reqEmptyPost.Header.Set("Idempotency-Key", "idemp-empty-post")
	var bufEmptyPost bytes.Buffer
	srv.handleDecryptedRequest(&bufEmptyPost, reqEmptyPost, apiHost)
	if !strings.HasPrefix(bufEmptyPost.String(), "HTTP/1.1 502 Bad Gateway") {
		t.Fatalf("expected 502 Bad Gateway on dropped empty-body POST, got: %s", firstLine(bufEmptyPost.String()))
	}
	if got := upstreamReceived.Load() - beforeEmptyPost; got != 1 {
		t.Fatalf("CRITICAL P0 VIOLATION: empty-body POST with Idempotency-Key was sent %d times (want 1)", got)
	}

	// Case 2: Pre-write failure (nothingWrittenError before WroteHeaders) with 2 warm idle connections in pool:
	// When the 1st idle connection fails on Write (0 bytes written), Transport must NOT pop the 2nd idle connection!
	warmTwoIdleConns()
	beforePreWriteFail := upstreamReceived.Load()
	failNextConnWrite.Store(true)
	reqPreWritePost, _ := http.NewRequest(http.MethodPost, "https://"+apiHost+"/rest/raid/prewrite_fail.json", http.NoBody)
	reqPreWritePost.Header.Set("Idempotency-Key", "idemp-prewrite-post")
	var bufPreWritePost bytes.Buffer
	srv.handleDecryptedRequest(&bufPreWritePost, reqPreWritePost, apiHost)
	if !strings.HasPrefix(bufPreWritePost.String(), "HTTP/1.1 502 Bad Gateway") {
		t.Fatalf("expected 502 Bad Gateway when first idle conn fails before write, got: %s", firstLine(bufPreWritePost.String()))
	}
	if got := upstreamReceived.Load() - beforePreWriteFail; got != 0 {
		t.Fatalf("CRITICAL P0 VIOLATION: pre-write failure on 1st idle conn replayed onto 2nd idle conn (%d upstream hits, want 0)", got)
	}

	// Case 3: Non-whitelisted GET with 2 warm idle connections on post-write drop -> sent at most once.
	warmTwoIdleConns()
	beforeNonRetryGet := upstreamReceived.Load()
	dropNextAfterRead.Store(true)
	reqNonRetryGet, _ := http.NewRequest(http.MethodGet, "https://"+apiHost+"/rest/user/status", nil)
	var bufNonRetryGet bytes.Buffer
	srv.handleDecryptedRequest(&bufNonRetryGet, reqNonRetryGet, apiHost)
	if !strings.HasPrefix(bufNonRetryGet.String(), "HTTP/1.1 502 Bad Gateway") {
		t.Fatalf("expected 502 Bad Gateway on dropped non-whitelisted GET, got: %s", firstLine(bufNonRetryGet.String()))
	}
	if got := upstreamReceived.Load() - beforeNonRetryGet; got != 1 {
		t.Fatalf("non-whitelisted GET was sent %d times (want 1)", got)
	}
}

// TestProxy_RangeDiskCacheMetadataFirstAndIsolation verifies:
// 1. Disk cache Range hits read metadata first: HEAD+Range, 304, 416 do not read the disk body into RAM,
//    and single-range 206 reads only the requested slice (or streams if > MaxRAMItemSize).
// 2. Cache-miss 206 Partial Content responses are never stored in the full-asset cache.
// 3. Upstream 200 OK responses to Range requests are returned as 200 OK without local re-slicing.
// 4. Unknown Range units, strong/weak ETag If-Range, and HTTP-date If-Range are handled per RFC 9110.
func TestProxy_RangeDiskCacheMetadataFirstAndIsolation(t *testing.T) {
	tempDir := t.TempDir()
	cfgMgr := config.NewManager(filepath.Join(tempDir, "config.json"))
	certMgr, err := cert.NewManager(filepath.Join(tempDir, "certs"))
	if err != nil {
		t.Fatalf("failed to create cert manager: %v", err)
	}
	// Use 64 MB cache so MaxRAMItemSize = 4 MiB, while MaxDiskDirectReadSize = 2 MiB
	cacheMgr := cache.NewManager(tempDir, 64)
	defer cacheMgr.Close()
	stats := telemetry.NewStats()
	defer stats.Close()

	srv := NewProxyServer(cfgMgr, certMgr, cacheMgr, stats)
	defer srv.Stop()

	// Create a 3 MiB PNG on disk (> 2 MiB MaxDiskDirectReadSize so disk hits never auto-promote to RAM)
	const diskAssetSize = 3 * 1024 * 1024
	diskPayload := make([]byte, diskAssetSize)
	copy(diskPayload, []byte("\x89PNG\r\n\x1a\n"))
	for i := 8; i < len(diskPayload); i++ {
		diskPayload[i] = byte((i * 13) % 253)
	}
	diskPath := "/assets/img/sp/large_disk_range.png"
	lastModHTTP := "Wed, 21 Oct 2015 07:28:00 GMT"
	strongETag := `"disk-range-etag-v1"`

	if !cacheMgr.SaveWithNamespace("gbf", diskPath, map[string]string{
		"content-type":  "image/png",
		"etag":          strongETag,
		"last-modified": lastModHTTP,
	}, diskPayload) {
		t.Fatalf("failed to save 3 MiB disk asset")
	}
	cacheMgr.ClearRAM()

	targetHost := "prd-game-a-granbluefantasy.akamaized.net"

	var upstreamMiss206Hits atomic.Int64
	var upstreamMiss200Hits atomic.Int64
	smallPNG := append([]byte("\x89PNG\r\n\x1a\n"), bytes.Repeat([]byte("A"), 120)...)

	ts := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/assets/img/sp/miss_206_no_cache_pollution.png":
			if r.Header.Get("Range") != "" {
				upstreamMiss206Hits.Add(1)
				// Even if upstream returns 206 starting at 0, it must NOT be stored as a full resource
				w.Header().Set("Content-Type", "image/png")
				w.Header().Set("Content-Range", fmt.Sprintf("bytes 0-31/%d", len(smallPNG)))
				w.Header().Set("Content-Length", "32")
				w.WriteHeader(http.StatusPartialContent)
				_, _ = w.Write(smallPNG[:32])
				return
			}
			upstreamMiss200Hits.Add(1)
			w.Header().Set("Content-Type", "image/png")
			w.Header().Set("Content-Length", fmt.Sprintf("%d", len(smallPNG)))
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write(smallPNG)

		case "/assets/img/sp/head_range_miss_sat.png", "/assets/img/sp/head_range_miss_unsat.png":
			w.Header().Set("Content-Type", "image/png")
			w.Header().Set("Content-Length", fmt.Sprintf("%d", len(smallPNG)))
			w.WriteHeader(http.StatusOK)
			if r.Method != http.MethodHead {
				_, _ = w.Write(smallPNG)
			}

		case "/assets/img/sp/head_range_miss_large_sat.png", "/assets/img/sp/head_range_miss_large_unsat.png":
			largeMissPNG := make([]byte, 5*1024*1024)
			copy(largeMissPNG, []byte("\x89PNG\r\n\x1a\n"))
			w.Header().Set("Content-Type", "image/png")
			w.Header().Set("Content-Length", fmt.Sprintf("%d", len(largeMissPNG)))
			w.WriteHeader(http.StatusOK)
			if r.Method != http.MethodHead {
				_, _ = w.Write(largeMissPNG)
			}

		case "/assets/img/sp/upstream_returns_200_for_range.png":
			// Upstream ignores Range (e.g. If-Range mismatch on origin) and returns 200 OK full resource
			w.Header().Set("Content-Type", "image/png")
			w.Header().Set("ETag", `"origin-new-etag"`)
			w.Header().Set("Content-Length", fmt.Sprintf("%d", len(smallPNG)))
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write(smallPNG)
		default:
			t.Errorf("unexpected upstream request for %s", r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer ts.Close()

	tr := ts.Client().Transport.(*http.Transport).Clone()
	tr.DialContext = func(ctx context.Context, network, addr string) (net.Conn, error) {
		return net.Dial("tcp", ts.Listener.Addr().String())
	}
	tr.TLSClientConfig.InsecureSkipVerify = true
	setTestAssetClient(srv, &http.Client{Transport: tr})

	doReq := func(method, path string, hdrs map[string]string) *http.Response {
		req, _ := http.NewRequest(method, "https://"+targetHost+path, nil)
		for k, v := range hdrs {
			req.Header.Set(k, v)
		}
		var raw bytes.Buffer
		srv.handleDecryptedRequest(&raw, req, targetHost)
		resp, err := http.ReadResponse(bufio.NewReader(&raw), req)
		if err != nil {
			t.Fatalf("failed to parse response for %s %s: %v", method, path, err)
		}
		return resp
	}

	// 1a. Cache hit: HEAD + satisfiable Range on 3 MiB Disk asset -> 200 OK, full CL, empty body
	respHeadHitSat := doReq(http.MethodHead, diskPath, map[string]string{"Range": "bytes=100-163"})
	bodyHeadHitSat, _ := io.ReadAll(respHeadHitSat.Body)
	_ = respHeadHitSat.Body.Close()
	if respHeadHitSat.StatusCode != http.StatusOK || respHeadHitSat.ContentLength != diskAssetSize || len(bodyHeadHitSat) != 0 {
		t.Fatalf("HEAD+Range disk hit (satisfiable) failed: status=%d CL=%d bodyLen=%d", respHeadHitSat.StatusCode, respHeadHitSat.ContentLength, len(bodyHeadHitSat))
	}

	// 1b. Cache hit: HEAD + unsatisfiable Range on 3 MiB Disk asset -> 200 OK, full CL, empty body (Range ignored on HEAD, never 416)
	respHeadHitUnsat := doReq(http.MethodHead, diskPath, map[string]string{"Range": "bytes=9000000-9000100"})
	bodyHeadHitUnsat, _ := io.ReadAll(respHeadHitUnsat.Body)
	_ = respHeadHitUnsat.Body.Close()
	if respHeadHitUnsat.StatusCode != http.StatusOK || respHeadHitUnsat.ContentLength != diskAssetSize || len(bodyHeadHitUnsat) != 0 {
		t.Fatalf("HEAD+Range disk hit (unsatisfiable) failed: status=%d CL=%d bodyLen=%d", respHeadHitUnsat.StatusCode, respHeadHitUnsat.ContentLength, len(bodyHeadHitUnsat))
	}

	// 1c. Cache miss: HEAD + satisfiable Range -> 200 OK, full CL, empty body
	respHeadMissSat := doReq(http.MethodHead, "/assets/img/sp/head_range_miss_sat.png", map[string]string{"Range": "bytes=0-15"})
	bodyHeadMissSat, _ := io.ReadAll(respHeadMissSat.Body)
	_ = respHeadMissSat.Body.Close()
	if respHeadMissSat.StatusCode != http.StatusOK || respHeadMissSat.ContentLength != int64(len(smallPNG)) || len(bodyHeadMissSat) != 0 {
		t.Fatalf("HEAD+Range miss (satisfiable) failed: status=%d CL=%d bodyLen=%d", respHeadMissSat.StatusCode, respHeadMissSat.ContentLength, len(bodyHeadMissSat))
	}

	// 1d. Cache miss: HEAD + unsatisfiable Range -> 200 OK, full CL, empty body (never 416)
	respHeadMissUnsat := doReq(http.MethodHead, "/assets/img/sp/head_range_miss_unsat.png", map[string]string{"Range": "bytes=9000-10000"})
	bodyHeadMissUnsat, _ := io.ReadAll(respHeadMissUnsat.Body)
	_ = respHeadMissUnsat.Body.Close()
	if respHeadMissUnsat.StatusCode != http.StatusOK || respHeadMissUnsat.ContentLength != int64(len(smallPNG)) || len(bodyHeadMissUnsat) != 0 {
		t.Fatalf("HEAD+Range miss (unsatisfiable) failed: status=%d CL=%d bodyLen=%d", respHeadMissUnsat.StatusCode, respHeadMissUnsat.ContentLength, len(bodyHeadMissUnsat))
	}

	// 1e. Cache miss: HEAD + satisfiable Range on 5 MiB asset (> 4 MiB MaxRAMItemSize) -> 200 OK, full CL, empty body (never 502)
	const largeMissSize = 5 * 1024 * 1024
	respHeadMissLargeSat := doReq(http.MethodHead, "/assets/img/sp/head_range_miss_large_sat.png", map[string]string{"Range": "bytes=0-15"})
	bodyHeadMissLargeSat, _ := io.ReadAll(respHeadMissLargeSat.Body)
	_ = respHeadMissLargeSat.Body.Close()
	if respHeadMissLargeSat.StatusCode != http.StatusOK || respHeadMissLargeSat.ContentLength != largeMissSize || len(bodyHeadMissLargeSat) != 0 {
		t.Fatalf("HEAD+Range miss large (satisfiable) failed: status=%d CL=%d bodyLen=%d", respHeadMissLargeSat.StatusCode, respHeadMissLargeSat.ContentLength, len(bodyHeadMissLargeSat))
	}

	// 1f. Cache miss: HEAD + unsatisfiable Range on 5 MiB asset (> 4 MiB MaxRAMItemSize) -> 200 OK, full CL, empty body (never 502, never 416)
	respHeadMissLargeUnsat := doReq(http.MethodHead, "/assets/img/sp/head_range_miss_large_unsat.png", map[string]string{"Range": "bytes=9000000-9000100"})
	bodyHeadMissLargeUnsat, _ := io.ReadAll(respHeadMissLargeUnsat.Body)
	_ = respHeadMissLargeUnsat.Body.Close()
	if respHeadMissLargeUnsat.StatusCode != http.StatusOK || respHeadMissLargeUnsat.ContentLength != largeMissSize || len(bodyHeadMissLargeUnsat) != 0 {
		t.Fatalf("HEAD+Range miss large (unsatisfiable) failed: status=%d CL=%d bodyLen=%d", respHeadMissLargeUnsat.StatusCode, respHeadMissLargeUnsat.ContentLength, len(bodyHeadMissLargeUnsat))
	}

	// Verify that large asset cache miss HEAD does not leave partial/corrupted cache item
	if item, _ := cacheMgr.GetWithNamespace("gbf", "/assets/img/sp/head_range_miss_large_sat.png"); item != nil {
		t.Fatalf("HEAD cache miss on large asset must not pollute cache with partial item")
	}
	if meta, _ := cacheMgr.VerifyAndGetMetadataWithNamespace("gbf", "/assets/img/sp/head_range_miss_large_sat.png"); meta != nil {
		t.Fatalf("HEAD cache miss on large asset must not pollute cache metadata")
	}

	// 2. Unsatisfiable Range on Disk asset -> 416
	resp416 := doReq(http.MethodGet, diskPath, map[string]string{"Range": "bytes=9000000-9000100"})
	_ = resp416.Body.Close()
	if resp416.StatusCode != http.StatusRequestedRangeNotSatisfiable || resp416.Header.Get("Content-Range") != fmt.Sprintf("bytes */%d", diskAssetSize) {
		t.Fatalf("416 disk hit failed: status=%d CR=%q", resp416.StatusCode, resp416.Header.Get("Content-Range"))
	}

	// 3. Single-range GET on 3 MiB Disk asset with matching HTTP-date If-Range -> 206 with exact slice
	respSlice := doReq(http.MethodGet, diskPath, map[string]string{
		"Range":    "bytes=1024-2047",
		"If-Range": lastModHTTP,
	})
	bodySlice, _ := io.ReadAll(respSlice.Body)
	_ = respSlice.Body.Close()
	if respSlice.StatusCode != http.StatusPartialContent || !bytes.Equal(bodySlice, diskPayload[1024:2048]) {
		t.Fatalf("disk Range slice with HTTP-date If-Range failed: status=%d len=%d", respSlice.StatusCode, len(bodySlice))
	}

	// 4. Weak ETag in If-Range must NOT match per RFC 9110 -> returns 200 OK full asset
	respWeakIR := doReq(http.MethodGet, diskPath, map[string]string{
		"Range":    "bytes=0-15",
		"If-Range": `W/` + strongETag,
	})
	bodyWeakIR, _ := io.ReadAll(respWeakIR.Body)
	_ = respWeakIR.Body.Close()
	if respWeakIR.StatusCode != http.StatusOK || !bytes.Equal(bodyWeakIR, diskPayload) {
		t.Fatalf("weak ETag If-Range expected 200 OK full asset, got status=%d len=%d", respWeakIR.StatusCode, len(bodyWeakIR))
	}

	// 5. Unknown Range unit ("items=0-5") -> ignored per RFC 9110, returns 200 OK full asset
	respUnknownUnit := doReq(http.MethodGet, diskPath, map[string]string{
		"Range": "items=0-5",
	})
	bodyUnknownUnit, _ := io.ReadAll(respUnknownUnit.Body)
	_ = respUnknownUnit.Body.Close()
	if respUnknownUnit.StatusCode != http.StatusOK || !bytes.Equal(bodyUnknownUnit, diskPayload) {
		t.Fatalf("unknown Range unit expected 200 OK full asset, got status=%d len=%d", respUnknownUnit.StatusCode, len(bodyUnknownUnit))
	}

	// 6. Cache miss 206 must NOT pollute full-asset cache
	miss206Path := "/assets/img/sp/miss_206_no_cache_pollution.png"
	respMiss206 := doReq(http.MethodGet, miss206Path, map[string]string{"Range": "bytes=0-31"})
	bodyMiss206, _ := io.ReadAll(respMiss206.Body)
	_ = respMiss206.Body.Close()
	if respMiss206.StatusCode != http.StatusPartialContent || !bytes.Equal(bodyMiss206, smallPNG[:32]) {
		t.Fatalf("miss 206 expected 206 with 32 bytes, got status=%d len=%d", respMiss206.StatusCode, len(bodyMiss206))
	}
	// Now request the full asset without Range -> must fetch full 200 OK from upstream (not serve 32-byte partial!)
	respFullAfter206 := doReq(http.MethodGet, miss206Path, nil)
	bodyFullAfter206, _ := io.ReadAll(respFullAfter206.Body)
	_ = respFullAfter206.Body.Close()
	if respFullAfter206.StatusCode != http.StatusOK || !bytes.Equal(bodyFullAfter206, smallPNG) || upstreamMiss200Hits.Load() != 1 {
		t.Fatalf("expected full 200 OK from upstream after 206 miss, got status=%d len=%d upstream200=%d",
			respFullAfter206.StatusCode, len(bodyFullAfter206), upstreamMiss200Hits.Load())
	}

	// 7. When upstream returns 200 OK to a Range request, proxy must return 200 OK as-is without re-slicing to 206
	respUp200 := doReq(http.MethodGet, "/assets/img/sp/upstream_returns_200_for_range.png", map[string]string{
		"Range": "bytes=0-15",
	})
	bodyUp200, _ := io.ReadAll(respUp200.Body)
	_ = respUp200.Body.Close()
	if respUp200.StatusCode != http.StatusOK || !bytes.Equal(bodyUp200, smallPNG) {
		t.Fatalf("upstream 200 OK on Range request must be returned as 200 OK full payload, got status=%d len=%d",
			respUp200.StatusCode, len(bodyUp200))
	}
}

// TestProxy_RangeUpstream200_LargeAssetBoundedStreamingAndCache verifies:
// 1. Upstream 200 OK responses to Range requests for assets > MaxRAMItemSize (e.g. If-Range mismatch)
//    are preserved as 200 OK full responses without 502 Bad Gateway and without re-slicing to 206/416.
// 2. The proxy streams the response using bounded memory buffers and writes to disk temp file.
// 3. Response body is complete and identical byte-for-byte to upstream payload, with correct Content-Length.
// 4. Subsequent normal GET hits the valid disk cache and receives the identical full payload.
// 5. Truncated upstream 200 responses are never committed to cache, so subsequent requests never hit incomplete cache.
func TestProxy_RangeUpstream200_LargeAssetBoundedStreamingAndCache(t *testing.T) {
	tempDir := t.TempDir()
	cfgMgr := config.NewManager(filepath.Join(tempDir, "config.json"))
	certMgr, err := cert.NewManager(filepath.Join(tempDir, "certs"))
	if err != nil {
		t.Fatalf("failed to create cert manager: %v", err)
	}
	// 64 MB cache -> MaxRAMItemSize is 4 MiB (4 * 1024 * 1024 bytes)
	cacheMgr := cache.NewManager(tempDir, 64)
	defer cacheMgr.Close()
	stats := telemetry.NewStats()
	defer stats.Close()

	srv := NewProxyServer(cfgMgr, certMgr, cacheMgr, stats)
	defer srv.Stop()

	// Construct a 5 MiB PNG asset (> 4 MiB MaxRAMItemSize)
	const largeAssetSize = 5 * 1024 * 1024
	largePayload := make([]byte, largeAssetSize)
	copy(largePayload, []byte("\x89PNG\r\n\x1a\n"))
	for i := 8; i < len(largePayload); i++ {
		largePayload[i] = byte((i * 17) % 251)
	}

	largePath := "/assets/img/sp/large_upstream_200.png"
	truncatedPath := "/assets/img/sp/large_upstream_truncated.png"
	const upstreamETag = `"large-upstream-v2"`

	var upstreamLargeHits atomic.Int64
	var upstreamTruncatedHits atomic.Int64

	ts := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case largePath:
			upstreamLargeHits.Add(1)
			// Upstream returns 200 OK (e.g. If-Range mismatch) with full 5 MiB resource
			w.Header().Set("Content-Type", "image/png")
			w.Header().Set("ETag", upstreamETag)
			w.Header().Set("Content-Length", fmt.Sprintf("%d", len(largePayload)))
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write(largePayload)

		case truncatedPath:
			upstreamTruncatedHits.Add(1)
			// Upstream declares 5 MiB Content-Length, but only writes 1 MiB and abruptly aborts
			w.Header().Set("Content-Type", "image/png")
			w.Header().Set("ETag", `"truncated-v1"`)
			w.Header().Set("Content-Length", fmt.Sprintf("%d", len(largePayload)))
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write(largePayload[:1024*1024])
			// Abort stream by hijacking and closing raw connection
			hj, ok := w.(http.Hijacker)
			if ok {
				conn, _, _ := hj.Hijack()
				if conn != nil {
					_ = conn.Close()
				}
			}

		default:
			t.Errorf("unexpected upstream request for %s", r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer ts.Close()

	tr := ts.Client().Transport.(*http.Transport).Clone()
	tr.DialContext = func(ctx context.Context, network, addr string) (net.Conn, error) {
		return net.Dial("tcp", ts.Listener.Addr().String())
	}
	tr.TLSClientConfig.InsecureSkipVerify = true
	setTestAssetClient(srv, &http.Client{Transport: tr})

	targetHost := "prd-game-a-granbluefantasy.akamaized.net"

	doReq := func(method, path string, hdrs map[string]string) (*http.Response, []byte, error) {
		req, _ := http.NewRequest(method, "https://"+targetHost+path, nil)
		for k, v := range hdrs {
			req.Header.Set(k, v)
		}
		var raw bytes.Buffer
		srv.handleDecryptedRequest(&raw, req, targetHost)
		resp, err := http.ReadResponse(bufio.NewReader(&raw), req)
		if err != nil {
			return nil, nil, err
		}
		body, rErr := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		return resp, body, rErr
	}

	// 1. Request with Range and mismatched If-Range -> Upstream returns 200 OK full 5 MiB
	resp, body, err := doReq(http.MethodGet, largePath, map[string]string{
		"Range":    "bytes=0-15",
		"If-Range": `"stale-mismatched-etag"`,
	})
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 OK for upstream 200 on Range request, got status=%d", resp.StatusCode)
	}
	if resp.ContentLength != largeAssetSize {
		t.Fatalf("expected Content-Length %d, got %d", largeAssetSize, resp.ContentLength)
	}
	if len(body) != largeAssetSize {
		t.Fatalf("expected body length %d, got %d", largeAssetSize, len(body))
	}
	if !bytes.Equal(body, largePayload) {
		t.Fatalf("body content mismatch against upstream payload")
	}

	// 2. Subsequent normal GET without Range -> served from disk cache without reaching upstream
	upstreamHitsBefore := upstreamLargeHits.Load()
	resp2, body2, err2 := doReq(http.MethodGet, largePath, nil)
	if err2 != nil {
		t.Fatalf("subsequent GET failed: %v", err2)
	}
	if resp2.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 OK from disk cache, got %d", resp2.StatusCode)
	}
	if resp2.ContentLength != largeAssetSize {
		t.Fatalf("expected Content-Length %d from cache, got %d", largeAssetSize, resp2.ContentLength)
	}
	if !bytes.Equal(body2, largePayload) {
		t.Fatalf("cached body content mismatch against upstream payload")
	}
	if upstreamLargeHits.Load() != upstreamHitsBefore {
		t.Fatalf("expected subsequent GET to be served from cache without extra upstream call, got %d vs %d",
			upstreamLargeHits.Load(), upstreamHitsBefore)
	}

	// 3. Truncated upstream stream: Range request with mismatched If-Range where upstream aborts
	_, _, _ = doReq(http.MethodGet, truncatedPath, map[string]string{
		"Range":    "bytes=0-15",
		"If-Range": `"stale-mismatched-etag"`,
	})

	// Verify that truncated asset is NEVER committed into cache
	if item, _ := cacheMgr.GetWithNamespace("gbf", truncatedPath); item != nil {
		t.Fatalf("truncated stream must not be stored in cache, but found cache item of size %d", item.Size)
	}
	if meta, _ := cacheMgr.VerifyAndGetMetadataWithNamespace("gbf", truncatedPath); meta != nil {
		t.Fatalf("truncated stream metadata must not exist in cache")
	}
}

// TestProxy_HeadCacheMiss_MinimalReadAndConcurrentWithGet verifies:
// 1. Cold cache miss HEAD on a large asset (> MaxRAMItemSize) issues an upstream HEAD request,
//    reads exactly zero body bytes, ignores Range, returns 200 OK with full Content-Length and empty body,
//    and does not commit a metadata-only entry to cache.
// 2. HEAD conditional requests correctly return 304 or 200 with empty body.
// 3. Concurrent HEAD and GET requests for the same un-cached resource are properly isolated:
//    GET requests always receive complete, byte-for-byte identical payloads (never empty bodies from HEAD),
//    while HEAD requests receive empty bodies with valid metadata.
// 4. Subsequent GET requests hit the committed disk cache with full byte fidelity.
func TestProxy_HeadCacheMiss_MinimalReadAndConcurrentWithGet(t *testing.T) {
	tempDir := t.TempDir()
	cfgMgr := config.NewManager(filepath.Join(tempDir, "config.json"))
	certMgr, err := cert.NewManager(filepath.Join(tempDir, "certs"))
	if err != nil {
		t.Fatalf("failed to create cert manager: %v", err)
	}
	// 64 MB cache -> MaxRAMItemSize is 4 MiB
	cacheMgr := cache.NewManager(tempDir, 64)
	defer cacheMgr.Close()
	stats := telemetry.NewStats()
	defer stats.Close()

	srv := NewProxyServer(cfgMgr, certMgr, cacheMgr, stats)
	defer srv.Stop()

	const largeAssetSize = 5 * 1024 * 1024 // 5 MiB (> 4 MiB MaxRAMItemSize)
	largePayload := make([]byte, largeAssetSize)
	copy(largePayload, []byte("\x89PNG\r\n\x1a\n"))
	for i := 8; i < len(largePayload); i++ {
		largePayload[i] = byte((i * 31) % 251)
	}

	largePath := "/assets/img/sp/head_minimal_read_large.png"
	concurrentPath := "/assets/img/sp/head_concurrent_large.png"
	const upstreamETag = `"head-large-v1"`
	upstreamLastMod := time.Now().UTC().Add(-24 * time.Hour).Format(http.TimeFormat)

	var upstreamHeadHits atomic.Int64
	var upstreamGetHits atomic.Int64
	var upstreamBodyBytesSent atomic.Int64

	ts := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case largePath, concurrentPath:
			w.Header().Set("Content-Type", "image/png")
			w.Header().Set("ETag", upstreamETag)
			w.Header().Set("Last-Modified", upstreamLastMod)
			w.Header().Set("Content-Length", fmt.Sprintf("%d", len(largePayload)))

			if r.Method == http.MethodHead {
				upstreamHeadHits.Add(1)
				// RFC 9110 check: Range header must have been ignored/stripped on upstream HEAD
				if r.Header.Get("Range") != "" {
					t.Errorf("RFC 9110 violation: upstream HEAD request must not contain Range header, got: %s", r.Header.Get("Range"))
				}
				if r.Header.Get("If-None-Match") == upstreamETag {
					w.WriteHeader(http.StatusNotModified)
					return
				}
				w.WriteHeader(http.StatusOK)
				return // Zero body bytes sent for HEAD
			}

			if r.Method == http.MethodGet {
				upstreamGetHits.Add(1)
				w.WriteHeader(http.StatusOK)
				n, _ := w.Write(largePayload)
				upstreamBodyBytesSent.Add(int64(n))
				return
			}

			w.WriteHeader(http.StatusMethodNotAllowed)
		default:
			t.Errorf("unexpected upstream request: %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer ts.Close()

	tr := ts.Client().Transport.(*http.Transport).Clone()
	tr.DialContext = func(ctx context.Context, network, addr string) (net.Conn, error) {
		return net.Dial("tcp", ts.Listener.Addr().String())
	}
	tr.TLSClientConfig.InsecureSkipVerify = true
	setTestAssetClient(srv, &http.Client{Transport: tr})

	targetHost := "prd-game-a-granbluefantasy.akamaized.net"

	doReq := func(method, path string, hdrs map[string]string) (*http.Response, []byte, error) {
		req, _ := http.NewRequest(method, "https://"+targetHost+path, nil)
		for k, v := range hdrs {
			req.Header.Set(k, v)
		}
		var raw bytes.Buffer
		srv.handleDecryptedRequest(&raw, req, targetHost)
		resp, err := http.ReadResponse(bufio.NewReader(&raw), req)
		if err != nil {
			return nil, nil, err
		}
		body, rErr := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		return resp, body, rErr
	}

	// 1. Cold cache miss HEAD on 5 MiB asset (with Range: bytes=0-15)
	respHead, bodyHead, errHead := doReq(http.MethodHead, largePath, map[string]string{
		"Range": "bytes=0-15",
	})
	if errHead != nil {
		t.Fatalf("HEAD request failed: %v", errHead)
	}
	if respHead.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 OK for HEAD miss, got status=%d", respHead.StatusCode)
	}
	if respHead.ContentLength != largeAssetSize {
		t.Fatalf("expected Content-Length %d, got %d", largeAssetSize, respHead.ContentLength)
	}
	if len(bodyHead) != 0 {
		t.Fatalf("expected 0-byte body for HEAD, got %d bytes", len(bodyHead))
	}
	if upstreamHeadHits.Load() != 1 {
		t.Fatalf("expected 1 upstream HEAD hit, got %d", upstreamHeadHits.Load())
	}
	if upstreamGetHits.Load() != 0 {
		t.Fatalf("HEAD miss must not issue upstream GET, got %d GET hits", upstreamGetHits.Load())
	}
	if upstreamBodyBytesSent.Load() != 0 {
		t.Fatalf("HEAD miss must read 0 body bytes from upstream, got %d bytes", upstreamBodyBytesSent.Load())
	}
	// Verify cache was not polluted with a bodyless item
	if item, _ := cacheMgr.GetWithNamespace("gbf", largePath); item != nil {
		t.Fatalf("HEAD miss must not write to cache, found item: %+v", item)
	}

	// 2. HEAD conditional requests
	// 2a. Conditional match -> 304 Not Modified
	respCond304, bodyCond304, errCond304 := doReq(http.MethodHead, largePath, map[string]string{
		"If-None-Match": upstreamETag,
	})
	if errCond304 != nil {
		t.Fatalf("HEAD conditional 304 failed: %v", errCond304)
	}
	if respCond304.StatusCode != http.StatusNotModified {
		t.Fatalf("expected 304 Not Modified, got status=%d", respCond304.StatusCode)
	}
	if len(bodyCond304) != 0 {
		t.Fatalf("expected 0-byte body for 304, got %d bytes", len(bodyCond304))
	}

	// 2b. Conditional mismatch -> 200 OK with full Content-Length and 0-byte body
	respCond200, bodyCond200, errCond200 := doReq(http.MethodHead, largePath, map[string]string{
		"If-None-Match": `"mismatched-etag"`,
	})
	if errCond200 != nil {
		t.Fatalf("HEAD conditional 200 failed: %v", errCond200)
	}
	if respCond200.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 OK on condition mismatch, got status=%d", respCond200.StatusCode)
	}
	if respCond200.ContentLength != largeAssetSize {
		t.Fatalf("expected Content-Length %d, got %d", largeAssetSize, respCond200.ContentLength)
	}
	if len(bodyCond200) != 0 {
		t.Fatalf("expected 0-byte body on condition mismatch, got %d bytes", len(bodyCond200))
	}

	// 3. Concurrent HEAD and GET requests for the same un-cached resource
	const concurrentCount = 6
	var startBarrier sync.WaitGroup
	startBarrier.Add(1)

	type reqResult struct {
		isHead     bool
		statusCode int
		contentLen int64
		body       []byte
		err        error
	}

	results := make([]reqResult, concurrentCount*2)
	var wg sync.WaitGroup

	for i := 0; i < concurrentCount*2; i++ {
		idx := i
		isHead := (idx % 2 == 0)
		wg.Add(1)
		go func() {
			defer wg.Done()
			startBarrier.Wait() // Synchronize all goroutines to launch at the exact same instant

			method := http.MethodGet
			if isHead {
				method = http.MethodHead
			}
			resp, body, err := doReq(method, concurrentPath, nil)
			if err != nil {
				results[idx] = reqResult{isHead: isHead, err: err}
				return
			}
			results[idx] = reqResult{
				isHead:     isHead,
				statusCode: resp.StatusCode,
				contentLen: resp.ContentLength,
				body:       body,
			}
		}()
	}

	startBarrier.Done()
	wg.Wait()

	for i, res := range results {
		if res.err != nil {
			t.Fatalf("concurrent request #%d (isHead=%v) failed with error: %v", i, res.isHead, res.err)
		}
		if res.statusCode != http.StatusOK {
			t.Fatalf("concurrent request #%d (isHead=%v) returned non-200 status: %d", i, res.isHead, res.statusCode)
		}
		if res.contentLen != largeAssetSize {
			t.Fatalf("concurrent request #%d (isHead=%v) returned wrong Content-Length: %d (want %d)", i, res.isHead, res.contentLen, largeAssetSize)
		}
		if res.isHead {
			if len(res.body) != 0 {
				t.Fatalf("concurrent HEAD request #%d returned non-empty body: %d bytes", i, len(res.body))
			}
		} else {
			if len(res.body) != largeAssetSize {
				t.Fatalf("CRITICAL ISOLATION DEFECT: concurrent GET request #%d received truncated/empty body: %d bytes (want %d)", i, len(res.body), largeAssetSize)
			}
			if !bytes.Equal(res.body, largePayload) {
				t.Fatalf("CRITICAL ISOLATION DEFECT: concurrent GET request #%d body content corrupted/mismatched", i)
			}
		}
	}

	// 4. Post-concurrency cache verification: Subsequent GET must hit committed disk cache
	getHitsBefore := upstreamGetHits.Load()
	respPostGET, bodyPostGET, errPostGET := doReq(http.MethodGet, concurrentPath, nil)
	if errPostGET != nil {
		t.Fatalf("post-concurrency GET failed: %v", errPostGET)
	}
	if respPostGET.StatusCode != http.StatusOK || respPostGET.ContentLength != largeAssetSize || !bytes.Equal(bodyPostGET, largePayload) {
		t.Fatalf("post-concurrency GET failed to return valid cached asset")
	}
	if upstreamGetHits.Load() != getHitsBefore {
		t.Fatalf("post-concurrency GET must hit disk cache without new upstream request (upstream hits: %d vs %d)",
			upstreamGetHits.Load(), getHitsBefore)
	}

	// Subsequent HEAD must hit cached metadata with zero body
	respPostHEAD, bodyPostHEAD, errPostHEAD := doReq(http.MethodHead, concurrentPath, nil)
	if errPostHEAD != nil {
		t.Fatalf("post-concurrency HEAD failed: %v", errPostHEAD)
	}
	if respPostHEAD.StatusCode != http.StatusOK || respPostHEAD.ContentLength != largeAssetSize || len(bodyPostHEAD) != 0 {
		t.Fatalf("post-concurrency HEAD failed to return valid cached metadata with 0 body")
	}
}




