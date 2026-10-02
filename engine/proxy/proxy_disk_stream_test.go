package proxy

import (
	"bufio"
	"bytes"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"gbf-proxy/cache"
	"gbf-proxy/cert"
	"gbf-proxy/config"
	"gbf-proxy/process"
	"gbf-proxy/telemetry"
)

func makeTestPayload(size int) []byte {
	buf := make([]byte, size)
	copy(buf, "\x89PNG\r\n\x1a\n\x00\x00\x00\rIHDRgood")
	return buf
}

// setupDiskStreamProxy creates a test ProxyServer backed by a temporary directory cache.
func setupDiskStreamProxy(t *testing.T) (*ProxyServer, *cache.Manager, string) {
	t.Helper()
	tempDir := t.TempDir()

	cfgMgr := config.NewManager(filepath.Join(tempDir, "config.json"))
	certMgr, err := cert.NewManager(filepath.Join(tempDir, "certs"))
	if err != nil {
		t.Fatalf("failed to initialize cert manager: %v", err)
	}
	cacheMgr := cache.NewManager(filepath.Join(tempDir, "cache"), 32)
	stats := telemetry.NewStats()

	srv := NewProxyServer(cfgMgr, certMgr, cacheMgr, stats)
	t.Cleanup(func() {
		srv.Stop()
		cacheMgr.Close()
	})
	return srv, cacheMgr, tempDir
}

// TestProxy_DiskCacheHit_HEAD_ZeroByteBody tests that a HEAD request for a verified
// disk cache item returns 200 OK with correct Content-Length and 0-byte payload.
func TestProxy_DiskCacheHit_HEAD_ZeroByteBody(t *testing.T) {
	srv, cacheMgr, _ := setupDiskStreamProxy(t)

	targetHost := "prd-game-a-granbluefantasy.akamaized.net"
	urlPath := "/assets/sound/bgm_large.mp3"
	cleanKey := strings.TrimPrefix(urlPath, "/")

	// Create 3MB verified file on disk
	fileSize := int64(3 * 1024 * 1024)
	payload := makeTestPayload(int(fileSize))
	headers := map[string]string{
		"content-type":  "audio/mpeg",
		"etag":          `"large-audio-etag"`,
		"last-modified": "Wed, 21 Oct 2026 07:28:00 GMT",
	}

	if !cacheMgr.Save(urlPath, headers, payload) {
		t.Fatal("failed to save asset to disk")
	}

	// Wait for background persistence if needed and clear RAM
	filePath, ok := cacheMgr.ResolvePathWithNamespace("gbf", cleanKey)
	if !ok {
		t.Fatal("failed to resolve path")
	}
	// Ensure file is on disk
	_ = os.WriteFile(filePath, payload, 0644)
	fi, _ := os.Stat(filePath)
	extMeta := fmt.Sprintf(`{"ct":"audio/mpeg","ETag":"\"large-audio-etag\"","LastModified":"Wed, 21 Oct 2026 07:28:00 GMT","v":1,"verified":true,"size":%d,"mtime":%d}`, fi.Size(), fi.ModTime().UnixNano())
	_ = os.WriteFile(filePath+".ext", []byte(extMeta), 0644)

	cacheMgr.ClearRAM()

	// Send HEAD request
	headReq, err := http.NewRequest(http.MethodHead, "https://"+targetHost+urlPath, nil)
	if err != nil {
		t.Fatal(err)
	}
	headReq.RequestURI = urlPath

	var headBuf bytes.Buffer
	keepAlive := srv.handleDecryptedRequest(&headBuf, headReq, targetHost)
	if !keepAlive {
		t.Error("expected keep-alive for HEAD request")
	}

	rawOut := headBuf.String()
	if !strings.HasPrefix(rawOut, "HTTP/1.1 200 OK\r\n") {
		t.Fatalf("expected HTTP/1.1 200 OK, got:\n%s", rawOut)
	}

	// Verify Content-Length advertises 3MB
	cl := extractHeader(rawOut, "Content-Length")
	if cl != strconv.FormatInt(fileSize, 10) {
		t.Fatalf("expected Content-Length %d, got %q", fileSize, cl)
	}

	// Verify ETag and Content-Type
	if etag := extractHeader(rawOut, "ETag"); etag != `"large-audio-etag"` {
		t.Fatalf("expected ETag \"large-audio-etag\", got %q", etag)
	}
	if ct := extractHeader(rawOut, "Content-Type"); ct != "audio/mpeg" {
		t.Fatalf("expected Content-Type audio/mpeg, got %q", ct)
	}

	// Verify 0 bytes of payload sent
	_, _, body := splitRawResponse(t, headBuf.Bytes())
	if len(body) != 0 {
		t.Fatalf("expected 0 bytes body for HEAD, got %d bytes", len(body))
	}
}

// TestProxy_DiskCacheHit_304_ZeroByteBody tests that a conditional GET matching ETag
// for a verified disk cache item returns 304 Not Modified with 0-byte payload.
func TestProxy_DiskCacheHit_304_ZeroByteBody(t *testing.T) {
	srv, cacheMgr, _ := setupDiskStreamProxy(t)

	targetHost := "prd-game-a-granbluefantasy.akamaized.net"
	urlPath := "/assets/sound/bgm_large.mp3"
	cleanKey := strings.TrimPrefix(urlPath, "/")

	fileSize := int64(3 * 1024 * 1024)
	payload := makeTestPayload(int(fileSize))

	filePath, ok := cacheMgr.ResolvePathWithNamespace("gbf", cleanKey)
	if !ok {
		t.Fatal("failed to resolve path")
	}
	_ = os.MkdirAll(filepath.Dir(filePath), 0755)
	_ = os.WriteFile(filePath, payload, 0644)
	fi, _ := os.Stat(filePath)
	extMeta := fmt.Sprintf(`{"ct":"audio/mpeg","ETag":"\"large-audio-etag\"","LastModified":"Wed, 21 Oct 2026 07:28:00 GMT","v":1,"verified":true,"size":%d,"mtime":%d}`, fi.Size(), fi.ModTime().UnixNano())
	_ = os.WriteFile(filePath+".ext", []byte(extMeta), 0644)

	cacheMgr.ClearRAM()

	// Send conditional GET
	condReq, err := http.NewRequest(http.MethodGet, "https://"+targetHost+urlPath, nil)
	if err != nil {
		t.Fatal(err)
	}
	condReq.RequestURI = urlPath
	condReq.Header.Set("If-None-Match", `"large-audio-etag"`)

	var condBuf bytes.Buffer
	keepAlive := srv.handleDecryptedRequest(&condBuf, condReq, targetHost)
	if !keepAlive {
		t.Error("expected keep-alive for 304 response")
	}

	rawOut := condBuf.String()
	if !strings.HasPrefix(rawOut, "HTTP/1.1 304 Not Modified\r\n") {
		t.Fatalf("expected HTTP/1.1 304 Not Modified, got:\n%s", rawOut)
	}

	_, _, body := splitRawResponse(t, condBuf.Bytes())
	if len(body) != 0 {
		t.Fatalf("expected 0 bytes body for 304, got %d bytes", len(body))
	}
}

