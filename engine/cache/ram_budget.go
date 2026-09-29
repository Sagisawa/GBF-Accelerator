package cache

import (
	"sync"
)

const minSLRUBudgetBytes = 16 * 1024 * 1024 // 16 MB minimum capacity floor for SLRU operations

// RAMBudget coordinates the unified memory budget between the 16-shard SLRU cache
// and the RAM Boost Resident Pool.
//
// Under high concurrency, RAMBudget maintains unified memory control through atomic
// quota reading and immediate capacity shrinking (SetMaxBytes):
//
//	actual_slru_bytes + resident_bytes <= total_budget (converged invariant)
//
// To preserve zero-alloc throughput on the proxy data plane, SLRU shards retain independent
// striped locking. During microsecond interleavings between prewarm workers and concurrent
// network traffic, transient deviations are bounded and immediately converge back under budget.
type RAMBudget struct {
	mu            sync.RWMutex
	totalBudget   int64     // Total configured data budget in bytes
	residentBytes int64     // Net payload bytes currently held in the Resident Pool
	slru          *LRUCache // Reference to SLRU cache for real-time actual byte verification
}

// NewRAMBudget creates a budget coordinator with the given total budget and optional SLRU reference.
func NewRAMBudget(totalBytes int64, slru ...*LRUCache) *RAMBudget {
	if totalBytes < 0 {
		totalBytes = 0
	}
	var s *LRUCache
	if len(slru) > 0 {
		s = slru[0]
	}
	return &RAMBudget{
		totalBudget: totalBytes,
		slru:        s,
	}
}

// SetSLRU binds or updates the SLRU cache reference.
func (b *RAMBudget) SetSLRU(slru *LRUCache) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.slru = slru
}

// SetTotalBudget updates the total budget and adjusts SLRU capacity accordingly.
func (b *RAMBudget) SetTotalBudget(totalBytes int64, slru *LRUCache) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if totalBytes < 0 {
		totalBytes = 0
	}
	b.totalBudget = totalBytes
	s := slru
	if s == nil {
		s = b.slru
	}
	if s != nil {
		b.syncSLRULocked(s)
	}
}

// TotalBudget returns the total configured budget in bytes.
func (b *RAMBudget) TotalBudget() int64 {
	b.mu.RLock()
	defer b.mu.RUnlock()
	return b.totalBudget
}

// ResidentBytes returns the number of bytes committed to the Resident Pool.
func (b *RAMBudget) ResidentBytes() int64 {
	b.mu.RLock()
	defer b.mu.RUnlock()
	return b.residentBytes
}

// SLRUActualBytes returns the actual bytes currently stored in the SLRU cache.
func (b *RAMBudget) SLRUActualBytes() int64 {
	b.mu.RLock()
	defer b.mu.RUnlock()
	if b.slru == nil {
		return 0
	}
	return b.slru.TotalBytes()
}

// RemainingResidentBudget returns the bytes still available for Resident Pool allocation,
// strictly bounded by both configured total budget and actual SLRU bytes:
//
//	available = max(0, totalBudget - residentBytes - slruActualBytes)
func (b *RAMBudget) RemainingResidentBudget() int64 {
	b.mu.RLock()
	defer b.mu.RUnlock()
	var slruBytes int64
	if b.slru != nil {
		slruBytes = b.slru.TotalBytes()
	}
	used := b.residentBytes + slruBytes
	rem := b.totalBudget - used
	if rem < 0 {
		return 0
	}
	return rem
}

// TryAcquireResident attempts to allocate bytes for a new resident item.
// Returns true if within the unified budget (residentBytes + slruActualBytes + bytes <= totalBudget),
// or false if it would exceed total_budget.
func (b *RAMBudget) TryAcquireResident(bytes int64) bool {
	if bytes <= 0 {
		return true
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	var slruBytes int64
	if b.slru != nil {
		slruBytes = b.slru.TotalBytes()
	}
	if b.residentBytes+slruBytes+bytes > b.totalBudget {
		return false
	}
	b.residentBytes += bytes
	if b.slru != nil {
		b.syncSLRULocked(b.slru)
	}
	return true
}

// ReleaseResident frees resident bytes from the budget.
func (b *RAMBudget) ReleaseResident(bytes int64) {
	if bytes <= 0 {
		return
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	b.residentBytes -= bytes
	if b.residentBytes < 0 {
		b.residentBytes = 0
	}
	if b.slru != nil {
		b.syncSLRULocked(b.slru)
	}
}

// ResetResident completely frees all resident bytes and restores the SLRU
// cache to the full total budget.
func (b *RAMBudget) ResetResident(slru *LRUCache) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.residentBytes = 0
	s := slru
	if s == nil {
		s = b.slru
	}
	if s != nil {
		s.SetMaxBytes(b.totalBudget)
	}
}

// SyncSLRU adjusts the SLRU capacity to ensure:
//
//	targetSLRU = max(0, totalBudget - residentBytes)
func (b *RAMBudget) SyncSLRU(slru *LRUCache) {
	b.mu.Lock()
	defer b.mu.Unlock()
	s := slru
	if s == nil {
		s = b.slru
	}
	if s != nil {
		b.syncSLRULocked(s)
	}
}

func (b *RAMBudget) syncSLRULocked(slru *LRUCache) {
	target := b.totalBudget - b.residentBytes
	if target < 0 {
		target = 0
	}
	slru.SetMaxBytes(target)
}

// ValidateUnifiedBudget checks if current actual residentBytes + slruActualBytes <= totalBudget,
// and ensures neither value is negative.
func (b *RAMBudget) ValidateUnifiedBudget() (ok bool, residentBytes int64, slruBytes int64, totalBudget int64) {
	b.mu.RLock()
	defer b.mu.RUnlock()
	var sb int64
	if b.slru != nil {
		sb = b.slru.TotalBytes()
	}
	rb := b.residentBytes
	tb := b.totalBudget
	ok = (rb >= 0 && sb >= 0 && rb+sb <= tb)
	return ok, rb, sb, tb
}
