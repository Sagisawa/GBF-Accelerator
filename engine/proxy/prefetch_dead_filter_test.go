package proxy

import (
	"bytes"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"runtime"
	"sync/atomic"
	"testing"
	"time"

	"gbf-proxy/cache"
	"gbf-proxy/config"
	"gbf-proxy/telemetry"
)

// 1. 404/410 会记录，其他错误 (如 500) 不会记录
func TestDeadFilter_Record404And410Only(t *testing.T) {
	tempDir := t.TempDir()
	cfgMgr := config.NewManager(filepath.Join(tempDir, "config.json"))
	cacheMgr := cache.NewManager(tempDir, 16)
	defer cacheMgr.Close()
	stats := telemetry.NewStats()
	defer stats.Close()

	var upstreamStatus atomic.Int32
	upstreamStatus.Store(http.StatusNotFound)

	ts, client := newMockAssetServer(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(int(upstreamStatus.Load()))
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

	const host = "prd-game-a-granbluefantasy.akamaized.net"
	const path404 = "/assets/img/sp/not_found.png"
	const path410 = "/assets/img/sp/gone.png"
	const path500 = "/assets/img/sp/server_error.png"

	// 1a. Test 404
	upstreamStatus.Store(http.StatusNotFound)
	pe.processFetchItem(prefetchItem{prio: 2, host: host, path: path404})
	if !pe.deadFilter.IsDead(host, path404) {
		t.Fatalf("expected %s to be marked dead after 404", path404)
	}

	// 1b. Test 410
	upstreamStatus.Store(http.StatusGone)
	pe.processFetchItem(prefetchItem{prio: 2, host: host, path: path410})
	if !pe.deadFilter.IsDead(host, path410) {
		t.Fatalf("expected %s to be marked dead after 410", path410)
	}

	// 1c. Test 500 (should NOT be added to deadFilter)
	upstreamStatus.Store(http.StatusInternalServerError)
	pe.processFetchItem(prefetchItem{prio: 2, host: host, path: path500})
	if pe.deadFilter.IsDead(host, path500) {
		t.Fatalf("expected %s NOT to be marked dead after 500 error", path500)
	}
}

// 2. 未过期时会过滤 (包括 discoveryWorker 入队过滤和 processFetchItem 快速退出)
func TestDeadFilter_FilteredBeforeExpiration(t *testing.T) {
	tempDir := t.TempDir()
	cfgMgr := config.NewManager(filepath.Join(tempDir, "config.json"))
	cacheMgr := cache.NewManager(tempDir, 16)
	defer cacheMgr.Close()
	stats := telemetry.NewStats()
	defer stats.Close()

	var upstreamHits atomic.Int64
	ts, client := newMockAssetServer(func(w http.ResponseWriter, r *http.Request) {
		upstreamHits.Add(1)
		w.WriteHeader(http.StatusNotFound)
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

	const host = "prd-game-a-granbluefantasy.akamaized.net"
	const deadPath = "/assets/img/sp/dead_asset.png"

	// Pre-seed deadFilter
	pe.deadFilter.Add(host, deadPath)

	// 2a. Verify processFetchItem returns immediately without hitting upstream
	pe.processFetchItem(prefetchItem{prio: 2, host: host, path: deadPath})
	if hits := upstreamHits.Load(); hits != 0 {
		t.Fatalf("expected 0 upstream hits for dead asset, got %d", hits)
	}

	// 2b. Verify discoveryWorker skips enqueuing this dead item
	jsPayload := fmt.Sprintf(`Game.imgUri + '%s';`, deadPath)
	pe.MaybeEnqueueDiscovery(host, "/assets/js/test_discovery.js", []byte(jsPayload))

	// Give discoveryWorker time to process candidate
	time.Sleep(100 * time.Millisecond)

	// Queue should NOT contain the deadPath
	if pe.QueueLen() != 0 {
		t.Fatalf("expected queue length 0 because dead asset was filtered, got %d", pe.QueueLen())
	}
}

// 3. TTL 到期后可以再次尝试
func TestDeadFilter_TTLExpirationAllowsRetry(t *testing.T) {
	filter := newPrefetchDeadFilter(10, 50*time.Millisecond)

	const host = "prd-game-a-granbluefantasy.akamaized.net"
	const path = "/assets/img/sp/temp_dead.png"

	filter.Add(host, path)
	if !filter.IsDead(host, path) {
		t.Fatalf("expected asset to be dead immediately after Add")
	}

	// Wait for TTL to expire
	time.Sleep(70 * time.Millisecond)

	if filter.IsDead(host, path) {
		t.Fatalf("expected asset to be NO LONGER dead after TTL expiration")
	}
}

// 4. 1024 容量不会无限增长，最旧的 LRU 条目被正确淘汰
func TestDeadFilter_CapacityBoundedAndLRUEviction(t *testing.T) {
	const testCap = 1024
	filter := newPrefetchDeadFilter(testCap, 30*time.Minute)

	const host = "prd-game-a-granbluefantasy.akamaized.net"

	// Insert 2500 entries (more than double capacity)
	for i := 0; i < 2500; i++ {
		filter.Add(host, fmt.Sprintf("/assets/item_%d.png", i))
	}

	// Assert: Len() must be strictly capped at testCap
	if filter.Len() != testCap {
		t.Fatalf("expected deadFilter len == %d, got %d", testCap, filter.Len())
	}

	// Oldest entries (e.g. item_0 to item_100) must have been evicted
	for i := 0; i < 100; i++ {
		path := fmt.Sprintf("/assets/item_%d.png", i)
		if filter.IsDead(host, path) {
			t.Fatalf("expected old item %s to have been evicted", path)
		}
	}

	// Most recent entries (e.g. item_2000 to item_2499) must remain present
	for i := 2000; i < 2499; i++ {
		path := fmt.Sprintf("/assets/item_%d.png", i)
		if !filter.IsDead(host, path) {
			t.Fatalf("expected recent item %s to be present in filter", path)
		}
	}
}

// 4b. 实际内存占用测量: 验证 1024 槽位在运行时的实际堆内存增量
func TestDeadFilter_MemoryFootprintMeasurement(t *testing.T) {
	runtime.GC()
	var m1, m2 runtime.MemStats
	runtime.ReadMemStats(&m1)

	filter := newPrefetchDeadFilter(1024, 30*time.Minute)
	const host = "prd-game-a-granbluefantasy.akamaized.net"
	for i := 0; i < 1024; i++ {
		filter.Add(host, fmt.Sprintf("/assets/img/sp/quest/scene/character/body/test_dead_item_%d.png", i))
	}

	runtime.ReadMemStats(&m2)
	deltaAlloc := int64(m2.Alloc) - int64(m1.Alloc)
	if deltaAlloc < 0 {
		deltaAlloc = 0
	}
	t.Logf("1024 items DeadFilter actual HeapAlloc delta: %d bytes (%.2f KB)", deltaAlloc, float64(deltaAlloc)/1024)
}

// 5. 重复 Add 同一个 key 会刷新热度并移至表头，绝不会被提前误淘汰
func TestDeadFilter_DuplicateAddRefreshesRecency(t *testing.T) {
	const testCap = 3
	filter := newPrefetchDeadFilter(testCap, 30*time.Minute)

	const host = "prd-game-a-granbluefantasy.akamaized.net"

	// 1. Add A, B, C (cap full)
	filter.Add(host, "/a.png")
	filter.Add(host, "/b.png")
	filter.Add(host, "/c.png")

	// 2. Re-add A (should move A to MRU front)
	filter.Add(host, "/a.png")

	// 3. Add D (should evict B, which is now the oldest)
	filter.Add(host, "/d.png")

	if !filter.IsDead(host, "/a.png") {
		t.Fatalf("expected /a.png to remain in filter because it was refreshed before /d.png was added")
	}
	if filter.IsDead(host, "/b.png") {
		t.Fatalf("expected /b.png to be evicted as the least recently used")
	}
	if !filter.IsDead(host, "/c.png") {
		t.Fatalf("expected /c.png to remain")
	}
	if !filter.IsDead(host, "/d.png") {
		t.Fatalf("expected /d.png to be present")
	}
}

// 6. 不同 host 的相同 path 不互相污染
func TestDeadFilter_HostIsolation(t *testing.T) {
	filter := newPrefetchDeadFilter(1024, 30*time.Minute)

	const hostA = "prd-game-a-granbluefantasy.akamaized.net"
	const hostB = "prd-game-b-granbluefantasy.akamaized.net"
	const sharedPath = "/assets/img/sp/shared_name.png"

	filter.Add(hostA, sharedPath)

	if !filter.IsDead(hostA, sharedPath) {
		t.Fatalf("expected hostA to be dead")
	}
	if filter.IsDead(hostB, sharedPath) {
		t.Fatalf("expected hostB NOT to be dead (isolated hosts)")
	}
}

// 7. 前台请求不受影响 (前台请求 deadFilter 资产依然正常回源，上游 404 时代理按既有逻辑返回 502 Bad Gateway)
func TestDeadFilter_ForegroundRequestUnaffected(t *testing.T) {
	tempDir := t.TempDir()
	cfgMgr := config.NewManager(filepath.Join(tempDir, "config.json"))
	cacheMgr := cache.NewManager(tempDir, 16)
	defer cacheMgr.Close()
	stats := telemetry.NewStats()
	defer stats.Close()

	var upstreamHits atomic.Int64
	ts, client := newMockAssetServer(func(w http.ResponseWriter, r *http.Request) {
		upstreamHits.Add(1)
		w.Header().Set("Content-Type", "text/plain")
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte("Akamai 404 Not Found"))
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

	const host = "prd-game-a-granbluefantasy.akamaized.net"
	const deadPath = "/assets/img/sp/dead_foreground_test.png"

	// Pre-seed deadFilter in prefetch engine
	pe.deadFilter.Add(host, deadPath)

	// Real foreground request arrives via HTTP
	req := httptest.NewRequest(http.MethodGet, "https://"+host+deadPath, nil)
	req.RequestURI = deadPath
	req.Host = host
	var respBuf bytes.Buffer

	srv.handleStaticAssetLower(&respBuf, req, host, deadPath)

	// Upstream MUST be contacted (not short-circuited by prefetch's deadFilter)
	if hits := upstreamHits.Load(); hits != 1 {
		t.Fatalf("expected foreground to contact upstream exactly 1 time, got %d", hits)
	}

	// Verify proxy wrote an HTTP response (not blocked silently)
	if !bytes.Contains(respBuf.Bytes(), []byte("502 Bad Gateway")) && !bytes.Contains(respBuf.Bytes(), []byte("404")) {
		t.Fatalf("expected foreground response to contain 502 Bad Gateway or 404, got: %s", respBuf.String())
	}
}

// 8. 真实端到端集成测试:
// - Prefetch 第一次请求 Mock upstream 得到 404
// - 再次发现相同 host + path 时，第二次 Prefetch 不再访问 Mock upstream
// - 前台真实请求该 404 URL 时，依然正常访问 Mock upstream 并返回错误
func TestDeadFilter_Integration_Prefetch404NotRepeatedAndForegroundUnaffected(t *testing.T) {
	tempDir := t.TempDir()
	cfgMgr := config.NewManager(filepath.Join(tempDir, "config.json"))
	cacheMgr := cache.NewManager(tempDir, 16)
	defer cacheMgr.Close()
	stats := telemetry.NewStats()
	defer stats.Close()

	var upstreamHits atomic.Int64
	const deadPath = "/assets/img/sp/real_e2e_dead_asset.png"

	ts, client := newMockAssetServer(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == deadPath {
			upstreamHits.Add(1)
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte("Not Found"))
			return
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("OK"))
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

	const host = "prd-game-a-granbluefantasy.akamaized.net"

	// 步骤 1: 第一次发现该素材 (来自 bundle_1.js)
	jsBundle1 := fmt.Sprintf(`Game.imgUri + '%s';`, deadPath)
	pe.MaybeEnqueueDiscovery(host, "/assets/js/bundle_1.js", []byte(jsBundle1))

	// 等待 prefetch worker 消费并完成第一次上游请求
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if pe.deadFilter.IsDead(host, deadPath) && upstreamHits.Load() == 1 {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}

	if hits := upstreamHits.Load(); hits != 1 {
		t.Fatalf("expected exactly 1 upstream hit on first prefetch, got %d", hits)
	}
	if !pe.deadFilter.IsDead(host, deadPath) {
		t.Fatalf("expected asset to be added to deadFilter after 404 response")
	}

	// 步骤 2: 第二次发现同一个 host + path (来自不同的 bundle_2.js)
	jsBundle2 := fmt.Sprintf(`Game.imgUri + '%s';`, deadPath)
	pe.MaybeEnqueueDiscovery(host, "/assets/js/bundle_2.js", []byte(jsBundle2))

	// 等待一段时间，确认 discoveryWorker 过滤后不会再发起第二次请求
	time.Sleep(150 * time.Millisecond)

	if hits := upstreamHits.Load(); hits != 1 {
		t.Fatalf("expected upstream hits to REMAIN 1 (second prefetch must be filtered), got %d", hits)
	}

	// 步骤 3: 前台真实用户请求该 404 URL
	req := httptest.NewRequest(http.MethodGet, "https://"+host+deadPath, nil)
	req.RequestURI = deadPath
	req.Host = host
	var respBuf bytes.Buffer

	srv.handleStaticAssetLower(&respBuf, req, host, deadPath)

	// 前台请求必须不受 deadFilter 限制，正常向 upstream 穿透
	if hits := upstreamHits.Load(); hits != 2 {
		t.Fatalf("expected upstream hits to increment to 2 on foreground request, got %d", hits)
	}

	if !bytes.Contains(respBuf.Bytes(), []byte("502 Bad Gateway")) && !bytes.Contains(respBuf.Bytes(), []byte("404")) {
		t.Fatalf("expected foreground response to reflect failure, got: %s", respBuf.String())
	}
}

// 9. Benchmark: 验证 IsDead 查表性能与并发吞吐量 (0 allocs)
func BenchmarkPrefetchDeadFilter_IsDead(b *testing.B) {
	filter := newPrefetchDeadFilter(1024, 30*time.Minute)
	const host = "prd-game-a-granbluefantasy.akamaized.net"

	for i := 0; i < 500; i++ {
		filter.Add(host, fmt.Sprintf("/assets/item_%d.png", i))
	}

	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_ = filter.IsDead(host, "/assets/item_250.png")
	}
}

func BenchmarkPrefetchDeadFilter_Parallel(b *testing.B) {
	filter := newPrefetchDeadFilter(1024, 30*time.Minute)
	const host = "prd-game-a-granbluefantasy.akamaized.net"

	for i := 0; i < 500; i++ {
		filter.Add(host, fmt.Sprintf("/assets/item_%d.png", i))
	}

	b.ResetTimer()
	b.ReportAllocs()
	b.RunParallel(func(pb *testing.PB) {
		idx := 0
		for pb.Next() {
			path := fmt.Sprintf("/assets/item_%d.png", idx%500)
			_ = filter.IsDead(host, path)
			idx++
		}
	})
}
