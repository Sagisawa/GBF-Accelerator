package cache

import (
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
)

func TestRAMBudget_AllocationAndRelease(t *testing.T) {
	total := int64(100 * 1024 * 1024) // 100 MB
	budget := NewRAMBudget(total)

	if budget.TotalBudget() != total {
		t.Fatalf("expected total %d, got %d", total, budget.TotalBudget())
	}
	if budget.ResidentBytes() != 0 {
		t.Fatalf("expected 0 resident bytes, got %d", budget.ResidentBytes())
	}
	if budget.RemainingResidentBudget() != total {
		t.Fatalf("expected remaining %d, got %d", total, budget.RemainingResidentBudget())
	}

	// Allocate 40 MB
	chunk := int64(40 * 1024 * 1024)
	if !budget.TryAcquireResident(chunk) {
		t.Fatalf("failed to acquire 40 MB")
	}
	if budget.ResidentBytes() != chunk {
		t.Fatalf("expected %d resident bytes, got %d", chunk, budget.ResidentBytes())
	}
	if budget.RemainingResidentBudget() != total-chunk {
		t.Fatalf("expected remaining %d, got %d", total-chunk, budget.RemainingResidentBudget())
	}

	// Allocate another 50 MB (total 90 MB)
	chunk2 := int64(50 * 1024 * 1024)
	if !budget.TryAcquireResident(chunk2) {
		t.Fatalf("failed to acquire 50 MB")
	}

	// Attempting to allocate another 20 MB should fail (90 + 20 > 100)
	if budget.TryAcquireResident(int64(20 * 1024 * 1024)) {
		t.Fatalf("expected budget acquisition to be rejected when exceeding total")
	}

	// Release 40 MB
	budget.ReleaseResident(chunk)
	if budget.ResidentBytes() != chunk2 {
		t.Fatalf("expected %d resident bytes after release, got %d", chunk2, budget.ResidentBytes())
	}

	// Reset with SLRU
	slru := NewLRUCache(total)
	budget.SyncSLRU(slru)
	// target should be total - 50MB = 50MB
	if slru.maxBytes != int64(50*1024*1024) {
		t.Fatalf("expected slru maxBytes %d, got %d", int64(50*1024*1024), slru.maxBytes)
	}

	// ResetResident restores SLRU to total
	budget.ResetResident(slru)
	if budget.ResidentBytes() != 0 {
		t.Fatalf("expected 0 resident bytes after reset, got %d", budget.ResidentBytes())
	}
	if slru.maxBytes != total {
		t.Fatalf("expected slru maxBytes restored to %d, got %d", total, slru.maxBytes)
	}
}

func TestRAMBudget_ConcurrentAcquire(t *testing.T) {
	total := int64(10 * 1024 * 1024) // 10 MB
	budget := NewRAMBudget(total)

	var wg sync.WaitGroup
	workers := 20
	chunk := int64(1024 * 1024) // 1 MB
	successful := 0
	var mu sync.Mutex

	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if budget.TryAcquireResident(chunk) {
				mu.Lock()
				successful++
				mu.Unlock()
			}
		}()
	}
	wg.Wait()

	if successful != 10 {
		t.Fatalf("expected exactly 10 successful acquisitions, got %d", successful)
	}
	if budget.ResidentBytes() != total {
		t.Fatalf("expected exactly %d bytes, got %d", total, budget.ResidentBytes())
	}
}

func TestRAMBudget_UnifiedConstraintWithSLRU(t *testing.T) {
	total := int64(10 * 1024 * 1024) // 10 MB
	slru := NewLRUCache(total)
	budget := NewRAMBudget(total, slru)

	// Populate SLRU with ~6 MB of data using items sized within shard capacity (shardMax = 640KB)
	itemSize := 60 * 1024 // 60 KB per item
	itemsCount := 100     // 100 * 60KB = 6,000,000 bytes (~5.72 MB)
	for i := 0; i < itemsCount; i++ {
		key := fmt.Sprintf("key_%d", i)
		slru.Set(key, &CacheItem{Data: make([]byte, itemSize)})
	}

	slruBytes := budget.SLRUActualBytes()
	if slruBytes < int64(itemsCount*itemSize) {
		t.Fatalf("expected SLRU bytes >= %d, got %d", itemsCount*itemSize, slruBytes)
	}

	remaining := budget.RemainingResidentBudget()
	expectedRemaining := total - slruBytes
	if remaining != expectedRemaining {
		t.Fatalf("expected remaining %d, got %d", expectedRemaining, remaining)
	}

	// 5 MB acquire should be rejected because slru (~6MB) + 5MB > 10MB
	if budget.TryAcquireResident(5 * 1024 * 1024) {
		t.Fatalf("expected 5MB acquire to be rejected due to SLRU occupancy")
	}

	// 3 MB acquire should succeed because slru (~6MB) + 3MB <= 10MB
	if !budget.TryAcquireResident(3 * 1024 * 1024) {
		t.Fatalf("expected 3MB acquire to succeed")
	}

	ok, res, sl, tot := budget.ValidateUnifiedBudget()
	if !ok {
		t.Fatalf("expected unified budget valid, got res=%d, slru=%d, tot=%d", res, sl, tot)
	}

	budget.ReleaseResident(3 * 1024 * 1024)
	if budget.ResidentBytes() != 0 {
		t.Fatalf("expected 0 resident bytes after release, got %d", budget.ResidentBytes())
	}
}