// TestProxy_DiskCacheHit_ConditionalMismatch_FullReadFastResponse tests that a conditional GET
// with mismatched ETag on a verified disk cache item loads full Data and returns 200 OK fast response.
func TestProxy_DiskCacheHit_ConditionalMismatch_FullReadFastResponse(t *testing.T) {
	srv, cacheMgr, _ := setupDiskStreamProxy(t)

	targetHost := "prd-game-a-granbluefantasy.akamaized.net"
	urlPath := "/assets/sound/bgm_cond_mismatch.mp3"
	cleanKey := strings.TrimPrefix(urlPath, "/")

	fileSize := int64(3 * 1024 * 1024)
	payload := makeTestPayload(int(fileSize))

	filePath, ok := cacheMgr.ResolvePathWithNamespace("gbf", cleanKey)
	if !ok {
		t.Fatal("failed to resolve path")
	}
	_ = os.MkdirAll(filepath.Dir(filePath), 0755)
	_ = os.WriteFile(filePath, payload, 0644)
	fi, _ := os.Stat(filePath)
	extMeta := fmt.Sprintf(`{"ct":"audio/mpeg","ETag":"\"current-etag\"","LastModified":"Wed, 21 Oct 2026 07:28:00 GMT","v":1,"verified":true,"size":%d,"mtime":%d}`, fi.Size(), fi.ModTime().UnixNano())
	_ = os.WriteFile(filePath+".ext", []byte(extMeta), 0644)

	cacheMgr.ClearRAM()

	// Send conditional GET with mismatched ETag
	condReq, err := http.NewRequest(http.MethodGet, "https://"+targetHost+urlPath, nil)
	if err != nil {
		t.Fatal(err)
	}
	condReq.RequestURI = urlPath
	condReq.Header.Set("If-None-Match", `"outdated-etag"`)

	var condBuf bytes.Buffer
	keepAlive := srv.handleDecryptedRequest(&condBuf, condReq, targetHost)
	if !keepAlive {
		t.Error("expected keep-alive for 200 response on conditional mismatch")
	}

	rawOut := condBuf.String()
	if !strings.HasPrefix(rawOut, "HTTP/1.1 200 OK\r\n") {
		t.Fatalf("expected HTTP/1.1 200 OK, got:\n%s", rawOut)
	}
	cl := extractHeader(rawOut, "Content-Length")
	if cl != strconv.FormatInt(fileSize, 10) {
		t.Fatalf("expected Content-Length %d, got %q", fileSize, cl)
	}

	_, _, body := splitRawResponse(t, condBuf.Bytes())
	if !bytes.Equal(body, payload) {
		t.Fatalf("body mismatch: got %d bytes, want %d bytes", len(body), len(payload))
	}

	// Verify Data is fully loaded in memory on disk cache hit
	item, src := cacheMgr.GetWithNamespace("gbf", cleanKey)
	if item == nil || src != "DISK" {
		t.Fatalf("expected DISK hit, got src=%s", src)
	}
	if item.Data == nil || len(item.Data) != int(fileSize) || !bytes.Equal(item.Data, payload) {
		t.Fatalf("expected full in-memory Data for disk cache hit, got %d bytes", len(item.Data))
	}
	if cacheMgr.IsRAMProtected("gbf", cleanKey) {
		t.Fatal("large asset must not enter RAM Protected segment")
	}
}

// TestProxy_DiskCacheHit_LargeAsset_FastPathFullData tests that GET on a verified >2MB disk item
// loads the entire file into memory (Data != nil), returns HTTP 200 OK with byte-for-byte identical content
// via sendCachedAssetResponseFast, and does not admit the large asset to RAM SLRU.
func TestProxy_DiskCacheHit_LargeAsset_FastPathFullData(t *testing.T) {
	srv, cacheMgr, _ := setupDiskStreamProxy(t)

	targetHost := "prd-game-a-granbluefantasy.akamaized.net"
	urlPath := "/assets/sound/bgm_large.mp3"
	cleanKey := strings.TrimPrefix(urlPath, "/")

	fileSize := int64(3 * 1024 * 1024)
	payload := makeTestPayload(int(fileSize))

	filePath, ok := cacheMgr.ResolvePathWithNamespace("gbf", cleanKey)
	if !ok {
		t.Fatal("failed to resolve path")
	}
	_ = os.MkdirAll(filepath.Dir(filePath), 0755)
	_ = os.WriteFile(filePath, payload, 0644)
	fi, _ := os.Stat(filePath)
	extMeta := fmt.Sprintf(`{"ct":"audio/mpeg","ETag":"\"large-audio-etag\"","LastModified":"Wed, 21 Oct 2026 07:28:00 GMT","v":1,"verified":true,"size":%d,"mtime":%d}`, fi.Size(), fi.ModTime().UnixNano())
	_ = os.WriteFile(filePath+".ext", []byte(extMeta), 0644)

	cacheMgr.ClearRAM()

	// Send GET request
	getReq, err := http.NewRequest(http.MethodGet, "https://"+targetHost+urlPath, nil)
	if err != nil {
		t.Fatal(err)
	}
	getReq.RequestURI = urlPath

	var getBuf bytes.Buffer
	keepAlive := srv.handleDecryptedRequest(&getBuf, getReq, targetHost)
	if !keepAlive {
		t.Error("expected keep-alive for 200 fast response")
	}

	rawOut := getBuf.String()
	if !strings.HasPrefix(rawOut, "HTTP/1.1 200 OK\r\n") {
		t.Fatalf("expected HTTP/1.1 200 OK, got:\n%s", rawOut)
	}

	cl := extractHeader(rawOut, "Content-Length")
	if cl != strconv.FormatInt(fileSize, 10) {
		t.Fatalf("expected Content-Length %d, got %q", fileSize, cl)
	}

	// Verify byte-for-byte stream integrity
	_, _, body := splitRawResponse(t, getBuf.Bytes())
	if !bytes.Equal(body, payload) {
		t.Fatalf("body mismatch: got %d bytes, want %d bytes", len(body), len(payload))
	}

	// Verify full Data is loaded in CacheItem on disk hit
	item, src := cacheMgr.GetWithNamespace("gbf", cleanKey)
	if item == nil || src != "DISK" {
		t.Fatalf("expected DISK hit, got src=%s", src)
	}
	if item.Data == nil || len(item.Data) != int(fileSize) || !bytes.Equal(item.Data, payload) {
		t.Fatalf("expected full in-memory Data for disk cache hit, got %d bytes", len(item.Data))
	}

	// RAM admission check: large item must NOT be admitted to SLRU
	if cacheMgr.IsRAMProtected("gbf", cleanKey) {
		t.Fatal("large item must not enter RAM Protected segment")
	}
}

