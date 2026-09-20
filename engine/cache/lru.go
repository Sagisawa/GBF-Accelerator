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
	key  string
	item *CacheItem
	prev *lruNode
	next *lruNode
}

type lruShard struct {
	mu       sync.RWMutex
	maxBytes int64
	curBytes int64
	items    map[string]*lruNode
	head     *lruNode // dummy sentinel head
	tail     *lruNode // dummy sentinel tail
}

func newLRUShard(maxBytes int64) *lruShard {
	head := &lruNode{}
	tail := &lruNode{}
	head.next = tail
	tail.prev = head
	return &lruShard{
		maxBytes: maxBytes,
		items:    make(map[string]*lruNode),
		head:     head,
		tail:     tail,
	}
}

func (s *lruShard) removeNode(n *lruNode) {
	n.prev.next = n.next
	n.next.prev = n.prev
	n.prev = nil
	n.next = nil
}

func (s *lruShard) pushFront(n *lruNode) {
	n.next = s.head.next
	n.prev = s.head
	s.head.next.prev = n
	s.head.next = n
}

func (s *lruShard) moveToFront(n *lruNode) {
	if s.head.next == n {
		return
	}
	s.removeNode(n)
	s.pushFront(n)
}

func (s *lruShard) popTail() *lruNode {
	if s.tail.prev == s.head {
		return nil
	}
	n := s.tail.prev
	s.removeNode(n)
	return n
}

func (s *lruShard) setMaxBytes(maxBytes int64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.maxBytes = maxBytes
	s.evict()
}

func (s *lruShard) get(key string) (*CacheItem, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if node, ok := s.items[key]; ok {
		s.moveToFront(node)
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

func (s *lruShard) set(key string, item *CacheItem) {
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
			s.curBytes -= node.item.Size
		}
		return
	}

	if node, ok := s.items[key]; ok {
		s.moveToFront(node)
		s.curBytes -= node.item.Size
		node.item = item
		s.curBytes += item.Size
		s.evict()
		return
	}

	node := &lruNode{key: key, item: item}
	s.pushFront(node)
	s.items[key] = node
	s.curBytes += item.Size
	s.evict()
}

func (s *lruShard) evict() {
	for s.maxBytes > 0 && s.curBytes > s.maxBytes && len(s.items) > 0 {
		node := s.popTail()
		if node == nil {
			break
		}
		delete(s.items, node.key)
		s.curBytes -= node.item.Size
	}
}

func (s *lruShard) delete(key string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if node, ok := s.items[key]; ok {
		s.removeNode(node)
		delete(s.items, key)
		s.curBytes -= node.item.Size
		return true
	}
	return false
}

func (s *lruShard) clear() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.items = make(map[string]*lruNode)
	s.head.next = s.tail
	s.tail.prev = s.head
	s.curBytes = 0
}

func (s *lruShard) stats() (int, int64) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.items), s.curBytes
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

func (c *LRUCache) Contains(key string) bool {
	return c.getShard(key).contains(key)
}

func (c *LRUCache) Set(key string, item *CacheItem) {
	c.getShard(key).set(key, item)
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
