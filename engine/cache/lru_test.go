package cache

import (
	"testing"
)

func TestLRUCache(t *testing.T) {
	// 100 bytes max limit
	lru := NewLRUCache(100)

	item1 := &CacheItem{Key: "item1", Data: make([]byte, 40)}
	item2 := &CacheItem{Key: "item2", Data: make([]byte, 40)}
	item3 := &CacheItem{Key: "item3", Data: make([]byte, 40)}

	lru.Set("item1", item1)
	lru.Set("item2", item2)

	items, bytes := lru.Stats()
	if items != 2 || bytes != 80 {
		t.Fatalf("expected 2 items, 80 bytes; got %d items, %d bytes", items, bytes)
	}

	// Adding item3 (40 bytes) exceeds 100 bytes (80 + 40 = 120 > 100).
	// item1 (least recently used) must be evicted!
	lru.Set("item3", item3)

	items, bytes = lru.Stats()
	if items != 2 || bytes != 80 {
		t.Fatalf("expected 2 items, 80 bytes after eviction; got %d items, %d bytes", items, bytes)
	}

	if _, ok := lru.Get("item1"); ok {
		t.Error("item1 should have been evicted")
	}
	if _, ok := lru.Get("item2"); !ok {
		t.Error("item2 should still be in cache")
	}
	if _, ok := lru.Get("item3"); !ok {
		t.Error("item3 should still be in cache")
	}

	// Access item2, making it most recently used
	_, _ = lru.Get("item2")

	// Add item4 (40 bytes), item3 should now be evicted
	item4 := &CacheItem{Key: "item4", Data: make([]byte, 40)}
	lru.Set("item4", item4)

	if _, ok := lru.Get("item3"); ok {
		t.Error("item3 should have been evicted")
	}
	if _, ok := lru.Get("item2"); !ok {
		t.Error("item2 should still be in cache")
	}

	// Clear cache
	lru.Clear()
	items, bytes = lru.Stats()
	if items != 0 || bytes != 0 {
		t.Fatalf("expected empty cache after Clear(), got %d items, %d bytes", items, bytes)
	}
}

func TestLRUCacheOversizedItem(t *testing.T) {
	// 100 bytes max limit
	lru := NewLRUCache(100)

	item1 := &CacheItem{Key: "item1", Data: make([]byte, 50)}
	lru.Set("item1", item1)

	// An item larger than maxBytes (120 > 100) must be rejected and must NOT evict item1
	oversized := &CacheItem{Key: "huge", Data: make([]byte, 120)}
	lru.Set("huge", oversized)

	if _, ok := lru.Get("huge"); ok {
		t.Error("oversized item should not be admitted to RAM cache")
	}

	if _, ok := lru.Get("item1"); !ok {
		t.Error("existing item1 should not have been evicted by oversized item")
	}

	items, bytes := lru.Stats()
	if items != 1 || bytes != 50 {
		t.Fatalf("expected 1 item with 50 bytes, got %d items with %d bytes", items, bytes)
	}
}

func TestLRUCacheDelete(t *testing.T) {
	lru := NewLRUCache(100)
	lru.Set("a", &CacheItem{Key: "a", Data: make([]byte, 20)})
	lru.Set("b", &CacheItem{Key: "b", Data: make([]byte, 20)})
	lru.Set("c", &CacheItem{Key: "c", Data: make([]byte, 20)})

	// Delete non-existent
	if lru.Delete("non-existent") {
		t.Error("expected Delete(non-existent) to return false")
	}

	// Delete middle
	if !lru.Delete("b") {
		t.Error("expected Delete(b) to return true")
	}
	if lru.Contains("b") {
		t.Error("b should have been deleted")
	}
	items, bytes := lru.Stats()
	if items != 2 || bytes != 40 {
		t.Fatalf("expected 2 items, 40 bytes; got %d items, %d bytes", items, bytes)
	}

	// Delete head (c)
	if !lru.Delete("c") {
		t.Error("expected Delete(c) to return true")
	}
	// Delete tail (a)
	if !lru.Delete("a") {
		t.Error("expected Delete(a) to return true")
	}

	items, bytes = lru.Stats()
	if items != 0 || bytes != 0 {
		t.Fatalf("expected 0 items, 0 bytes; got %d items, %d bytes", items, bytes)
	}
}

