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

