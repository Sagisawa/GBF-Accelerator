package cache

import (
	crand "crypto/rand"
	"fmt"
	mrand "math/rand/v2"
	"sync/atomic"
	"testing"
	"time"
)

func makeFakePNG(size int) []byte {
	buf := make([]byte, size)
	_, _ = crand.Read(buf)
	copy(buf, []byte("\x89PNG\r\n\x1a\n"))
	return buf
}

func makeFakeJS(size int) []byte {
	buf := make([]byte, size)
	_, _ = crand.Read(buf)
	copy(buf, []byte("var a = 1; "))
	return buf
}

// BenchmarkRAMHit measures the pure latency and allocation of RAM cache hits.
func BenchmarkRAMHit(b *testing.B) {
	tempDir := b.TempDir()
	mgr := NewManager(tempDir, 128)
	defer mgr.Close()

	payload := makeFakeJS(16 * 1024)

	const urlPath = "/assets/app.js"
	headers := map[string]string{"content-type": "application/javascript"}
	mgr.SaveRAM(urlPath, headers, payload)

	b.ReportAllocs()
	b.SetBytes(int64(len(payload)))
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		item, src := mgr.Get(urlPath)
		if item == nil || src != "RAM" {
			b.Fatalf("expected RAM hit, got %s", src)
		}
	}
}

// BenchmarkRAMMiss_DiskHit measures the cost of a RAM miss falling back to Disk,
// including disk reading, .ext json parsing, and RAM promotion.
func BenchmarkRAMMiss_DiskHit(b *testing.B) {
	tempDir := b.TempDir()
	mgr := NewManager(tempDir, 128)
	defer mgr.Close()

	payload := makeFakePNG(32 * 1024)

	headers := map[string]string{"content-type": "image/png"}
	const numAssets = 100
	paths := make([]string, numAssets)
	for i := 0; i < numAssets; i++ {
		paths[i] = fmt.Sprintf("/assets/img/sp/quest_%d.png", i)
		if !mgr.Save(paths[i], headers, payload) {
			b.Fatalf("failed to save initial disk asset %d", i)
		}
	}

	b.ReportAllocs()
	b.SetBytes(int64(len(payload)))
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		// Clear RAM to simulate cold disk hits
		mgr.ClearRAM()
		p := paths[i%numAssets]
		item, src := mgr.Get(p)
		if item == nil || src != "DISK" {
			b.Fatalf("expected DISK hit, got %s", src)
		}
	}
}

// BenchmarkDiskColdHit_SyscallCost isolates the time spent reading from Disk when
// the item is NOT in RAM.
func BenchmarkDiskColdHit_SyscallCost(b *testing.B) {
	tempDir := b.TempDir()
	mgr := NewManager(tempDir, 128)
	defer mgr.Close()

	payload := makeFakeJS(8 * 1024)
	const testPath = "/assets/js/model/manifest/10001.js"
	if !mgr.Save(testPath, map[string]string{"content-type": "application/javascript"}, payload) {
		b.Fatalf("failed to seed test asset")
	}

	b.ReportAllocs()
	b.SetBytes(int64(len(payload)))
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		b.StopTimer()
		mgr.ClearRAM()
		b.StartTimer()

		item, src := mgr.Get(testPath)
		if item == nil || src != "DISK" {
			b.Fatalf("expected DISK hit, got %s", src)
		}
	}
}

// BenchmarkRealRequestWrite measures SaveRAM (Respond-First: instant RAM write + async disk queue).
func BenchmarkRealRequestWrite(b *testing.B) {
	tempDir := b.TempDir()
	mgr := NewManager(tempDir, 128)
	defer mgr.Close()

	payload := makeFakeJS(16 * 1024)
	headers := map[string]string{"content-type": "application/javascript"}

	b.ReportAllocs()
	b.SetBytes(int64(len(payload)))
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		path := fmt.Sprintf("/assets/js/fg_%d.js", i%1000)
		_, ok := mgr.SaveRAM(path, headers, payload)
		if !ok {
			b.Fatalf("SaveRAM failed at %d", i)
		}
	}
}