func TestLRUCacheUpdateExisting(t *testing.T) {
	lru := NewLRUCache(100)
	lru.Set("a", &CacheItem{Key: "a", Data: make([]byte, 30)})
	lru.Set("b", &CacheItem{Key: "b", Data: make([]byte, 30)})

	// Update "a" with larger data (40 bytes)
	lru.Set("a", &CacheItem{Key: "a", Data: make([]byte, 40)})
	items, bytes := lru.Stats()
	if items != 2 || bytes != 70 {
		t.Fatalf("expected 2 items, 70 bytes; got %d items, %d bytes", items, bytes)
	}

	// Update "a" with data exceeding total capacity (110 bytes)
	lru.Set("a", &CacheItem{Key: "a", Data: make([]byte, 110)})
	// "a" should be removed because it exceeds capacity, leaving only "b"
	if lru.Contains("a") {
		t.Error("a should be removed after update with oversized item")
	}
	if !lru.Contains("b") {
		t.Error("b should still remain")
	}
	items, bytes = lru.Stats()
	if items != 1 || bytes != 30 {
		t.Fatalf("expected 1 item, 30 bytes; got %d items, %d bytes", items, bytes)
	}
}

func TestLRUCacheNilItem(t *testing.T) {
	lru := NewLRUCache(100)
	// Setting a nil item should safely return without panicking
	lru.Set("nil_key", nil)
	if lru.Contains("nil_key") {
		t.Error("nil item should not be stored in cache")
	}
}

func BenchmarkLRUGetSet(b *testing.B) {
	lru := NewLRUCache(1024 * 1024)
	item := &CacheItem{Key: "bench", Data: make([]byte, 64)}

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		lru.Set("bench", item)
		_, _ = lru.Get("bench")
	}
}

func BenchmarkLRUGetSet_Parallel(b *testing.B) {
	lru := NewLRUCache(64 * 1024 * 1024)
	keys := make([]string, 1000)
	for i := 0; i < 1000; i++ {
		keys[i] = "bench_key_" + string(rune('a'+(i%26))) + string(rune('0'+(i%10)))
		lru.Set(keys[i], &CacheItem{Key: keys[i], Data: make([]byte, 64)})
	}

	b.ReportAllocs()
	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		idx := 0
		for pb.Next() {
			k := keys[idx%1000]
			_, _ = lru.Get(k)
			idx++
		}
	})
}

func TestShardedLRU(t *testing.T) {
	// >= 1MB triggers 16 shards
	lru := NewLRUCache(16 * 1024 * 1024)
	if lru.numShards != 16 {
		t.Fatalf("expected 16 shards, got %d", lru.numShards)
	}

	// Insert items across shards
	for i := 0; i < 100; i++ {
		key := "item_" + string(rune('a'+(i%26))) + string(rune('0'+(i%10)))
		lru.Set(key, &CacheItem{Key: key, Data: make([]byte, 1024)})
		if !lru.Contains(key) {
			t.Fatalf("expected item %s to be contained", key)
		}
		item, ok := lru.Get(key)
		if !ok || item == nil {
			t.Fatalf("expected item %s to be retrieved", key)
		}
	}

	items, bytes := lru.Stats()
	if items == 0 || bytes == 0 {
		t.Fatalf("expected positive stats, got items=%d bytes=%d", items, bytes)
	}

	lru.Clear()
	items, bytes = lru.Stats()
	if items != 0 || bytes != 0 {
		t.Fatalf("expected 0 stats after clear, got items=%d bytes=%d", items, bytes)
	}
}


