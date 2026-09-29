package cache

import (
	"sync"
	"sync/atomic"
)

type residentShard struct {
	mu    sync.RWMutex
	items map[string]*CacheItem
	bytes int64
}

func newResidentShard() *residentShard {
	return &residentShard{
		items: make(map[string]*CacheItem),
	}
}

func (s *residentShard) get(key string) (*CacheItem, bool) {
	s.mu.RLock()
	item, ok := s.items[key]
	s.mu.RUnlock()
	return item, ok
}

func (s *residentShard) set(key string, item *CacheItem) int64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	if old, exists := s.items[key]; exists {
		deltaBytes := item.Size - old.Size
		s.bytes += deltaBytes
		s.items[key] = item
		return deltaBytes
	}
	s.items[key] = item
	s.bytes += item.Size
	return item.Size
}

func (s *residentShard) delete(key string) int64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	if old, exists := s.items[key]; exists {
		delete(s.items, key)
		s.bytes -= old.Size
		return old.Size
	}
	return 0
}

func (s *residentShard) clear() (int, int64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	count := len(s.items)
	b := s.bytes
	s.items = make(map[string]*CacheItem)
	s.bytes = 0
	return count, b
}

func (s *residentShard) stats() (int, int64) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.items), s.bytes
}

const residentShardCount = 16

// ResidentPool stores a read-only, non-evicting snapshot of disk cache assets in RAM.
// It does not share eviction state with the 16-shard SLRU cache and obeys total RAM budget limits.
type ResidentPool struct {
	lifecycleMu sync.RWMutex
	shards      [residentShardCount]*residentShard
	budget      *RAMBudget
	enabled     atomic.Bool
	generation  atomic.Uint64
}

// NewResidentPool constructs an independent Resident Pool bound to the given RAM budget coordinator.
func NewResidentPool(budget *RAMBudget) *ResidentPool {
	p := &ResidentPool{
		budget: budget,
	}
	p.generation.Store(1)
	for i := 0; i < residentShardCount; i++ {
		p.shards[i] = newResidentShard()
	}
	return p
}

func (p *ResidentPool) getShard(key string) *residentShard {
	return p.shards[int(fnv32(key)&(residentShardCount-1))]
}

// Enabled reports whether RAM Boost resident lookups are currently active.
func (p *ResidentPool) Enabled() bool {
	return p.enabled.Load()
}

// SetEnabled enables or disables resident lookups.
func (p *ResidentPool) SetEnabled(enabled bool) {
	p.lifecycleMu.Lock()
	defer p.lifecycleMu.Unlock()
	p.enabled.Store(enabled)
}

// Generation returns the current generation epoch of the resident pool.
func (p *ResidentPool) Generation() uint64 {
	return p.generation.Load()
}

// NextGeneration increments the generation counter, invalidating all prior workers.
func (p *ResidentPool) NextGeneration() uint64 {
	p.lifecycleMu.Lock()
	defer p.lifecycleMu.Unlock()
	return p.generation.Add(1)
}

// ResetForGen clears all items and activates the resident pool for the specified generation.
func (p *ResidentPool) ResetForGen(gen uint64) {
	p.lifecycleMu.Lock()
	defer p.lifecycleMu.Unlock()

	p.generation.Store(gen)
	for _, shard := range p.shards {
		shard.clear()
	}
	if p.budget != nil {
		p.budget.ResetResident(nil)
	}
	p.enabled.Store(true)
}

// Disable deactivates the resident pool, advances the generation to invalidate running workers,
// clears all resident data, and resets the RAM budget.
func (p *ResidentPool) Disable(gen uint64) {
	p.lifecycleMu.Lock()
	defer p.lifecycleMu.Unlock()

	if gen == 0 {
		p.generation.Add(1)
	} else {
		p.generation.Store(gen)
	}
	p.enabled.Store(false)
	for _, shard := range p.shards {
		shard.clear()
	}
	if p.budget != nil {
		p.budget.ResetResident(nil)
	}
}

// TryAcquireBudget attempts to acquire resident budget under the current generation.
// Returns false immediately if the pool is disabled or the generation has changed.
func (p *ResidentPool) TryAcquireBudget(bytes int64, gen uint64) bool {
	p.lifecycleMu.RLock()
	defer p.lifecycleMu.RUnlock()

	if !p.enabled.Load() || p.generation.Load() != gen {
		return false
	}
	if p.budget == nil {
		return true
	}
	return p.budget.TryAcquireResident(bytes)
}

