package cache

import (
	"sync"
)

type CacheItem struct {
	Key             string
	Data            []byte
	ContentType     string
	ContentEncoding string
	ETag            string
	LastModified    string
	Size            int64
}

type lruNode struct {
	key         string
	item        *CacheItem
	prev        *lruNode
	next        *lruNode
	isProtected bool
}

type lruShard struct {
	mu           sync.RWMutex
	maxBytes     int64
	probBytes    int64
	probMaxBytes int64
	protBytes    int64
	protMaxBytes int64
	items        map[string]*lruNode

	probHead *lruNode // dummy sentinel head for probationary list
	probTail *lruNode // dummy sentinel tail for probationary list

	protHead *lruNode // dummy sentinel head for protected list
	protTail *lruNode // dummy sentinel tail for protected list
}

func newLRUShard(maxBytes int64) *lruShard {
	probHead := &lruNode{}
	probTail := &lruNode{}
	probHead.next = probTail
	probTail.prev = probHead

	protHead := &lruNode{}
	protTail := &lruNode{}
	protHead.next = protTail
	protTail.prev = protHead

	s := &lruShard{
		maxBytes: maxBytes,
		items:    make(map[string]*lruNode),
		probHead: probHead,
		probTail: probTail,
		protHead: protHead,
		protTail: protTail,
	}
	s.calcCapacities(maxBytes)
	return s
}

func (s *lruShard) calcCapacities(maxBytes int64) {
	s.maxBytes = maxBytes
	if maxBytes <= 0 {
		s.probMaxBytes = 0
		s.protMaxBytes = 0
		return
	}
	// Segmented LRU: 25% probationary target, 75% protected target
	s.probMaxBytes = maxBytes / 4
	if s.probMaxBytes == 0 {
		s.probMaxBytes = 1
	}
	s.protMaxBytes = maxBytes - s.probMaxBytes
}

func (s *lruShard) removeNode(n *lruNode) {
	n.prev.next = n.next
	n.next.prev = n.prev
	n.prev = nil
	n.next = nil
}

func (s *lruShard) probPushFront(n *lruNode) {
	n.next = s.probHead.next
	n.prev = s.probHead
	s.probHead.next.prev = n
	s.probHead.next = n
	n.isProtected = false
}

func (s *lruShard) protPushFront(n *lruNode) {
	n.next = s.protHead.next
	n.prev = s.protHead
	s.protHead.next.prev = n
	s.protHead.next = n
	n.isProtected = true
}

func (s *lruShard) probPopTail() *lruNode {
	if s.probTail.prev == s.probHead {
		return nil
	}
	n := s.probTail.prev
	s.removeNode(n)
	return n
}

func (s *lruShard) protPopTail() *lruNode {
	if s.protTail.prev == s.protHead {
		return nil
	}
	n := s.protTail.prev
	s.removeNode(n)
	return n
}

func (s *lruShard) demoteProtected() {
	for s.protMaxBytes > 0 && s.protBytes > s.protMaxBytes && s.protTail.prev != s.protHead {
		n := s.protPopTail()
		if n == nil {
			break
		}
		s.protBytes -= n.item.Size
		s.probPushFront(n)
		s.probBytes += n.item.Size
	}
}

func (s *lruShard) evictProbation() {
	// First evict from probationary list to keep total usage <= maxBytes
	for s.maxBytes > 0 && (s.probBytes+s.protBytes > s.maxBytes) && s.probTail.prev != s.probHead {
		n := s.probPopTail()
		if n == nil {
			break
		}
		delete(s.items, n.key)
		s.probBytes -= n.item.Size
	}
	// If probationary is empty and protected alone still exceeds maxBytes:
	for s.maxBytes > 0 && (s.probBytes+s.protBytes > s.maxBytes) && s.protTail.prev != s.protHead {
		n := s.protPopTail()
		if n == nil {
			break
		}
		delete(s.items, n.key)
		s.protBytes -= n.item.Size
	}
}

func (s *lruShard) setMaxBytes(maxBytes int64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.calcCapacities(maxBytes)
	s.demoteProtected()
	s.evictProbation()
}

func (s *lruShard) get(key string) (*CacheItem, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if node, ok := s.items[key]; ok {
		if node.isProtected {
			if s.protHead.next != node {
				s.removeNode(node)
				s.protPushFront(node)
			}
		} else {
			// Second access / hit: Promote from Probationary to Protected
			s.removeNode(node)
			s.probBytes -= node.item.Size
			s.protPushFront(node)
			s.protBytes += node.item.Size

			s.demoteProtected()
			s.evictProbation()
		}
		return node.item, true
	}
	return nil, false
}

func (s *lruShard) peek(key string) (*CacheItem, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if node, ok := s.items[key]; ok {
		return node.item, true
	}
	return nil, false
}

func (s *lruShard) contains(key string) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	_, ok := s.items[key]
	return ok
}

