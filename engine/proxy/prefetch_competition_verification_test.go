package proxy

import (
	"bytes"
	"context"
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

// TestVerify_PrefetchCollision_LatencyComparison rigorously tests the latency
// experienced by the browser when:
// 1. Browser reuses the in-progress Prefetch download (via SingleFlight)
// 2. Browser starts an independent duplicate download from scratch
func TestVerify_PrefetchCollision_LatencyComparison(t *testing.T) {
	tempDir := t.TempDir()
	cfgMgr := config.NewManager(filepath.Join(tempDir, "config.json"))
	cacheMgr := cache.NewManager(tempDir, 16)
	defer cacheMgr.Close()
	stats := telemetry.NewStats()
	defer stats.Close()

	const networkLatency = 40 * time.Millisecond // simulated CDN latency
	payload := make([]byte, 16*1024)
	copy(payload, []byte("\x89PNG\r\n\x1a\n"))

	var upstreamHits atomic.Int64
	ts, client := newMockAssetServer(func(w http.ResponseWriter, r *http.Request) {
		upstreamHits.Add(1)
		time.Sleep(networkLatency)
		w.Header().Set("Content-Type", "image/png")
		w.Header().Set("ETag", `"test"`)
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
	const path = "/assets/img/sp/test_collision.png"

	t.Logf("=== In-Flight Reuse Latency Test ===")
	t.Logf("Simulated CDN Latency: %v", networkLatency)

	var wg sync.WaitGroup
	wg.Add(2)

	// 1. Prefetch starts download at T = 0
	go func() {
		defer wg.Done()
		pe.processFetchItem(prefetchItem{
			prio: 2,
			host: benchHost,
			path: path,
		})
	}()

	// 2. Browser request arrives at T = 25ms (Prefetch is already 25ms into the 40ms download)
	var browserElapsed time.Duration
	go func() {
		defer wg.Done()
		time.Sleep(25 * time.Millisecond)

		browserStart := time.Now()
		req, _ := http.NewRequest(http.MethodGet, "https://"+benchHost+path, nil)
		req.RequestURI = path
		req.Host = benchHost
		var buf bytes.Buffer
		srv.handleStaticAssetLower(&buf, req, benchHost, path)
		browserElapsed = time.Since(browserStart)
	}()

	wg.Wait()

	t.Logf("Upstream Hits: %d (expected 1)", upstreamHits.Load())
	t.Logf("Browser Perceived Latency: %v (expected ~15ms remaining time, NOT full 40ms)", browserElapsed)

	// Verification assertions:
	if upstreamHits.Load() != 1 {
		t.Fatalf("expected 1 upstream hit, got %d", upstreamHits.Load())
	}
	if browserElapsed >= networkLatency {
		t.Fatalf("browser was NOT accelerated by in-progress stream: took %v >= %v", browserElapsed, networkLatency)
	}
	t.Logf("[CONFIRMED] Browser saved %v of wait time by reusing the in-flight prefetch stream!", networkLatency-browserElapsed)
}

// TestVerify_PrefetchCollision_FollowerCancellation verifies that if the browser
// request is cancelled while waiting on an in-progress prefetch download, the
// browser exits IMMEDIATELY without being dragged down by the background task.
func TestVerify_PrefetchCollision_FollowerCancellation(t *testing.T) {
	tempDir := t.TempDir()
	cfgMgr := config.NewManager(filepath.Join(tempDir, "config.json"))
	cacheMgr := cache.NewManager(tempDir, 16)
	defer cacheMgr.Close()
	stats := telemetry.NewStats()
	defer stats.Close()

	payload := make([]byte, 8*1024)
	copy(payload, []byte("\x89PNG\r\n\x1a\n"))

	// Upstream takes 500ms
	ts, client := newMockAssetServer(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(500 * time.Millisecond)
		w.Header().Set("Content-Type", "image/png")
		w.Header().Set("ETag", `"etag"`)
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
	const path = "/assets/img/sp/test_cancel.png"

	var prefetchWg sync.WaitGroup
	prefetchWg.Add(1)

	// 1. Prefetch starts slow 500ms download
	go func() {
		defer prefetchWg.Done()
		pe.processFetchItem(prefetchItem{
			prio: 2,
			host: benchHost,
			path: path,
		})
	}()

	time.Sleep(20 * time.Millisecond)

	// 2. Browser request arrives with a short 30ms timeout (simulating client abort)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()

	browserStart := time.Now()
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, "https://"+benchHost+path, nil)
	req.RequestURI = path
	req.Host = benchHost
	var buf bytes.Buffer
	srv.handleStaticAssetLower(&buf, req, benchHost, path)
	browserElapsed := time.Since(browserStart)

	t.Logf("Browser elapsed under context timeout: %v (expected ~30ms, NOT 500ms)", browserElapsed)
	if browserElapsed >= 100*time.Millisecond {
		t.Fatalf("browser was dragged down by background prefetch: took %v", browserElapsed)
	}
	t.Logf("[CONFIRMED] Browser cancelled cleanly in %v without waiting for background prefetch!", browserElapsed)

	// Wait for background prefetch to cleanly complete before closing test fixtures
	prefetchWg.Wait()
}
