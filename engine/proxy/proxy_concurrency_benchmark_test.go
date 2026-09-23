package proxy

import (
	"fmt"
	"io"
	"net/http"
	"path/filepath"
	"runtime"
	"testing"

	"gbf-proxy/cache"
	"gbf-proxy/config"
	"gbf-proxy/telemetry"
)

// BenchmarkRAMHitCachedAssetHandlerParallel measures the full foreground
// RAM-cache hit path under controlled parallelism. It intentionally uses one
// hot key, which concentrates cache-shard and telemetry synchronization and
// therefore acts as a conservative contention test.
func BenchmarkRAMHitCachedAssetHandlerParallel(b *testing.B) {
	tempDir := b.TempDir()
	cfgMgr := config.NewManager(filepath.Join(tempDir, "config.json"))
	cacheMgr := cache.NewManager(tempDir, 16)
	stats := telemetry.NewStats()
	defer cacheMgr.Close()
	defer stats.Close()

	const benchHost = "prd-game-a-granbluefantasy.akamaized.net"
	const benchPath = "/assets/js/1000/bench.js"

	payload := benchCachedAssetPayload(8 * 1024)
	if _, ok := cacheMgr.SaveRAM(benchPath, map[string]string{"content-type": "application/javascript"}, payload); !ok {
		b.Fatalf("failed to seed RAM cache")
	}

	req, err := http.NewRequest(http.MethodGet, "https://"+benchHost+benchPath, nil)
	if err != nil {
		b.Fatalf("failed to build request: %v", err)
	}
	req.RequestURI = benchPath

	srv := &ProxyServer{
		cfgMgr:   cfgMgr,
		cacheMgr: cacheMgr,
		stats:    stats,
	}

	for _, workers := range []int{1, 2, 4, 8, 16, 32} {
		workers := workers
		b.Run(fmt.Sprintf("workers_%d", workers), func(b *testing.B) {
			old := runtime.GOMAXPROCS(workers)
			defer runtime.GOMAXPROCS(old)

			b.ReportAllocs()
			b.SetBytes(int64(len(payload)))
			b.ResetTimer()
			b.RunParallel(func(pb *testing.PB) {
				for pb.Next() {
					_ = srv.handleStaticAssetLower(io.Discard, req, benchHost, benchPath)
				}
			})
		})
	}
}