// ReleaseBudget releases resident budget if the generation is still active.
// If the generation was already invalidated or the pool disabled, the budget was already reset,
// so no release is performed.
func (p *ResidentPool) ReleaseBudget(bytes int64, gen uint64) {
	p.lifecycleMu.RLock()
	defer p.lifecycleMu.RUnlock()

	if !p.enabled.Load() || p.generation.Load() != gen {
		return
	}
	if p.budget != nil {
		p.budget.ReleaseResident(bytes)
	}
}

// Get retrieves an item from the resident pool. If Boost is not enabled, it returns (nil, false) immediately
// without acquiring shard locks.
func (p *ResidentPool) Get(key string) (*CacheItem, bool) {
	if !p.enabled.Load() {
		return nil, false
	}
	return p.getShard(key).get(key)
}

// Set inserts or updates an item in the resident pool,
// checking and acquiring budget as needed.
func (p *ResidentPool) Set(key string, item *CacheItem) bool {
	if item == nil {
		return false
	}
	p.lifecycleMu.RLock()
	defer p.lifecycleMu.RUnlock()

	if !p.enabled.Load() {
		return false
	}

	shard := p.getShard(key)
	shard.mu.Lock()
	defer shard.mu.Unlock()

	if !p.enabled.Load() {
		return false
	}

	var delta int64 = item.Size
	if old, exists := shard.items[key]; exists {
		delta = item.Size - old.Size
	}

	if p.budget != nil {
		if delta > 0 {
			if !p.budget.TryAcquireResident(delta) {
				return false
			}
		} else if delta < 0 {
			p.budget.ReleaseResident(-delta)
		}
	}

	shard.items[key] = item
	shard.bytes += delta
	return true
}

// SetWithGen inserts or updates an item in the resident pool only if the provided generation
// strictly matches the pool's current active generation and the pool is enabled.
// If the generation was invalidated or the pool disabled, it immediately rejects the write and returns false.
func (p *ResidentPool) SetWithGen(key string, item *CacheItem, gen uint64) bool {
	if item == nil {
		return false
	}
	p.lifecycleMu.RLock()
	defer p.lifecycleMu.RUnlock()

	if !p.enabled.Load() || p.generation.Load() != gen {
		return false
	}

	shard := p.getShard(key)
	shard.mu.Lock()
	defer shard.mu.Unlock()

	// Double check under shard lock to prevent stale commits
	if !p.enabled.Load() || p.generation.Load() != gen {
		return false
	}

	var delta int64 = item.Size
	if old, exists := shard.items[key]; exists {
		delta = item.Size - old.Size
	}

	shard.items[key] = item
	shard.bytes += delta
	return true
}

// Delete removes an item from the resident pool and releases its occupied bytes from the budget coordinator.
func (p *ResidentPool) Delete(key string) {
	p.lifecycleMu.RLock()
	defer p.lifecycleMu.RUnlock()

	shard := p.getShard(key)
	shard.mu.Lock()
	defer shard.mu.Unlock()
	if old, exists := shard.items[key]; exists {
		delete(shard.items, key)
		shard.bytes -= old.Size
		if p.budget != nil && p.enabled.Load() {
			p.budget.ReleaseResident(old.Size)
		}
	}
}

// Clear removes all items from the resident pool, advances the generation counter,
// and resets occupied resident bytes in the budget coordinator.
func (p *ResidentPool) Clear() {
	p.lifecycleMu.Lock()
	defer p.lifecycleMu.Unlock()

	p.generation.Add(1)
	p.enabled.Store(false)
	for _, shard := range p.shards {
		shard.clear()
	}
	if p.budget != nil {
		p.budget.ResetResident(nil)
	}
}

// Stats returns the total count of items and total payload bytes held in the resident pool.
// When the pool is disabled, it strictly returns (0, 0).
func (p *ResidentPool) Stats() (items int, bytes int64) {
	p.lifecycleMu.RLock()
	defer p.lifecycleMu.RUnlock()

	if !p.enabled.Load() {
		return 0, 0
	}
	for _, shard := range p.shards {
		cnt, b := shard.stats()
		items += cnt
		bytes += b
	}
	return items, bytes
}