// TestProxy_DiskCacheHit_SmallAsset_FastPath tests that <=2MB items continue to use
// the existing fast path with full in-memory Data and standard SLRU caching.
func TestProxy_DiskCacheHit_SmallAsset_FastPath(t *testing.T) {
	srv, cacheMgr, _ := setupDiskStreamProxy(t)

	targetHost := "prd-game-a-granbluefantasy.akamaized.net"
	urlPath := "/assets/img/small_asset.png"
	cleanKey := strings.TrimPrefix(urlPath, "/")

	fileSize := int64(64 * 1024) // 64 KB <= 2 MB
	payload := makeTestPayload(int(fileSize))

	filePath, ok := cacheMgr.ResolvePathWithNamespace("gbf", cleanKey)
	if !ok {
		t.Fatal("failed to resolve path")
	}
	_ = os.MkdirAll(filepath.Dir(filePath), 0755)
	_ = os.WriteFile(filePath, payload, 0644)
	fi, _ := os.Stat(filePath)
	extMeta := fmt.Sprintf(`{"ct":"image/png","ETag":"\"small-etag\"","LastModified":"Wed, 21 Oct 2026 07:28:00 GMT","v":1,"verified":true,"size":%d,"mtime":%d}`, fi.Size(), fi.ModTime().UnixNano())
	_ = os.WriteFile(filePath+".ext", []byte(extMeta), 0644)

	cacheMgr.ClearRAM()

	// Send GET request
	getReq, err := http.NewRequest(http.MethodGet, "https://"+targetHost+urlPath, nil)
	if err != nil {
		t.Fatal(err)
	}
	getReq.RequestURI = urlPath

	var getBuf bytes.Buffer
	keepAlive := srv.handleDecryptedRequest(&getBuf, getReq, targetHost)
	if !keepAlive {
		t.Error("expected keep-alive for small asset 200 response")
	}

	rawOut := getBuf.String()
	if !strings.HasPrefix(rawOut, "HTTP/1.1 200 OK\r\n") {
		t.Fatalf("expected HTTP/1.1 200 OK, got:\n%s", rawOut)
	}

	_, _, body := splitRawResponse(t, getBuf.Bytes())
	if !bytes.Equal(body, payload) {
		t.Fatalf("small asset body mismatch: got %d bytes, want %d bytes", len(body), len(payload))
	}
}

// TestProxy_DiskCacheHit_UnverifiedAsset_Promotion tests that an unverified historical
// asset is fully validated on first request, promoted to verified, and future requests hit fast path.
func TestProxy_DiskCacheHit_UnverifiedAsset_Promotion(t *testing.T) {
	srv, cacheMgr, _ := setupDiskStreamProxy(t)

	targetHost := "prd-game-a-granbluefantasy.akamaized.net"
	urlPath := "/assets/sound/unverified_bgm.mp3"
	cleanKey := strings.TrimPrefix(urlPath, "/")

	fileSize := int64(3 * 1024 * 1024)
	payload := makeTestPayload(int(fileSize))

	filePath, ok := cacheMgr.ResolvePathWithNamespace("gbf", cleanKey)
	if !ok {
		t.Fatal("failed to resolve path")
	}
	_ = os.MkdirAll(filepath.Dir(filePath), 0755)
	_ = os.WriteFile(filePath, payload, 0644)
	// Legacy .ext without verified field
	extMeta := `{"ContentType":"audio/mpeg","ETag":"\"unverified-etag\"","LastModified":"Wed, 21 Oct 2026 07:28:00 GMT","v":1}`
	_ = os.WriteFile(filePath+".ext", []byte(extMeta), 0644)

	cacheMgr.ClearRAM()

	// 1. First request: unverified asset goes through full validation
	req1, _ := http.NewRequest(http.MethodGet, "https://"+targetHost+urlPath, nil)
	req1.RequestURI = urlPath
	var buf1 bytes.Buffer
	srv.handleDecryptedRequest(&buf1, req1, targetHost)

	if !strings.HasPrefix(buf1.String(), "HTTP/1.1 200 OK\r\n") {
		t.Fatalf("expected 200 OK on first request, got:\n%s", buf1.String())
	}
	_, _, body1 := splitRawResponse(t, buf1.Bytes())
	if !bytes.Equal(body1, payload) {
		t.Fatalf("first request payload mismatch: got %d bytes", len(body1))
	}

	// RAM admission check: unverified large asset promoted on first hit must NOT enter RAM
	if cacheMgr.IsRAMProtected("gbf", cleanKey) {
		t.Fatal("large asset promoted on first hit must not enter RAM Protected segment")
	}

	// Verify .ext file was updated to verified: true
	extBytes, err := os.ReadFile(filePath + ".ext")
	if err != nil {
		t.Fatalf("failed to read updated .ext: %v", err)
	}
	if !strings.Contains(string(extBytes), `"verified":true`) {
		t.Fatalf("expected updated .ext to contain verified:true, got: %s", string(extBytes))
	}

	cacheMgr.ClearRAM()

	// 2. Second request: HEAD now hits the verified metadata fastpath directly
	req2, _ := http.NewRequest(http.MethodHead, "https://"+targetHost+urlPath, nil)
	req2.RequestURI = urlPath
	var buf2 bytes.Buffer
	srv.handleDecryptedRequest(&buf2, req2, targetHost)

	if !strings.HasPrefix(buf2.String(), "HTTP/1.1 200 OK\r\n") {
		t.Fatalf("expected 200 OK on second HEAD request, got:\n%s", buf2.String())
	}
	cl := extractHeader(buf2.String(), "Content-Length")
	if cl != strconv.FormatInt(fileSize, 10) {
		t.Fatalf("expected Content-Length %d on second request, got %q", fileSize, cl)
	}
	_, _, body2 := splitRawResponse(t, buf2.Bytes())
	if len(body2) != 0 {
		t.Fatalf("expected 0 bytes body on second HEAD request, got %d bytes", len(body2))
	}
}

type failingWriter struct {
	headerWritten bool
}

func (f *failingWriter) Write(p []byte) (n int, err error) {
	if !f.headerWritten {
		f.headerWritten = true
		return len(p), nil
	}
	return 0, fmt.Errorf("simulated network connection aborted")
}

