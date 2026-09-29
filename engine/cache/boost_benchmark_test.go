package cache

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

// BenchmarkSLRU_Only benchmarks standard SLRU RAM cache lookups.
func BenchmarkSLRU_Only(b *testing.B) {
	tempDir, _ := os.MkdirTemp("", "gbf_bench_slru_*")
	defer os.RemoveAll(tempDir)

	mgr := NewManager(tempDir, 64)
	defer mgr.Close()

	key := "assets/test/hero.png"
	data := []byte("\x89PNG\r\n\x1a\n\x00\x00\x00\rIHDRbench")
	mgr.SaveRAMWithNamespace("gbf", key, map[string]string{"content-type": "image/png"}, data)

	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		item, _ := mgr.GetWithNamespace("gbf", key)
		if item == nil {
			b.Fatal("missing item")
		}
	}
}

// BenchmarkBoost_Disabled benchmarks GetWithNamespace when Boost feature is present but disabled.
func BenchmarkBoost_Disabled(b *testing.B) {
	tempDir, _ := os.MkdirTemp("", "gbf_bench_disabled_*")
	defer os.RemoveAll(tempDir)

	mgr := NewManager(tempDir, 64)
	defer mgr.Close()

	key := "assets/test/hero.png"
	data := []byte("\x89PNG\r\n\x1a\n\x00\x00\x00\rIHDRbench")
	mgr.SaveRAMWithNamespace("gbf", key, map[string]string{"content-type": "image/png"}, data)

	// Ensure resident pool is disabled
	mgr.ResidentPool().SetEnabled(false)

	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		item, _ := mgr.GetWithNamespace("gbf", key)
		if item == nil {
			b.Fatal("missing item")
		}
	}
}

// BenchmarkBoost_Enabled_Hit benchmarks Resident Pool lookups when Boost is active and hits (must be 0 allocs/op).
func BenchmarkBoost_Enabled_Hit(b *testing.B) {
	tempDir, _ := os.MkdirTemp("", "gbf_bench_hit_*")
	defer os.RemoveAll(tempDir)

	mgr := NewManager(tempDir, 64)
	defer mgr.Close()

	key := "assets/test/hero.png"
	data := []byte("\x89PNG\r\n\x1a\n\x00\x00\x00\rIHDRbench")
	p := filepath.Join(tempDir, "assets", "test", "hero.png")
	_ = os.MkdirAll(filepath.Dir(p), 0755)
	_ = os.WriteFile(p, data, 0644)
	_ = os.WriteFile(p+".ext", []byte(`{"ContentType":"image/png","v":1}`), 0644)

	mgr.PrewarmBoostPoolWithProgress(nil, nil, nil)

	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		item, _ := mgr.GetWithNamespace("gbf", key)
		if item == nil {
			b.Fatal("missing item")
		}
	}
}

// BenchmarkBoost_Enabled_Miss benchmarks lookup when Boost is enabled but key misses Resident Pool and hits SLRU.
func BenchmarkBoost_Enabled_Miss(b *testing.B) {
	tempDir, _ := os.MkdirTemp("", "gbf_bench_miss_*")
	defer os.RemoveAll(tempDir)

	mgr := NewManager(tempDir, 64)
	defer mgr.Close()

	key := "assets/test/hero.png"
	data := []byte("\x89PNG\r\n\x1a\n\x00\x00\x00\rIHDRbench")
	// Save to SLRU only
	mgr.SaveRAMWithNamespace("gbf", key, map[string]string{"content-type": "image/png"}, data)

	// Enable resident pool (empty)
	mgr.ResidentPool().SetEnabled(true)

	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		item, _ := mgr.GetWithNamespace("gbf", key)
		if item == nil {
			b.Fatal("missing item")
		}
	}
}

// BenchmarkBoost_Concurrent_Read benchmarks high concurrency read throughput in Resident Pool.
func BenchmarkBoost_Concurrent_Read(b *testing.B) {
	tempDir, _ := os.MkdirTemp("", "gbf_bench_conc_*")
	defer os.RemoveAll(tempDir)

	mgr := NewManager(tempDir, 64)
	defer mgr.Close()

	const numKeys = 100
	for i := 0; i < numKeys; i++ {
		p := filepath.Join(tempDir, "assets", fmt.Sprintf("asset_%d.png", i))
		_ = os.MkdirAll(filepath.Dir(p), 0755)
		data := []byte(fmt.Sprintf("\x89PNG\r\n\x1a\n\x00\x00\x00\rIHDRdata_%d", i))
		_ = os.WriteFile(p, data, 0644)
		_ = os.WriteFile(p+".ext", []byte(`{"ContentType":"image/png","v":1}`), 0644)
	}

	mgr.PrewarmBoostPoolWithProgress(nil, nil, nil)

	b.ResetTimer()
	b.ReportAllocs()
	b.RunParallel(func(pb *testing.PB) {
		idx := 0
		for pb.Next() {
			key := fmt.Sprintf("assets/asset_%d.png", idx%numKeys)
			item, _ := mgr.GetWithNamespace("gbf", key)
			if item == nil {
				b.Fatal("missing item")
			}
			idx++
		}
	})
}
