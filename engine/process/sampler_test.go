package process

import (
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestMemorySampler_CacheAndRefresh(t *testing.T) {
	var callCount atomic.Int64
	mockFetcher := func() (MemoryInfo, error) {
		cnt := callCount.Add(1)
		return MemoryInfo{
			TotalBytes:     16 * 1024 * 1024 * 1024,
			AvailableBytes: uint64(cnt) * 1024 * 1024 * 1024, // changes on every call
		}, nil
	}

	ttl := 100 * time.Millisecond
	sampler := NewMemorySampler(ttl, mockFetcher)

	// Call 1: First call must invoke fetcher
	info1, err := sampler.Get()
	if err != nil {
		t.Fatalf("first Get failed: %v", err)
	}
	if callCount.Load() != 1 {
		t.Fatalf("expected 1 call, got %d", callCount.Load())
	}
	if info1.AvailableBytes != 1*1024*1024*1024 {
		t.Fatalf("unexpected info1 available: %d", info1.AvailableBytes)
	}

	// Call 2 & 3 within TTL: must return cached result without calling fetcher
	for i := 0; i < 5; i++ {
		cachedInfo, err := sampler.Get()
		if err != nil {
			t.Fatalf("cached Get failed: %v", err)
		}
		if cachedInfo.AvailableBytes != info1.AvailableBytes {
			t.Fatalf("expected cached available bytes %d, got %d", info1.AvailableBytes, cachedInfo.AvailableBytes)
		}
		if callCount.Load() != 1 {
			t.Fatalf("fetcher invoked while within TTL, call count: %d", callCount.Load())
		}
	}

	// Sleep until TTL expires
	time.Sleep(120 * time.Millisecond)

	// Call 4 after expiration: must invoke fetcher again and refresh cache
	info2, err := sampler.Get()
	if err != nil {
		t.Fatalf("expired Get failed: %v", err)
	}
	if callCount.Load() != 2 {
		t.Fatalf("expected 2 calls after expiration, got %d", callCount.Load())
	}
	if info2.AvailableBytes != 2*1024*1024*1024 {
		t.Fatalf("unexpected info2 available: %d", info2.AvailableBytes)
	}

	// Test Reset()
	sampler.Reset()
	info3, err := sampler.Get()
	if err != nil {
		t.Fatalf("Get after Reset failed: %v", err)
	}
	if callCount.Load() != 3 {
		t.Fatalf("expected 3 calls after reset, got %d", callCount.Load())
	}
	if info3.AvailableBytes != 3*1024*1024*1024 {
		t.Fatalf("unexpected info3 available: %d", info3.AvailableBytes)
	}
}

func TestMemorySampler_ConcurrentSafety(t *testing.T) {
	var callCount atomic.Int64
	sampler := NewMemorySampler(50*time.Millisecond, func() (MemoryInfo, error) {
		callCount.Add(1)
		return MemoryInfo{
			TotalBytes:     32 * 1024 * 1024 * 1024,
			AvailableBytes: 16 * 1024 * 1024 * 1024,
		}, nil
	})

	const numWorkers = 16
	var wg sync.WaitGroup
	wg.Add(numWorkers)

	for i := 0; i < numWorkers; i++ {
		go func() {
			defer wg.Done()
			for j := 0; j < 50; j++ {
				_, _ = sampler.Get()
				time.Sleep(1 * time.Millisecond)
			}
		}()
	}

	wg.Wait()
	if callCount.Load() == 0 {
		t.Fatalf("fetcher should have been called at least once")
	}
}