func TestRAMBudget_ConcurrentAcquireRelease(t *testing.T) {
	total := int64(8 * 1024 * 1024) // 8 MB
	slru := NewLRUCache(total)
	budget := NewRAMBudget(total, slru)

	// Fill SLRU with 2 MB (20 items of 100 KB)
	for i := 0; i < 20; i++ {
		slru.Set(fmt.Sprintf("seed_%d", i), &CacheItem{Data: make([]byte, 100*1024)})
	}

	var wg sync.WaitGroup
	workers := 16
	iterations := 50

	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			chunk := int64(512 * 1024) // 512 KB
			for j := 0; j < iterations; j++ {
				if budget.TryAcquireResident(chunk) {
					// Invariant check
					ok, res, sl, tot := budget.ValidateUnifiedBudget()
					if !ok {
						t.Errorf("invariant violated: res=%d slru=%d tot=%d", res, sl, tot)
					}
					budget.ReleaseResident(chunk)
				}
			}
		}()
	}
	wg.Wait()

	if budget.ResidentBytes() != 0 {
		t.Fatalf("expected 0 resident bytes at end, got %d", budget.ResidentBytes())
	}
}

func TestRAMBudget_ConcurrentAcquireReleaseWithSLRUMutations(t *testing.T) {
	total := int64(16 * 1024 * 1024) // 16 MB
	slru := NewLRUCache(total)
	budget := NewRAMBudget(total, slru)

	var wg sync.WaitGroup
	iterations := 100

	// Goroutines mutating SLRU
	for s := 0; s < 8; s++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for i := 0; i < iterations; i++ {
				key := fmt.Sprintf("slru_mut_%d_%d", id, i%30)
				slru.Set(key, &CacheItem{Key: key, Data: make([]byte, 16*1024)})
				if i%5 == 0 {
					slru.Delete(key)
				}
			}
		}(s)
	}

	// Goroutines acquiring and releasing Resident
	for r := 0; r < 8; r++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			chunk := int64(64 * 1024) // 64 KB
			for i := 0; i < iterations; i++ {
				if budget.TryAcquireResident(chunk) {
					ok, res, sl, tot := budget.ValidateUnifiedBudget()
					if !ok {
						t.Errorf("invariant violated during acquire: res=%d, slru=%d, tot=%d", res, sl, tot)
					}
					budget.ReleaseResident(chunk)
				}
			}
		}(r)
	}

	wg.Wait()

	ok, res, sl, tot := budget.ValidateUnifiedBudget()
	if !ok {
		t.Fatalf("final invariant violated: res=%d, slru=%d, tot=%d", res, sl, tot)
	}
	if res < 0 || sl < 0 || res+sl > tot {
		t.Fatalf("invalid bounds: res=%d, slru=%d, tot=%d", res, sl, tot)
	}
}

func TestRAMBudget_ConcurrentSLRUSetInterleaving(t *testing.T) {
	total := int64(8 * 1024 * 1024) // 8 MB
	slru := NewLRUCache(total)
	budget := NewRAMBudget(total, slru)

	var wg sync.WaitGroup
	iterations := 200
	var maxExceeded atomic.Int64
	var totalObservations atomic.Int64

	// 10 Goroutines heavily calling slru.Set with large chunks
	for s := 0; s < 10; s++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for i := 0; i < iterations; i++ {
				key := fmt.Sprintf("heavy_slru_%d_%d", id, i%15)
				slru.Set(key, &CacheItem{Key: key, Data: make([]byte, 128*1024)}) // 128 KB
			}
		}(s)
	}

	// 10 Goroutines heavily acquiring and releasing Resident
	for r := 0; r < 10; r++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			chunk := int64(256 * 1024) // 256 KB
			for i := 0; i < iterations; i++ {
				if budget.TryAcquireResident(chunk) {
					totalObservations.Add(1)
					ok, res, sl, tot := budget.ValidateUnifiedBudget()
					if !ok {
						excess := (res + sl) - tot
						if excess > maxExceeded.Load() {
							maxExceeded.Store(excess)
						}
					}
					budget.ReleaseResident(chunk)
				}
			}
		}(r)
	}

	wg.Wait()

	t.Logf("Observed %d acquires under heavy concurrent LRU.Set(); max transient excess: %d bytes (%.2f KB)",
		totalObservations.Load(), maxExceeded.Load(), float64(maxExceeded.Load())/1024.0)

	// After all concurrent activity stops, the system MUST converge:
	ok, res, sl, tot := budget.ValidateUnifiedBudget()
	if !ok {
		t.Fatalf("failed to converge after concurrency: res=%d, slru=%d, tot=%d", res, sl, tot)
	}
	if res != 0 {
		t.Fatalf("expected resident bytes to be 0 after all releases, got %d", res)
	}
	if sl > tot {
		t.Fatalf("slru bytes %d exceeded total budget %d", sl, tot)
	}
}