// TestProxy_DiskCacheHit_LargeAsset_FullReadFastResponse tests that GET on a verified >2MB disk item
// loads the entire file into memory (Data != nil), returns HTTP 200 OK with byte-for-byte identical content
// via sendCachedAssetResponseFast, and respects connection close semantics.
func TestProxy_DiskCacheHit_LargeAsset_FullReadFastResponse(t *testing.T) {
	srv, cacheMgr, _ := setupDiskStreamProxy(t)

	targetHost := "prd-game-a-granbluefantasy.akamaized.net"
	urlPath := "/assets/sound/large_asset_full_read.mp3"
	cleanKey := strings.TrimPrefix(urlPath, "/")

	fileSize := int64(3 * 1024 * 1024)
	payload := makeTestPayload(int(fileSize))

	filePath, ok := cacheMgr.ResolvePathWithNamespace("gbf", cleanKey)
	if !ok {
		t.Fatal("failed to resolve path")
	}
	_ = os.MkdirAll(filepath.Dir(filePath), 0755)
	_ = os.WriteFile(filePath, payload, 0644)
	fi, _ := os.Stat(filePath)
	extMeta := fmt.Sprintf(`{"ct":"audio/mpeg","ETag":"\"large-etag\"","LastModified":"Wed, 21 Oct 2026 07:28:00 GMT","v":1,"verified":true,"size":%d,"mtime":%d}`, fi.Size(), fi.ModTime().UnixNano())
	_ = os.WriteFile(filePath+".ext", []byte(extMeta), 0644)

	cacheMgr.ClearRAM()

	// 1. GET with keep-alive
	req, _ := http.NewRequest(http.MethodGet, "https://"+targetHost+urlPath, nil)
	req.RequestURI = urlPath

	var getBuf bytes.Buffer
	keepAlive := srv.handleDecryptedRequest(&getBuf, req, targetHost)
	if !keepAlive {
		t.Fatal("expected keepAlive to be true for GET request")
	}

	rawOut := getBuf.String()
	if !strings.HasPrefix(rawOut, "HTTP/1.1 200 OK\r\n") {
		t.Fatalf("expected HTTP/1.1 200 OK, got:\n%s", rawOut)
	}

	cl := extractHeader(rawOut, "Content-Length")
	if cl != strconv.FormatInt(fileSize, 10) {
		t.Fatalf("expected Content-Length %d, got %q", fileSize, cl)
	}

	_, _, body := splitRawResponse(t, getBuf.Bytes())
	if !bytes.Equal(body, payload) {
		t.Fatalf("body mismatch: got %d bytes, want %d bytes", len(body), len(payload))
	}

	// 2. Verify Data is fully loaded in memory on disk cache hit
	item, src := cacheMgr.GetWithNamespace("gbf", cleanKey)
	if item == nil || src != "DISK" {
		t.Fatalf("expected DISK hit, got src=%s", src)
	}
	if item.Data == nil || len(item.Data) != int(fileSize) || !bytes.Equal(item.Data, payload) {
		t.Fatalf("expected full in-memory Data for disk cache hit, got %d bytes", len(item.Data))
	}
	if cacheMgr.IsRAMProtected("gbf", cleanKey) {
		t.Fatal("large asset must not enter RAM Protected segment")
	}

	// 3. GET with req.Close = true
	cacheMgr.ClearRAM()
	reqClose, _ := http.NewRequest(http.MethodGet, "https://"+targetHost+urlPath, nil)
	reqClose.RequestURI = urlPath
	reqClose.Close = true

	var closeBuf bytes.Buffer
	keepAliveClose := srv.handleDecryptedRequest(&closeBuf, reqClose, targetHost)
	if keepAliveClose {
		t.Fatal("expected keepAlive to be false when req.Close is true")
	}
	connHdr := extractHeader(closeBuf.String(), "Connection")
	if connHdr != "close" {
		t.Fatalf("expected Connection: close, got %q", connHdr)
	}
}

// TestProxy_DiskStream_ConcurrentAccess tests multiple concurrent goroutines streaming
// the same large asset from disk simultaneously.
func TestProxy_DiskStream_ConcurrentAccess(t *testing.T) {
	srv, cacheMgr, _ := setupDiskStreamProxy(t)

	targetHost := "prd-game-a-granbluefantasy.akamaized.net"
	urlPath := "/assets/sound/concurrent_bgm.mp3"
	cleanKey := strings.TrimPrefix(urlPath, "/")

	fileSize := int64(3 * 1024 * 1024)
	payload := makeTestPayload(int(fileSize))

	filePath, ok := cacheMgr.ResolvePathWithNamespace("gbf", cleanKey)
	if !ok {
		t.Fatal("failed to resolve path")
	}
	_ = os.MkdirAll(filepath.Dir(filePath), 0755)
	_ = os.WriteFile(filePath, payload, 0644)
	fi, _ := os.Stat(filePath)
	extMeta := fmt.Sprintf(`{"ct":"audio/mpeg","ETag":"\"concurrent-etag\"","LastModified":"Wed, 21 Oct 2026 07:28:00 GMT","v":1,"verified":true,"size":%d,"mtime":%d}`, fi.Size(), fi.ModTime().UnixNano())
	_ = os.WriteFile(filePath+".ext", []byte(extMeta), 0644)

	cacheMgr.ClearRAM()

	const concurrency = 8
	errCh := make(chan error, concurrency)

	for i := 0; i < concurrency; i++ {
		go func(id int) {
			req, _ := http.NewRequest(http.MethodGet, "https://"+targetHost+urlPath, nil)
			req.RequestURI = urlPath
			var buf bytes.Buffer
			keepAlive := srv.handleDecryptedRequest(&buf, req, targetHost)
			if !keepAlive {
				errCh <- fmt.Errorf("worker %d: expected keepAlive true", id)
				return
			}
			_, _, body := splitRawResponse(t, buf.Bytes())
			if !bytes.Equal(body, payload) {
				errCh <- fmt.Errorf("worker %d: body mismatch (got %d bytes)", id, len(body))
				return
			}
			errCh <- nil
		}(i)
	}

	for i := 0; i < concurrency; i++ {
		if err := <-errCh; err != nil {
			t.Fatal(err)
		}
	}
}

