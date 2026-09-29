package cache

import (
	"fmt"
	"sync"
	"testing"
)

func TestResidentPool_Basics(t *testing.T) {
	budget := NewRAMBudget(50 * 1024 * 1024)
	pool := NewResidentPool(budget)

	item := &CacheItem{
		Key:  "test/asset.png",
		Data: []byte("sample-data"),
		Size: 11,
	}

	// When disabled, Set is rejected and Get returns nil, false
	if pool.Enabled() {
		t.Fatalf("expected pool to be disabled initially")
	}
	if pool.Set(item.Key, item) {
		t.Fatalf("expected Set to be rejected when pool is disabled")
	}
	if got, ok := pool.Get(item.Key); ok || got != nil {
		t.Fatalf("expected Get to return nil, false when pool is disabled")
	}

	// Enable
	pool.SetEnabled(true)
	if !pool.Enabled() {
		t.Fatalf("expected pool to be enabled")
	}
	if !pool.Set(item.Key, item) {
		t.Fatalf("expected Set to succeed when pool is enabled")
	}

	got, ok := pool.Get(item.Key)
	if !ok || got == nil || string(got.Data) != "sample-data" {
		t.Fatalf("failed to retrieve item after enabling: got %+v, ok=%v", got, ok)
	}

	items, bytes := pool.Stats()
	if items != 1 || bytes != 11 {
		t.Fatalf("expected stats (1, 11), got (%d, %d)", items, bytes)
	}
	if budget.ResidentBytes() != 11 {
		t.Fatalf("expected budget resident bytes 11, got %d", budget.ResidentBytes())
	}

	// Delete
	pool.Delete(item.Key)
	if _, ok := pool.Get(item.Key); ok {
		t.Fatalf("expected item to be deleted")
	}
	if budget.ResidentBytes() != 0 {
		t.Fatalf("expected budget resident bytes 0 after delete, got %d", budget.ResidentBytes())
	}

	// Re-add and Clear
	pool.Set(item.Key, item)
	pool.Clear()
	if pool.Enabled() {
		t.Fatalf("expected pool to be disabled after Clear")
	}
	if items, bytes := pool.Stats(); items != 0 || bytes != 0 {
		t.Fatalf("expected 0 stats after clear, got (%d, %d)", items, bytes)
	}
	if budget.ResidentBytes() != 0 {
		t.Fatalf("expected 0 budget resident bytes after clear, got %d", budget.ResidentBytes())
	}
}

func TestResidentPool_BudgetEnforcement(t *testing.T) {
	budget := NewRAMBudget(1000) // 1000 bytes
	pool := NewResidentPool(budget)
	pool.SetEnabled(true)

	item1 := &CacheItem{Key: "item1", Size: 600, Data: make([]byte, 600)}
	item2 := &CacheItem{Key: "item2", Size: 500, Data: make([]byte, 500)}

	if !pool.Set(item1.Key, item1) {
		t.Fatalf("expected item1 to fit within budget")
	}

	// item2 (500) + item1 (600) = 1100 > 1000, must be rejected
	if pool.Set(item2.Key, item2) {
		t.Fatalf("expected item2 to be rejected due to budget limit")
	}

	if _, ok := pool.Get("item2"); ok {
		t.Fatalf("rejected item2 should not exist in pool")
	}
}

func TestResidentPool_Concurrency(t *testing.T) {
	budget := NewRAMBudget(10 * 1024 * 1024)
	pool := NewResidentPool(budget)
	pool.SetEnabled(true)

	var wg sync.WaitGroup
	workers := 16
	itemsPerWorker := 50

	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func(workerID int) {
			defer wg.Done()
			for i := 0; i < itemsPerWorker; i++ {
				key := fmt.Sprintf("worker_%d_item_%d.png", workerID, i)
				item := &CacheItem{
					Key:  key,
					Data: []byte("test-data"),
					Size: 9,
				}
				pool.Set(key, item)
				if got, ok := pool.Get(key); !ok || got == nil {
					t.Errorf("failed to get item %s", key)
				}
			}
		}(w)
	}
	wg.Wait()

	items, bytes := pool.Stats()
	expectedItems := workers * itemsPerWorker
	expectedBytes := int64(expectedItems * 9)
	if items != expectedItems || bytes != expectedBytes {
		t.Fatalf("expected (%d, %d), got (%d, %d)", expectedItems, expectedBytes, items, bytes)
	}
}

func TestResidentPool_ConcurrentSetWithGenAndDisable(t *testing.T) {
	budget := NewRAMBudget(50 * 1024 * 1024)
	pool := NewResidentPool(budget)

	for iter := 0; iter < 100; iter++ {
		gen := pool.NextGeneration()
		pool.ResetForGen(gen)

		var wg sync.WaitGroup
		wg.Add(2)

		go func(g uint64) {
			defer wg.Done()
			for i := 0; i < 50; i++ {
				key := fmt.Sprintf("asset_%d.png", i)
				item := &CacheItem{Key: key, Data: []byte("test"), Size: 4}
				if pool.TryAcquireBudget(4, g) {
					if !pool.SetWithGen(key, item, g) {
						pool.ReleaseBudget(4, g)
					}
				}
			}
		}(gen)

		go func() {
			defer wg.Done()
			pool.Disable(0)
		}()

		wg.Wait()

		// Verify invariant directly without extra Disable call:
		if pool.Enabled() {
			t.Fatalf("iteration %d: expected pool to be disabled", iter)
		}
		items, bytes := pool.Stats()
		if items != 0 || bytes != 0 {
			t.Fatalf("iteration %d: expected 0 items/bytes when disabled, got (%d, %d)", iter, items, bytes)
		}
		if budget.ResidentBytes() != 0 {
			t.Fatalf("iteration %d: expected 0 resident bytes in budget, got %d", iter, budget.ResidentBytes())
		}
	}
}

func TestResidentPool_ConcurrentSetWithGenAndClear(t *testing.T) {
	budget := NewRAMBudget(50 * 1024 * 1024)
	pool := NewResidentPool(budget)

	for iter := 0; iter < 100; iter++ {
		gen := pool.NextGeneration()
		pool.ResetForGen(gen)

		var wg sync.WaitGroup
		wg.Add(2)

		go func(g uint64) {
			defer wg.Done()
			for i := 0; i < 50; i++ {
				key := fmt.Sprintf("asset_%d.png", i)
				item := &CacheItem{Key: key, Data: []byte("test"), Size: 4}
				if pool.TryAcquireBudget(4, g) {
					if !pool.SetWithGen(key, item, g) {
						pool.ReleaseBudget(4, g)
					}
				}
			}
		}(gen)

		go func() {
			defer wg.Done()
			pool.Clear()
		}()

		wg.Wait()

		// Invariant check directly without extra Disable/Clear call:
		if pool.Enabled() {
			t.Fatalf("iteration %d: expected pool to be disabled after Clear", iter)
		}
		items, bytes := pool.Stats()
		if items != 0 || bytes != 0 {
			t.Fatalf("iteration %d: expected 0 items/bytes when cleared, got (%d, %d)", iter, items, bytes)
		}
		if budget.ResidentBytes() != 0 {
			t.Fatalf("iteration %d: expected 0 resident bytes in budget, got %d", iter, budget.ResidentBytes())
		}
	}
}