// BenchmarkPrefetchWrite measures Save (Prefetch: RAM write + sync disk write).
func BenchmarkPrefetchWrite(b *testing.B) {
	tempDir := b.TempDir()
	mgr := NewManager(tempDir, 128)
	defer mgr.Close()

	payload := makeFakePNG(16 * 1024)
	headers := map[string]string{"content-type": "image/png"}

	b.ReportAllocs()
	b.SetBytes(int64(len(payload)))
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		path := fmt.Sprintf("/assets/img/prefetch_%d.png", i%200)
		ok := mgr.Save(path, headers, payload)
		if !ok {
			b.Fatalf("Save failed at %d", i)
		}
	}
}

// BenchmarkRAMNearCapacity measures write latency when RAM cache is at capacity,
// forcing continuous evictions.
func BenchmarkRAMNearCapacity(b *testing.B) {
	tempDir := b.TempDir()
	// Small 4MB cache to reach capacity quickly
	mgr := NewManager(tempDir, 4)
	defer mgr.Close()

	payload := makeFakeJS(64 * 1024) // 64 KiB
	headers := map[string]string{"content-type": "application/javascript"}

	// Pre-fill to 4MB (approx 64 items)
	for i := 0; i < 70; i++ {
		path := fmt.Sprintf("/assets/init_%d.js", i)
		mgr.SaveRAM(path, headers, payload)
	}

	b.ReportAllocs()
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		path := fmt.Sprintf("/assets/capacity_test_%d.js", i)
		mgr.SaveRAM(path, headers, payload)
	}
}

// BenchmarkHotAssetContinuousAccess_WithColdPrefetchStream simulates a real GBF workload:
// 40 hot assets (combat UI, attack scripts, icons) are accessed repeatedly by the player.
// Meanwhile, a concurrent stream of cold prefetched assets (audio, full scene backgrounds) floods the cache.
// We record the hot asset hit rate and latency.
func BenchmarkHotAssetContinuousAccess_WithColdPrefetchStream(b *testing.B) {
	tempDir := b.TempDir()
	// 4 MB RAM cache (simulating high-pressure working set)
	mgr := NewManager(tempDir, 4)
	defer mgr.Close()

	const numHotAssets = 40
	hotPayload := makeFakeJS(32 * 1024) // 32KB each -> 1.28MB total hot set
	headers := map[string]string{"content-type": "application/javascript"}

	hotPaths := make([]string, numHotAssets)
	for i := 0; i < numHotAssets; i++ {
		hotPaths[i] = fmt.Sprintf("/assets/hot_asset_%d.js", i)
		mgr.SaveRAM(hotPaths[i], headers, hotPayload)
	}

	// Warm up hot assets in RAM
	for _, p := range hotPaths {
		mgr.Get(p)
	}

	var hotHits atomic.Int64
	var hotMisses atomic.Int64
	stopCold := make(chan struct{})

	// Start background cold prefetch stream: continuously writes 256KB assets
	go func() {
		coldPayload := makeFakePNG(256 * 1024) // 256KB large cold asset
		idx := 0
		for {
			select {
			case <-stopCold:
				return
			default:
				p := fmt.Sprintf("/assets/cold_prefetch_%d.png", idx)
				mgr.Save(p, map[string]string{"content-type": "image/png"}, coldPayload)
				idx++
				time.Sleep(1 * time.Millisecond) // realistic prefetch pacing
			}
		}
	}()

	b.ReportAllocs()
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		target := hotPaths[i%numHotAssets]
		item, src := mgr.Get(target)
		if item != nil && src == "RAM" {
			hotHits.Add(1)
		} else {
			hotMisses.Add(1)
		}
	}

	close(stopCold)
	total := hotHits.Load() + hotMisses.Load()
	hitRate := float64(hotHits.Load()) / float64(total) * 100
	b.Logf("Hot Asset RAM Hit Rate under Cold Stream: %.2f%% (%d hits / %d total)", hitRate, hotHits.Load(), total)
}