// TestProxy_DiskStream_SingleFlightFollowerLargeAsset verifies that when a leader
// fetches and commits a large asset (>2MB), waiting SingleFlight followers receive
// the full payload via ReadDiskItemData, and subsequent requests hit disk cache with full Data.
func TestProxy_DiskStream_SingleFlightFollowerLargeAsset(t *testing.T) {
	tempDir := t.TempDir()
	cfgMgr := config.NewManager(filepath.Join(tempDir, "config.json"))
	cacheMgr := cache.NewManager(tempDir, 16)
	defer cacheMgr.Close()
	stats := telemetry.NewStats()
	defer stats.Close()

	// 3 MB > MaxDiskDirectReadSize (2MB)
	const totalSize = 3 * 1024 * 1024
	payload := makeTestPayload(totalSize)

	var upstreamHits atomic.Int64
	ts, client := newMockAssetServer(func(w http.ResponseWriter, r *http.Request) {
		upstreamHits.Add(1)
		w.Header().Set("Content-Type", "image/png")
		w.Header().Set("ETag", `"sf-large-asset-etag"`)
		w.Header().Set("Content-Length", strconv.Itoa(len(payload)))
		w.WriteHeader(http.StatusOK)

		// Delay writing so follower can join leader's singleflight
		time.Sleep(30 * time.Millisecond)
		_, _ = w.Write(payload)
	})
	defer ts.Close()

	srv := &ProxyServer{
		cfgMgr:   cfgMgr,
		cacheMgr: cacheMgr,
		stats:    stats,
	}
	setTestAssetClient(srv, client)

	const host = "prd-game-a-granbluefantasy.akamaized.net"
	const path = "/assets/img/sp/sf_large_stream.png"

	var wg sync.WaitGroup
	wg.Add(2)

	var leaderBuf, followerBuf bytes.Buffer

	// Launch leader
	go func() {
		defer wg.Done()
		req, _ := http.NewRequest(http.MethodGet, "https://"+host+path, nil)
		req.RequestURI = path
		req.Host = host
		srv.handleStaticAssetLower(&leaderBuf, req, host, path)
	}()

	// Launch follower with small delay to queue behind leader
	go func() {
		defer wg.Done()
		time.Sleep(10 * time.Millisecond)
		req, _ := http.NewRequest(http.MethodGet, "https://"+host+path, nil)
		req.RequestURI = path
		req.Host = host
		srv.handleStaticAssetLower(&followerBuf, req, host, path)
	}()

	wg.Wait()

	if upstreamHits.Load() != 1 {
		t.Fatalf("expected exactly 1 upstream hit, got %d", upstreamHits.Load())
	}

	// Verify leader received full 3MB payload
	_, _, bodyL := splitRawResponse(t, leaderBuf.Bytes())
	if !bytes.Equal(bodyL, payload) {
		t.Fatalf("leader body mismatch: got %d bytes, want %d", len(bodyL), len(payload))
	}

	// Verify follower received full 3MB payload streamed from disk
	_, _, bodyF := splitRawResponse(t, followerBuf.Bytes())
	if !bytes.Equal(bodyF, payload) {
		t.Fatalf("follower body mismatch: got %d bytes, want %d", len(bodyF), len(payload))
	}

	// Large stream item must NOT be admitted to SLRU Protected segment
	cleanKey := strings.TrimPrefix(path, "/")
	if cacheMgr.IsRAMProtected("gbf", cleanKey) {
		t.Fatal("large asset must not enter RAM Protected segment")
	}

	// Subsequent Request: verify subsequent Cache Hit loads full in-memory Data and responds fast
	var thirdBuf bytes.Buffer
	reqThird, _ := http.NewRequest(http.MethodGet, "https://"+host+path, nil)
	reqThird.RequestURI = path
	reqThird.Host = host
	keepAliveThird := srv.handleStaticAssetLower(&thirdBuf, reqThird, host, path)
	if !keepAliveThird {
		t.Fatal("expected keepAlive true for 3rd request cache hit")
	}
	rawOutThird := thirdBuf.String()
	if !strings.HasPrefix(rawOutThird, "HTTP/1.1 200 OK\r\n") {
		t.Fatalf("expected 200 OK on 3rd request, got:\n%s", rawOutThird)
	}
	_, _, bodyThird := splitRawResponse(t, thirdBuf.Bytes())
	if !bytes.Equal(bodyThird, payload) {
		t.Fatalf("third request body mismatch: got %d bytes, want %d", len(bodyThird), len(payload))
	}
	itemThird, srcThird := cacheMgr.GetWithNamespace("gbf", cleanKey)
	if itemThird == nil || srcThird != "DISK" {
		t.Fatalf("expected DISK hit on 3rd request, got %s", srcThird)
	}
	if itemThird.Data == nil || len(itemThird.Data) != totalSize || !bytes.Equal(itemThird.Data, payload) {
		t.Fatalf("expected full Data in CacheItem on disk hit, got %d bytes", len(itemThird.Data))
	}
}

// TestProxy_DiskStream_OpenFailure_FallbackToCacheMiss verifies that when disk cache validation
// fails (e.g. file modified/corrupted), the proxy does NOT abort the connection,
// but instead invalidates the bad cache and falls back to Cache Miss / upstream fetch, returning 200 OK.
func TestProxy_DiskStream_OpenFailure_FallbackToCacheMiss(t *testing.T) {
	tempDir := t.TempDir()
	cfgMgr := config.NewManager(filepath.Join(tempDir, "config.json"))
	cacheMgr := cache.NewManager(tempDir, 16)
	defer cacheMgr.Close()
	stats := telemetry.NewStats()
	defer stats.Close()

	freshPayload := makeTestPayload(128 * 1024)
	var upstreamHits atomic.Int64
	ts, client := newMockAssetServer(func(w http.ResponseWriter, r *http.Request) {
		upstreamHits.Add(1)
		w.Header().Set("Content-Type", "image/png")
		w.Header().Set("ETag", `"fresh-upstream-etag"`)
		w.Header().Set("Content-Length", strconv.Itoa(len(freshPayload)))
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(freshPayload)
	})
	defer ts.Close()

	srv := &ProxyServer{
		cfgMgr:   cfgMgr,
		cacheMgr: cacheMgr,
		stats:    stats,
	}
	setTestAssetClient(srv, client)

	const host = "prd-game-a-granbluefantasy.akamaized.net"
	const path = "/assets/img/sp/fallback_stream.png"
	cleanKey := strings.TrimPrefix(path, "/")

	// Create initial 3MB file on disk with verified metadata
	origSize := int64(3 * 1024 * 1024)
	origPayload := makeTestPayload(int(origSize))
	filePath, ok := cacheMgr.ResolvePathWithNamespace("gbf", cleanKey)
	if !ok {
		t.Fatal("failed to resolve path")
	}
	_ = os.MkdirAll(filepath.Dir(filePath), 0755)
	_ = os.WriteFile(filePath, origPayload, 0644)
	fi, _ := os.Stat(filePath)
	extMeta := fmt.Sprintf(`{"ct":"image/png","ETag":"\"old-etag\"","LastModified":"Wed, 21 Oct 2026 07:28:00 GMT","v":1,"verified":true,"size":%d,"mtime":%d}`, fi.Size(), fi.ModTime().UnixNano())
	_ = os.WriteFile(filePath+".ext", []byte(extMeta), 0644)

	// Now corrupt the file by truncating to 100 bytes (size mismatch)
	// This simulates a corrupted file on disk
	f, err := os.OpenFile(filePath, os.O_WRONLY|os.O_TRUNC, 0644)
	if err != nil {
		t.Fatal(err)
	}
	_ = f.Truncate(100)
	_ = f.Close()

	cacheMgr.ClearRAM()

	// Send normal GET request for the asset
	req, _ := http.NewRequest(http.MethodGet, "https://"+host+path, nil)
	req.RequestURI = path
	req.Host = host

	var respBuf bytes.Buffer
	keepAlive := srv.handleStaticAssetLower(&respBuf, req, host, path)
	if !keepAlive {
		t.Error("expected keepAlive to be true for successful fallback response")
	}

	rawOut := respBuf.String()
	if !strings.HasPrefix(rawOut, "HTTP/1.1 200 OK\r\n") {
		t.Fatalf("expected HTTP/1.1 200 OK via cache miss fallback, got:\n%s", rawOut)
	}

	if upstreamHits.Load() != 1 {
		t.Fatalf("expected upstream to be hit for cache miss fallback, got %d hits", upstreamHits.Load())
	}

	_, _, body := splitRawResponse(t, respBuf.Bytes())
	if !bytes.Equal(body, freshPayload) {
		t.Fatalf("expected fresh upstream payload, got %d bytes (want %d)", len(body), len(freshPayload))
	}
}