func (s *lruShard) isProtected(key string) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if node, ok := s.items[key]; ok {
		return node.isProtected
	}
	return false
}

func (s *lruShard) set(key string, item *CacheItem, isProtected bool) {
	if item == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	item.Size = int64(len(item.Data))
	if s.maxBytes > 0 && item.Size > s.maxBytes {
		if node, ok := s.items[key]; ok {
			s.removeNode(node)
			delete(s.items, key)
			if node.isProtected {
				s.protBytes -= node.item.Size
			} else {
				s.probBytes -= node.item.Size
			}
		}
		return
	}

	if node, ok := s.items[key]; ok {
		if node.isProtected || isProtected {
			if node.isProtected {
				s.protBytes -= node.item.Size
			} else {
				s.removeNode(node)
				s.probBytes -= node.item.Size
			}
			node.item = item
			s.protPushFront(node)
			s.protBytes += item.Size
			s.demoteProtected()
			s.evictProbation()
		} else {
			// Remains in probation:
			s.probBytes -= node.item.Size
			node.item = item
			s.probBytes += item.Size
			if s.probHead.next != node {
				s.removeNode(node)
				s.probPushFront(node)
			}
			s.evictProbation()
		}
		return
	}

	// New entry:
	node := &lruNode{key: key, item: item}
	s.items[key] = node
	if isProtected {
		s.protPushFront(node)
		s.protBytes += item.Size
		s.demoteProtected()
		s.evictProbation()
	} else {
		s.probPushFront(node)
		s.probBytes += item.Size
		s.evictProbation()
	}
}

func (s *lruShard) delete(key string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if node, ok := s.items[key]; ok {
		s.removeNode(node)
		delete(s.items, key)
		if node.isProtected {
			s.protBytes -= node.item.Size
		} else {
			s.probBytes -= node.item.Size
		}
		return true
	}
	return false
}

func (s *lruShard) clear() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.items = make(map[string]*lruNode)
	s.probHead.next = s.probTail
	s.probTail.prev = s.probHead
	s.protHead.next = s.protTail
	s.protTail.prev = s.protHead
	s.probBytes = 0
	s.protBytes = 0
}

func (s *lruShard) stats() (int, int64) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.items), s.probBytes + s.protBytes
}

const defaultNumShards = 16

type LRUCache struct {
	shards    []*lruShard
	mask      uint32
	maxBytes  int64
	numShards int
}

func fnv32(key string) uint32 {
	var hash uint32 = 2166136261
	for i := 0; i < len(key); i++ {
		hash ^= uint32(key[i])
		hash *= 16777619
	}
	return hash
}

func NewLRUCache(maxBytes int64) *LRUCache {
	numShards := 1
	if maxBytes >= 1024*1024 {
		numShards = defaultNumShards
	}

	shards := make([]*lruShard, numShards)
	perShard := maxBytes
	if numShards > 1 {
		perShard = maxBytes / int64(numShards)
	}

	for i := 0; i < numShards; i++ {
		shards[i] = newLRUShard(perShard)
	}

	return &LRUCache{
		shards:    shards,
		mask:      uint32(numShards - 1),
		maxBytes:  maxBytes,
		numShards: numShards,
	}
}

func (c *LRUCache) getShard(key string) *lruShard {
	if c.numShards == 1 {
		return c.shards[0]
	}
	idx := fnv32(key) & c.mask
	return c.shards[idx]
}

func (c *LRUCache) SetMaxBytes(maxBytes int64) {
	c.maxBytes = maxBytes
	perShard := maxBytes
	if c.numShards > 1 {
		perShard = maxBytes / int64(c.numShards)
	}
	for _, s := range c.shards {
		s.setMaxBytes(perShard)
	}
}

func (c *LRUCache) Get(key string) (*CacheItem, bool) {
	return c.getShard(key).get(key)
}

func (c *LRUCache) Peek(key string) (*CacheItem, bool) {
	return c.getShard(key).peek(key)
}

func (c *LRUCache) Contains(key string) bool {
	return c.getShard(key).contains(key)
}

func (c *LRUCache) IsProtected(key string) bool {
	return c.getShard(key).isProtected(key)
}

func (c *LRUCache) Set(key string, item *CacheItem) {
	c.getShard(key).set(key, item, true)
}

func (c *LRUCache) SetProbation(key string, item *CacheItem) {
	c.getShard(key).set(key, item, false)
}

func (c *LRUCache) Delete(key string) bool {
	return c.getShard(key).delete(key)
}

func (c *LRUCache) Clear() {
	for _, shard := range c.shards {
		shard.clear()
	}
}

func (c *LRUCache) Stats() (int, int64) {
	var totalItems int
	var totalBytes int64
	for _, shard := range c.shards {
		items, bytes := shard.stats()
		totalItems += items
		totalBytes += bytes
	}
	return totalItems, totalBytes
}