// BenchmarkHotAssetRetention_PrefetchBurst measures the degradation of hot asset
// retrieval when a prefetch burst of cold assets enters the cache.
func BenchmarkHotAssetRetention_PrefetchBurst(b *testing.B) {
	tempDir := b.TempDir()
	// 4 MB RAM cache
	mgr := NewManager(tempDir, 4)
	defer mgr.Close()

	const numHotAssets = 30
	hotPayload := makeFakeJS(32 * 1024) // 32KB each -> 960KB hot working set
	headers := map[string]string{"content-type": "application/javascript"}

	hotPaths := make([]string, numHotAssets)
	for i := 0; i < numHotAssets; i++ {
		hotPaths[i] = fmt.Sprintf("/assets/hot_battle_%d.js", i)
		mgr.SaveRAM(hotPaths[i], headers, hotPayload)
	}

	coldPayload := makeFakePNG(128 * 1024) // 128KB cold assets
	coldHeaders := map[string]string{"content-type": "image/png"}

	b.ReportAllocs()
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		// Player accesses hot assets (working set is hot)
		for _, p := range hotPaths {
			mgr.Get(p)
		}

		// Background prefetch burst: 40 cold assets (40 * 128KB = 5.12MB > 4MB cache)
		for j := 0; j < 40; j++ {
			p := fmt.Sprintf("/assets/burst_%d_%d.png", i, j)
			mgr.Save(p, coldHeaders, coldPayload)
		}

		// Player immediately takes turn in battle: requests hot assets
		ramHits := 0
		for _, p := range hotPaths {
			item, src := mgr.Get(p)
			if item != nil && src == "RAM" {
				ramHits++
			}
		}
		if i == 0 {
			b.Logf("Iteration 0 Hot Assets retained in RAM after prefetch burst: %d / %d (%.1f%%)",
				ramHits, numHotAssets, float64(ramHits)/float64(numHotAssets)*100)
		}
	}
}

// BenchmarkLongRunningMixedWorkload simulates 10,000 mixed accesses over time:
// 70% hot reads, 15% warm reads, 10% disk/cold reads, 5% prefetch writes.
func BenchmarkLongRunningMixedWorkload(b *testing.B) {
	tempDir := b.TempDir()
	mgr := NewManager(tempDir, 16) // 16MB cache
	defer mgr.Close()

	smallPayload := makeFakeJS(16 * 1024)
	pngPayload := makeFakePNG(16 * 1024)
	headers := map[string]string{"content-type": "application/javascript"}
	pngHeaders := map[string]string{"content-type": "image/png"}

	// Seed 20 hot assets
	for i := 0; i < 20; i++ {
		p := fmt.Sprintf("/assets/hot_%d.js", i)
		mgr.SaveRAM(p, headers, smallPayload)
		mgr.Get(p)
	}

	// Seed 100 disk assets
	for i := 0; i < 100; i++ {
		p := fmt.Sprintf("/assets/disk_%d.js", i)
		mgr.Save(p, headers, smallPayload)
	}

	b.ReportAllocs()
	b.ResetTimer()

	b.RunParallel(func(pb *testing.PB) {
		idx := 0
		for pb.Next() {
			idx++
			dice := mrand.IntN(100)
			if dice < 70 {
				// 70% Hot read
				p := fmt.Sprintf("/assets/hot_%d.js", mrand.IntN(20))
				mgr.Get(p)
			} else if dice < 85 {
				// 15% Warm read
				p := fmt.Sprintf("/assets/warm_%d.js", mrand.IntN(30))
				mgr.Get(p)
			} else if dice < 95 {
				// 10% Disk read
				p := fmt.Sprintf("/assets/disk_%d.js", mrand.IntN(100))
				mgr.Get(p)
			} else {
				// 5% Background prefetch write
				p := fmt.Sprintf("/assets/prefetch_stream_%d.png", idx)
				mgr.Save(p, pngHeaders, pngPayload)
			}
		}
	})
}