// TestProxy_DiskCacheHit_LegacyUnverifiedLargeAsset_FirstFullReadThenStream verifies that
// when an unverified >2MB legacy disk cache item is accessed via proxy:
// 1. The first GET executes legacy full-read validation and serves the response via sendCachedAssetResponseFast.
// 2. The item is promoted to Verified=true.
// 3. The second GET executes full disk read and serves via sendCachedAssetResponseFast with full Data.
func TestProxy_DiskCacheHit_LegacyUnverifiedLargeAsset_FirstFullReadThenStream(t *testing.T) {
	srv, cacheMgr, _ := setupDiskStreamProxy(t)

	targetHost := "prd-game-a-granbluefantasy.akamaized.net"
	urlPath := "/assets/sound/bgm_legacy_stream.mp3"
	cleanKey := strings.TrimPrefix(urlPath, "/")

	fileSize := int64(3 * 1024 * 1024)
	payload := makeTestPayload(int(fileSize))

	filePath, ok := cacheMgr.ResolvePathWithNamespace("gbf", cleanKey)
	if !ok {
		t.Fatal("failed to resolve path")
	}
	_ = os.MkdirAll(filepath.Dir(filePath), 0755)
	_ = os.WriteFile(filePath, payload, 0644)
	fi, _ := os.Stat(filePath)
	legacyExt := fmt.Sprintf(`{"ct":"audio/mpeg","ETag":"\"legacy-stream-etag\"","LastModified":"Wed, 21 Oct 2026 07:28:00 GMT","v":1,"verified":false,"size":%d,"mtime":%d}`, fi.Size(), fi.ModTime().UnixNano())
	_ = os.WriteFile(filePath+".ext", []byte(legacyExt), 0644)

	cacheMgr.ClearRAM()

	// 1. First GET request: unverified item undergoes full read validation and promotion
	getReq1, err := http.NewRequest(http.MethodGet, "https://"+targetHost+urlPath, nil)
	if err != nil {
		t.Fatal(err)
	}
	getReq1.RequestURI = urlPath

	var buf1 bytes.Buffer
	keepAlive1 := srv.handleDecryptedRequest(&buf1, getReq1, targetHost)
	if !keepAlive1 {
		t.Error("expected keep-alive for first GET request")
	}

	rawOut1 := buf1.String()
	if !strings.HasPrefix(rawOut1, "HTTP/1.1 200 OK\r\n") {
		t.Fatalf("expected HTTP/1.1 200 OK on first GET, got:\n%s", rawOut1)
	}

	_, _, body1 := splitRawResponse(t, buf1.Bytes())
	if !bytes.Equal(body1, payload) {
		t.Fatalf("first GET body mismatch: got %d bytes, want %d bytes", len(body1), len(payload))
	}

	// Large item must NOT be admitted to SLRU Protected segment
	if cacheMgr.IsRAMProtected("gbf", cleanKey) {
		t.Fatal("large asset must not enter RAM Protected segment")
	}

	// 2. Second GET request: now verified, serves via full disk read and sendCachedAssetResponseFast
	getReq2, err := http.NewRequest(http.MethodGet, "https://"+targetHost+urlPath, nil)
	if err != nil {
		t.Fatal(err)
	}
	getReq2.RequestURI = urlPath

	var buf2 bytes.Buffer
	keepAlive2 := srv.handleDecryptedRequest(&buf2, getReq2, targetHost)
	if !keepAlive2 {
		t.Error("expected keep-alive for second GET request")
	}

	rawOut2 := buf2.String()
	if !strings.HasPrefix(rawOut2, "HTTP/1.1 200 OK\r\n") {
		t.Fatalf("expected HTTP/1.1 200 OK on second GET, got:\n%s", rawOut2)
	}

	cl2 := extractHeader(rawOut2, "Content-Length")
	if cl2 != strconv.FormatInt(fileSize, 10) {
		t.Fatalf("expected Content-Length %d on second GET, got %q", fileSize, cl2)
	}

	_, _, body2 := splitRawResponse(t, buf2.Bytes())
	if !bytes.Equal(body2, payload) {
		t.Fatalf("second GET body mismatch: got %d bytes, want %d bytes", len(body2), len(payload))
	}

	// Verify CacheItem has full Data populated and RAM SLRU exclusion holds
	item2, src2 := cacheMgr.GetWithNamespace("gbf", cleanKey)
	if item2 == nil || src2 != "DISK" {
		t.Fatalf("expected DISK hit on second GET, got %s", src2)
	}
	if item2.Data == nil || len(item2.Data) != int(fileSize) || !bytes.Equal(item2.Data, payload) {
		t.Fatalf("expected full Data on second GET after promotion, got %d bytes", len(item2.Data))
	}
	if cacheMgr.IsRAMProtected("gbf", cleanKey) {
		t.Fatal("large asset must not enter RAM Protected segment after promotion")
	}
}

// TestProxy_DiskCacheHit_LegacyUnverifiedLargeAsset_ConcurrentAccess verifies that multiple
// concurrent client requests hitting an unverified >2MB legacy asset simultaneously
// all receive complete, uncorrupted payloads without race conditions or deadlocks.
func TestProxy_DiskCacheHit_LegacyUnverifiedLargeAsset_ConcurrentAccess(t *testing.T) {
	srv, cacheMgr, _ := setupDiskStreamProxy(t)

	targetHost := "prd-game-a-granbluefantasy.akamaized.net"
	urlPath := "/assets/sound/bgm_legacy_concurrent.mp3"
	cleanKey := strings.TrimPrefix(urlPath, "/")

	fileSize := int64(3 * 1024 * 1024)
	payload := makeTestPayload(int(fileSize))

	filePath, ok := cacheMgr.ResolvePathWithNamespace("gbf", cleanKey)
	if !ok {
		t.Fatal("failed to resolve path")
	}
	_ = os.MkdirAll(filepath.Dir(filePath), 0755)
	_ = os.WriteFile(filePath, payload, 0644)
	legacyExt := `{"ContentType":"audio/mpeg","ETag":"\"legacy-concurrent-etag\"","LastModified":"Wed, 21 Oct 2026 07:28:00 GMT","v":1,"verified":false}`
	_ = os.WriteFile(filePath+".ext", []byte(legacyExt), 0644)

	cacheMgr.ClearRAM()

	const concurrency = 8
	errCh := make(chan error, concurrency)

	for i := 0; i < concurrency; i++ {
		go func(id int) {
			req, err := http.NewRequest(http.MethodGet, "https://"+targetHost+urlPath, nil)
			if err != nil {
				errCh <- fmt.Errorf("worker %d: req creation failed: %v", id, err)
				return
			}
			req.RequestURI = urlPath

			var buf bytes.Buffer
			keepAlive := srv.handleDecryptedRequest(&buf, req, targetHost)
			if !keepAlive {
				errCh <- fmt.Errorf("worker %d: expected keepAlive true", id)
				return
			}

			rawOut := buf.String()
			if !strings.HasPrefix(rawOut, "HTTP/1.1 200 OK\r\n") {
				errCh <- fmt.Errorf("worker %d: expected 200 OK, got: %s", id, rawOut[:min(len(rawOut), 100)])
				return
			}

			_, _, body := splitRawResponse(t, buf.Bytes())
			if !bytes.Equal(body, payload) {
				errCh <- fmt.Errorf("worker %d: body mismatch (got %d bytes, want %d)", id, len(body), len(payload))
				return
			}
			errCh <- nil
		}(i)
	}

	for i := 0; i < concurrency; i++ {
		if err := <-errCh; err != nil {
			t.Fatal(err)
		}
	}

	// Verify .ext file was promoted to verified: true
	extBytes, err := os.ReadFile(filePath + ".ext")
	if err != nil {
		t.Fatalf("failed to read updated .ext: %v", err)
	}
	if !strings.Contains(string(extBytes), `"verified":true`) {
		t.Fatalf("expected updated .ext to contain verified:true, got: %s", string(extBytes))
	}

	// Verify CacheItem has full Data and RAM SLRU exclusion holds
	item, src := cacheMgr.GetWithNamespace("gbf", cleanKey)
	if item == nil || src != "DISK" {
		t.Fatalf("expected DISK hit, got %s", src)
	}
	if item.Data == nil || len(item.Data) != int(fileSize) || !bytes.Equal(item.Data, payload) {
		t.Fatalf("expected full Data, got %d bytes", len(item.Data))
	}
	if cacheMgr.IsRAMProtected("gbf", cleanKey) {
		t.Fatal("large asset must not enter RAM Protected segment")
	}
}

