package cache

import (
	"fmt"
	"sync"
	"testing"
)

// Legacy Flat LRU implementation from main branch prior to 2.3.0
type legacyFlatNode struct {
	key  string
	item *CacheItem
	prev *legacyFlatNode
	next *legacyFlatNode
}

type legacyFlatShard struct {
	mu       sync.Mutex
	maxBytes int64
	curBytes int64
	items    map[string]*legacyFlatNode
	head     *legacyFlatNode
	tail     *legacyFlatNode
}

type legacyFlatLRUCache struct {
	shards    []*legacyFlatShard
	mask      uint32
	maxBytes  int64
	numShards int
}

func newLegacyFlatLRU(maxBytes int64) *legacyFlatLRUCache {
	numShards := 1
	if maxBytes >= 1024*1024 {
		numShards = 16
	}
	shards := make([]*legacyFlatShard, numShards)
	perShard := maxBytes / int64(numShards)
	for i := 0; i < numShards; i++ {
		head := &legacyFlatNode{}
		tail := &legacyFlatNode{}
		head.next = tail
		tail.prev = head
		shards[i] = &legacyFlatShard{
			maxBytes: perShard,
			items:    make(map[string]*legacyFlatNode),
			head:     head,
			tail:     tail,
		}
	}
	return &legacyFlatLRUCache{
		shards:    shards,
		mask:      uint32(numShards - 1),
		maxBytes:  maxBytes,
		numShards: numShards,
	}
}

func (c *legacyFlatLRUCache) getShard(key string) *legacyFlatShard {
	if c.numShards == 1 {
		return c.shards[0]
	}
	return c.shards[fnv32(key)&c.mask]
}

func (s *legacyFlatShard) remove(n *legacyFlatNode) {
	n.prev.next = n.next
	n.next.prev = n.prev
	n.prev = nil
	n.next = nil
}

func (s *legacyFlatShard) pushFront(n *legacyFlatNode) {
	n.next = s.head.next
	n.prev = s.head
	s.head.next.prev = n
	s.head.next = n
}

func (c *legacyFlatLRUCache) Get(key string) (*CacheItem, bool) {
	s := c.getShard(key)
	s.mu.Lock()
	defer s.mu.Unlock()
	if n, ok := s.items[key]; ok {
		if s.head.next != n {
			s.remove(n)
			s.pushFront(n)
		}
		return n.item, true
	}
	return nil, false
}

func (c *legacyFlatLRUCache) Set(key string, item *CacheItem) {
	s := c.getShard(key)
	s.mu.Lock()
	defer s.mu.Unlock()
	item.Size = int64(len(item.Data))
	if n, ok := s.items[key]; ok {
		s.curBytes -= n.item.Size
		n.item = item
		s.curBytes += item.Size
		if s.head.next != n {
			s.remove(n)
			s.pushFront(n)
		}
	} else {
		n := &legacyFlatNode{key: key, item: item}
		s.pushFront(n)
		s.items[key] = n
		s.curBytes += item.Size
	}
	for s.maxBytes > 0 && s.curBytes > s.maxBytes && len(s.items) > 0 {
		old := s.tail.prev
		if old == s.head {
			break
		}
		s.remove(old)
		delete(s.items, old.key)
		s.curBytes -= old.item.Size
	}
}

// TestVerify_RAMPollution_EmpiricalComparison directly compares the legacy flat LRU
// vs the new Segmented LRU under identical parameters to prove the existence of
// cache pollution and the exact baseline retention rate.
func TestVerify_RAMPollution_EmpiricalComparison(t *testing.T) {
	const cacheSize = 4 * 1024 * 1024 // 4 MB
	const numHot = 30
	const hotSize = 32 * 1024 // 32 KB
	const numCold = 40
	const coldSize = 128 * 1024 // 128 KB

	t.Logf("==================================================================")
	t.Logf("EMPIRICAL COMPARISON: Legacy Flat LRU vs 2.3.0 SLRU")
	t.Logf("Cache: 4.0 MB (16 shards x 256 KB)")
	t.Logf("Hot Working Set: 30 items x 32 KB = 0.94 MB")
	t.Logf("Cold Prefetch Burst: 40 items x 128 KB = 5.00 MB")
	t.Logf("==================================================================")

	// --- 1. Test Legacy Flat LRU ---
	flatCache := newLegacyFlatLRU(cacheSize)
	for i := 0; i < numHot; i++ {
		key := fmt.Sprintf("/assets/hot_asset_%d.js", i)
		flatCache.Set(key, &CacheItem{Key: key, Data: make([]byte, hotSize)})
		flatCache.Get(key) // Hot access
	}
	// Inject 40 cold items
	for i := 0; i < numCold; i++ {
		key := fmt.Sprintf("/assets/burst_cold_%d.png", i)
		flatCache.Set(key, &CacheItem{Key: key, Data: make([]byte, coldSize)})
	}
	flatRetained := 0
	for i := 0; i < numHot; i++ {
		key := fmt.Sprintf("/assets/hot_asset_%d.js", i)
		if _, ok := flatCache.Get(key); ok {
			flatRetained++
		}
	}
	flatRate := float64(flatRetained) / float64(numHot) * 100

	// --- 2. Test 2.3.0 SLRU ---
	slruCache := NewLRUCache(cacheSize)
	for i := 0; i < numHot; i++ {
		key := fmt.Sprintf("/assets/hot_asset_%d.js", i)
		slruCache.Set(key, &CacheItem{Key: key, Data: make([]byte, hotSize)})
		slruCache.Get(key) // Hot access (promotes to protected)
	}
	// Inject 40 cold items
	for i := 0; i < numCold; i++ {
		key := fmt.Sprintf("/assets/burst_cold_%d.png", i)
		slruCache.Set(key, &CacheItem{Key: key, Data: make([]byte, coldSize)})
	}
	slruRetained := 0
	for i := 0; i < numHot; i++ {
		key := fmt.Sprintf("/assets/hot_asset_%d.js", i)
		if _, ok := slruCache.Get(key); ok {
			slruRetained++
		}
	}
	slruRate := float64(slruRetained) / float64(numHot) * 100

	t.Logf("Legacy Flat LRU Retention: %d / %d (%.1f%%)", flatRetained, numHot, flatRate)
	t.Logf("2.3.0 SLRU Retention:      %d / %d (%.1f%%)", slruRetained, numHot, slruRate)
	t.Logf("==================================================================")
}
