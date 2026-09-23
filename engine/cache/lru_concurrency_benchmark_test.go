package cache

import (
	"fmt"
	"runtime"
	"testing"
)

// BenchmarkLRUGetParallel isolates the hot RAM-cache lookup path under controlled
// parallelism. Every worker reads the same key so the benchmark deliberately
// concentrates contention on one shard; this is the upper-bound contention case.
func BenchmarkLRUGetParallel(b *testing.B) {
	c := NewLRUCache(64 * 1024 * 1024)
	item := &CacheItem{
		Key:  "assets/js/1000/bench.js",
		Data: make([]byte, 8*1024),
		Size: 8 * 1024,
	}
	c.Set(item.Key, item)

	for _, workers := range []int{1, 2, 4, 8, 16, 32} {
		workers := workers
		b.Run(fmt.Sprintf("workers_%d", workers), func(b *testing.B) {
			old := runtime.GOMAXPROCS(workers)
			defer runtime.GOMAXPROCS(old)

			b.ReportAllocs()
			b.SetBytes(int64(len(item.Data)))
			b.ResetTimer()
			b.RunParallel(func(pb *testing.PB) {
				for pb.Next() {
					if got, ok := c.Get(item.Key); !ok || got != item {
						b.Fatal("unexpected cache miss")
					}
				}
			})
		})
	}
}