// TestProxy_LargeAsset_BoostRAM_vs_DiskStream tests that when Boost is active with sufficient budget,
// large assets (>2MB) are served directly from RAM (RAMHits incremented, fast path),
// and when Boost is disabled, the same asset falls back to full disk read (DiskHits incremented).
func TestProxy_LargeAsset_BoostRAM_vs_DiskStream(t *testing.T) {
	cleanupMem := cache.SetSystemMemoryProviderForTest(func() (process.MemoryInfo, error) {
		return process.MemoryInfo{
			TotalBytes:     32 * 1024 * 1024 * 1024,
			AvailableBytes: 16 * 1024 * 1024 * 1024,
		}, nil
	})
	defer cleanupMem()

	tempDir := t.TempDir()
	cfgMgr := config.NewManager(filepath.Join(tempDir, "config.json"))
	certMgr, err := cert.NewManager(filepath.Join(tempDir, "certs"))
	if err != nil {
		t.Fatalf("failed to init cert manager: %v", err)
	}
	cacheMgr := cache.NewManager(filepath.Join(tempDir, "cache"), 64) // 64MB budget
	defer cacheMgr.Close()
	stats := telemetry.NewStats()
	defer stats.Close()

	srv := NewProxyServer(cfgMgr, certMgr, cacheMgr, stats)
	defer srv.Stop()

	// 1. Create 3 MB verified file
	sizeBytes := int64(3 * 1024 * 1024)
	urlPath := "/assets/sound/large_voice.mp3"
	cleanKey := "assets/sound/large_voice.mp3"
	payload := make([]byte, sizeBytes)
	copy(payload, "\x89PNG\r\n\x1a\n\x00\x00\x00\rIHDRvoice")

	filePath, ok := cacheMgr.ResolvePathWithNamespace("gbf", cleanKey)
	if !ok {
		t.Fatalf("failed to resolve path")
	}
	_ = os.MkdirAll(filepath.Dir(filePath), 0755)
	if err := os.WriteFile(filePath, payload, 0644); err != nil {
		t.Fatalf("failed to write payload: %v", err)
	}
	fi, _ := os.Stat(filePath)
	extMeta := fmt.Sprintf(`{"ct":"audio/mpeg","ETag":"\"voice-etag\"","LastModified":"Wed, 21 Oct 2026 07:28:00 GMT","v":1,"verified":true,"size":%d,"mtime":%d}`, fi.Size(), fi.ModTime().UnixNano())
	if err := os.WriteFile(filePath+".ext", []byte(extMeta), 0644); err != nil {
		t.Fatalf("failed to write .ext: %v", err)
	}

	host := "prd-game-a-granbluefantasy.akamaized.net"

	// 2. Normal Mode (Boost disabled): must hit Disk Stream
	cacheMgr.ResidentPool().Disable(0)
	cacheMgr.ClearRAM()

	reqNormal, _ := http.NewRequest(http.MethodGet, "https://"+host+urlPath, nil)
	reqNormal.RequestURI = urlPath
	var bufNormal bytes.Buffer
	srv.handleStaticAssetLower(&bufNormal, reqNormal, host, urlPath)

	respNormal, err := http.ReadResponse(bufio.NewReader(&bufNormal), reqNormal)
	if err != nil {
		t.Fatalf("failed to parse normal response: %v", err)
	}
	bodyNormal, _ := io.ReadAll(respNormal.Body)
	respNormal.Body.Close()

	if respNormal.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 OK, got %d", respNormal.StatusCode)
	}
	if respNormal.ContentLength != sizeBytes {
		t.Fatalf("expected Content-Length %d, got %d", sizeBytes, respNormal.ContentLength)
	}
	if int64(len(bodyNormal)) != sizeBytes || !bytes.Equal(bodyNormal, payload) {
		t.Fatalf("expected body len %d matching payload, got %d", sizeBytes, len(bodyNormal))
	}
	if stats.DiskHits.Load() != 1 {
		t.Fatalf("expected 1 DiskHit in normal mode, got %d", stats.DiskHits.Load())
	}

	// 3. Boost Mode: prewarm cache, 3MB file enters ResidentPool
	progress := cacheMgr.PrewarmBoostPoolWithProgress(nil, nil, nil)
	if progress.State != cache.BoostStateCompleted {
		t.Fatalf("expected boost completed, got %s", progress.State)
	}
	if progress.LoadedFiles != 1 {
		t.Fatalf("expected 1 file loaded into boost resident pool, got %d", progress.LoadedFiles)
	}

	reqBoost, _ := http.NewRequest(http.MethodGet, "https://"+host+urlPath, nil)
	reqBoost.RequestURI = urlPath
	var bufBoost bytes.Buffer
	srv.handleStaticAssetLower(&bufBoost, reqBoost, host, urlPath)

	respBoost, err := http.ReadResponse(bufio.NewReader(&bufBoost), reqBoost)
	if err != nil {
		t.Fatalf("failed to parse boost response: %v", err)
	}
	bodyBoost, _ := io.ReadAll(respBoost.Body)
	respBoost.Body.Close()

	if respBoost.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 OK, got %d", respBoost.StatusCode)
	}
	if respBoost.ContentLength != sizeBytes {
		t.Fatalf("expected Content-Length %d, got %d", sizeBytes, respBoost.ContentLength)
	}
	if int64(len(bodyBoost)) != sizeBytes || !bytes.Equal(bodyBoost, payload) {
		t.Fatalf("expected body len %d matching payload, got %d", sizeBytes, len(bodyBoost))
	}
	if stats.RAMHits.Load() != 1 {
		t.Fatalf("expected 1 RAMHit in boost mode, got %d", stats.RAMHits.Load())
	}

	// 4. Disable Boost: must fall back to full disk read
	cacheMgr.ResidentPool().Disable(0)
	cacheMgr.ClearRAM()

	reqFallback, _ := http.NewRequest(http.MethodGet, "https://"+host+urlPath, nil)
	reqFallback.RequestURI = urlPath
	var bufFallback bytes.Buffer
	srv.handleStaticAssetLower(&bufFallback, reqFallback, host, urlPath)

	respFallback, err := http.ReadResponse(bufio.NewReader(&bufFallback), reqFallback)
	if err != nil {
		t.Fatalf("failed to parse fallback response: %v", err)
	}
	bodyFallback, _ := io.ReadAll(respFallback.Body)
	respFallback.Body.Close()

	if respFallback.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 OK, got %d", respFallback.StatusCode)
	}
	if int64(len(bodyFallback)) != sizeBytes || !bytes.Equal(bodyFallback, payload) {
		t.Fatalf("expected body len %d matching payload, got %d", sizeBytes, len(bodyFallback))
	}
	if stats.DiskHits.Load() != 2 {
		t.Fatalf("expected 2 DiskHits after fallback, got %d", stats.DiskHits.Load())
	}

	// Verify CacheItem has full Data and RAM SLRU exclusion holds
	itemFallback, srcFallback := cacheMgr.GetWithNamespace("gbf", cleanKey)
	if itemFallback == nil || srcFallback != "DISK" {
		t.Fatalf("expected DISK hit after fallback, got %s", srcFallback)
	}
	if itemFallback.Data == nil || len(itemFallback.Data) != int(sizeBytes) || !bytes.Equal(itemFallback.Data, payload) {
		t.Fatalf("expected full Data after fallback, got %d bytes", len(itemFallback.Data))
	}
	if cacheMgr.IsRAMProtected("gbf", cleanKey) {
		t.Fatal("large asset must not enter RAM Protected segment")
	}
}

