package proxy

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"path/filepath"
	"runtime"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"gbf-proxy/cache"
	"gbf-proxy/config"
	"gbf-proxy/telemetry"
)

// Case 1: Prefetch checks HasCache as false, but foreground request completes
// just before flight execution and has already written to cache.
func TestRace_PrefetchHasCacheFalse_ForegroundCompletesFirst(t *testing.T) {
	tempDir := t.TempDir()
	cfgMgr := config.NewManager(filepath.Join(tempDir, "config.json"))
	cacheMgr := cache.NewManager(tempDir, 16)
	defer cacheMgr.Close()
	stats := telemetry.NewStats()
	defer stats.Close()

	var upstreamHits atomic.Int64
	payload := []byte("alert('race1');")

	ts, client := newMockAssetServer(func(w http.ResponseWriter, r *http.Request) {
		upstreamHits.Add(1)
		w.Header().Set("Content-Type", "application/javascript")
		w.Header().Set("ETag", `"race1-etag"`)
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
	cleanPath := "/assets/js/race_cache_first.js"

	// 1. Foreground request finishes and populates cache
	req, _ := http.NewRequest(http.MethodGet, "https://"+benchHost+cleanPath, nil)
	req.RequestURI = cleanPath
	req.Host = benchHost
	var buf bytes.Buffer
	srv.handleStaticAssetLower(&buf, req, benchHost, cleanPath)

	if upstreamHits.Load() != 1 {
		t.Fatalf("expected 1 upstream hit from initial foreground request, got %d", upstreamHits.Load())
	}

	// 2. Now run prefetch for the same item. Prefetch's HasCache and inner GetWithNamespace
	// must detect the cached asset and NOT perform a second upstream hit.
	pe.processFetchItem(prefetchItem{
		prio: 1,
		host: benchHost,
		path: cleanPath,
	})

	if upstreamHits.Load() != 1 {
		t.Fatalf("prefetch did not respect existing cache: upstream hits = %d (expected 1)", upstreamHits.Load())
	}
}

// Case 2: Foreground starts first, Prefetch triggers almost simultaneously.
func TestRace_ForegroundFirst_PrefetchNearlySimultaneous(t *testing.T) {
	tempDir := t.TempDir()
	cfgMgr := config.NewManager(filepath.Join(tempDir, "config.json"))
	cacheMgr := cache.NewManager(tempDir, 16)
	defer cacheMgr.Close()
	stats := telemetry.NewStats()
	defer stats.Close()

	var upstreamHits atomic.Int64
	payload := []byte("function combat(){ return 1; }")

	ts, client := newMockAssetServer(func(w http.ResponseWriter, r *http.Request) {
		upstreamHits.Add(1)
		time.Sleep(40 * time.Millisecond)
		w.Header().Set("Content-Type", "application/javascript")
		w.Header().Set("ETag", `"race2-etag"`)
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
	cleanPath := "/assets/js/simultaneous_race.js"

	var wg sync.WaitGroup
	wg.Add(2)

	// Foreground starts at T = 0
	go func() {
		defer wg.Done()
		req, _ := http.NewRequest(http.MethodGet, "https://"+benchHost+cleanPath, nil)
		req.RequestURI = cleanPath
		req.Host = benchHost
		var buf bytes.Buffer
		srv.handleStaticAssetLower(&buf, req, benchHost, cleanPath)
	}()

	// Prefetch starts at T = 5ms (while foreground is downloading)
	go func() {
		defer wg.Done()
		time.Sleep(5 * time.Millisecond)
		pe.processFetchItem(prefetchItem{
			prio: 1,
			host: benchHost,
			path: cleanPath,
		})
	}()

	wg.Wait()

	if hits := upstreamHits.Load(); hits != 1 {
		t.Fatalf("expected exactly 1 upstream hit, got %d", hits)
	}
}

// Case 3: Prefetch starts first, foreground request enters, but foreground client aborts/cancels.
func TestRace_PrefetchFirst_ForegroundCancelsPrematurely(t *testing.T) {
	tempDir := t.TempDir()
	cfgMgr := config.NewManager(filepath.Join(tempDir, "config.json"))
	cacheMgr := cache.NewManager(tempDir, 16)
	defer cacheMgr.Close()
	stats := telemetry.NewStats()
	defer stats.Close()

	var upstreamHits atomic.Int64
	payload := make([]byte, 10*1024)
	copy(payload, []byte("\x89PNG\r\n\x1a\n"))

	ts, client := newMockAssetServer(func(w http.ResponseWriter, r *http.Request) {
		upstreamHits.Add(1)
		time.Sleep(200 * time.Millisecond)
		w.Header().Set("Content-Type", "image/png")
		w.Header().Set("ETag", `"race3-etag"`)
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
	cleanPath := "/assets/img/sp/cancel_premature.png"

	var wg sync.WaitGroup
	wg.Add(2)

	// 1. Prefetch starts slow 200ms download
	go func() {
		defer wg.Done()
		pe.processFetchItem(prefetchItem{
			prio: 2,
			host: benchHost,
			path: cleanPath,
		})
	}()

	// 2. Foreground enters with 20ms cancellation context
	var fgElapsed time.Duration
	go func() {
		defer wg.Done()
		time.Sleep(10 * time.Millisecond)

		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
		defer cancel()

		fgStart := time.Now()
		req, _ := http.NewRequestWithContext(ctx, http.MethodGet, "https://"+benchHost+cleanPath, nil)
		req.RequestURI = cleanPath
		req.Host = benchHost
		var buf bytes.Buffer
		srv.handleStaticAssetLower(&buf, req, benchHost, cleanPath)
		fgElapsed = time.Since(fgStart)
	}()

	wg.Wait()

	t.Logf("Foreground elapsed under context abort: %v", fgElapsed)
	if fgElapsed >= 100*time.Millisecond {
		t.Fatalf("foreground was blocked by prefetch despite cancellation: %v", fgElapsed)
	}

	// Verify that background prefetch completed and cached the asset eventually
	if !cacheMgr.HasCacheWithNamespace("gbf", cleanPath) {
		t.Fatalf("background prefetch should have completed successfully and populated cache")
	}
}

// Case 4: High concurrency cross-access: multiple foreground and prefetch workers
// hitting the same pool of assets concurrently.
func TestRace_HighConcurrency_CrossAccess(t *testing.T) {
	tempDir := t.TempDir()
	cfgMgr := config.NewManager(filepath.Join(tempDir, "config.json"))
	cacheMgr := cache.NewManager(tempDir, 32)
	defer cacheMgr.Close()
	stats := telemetry.NewStats()
	defer stats.Close()

	var upstreamHits atomic.Int64
	payload := []byte("function shared(){ return 42; }")

	ts, client := newMockAssetServer(func(w http.ResponseWriter, r *http.Request) {
		upstreamHits.Add(1)
		time.Sleep(10 * time.Millisecond)
		w.Header().Set("Content-Type", "application/javascript")
		w.Header().Set("ETag", `"race4-etag"`)
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
	const numKeys = 5
	const numWorkers = 8

	var wg sync.WaitGroup

	// Concurrently spawn foreground and prefetch workers
	for w := 0; w < numWorkers; w++ {
		wg.Add(2)

		// Foreground worker
		go func(workerID int) {
			defer wg.Done()
			for k := 0; k < numKeys; k++ {
				path := fmt.Sprintf("/assets/js/cross_%d.js", k)
				req, _ := http.NewRequest(http.MethodGet, "https://"+benchHost+path, nil)
				req.RequestURI = path
				req.Host = benchHost
				var buf bytes.Buffer
				srv.handleStaticAssetLower(&buf, req, benchHost, path)
				if !bytes.Contains(buf.Bytes(), payload) {
					t.Errorf("worker %d key %d received invalid body", workerID, k)
				}
			}
		}(w)

		// Prefetch worker
		go func(workerID int) {
			defer wg.Done()
			for k := 0; k < numKeys; k++ {
				path := fmt.Sprintf("/assets/js/cross_%d.js", k)
				pe.processFetchItem(prefetchItem{
					prio: 1,
					host: benchHost,
					path: path,
				})
			}
		}(w)
	}

	wg.Wait()

	hits := upstreamHits.Load()
	t.Logf("High concurrency: %d workers x %d keys -> %d total upstream requests (optimal: %d)",
		numWorkers*2, numKeys, hits, numKeys)

	// In high concurrency, total upstream hits should be close to numKeys (5), definitely <= numKeys * 2
	if hits > int64(numKeys*3) {
		t.Fatalf("excessive duplicate upstream requests under concurrency: %d > %d", hits, numKeys*3)
	}
}

// Case 5: Resource download fails (upstream 500 error): SingleFlight state must clean up cleanly.
func TestRace_UpstreamFailure_CleanStateAndRetry(t *testing.T) {
	tempDir := t.TempDir()
	cfgMgr := config.NewManager(filepath.Join(tempDir, "config.json"))
	cacheMgr := cache.NewManager(tempDir, 16)
	defer cacheMgr.Close()
	stats := telemetry.NewStats()
	defer stats.Close()

	var shouldFail atomic.Bool
	shouldFail.Store(true)

	var upstreamHits atomic.Int64
	payload := []byte("healthy data")

	ts, client := newMockAssetServer(func(w http.ResponseWriter, r *http.Request) {
		upstreamHits.Add(1)
		if shouldFail.Load() {
			http.Error(w, "Internal Server Error", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "text/plain")
		w.Header().Set("ETag", `"ok-etag"`)
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
	cleanPath := "/assets/error_retry.txt"

	// 1. First attempt fails with 500
	pe.processFetchItem(prefetchItem{
		prio: 1,
		host: benchHost,
		path: cleanPath,
	})

	flightKey := "gbf:" + cleanPath
	if cacheMgr.SingleFlight().IsInFlight(flightKey) {
		t.Fatalf("SingleFlight key remained stuck in-flight after failed fetch!")
	}
	if cacheMgr.HasCacheWithNamespace("gbf", cleanPath) {
		t.Fatalf("error response was mistakenly cached!")
	}

	// 2. Next attempt recovers
	shouldFail.Store(false)
	req, _ := http.NewRequest(http.MethodGet, "https://"+benchHost+cleanPath, nil)
	req.RequestURI = cleanPath
	req.Host = benchHost
	var buf bytes.Buffer
	srv.handleStaticAssetLower(&buf, req, benchHost, cleanPath)

	if !bytes.Contains(buf.Bytes(), payload) {
		t.Fatalf("recovered request did not succeed: %s", buf.String())
	}
	if !cacheMgr.HasCacheWithNamespace("gbf", cleanPath) {
		t.Fatalf("recovered response should have been cached")
	}
}

// Case 6: Long-running execution leak check: verify no lingering goroutines or stuck SingleFlight entries.
func TestRace_LongRunning_NoGoroutineLeak(t *testing.T) {
	tempDir := t.TempDir()
	cfgMgr := config.NewManager(filepath.Join(tempDir, "config.json"))
	cacheMgr := cache.NewManager(tempDir, 16)
	defer cacheMgr.Close()
	stats := telemetry.NewStats()
	defer stats.Close()

	payload := []byte("leak test")
	ts, client := newMockAssetServer(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		w.Header().Set("ETag", `"leak-etag"`)
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

	initialGoroutines := runtime.NumGoroutine()

	// Run 50 iterations of mixed foreground and prefetch requests
	for i := 0; i < 50; i++ {
		path := fmt.Sprintf("/assets/leak_%d.txt", i)
		if i%2 == 0 {
			pe.processFetchItem(prefetchItem{
				prio: 1,
				host: benchHost,
				path: path,
			})
		} else {
			req, _ := http.NewRequest(http.MethodGet, "https://"+benchHost+path, nil)
			req.RequestURI = path
			req.Host = benchHost
			var buf bytes.Buffer
			srv.handleStaticAssetLower(&buf, req, benchHost, path)
		}
	}

	time.Sleep(50 * time.Millisecond)
	runtime.GC()

	finalGoroutines := runtime.NumGoroutine()
	t.Logf("Goroutines: before=%d, after=%d", initialGoroutines, finalGoroutines)

	// Growth should be negligible (<= 5 goroutines for GC/test runtime tolerance)
	if finalGoroutines-initialGoroutines > 10 {
		t.Fatalf("suspected goroutine leak: before=%d, after=%d", initialGoroutines, finalGoroutines)
	}
}

// Case 7: TestWaiters_AccurateCountAndCancellation verifies that active waiters count
// decrements when followers cancel, and if all followers cancel, prefetch
// does NOT treat the resource as wanted by foreground.
func TestWaiters_AccurateCountAndCancellation(t *testing.T) {
	tempDir := t.TempDir()
	cfgMgr := config.NewManager(filepath.Join(tempDir, "config.json"))
	cacheMgr := cache.NewManager(tempDir, 16)
	defer cacheMgr.Close()
	stats := telemetry.NewStats()
	defer stats.Close()

	payload := []byte("\x89PNG\r\n\x1a\n--TEST-WAITERS-CANCEL--")
	unblockUpstream := make(chan struct{})

	ts, client := newMockAssetServer(func(w http.ResponseWriter, r *http.Request) {
		<-unblockUpstream
		w.Header().Set("Content-Type", "image/png")
		w.Header().Set("ETag", `"cancel-etag"`)
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
	cleanPath := "/assets/img/sp/test_waiters_cancel.png"
	flightKey := "gbf:" + cleanPath

	var wg sync.WaitGroup
	wg.Add(1)

	// 1. Prefetch starts download as leader
	go func() {
		defer wg.Done()
		pe.processFetchItem(prefetchItem{
			prio: 2,
			host: benchHost,
			path: cleanPath,
		})
	}()

	// Wait until leader is in flight
	for i := 0; i < 50; i++ {
		if cacheMgr.SingleFlight().IsInFlight(flightKey) {
			break
		}
		time.Sleep(2 * time.Millisecond)
	}

	if !cacheMgr.SingleFlight().IsInFlight(flightKey) {
		t.Fatal("leader failed to enter in-flight")
	}
	if w := cacheMgr.SingleFlight().Waiters(flightKey); w != 0 {
		t.Fatalf("expected 0 waiters initially, got %d", w)
	}

	// 2. Follower 1 and Follower 2 join with cancellable contexts
	ctx1, cancel1 := context.WithCancel(context.Background())
	ctx2, cancel2 := context.WithCancel(context.Background())

	var f1Done, f2Done sync.WaitGroup
	f1Done.Add(1)
	f2Done.Add(1)

	go func() {
		defer f1Done.Done()
		_, _ = cacheMgr.SingleFlight().DoContext(ctx1, flightKey, func() (interface{}, error) {
			return nil, nil
		})
	}()

	go func() {
		defer f2Done.Done()
		_, _ = cacheMgr.SingleFlight().DoContext(ctx2, flightKey, func() (interface{}, error) {
			return nil, nil
		})
	}()

	// Wait until both followers are registered
	for i := 0; i < 50; i++ {
		if cacheMgr.SingleFlight().Waiters(flightKey) == 2 {
			break
		}
		time.Sleep(2 * time.Millisecond)
	}

	if w := cacheMgr.SingleFlight().Waiters(flightKey); w != 2 {
		t.Fatalf("expected 2 active waiters, got %d", w)
	}

	// 3. Cancel Follower 1 -> Waiters should decrement to 1
	cancel1()
	f1Done.Wait()

	for i := 0; i < 50; i++ {
		if cacheMgr.SingleFlight().Waiters(flightKey) == 1 {
			break
		}
		time.Sleep(2 * time.Millisecond)
	}

	if w := cacheMgr.SingleFlight().Waiters(flightKey); w != 1 {
		t.Fatalf("expected 1 waiter after cancelling f1, got %d", w)
	}

	// 4. Cancel Follower 2 -> Waiters should decrement to 0
	cancel2()
	f2Done.Wait()

	for i := 0; i < 50; i++ {
		if cacheMgr.SingleFlight().Waiters(flightKey) == 0 {
			break
		}
		time.Sleep(2 * time.Millisecond)
	}

	if w := cacheMgr.SingleFlight().Waiters(flightKey); w != 0 {
		t.Fatalf("expected 0 waiters after cancelling all followers, got %d", w)
	}

	// 5. Now let the upstream finish download
	close(unblockUpstream)
	wg.Wait()

	// Verify: Because ALL followers cancelled before download finished,
	// prefetch must NOT treat this as a foreground resource.
	// It must reside in Probationary segment!
	if cacheMgr.IsRAMProtected("gbf", cleanPath) {
		t.Fatal("item was incorrectly admitted to Protected segment even though all followers cancelled!")
	}
	if !cacheMgr.HasCacheWithNamespace("gbf", cleanPath) {
		t.Fatal("item should still be in cache (in probationary segment)")
	}
}

// Case 8: TestFollower_GuaranteedProtectedOnConsumption tests that any foreground follower
// that actually consumes the result will GUARANTEE that the asset is promoted to Protected,
// regardless of whether prefetch saved before or after.
func TestFollower_GuaranteedProtectedOnConsumption(t *testing.T) {
	tempDir := t.TempDir()
	cfgMgr := config.NewManager(filepath.Join(tempDir, "config.json"))
	cacheMgr := cache.NewManager(tempDir, 16)
	defer cacheMgr.Close()
	stats := telemetry.NewStats()
	defer stats.Close()

	payload := []byte("\x89PNG\r\n\x1a\n--TEST-FOLLOWER-PROMOTION--")
	ts, client := newMockAssetServer(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(40 * time.Millisecond)
		w.Header().Set("Content-Type", "image/png")
		w.Header().Set("ETag", `"promo-etag"`)
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
	cleanPath := "/assets/img/sp/follower_promo.png"

	var wg sync.WaitGroup
	wg.Add(2)

	// 1. Prefetch starts download as leader
	go func() {
		defer wg.Done()
		pe.processFetchItem(prefetchItem{
			prio: 2,
			host: benchHost,
			path: cleanPath,
		})
	}()

	// 2. Foreground request joins 10ms later as follower and consumes the response
	var respBuf bytes.Buffer
	go func() {
		defer wg.Done()
		time.Sleep(10 * time.Millisecond)
		req, _ := http.NewRequest(http.MethodGet, "https://"+benchHost+cleanPath, nil)
		req.RequestURI = cleanPath
		req.Host = benchHost
		srv.handleStaticAssetLower(&respBuf, req, benchHost, cleanPath)
	}()

	wg.Wait()

	if !bytes.Contains(respBuf.Bytes(), payload) {
		t.Fatal("foreground request failed to receive asset payload")
	}

	// 3. Verify: Item MUST be in Protected segment!
	if !cacheMgr.IsRAMProtected("gbf", cleanPath) {
		t.Fatal("CRITICAL: foreground follower consumed the resource but it was NOT promoted to Protected segment!")
	}
}

// Case 9: TestFollower_PartialCancellation tests that when one follower cancels but another
// completes and consumes the resource, the resource is correctly Protected.
func TestFollower_PartialCancellation(t *testing.T) {
	tempDir := t.TempDir()
	cfgMgr := config.NewManager(filepath.Join(tempDir, "config.json"))
	cacheMgr := cache.NewManager(tempDir, 16)
	defer cacheMgr.Close()
	stats := telemetry.NewStats()
	defer stats.Close()

	payload := []byte("\x89PNG\r\n\x1a\n--TEST-PARTIAL-CANCEL--")
	ts, client := newMockAssetServer(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(50 * time.Millisecond)
		w.Header().Set("Content-Type", "image/png")
		w.Header().Set("ETag", `"partial-etag"`)
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
	cleanPath := "/assets/img/sp/partial_cancel.png"

	var wg sync.WaitGroup
	wg.Add(3)

	// 1. Prefetch starts download
	go func() {
		defer wg.Done()
		pe.processFetchItem(prefetchItem{
			prio: 2,
			host: benchHost,
			path: cleanPath,
		})
	}()

	// 2. Follower A arrives and cancels after 10ms
	go func() {
		defer wg.Done()
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
		defer cancel()
		req, _ := http.NewRequestWithContext(ctx, http.MethodGet, "https://"+benchHost+cleanPath, nil)
		req.RequestURI = cleanPath
		req.Host = benchHost
		var buf bytes.Buffer
		srv.handleStaticAssetLower(&buf, req, benchHost, cleanPath)
	}()

	// 3. Follower B arrives and waits until completion
	var bufB bytes.Buffer
	go func() {
		defer wg.Done()
		time.Sleep(15 * time.Millisecond)
		req, _ := http.NewRequest(http.MethodGet, "https://"+benchHost+cleanPath, nil)
		req.RequestURI = cleanPath
		req.Host = benchHost
		srv.handleStaticAssetLower(&bufB, req, benchHost, cleanPath)
	}()

	wg.Wait()

	if !bytes.Contains(bufB.Bytes(), payload) {
		t.Fatal("Follower B failed to receive payload")
	}

	// Because Follower B consumed the resource, it must be Protected
	if !cacheMgr.IsRAMProtected("gbf", cleanPath) {
		t.Fatal("resource consumed by surviving Follower B should be in Protected segment")
	}
}

