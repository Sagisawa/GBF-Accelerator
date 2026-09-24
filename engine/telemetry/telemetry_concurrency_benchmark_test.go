package telemetry

import (
	"fmt"
	"runtime"
	"testing"
)

// BenchmarkCheckAndRecordPrefetchReusedParallel measures the global prefetch
// bookkeeping mutex under controlled parallelism. The key is intentionally not
// present, matching the common cache-hit case where the asset was not just
// produced by prefetch and therefore only needs the lookup.
func BenchmarkCheckAndRecordPrefetchReusedParallel(b *testing.B) {
	stats := NewStats()
	defer stats.Close()

	for _, workers := range []int{1, 2, 4, 8, 16, 32} {
		workers := workers
		b.Run(fmt.Sprintf("workers_%d", workers), func(b *testing.B) {
			old := runtime.GOMAXPROCS(workers)
			defer runtime.GOMAXPROCS(old)

			b.ReportAllocs()
			b.ResetTimer()
			b.RunParallel(func(pb *testing.PB) {
				for pb.Next() {
					stats.CheckAndRecordPrefetchReused("/assets/js/1000/not-prefetched.js")
				}
			})
		})
	}
}