// TestProxy_DiskCacheHit_vs_NetworkMiss_StreamThroughDichotomy verifies that:
// 1. Network Cache Miss for a large asset (>2MB) executes Stream-Through (streaming from upstream while writing to disk).
// 2. Subsequent Cache Hit for the same asset executes full in-memory disk read and sendCachedAssetResponseFast.
// 3. Both responses deliver byte-for-byte identical content to the client.
// 4. Large assets (>2MB) remain strictly excluded from RAM SLRU admission on disk hits.
func TestProxy_DiskCacheHit_vs_NetworkMiss_StreamThroughDichotomy(t *testing.T) {
	tempDir := t.TempDir()
	cfgMgr := config.NewManager(filepath.Join(tempDir, "config.json"))
	cacheMgr := cache.NewManager(tempDir, 16)
	defer cacheMgr.Close()
	stats := telemetry.NewStats()
	defer stats.Close()

	const totalSize = 3 * 1024 * 1024
	payload := makeTestPayload(totalSize)

	var upstreamHits atomic.Int64
	ts, client := newMockAssetServer(func(w http.ResponseWriter, r *http.Request) {
		upstreamHits.Add(1)
		w.Header().Set("Content-Type", "image/png")
		w.Header().Set("ETag", `"dichotomy-etag"`)
		w.Header().Set("Content-Length", strconv.Itoa(len(payload)))
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(payload)
	})
	defer ts.Close()

	srv := &ProxyServer{
		cfgMgr:   cfgMgr,
		cacheMgr: cacheMgr,
		stats:    stats,
	}
	setTestAssetClient(srv, client)

	const host = "prd-game-a-granbluefantasy.akamaized.net"
	const path = "/assets/img/sp/dichotomy_test.png"
	cleanKey := strings.TrimPrefix(path, "/")

	// 1. First Request: Cache Miss -> Stream-Through from network to client and disk temp
	req1, _ := http.NewRequest(http.MethodGet, "https://"+host+path, nil)
	req1.RequestURI = path
	req1.Host = host

	var buf1 bytes.Buffer
	keepAlive1 := srv.handleStaticAssetLower(&buf1, req1, host, path)
	if !keepAlive1 {
		t.Fatal("expected keepAlive to be true for stream-through network miss")
	}
	if upstreamHits.Load() != 1 {
		t.Fatalf("expected exactly 1 upstream hit on cache miss, got %d", upstreamHits.Load())
	}
	if stats.CacheMisses.Load() != 1 {
		t.Fatalf("expected stats.CacheMisses == 1, got %d", stats.CacheMisses.Load())
	}

	_, _, body1 := splitRawResponse(t, buf1.Bytes())
	if !bytes.Equal(body1, payload) {
		t.Fatalf("cache miss stream-through body mismatch: got %d bytes, want %d", len(body1), len(payload))
	}

	// Verify asset was committed to disk
	filePath, ok := cacheMgr.ResolvePathWithNamespace("gbf", cleanKey)
	if !ok {
		t.Fatalf("failed to resolve path on disk")
	}
	diskBytes, err := os.ReadFile(filePath)
	if err != nil || !bytes.Equal(diskBytes, payload) {
		t.Fatalf("disk content mismatch after stream-through commit")
	}

	// Clear RAM cache so subsequent request must hit disk cache
	cacheMgr.ClearRAM()

	// 2. Second Request: Disk Cache Hit -> Full in-memory read, sendCachedAssetResponseFast
	req2, _ := http.NewRequest(http.MethodGet, "https://"+host+path, nil)
	req2.RequestURI = path
	req2.Host = host

	var buf2 bytes.Buffer
	keepAlive2 := srv.handleDecryptedRequest(&buf2, req2, host)
	if !keepAlive2 {
		t.Fatal("expected keepAlive to be true for disk cache hit fast response")
	}
	if upstreamHits.Load() != 1 {
		t.Fatalf("expected no new upstream hit on cache hit, got %d", upstreamHits.Load())
	}
	if stats.DiskHits.Load() != 1 {
		t.Fatalf("expected stats.DiskHits == 1, got %d", stats.DiskHits.Load())
	}

	rawOut2 := buf2.String()
	if !strings.HasPrefix(rawOut2, "HTTP/1.1 200 OK\r\n") {
		t.Fatalf("expected HTTP/1.1 200 OK on disk hit, got:\n%s", rawOut2)
	}

	_, _, body2 := splitRawResponse(t, buf2.Bytes())
	if !bytes.Equal(body2, payload) {
		t.Fatalf("disk hit body mismatch: got %d bytes, want %d", len(body2), len(payload))
	}

	// 3. Verify CacheItem loaded from disk has full Data populated
	item, src := cacheMgr.GetWithNamespace("gbf", cleanKey)
	if item == nil || src != "DISK" {
		t.Fatalf("expected DISK hit, got %s", src)
	}
	if item.Data == nil || len(item.Data) != len(payload) || !bytes.Equal(item.Data, payload) {
		t.Fatalf("expected full Data in CacheItem on disk hit, got %d bytes", len(item.Data))
	}

	// 4. Verify RAM SLRU admission restriction: >2MB file must NOT enter RAM SLRU
	if cacheMgr.IsRAMProtected("gbf", cleanKey) {
		t.Fatal("large asset (>2MB) must not enter RAM Protected segment")
	}
}
