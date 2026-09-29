package process

import (
	"sync"
	"time"
)

// MemorySampler caches memory probe results for a short TTL (e.g. 300ms)
// to prevent excessive subprocess spawning or syscalls during high-frequency loops.
type MemorySampler struct {
	mu         sync.Mutex
	cached     MemoryInfo
	lastSample time.Time
	ttl        time.Duration
	fetcher    func() (MemoryInfo, error)
}

// NewMemorySampler constructs a memory sampler with the specified TTL and fetch function.
func NewMemorySampler(ttl time.Duration, fetcher func() (MemoryInfo, error)) *MemorySampler {
	if ttl <= 0 {
		ttl = 300 * time.Millisecond
	}
	return &MemorySampler{
		ttl:     ttl,
		fetcher: fetcher,
	}
}

// Get returns the cached MemoryInfo if still within TTL, or invokes the fetcher to refresh.
func (s *MemorySampler) Get() (MemoryInfo, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	now := time.Now()
	if s.cached.TotalBytes > 0 && now.Sub(s.lastSample) < s.ttl {
		return s.cached, nil
	}

	info, err := s.fetcher()
	if err == nil && info.TotalBytes > 0 {
		s.cached = info
		s.lastSample = now
	}
	return info, err
}

// SetTTL updates the sampler's cache TTL.
func (s *MemorySampler) SetTTL(ttl time.Duration) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.ttl = ttl
}

// Reset clears the cached memory info.
func (s *MemorySampler) Reset() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.cached = MemoryInfo{}
	s.lastSample = time.Time{}
}
