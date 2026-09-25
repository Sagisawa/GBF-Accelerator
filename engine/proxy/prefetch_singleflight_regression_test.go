package proxy

import (
	"bytes"
	"net/http"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"gbf-proxy/cache"
	"gbf-proxy/config"
	"gbf-proxy/telemetry"
)

// TestSingleFlight_PrefetchAndForegroundCoalescing verifies that when Prefetch is
// downloading an asset, an incoming browser request for the same asset coalesces into
// the in-flight download without launching a second request to upstream.
func TestSingleFlight_PrefetchAndForegroundCoalescing(t *testing.T) {
	tempDir := t.TempDir()
	cfgMgr := config.NewManager(filepath.Join(tempDir, "config.json"))
	cacheMgr := cache.NewManager(tempDir, 16)
	defer cacheMgr.Close()
	stats := telemetry.NewStats()
	defer stats.Close()

	var upstreamHits atomic.Int64
	payload := make([]byte, 8*1024)
	copy(payload, []byte("\x89PNG\r\n\x1a\n"))

	ts, client := newMockAssetServer(func(w http.ResponseWriter, r *http.Request) {
		upstreamHits.Add(1)
		time.Sleep(30 * time.Millisecond) // slow download
		w.Header().Set("Content-Type", "image/png")
		w.Header().Set("ETag", `"singleflight-etag"`)
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
	pe := newPrefetchEngine(srv)
	defer pe.Stop()

	const benchHost = "prd-game-a-granbluefantasy.akamaized.net"
	const path = "/assets/img/sp/singleflight_coalesce.png"

	var wg sync.WaitGroup
	wg.Add(2)

	// 1. Prefetch starts download
	go func() {
		defer wg.Done()
		pe.processFetchItem(prefetchItem{
			prio: 2,
			host: benchHost,
			path: path,
		})
	}()

	// 2. Real browser request arrives 5ms later while download is in-flight
	var respBuf bytes.Buffer
	go func() {
		defer wg.Done()
		time.Sleep(5 * time.Millisecond)
		req, _ := http.NewRequest(http.MethodGet, "https://"+benchHost+path, nil)
		req.RequestURI = path
		req.Host = benchHost
		srv.handleStaticAssetLower(&respBuf, req, benchHost, path)
	}()

	wg.Wait()

	// Assert: Upstream was contacted exactly ONCE
	if hits := upstreamHits.Load(); hits != 1 {
		t.Fatalf("expected strictly 1 upstream hit (coalesced), got %d", hits)
	}

	// Assert: Browser received the full asset
	if !bytes.Contains(respBuf.Bytes(), []byte("\x89PNG")) {
		t.Fatalf("browser response did not contain asset data: %s", respBuf.String())
	}
}

// TestSingleFlight_ForegroundPreemptsPrefetch verifies that when a browser request
// is already in-flight, a subsequent prefetch task for the same resource detects
// that the flight is already active and skips launching a duplicate request.
func TestSingleFlight_ForegroundPreemptsPrefetch(t *testing.T) {
	tempDir := t.TempDir()
	cfgMgr := config.NewManager(filepath.Join(tempDir, "config.json"))
	cacheMgr := cache.NewManager(tempDir, 16)
	defer cacheMgr.Close()
	stats := telemetry.NewStats()
	defer stats.Close()

	var upstreamHits atomic.Int64
	payload := make([]byte, 8*1024)
	copy(payload, []byte("var fg = 1; "))

	ts, client := newMockAssetServer(func(w http.ResponseWriter, r *http.Request) {
		upstreamHits.Add(1)
		time.Sleep(30 * time.Millisecond)
		w.Header().Set("Content-Type", "application/javascript")
		w.Header().Set("ETag", `"fg-etag"`)
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
	pe := newPrefetchEngine(srv)
	defer pe.Stop()

	const benchHost = "prd-game-a-granbluefantasy.akamaized.net"
	const path = "/assets/js/fg_first.js"

	var wg sync.WaitGroup
	wg.Add(2)

	// 1. Foreground request starts download
	go func() {
		defer wg.Done()
		req, _ := http.NewRequest(http.MethodGet, "https://"+benchHost+path, nil)
		req.RequestURI = path
		req.Host = benchHost
		var buf bytes.Buffer
		srv.handleStaticAssetLower(&buf, req, benchHost, path)
	}()

	// 2. Prefetch tries to fetch the same item 5ms later
	go func() {
		defer wg.Done()
		time.Sleep(5 * time.Millisecond)
		pe.processFetchItem(prefetchItem{
			prio: 1,
			host: benchHost,
			path: path,
		})
	}()

	wg.Wait()

	if hits := upstreamHits.Load(); hits != 1 {
		t.Fatalf("expected strictly 1 upstream hit, got %d", hits)
	}
}
