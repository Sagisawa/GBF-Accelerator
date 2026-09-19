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

type LRUCache struct {
	mu       sync.RWMutex
	maxBytes int64
	curBytes int64
	items    map[string]*lruNode
	head     *lruNode // dummy sentinel head
	tail     *lruNode // dummy sentinel tail
}

func NewLRUCache(maxBytes int64) *LRUCache {
	head := &lruNode{}
	tail := &lruNode{}
	head.next = tail
	tail.prev = head
	return &LRUCache{
		maxBytes: maxBytes,
		items:    make(map[string]*lruNode),
		head:     head,
		tail:     tail,
	}
}

func (c *LRUCache) removeNode(n *lruNode) {
	n.prev.next = n.next
	n.next.prev = n.prev
	n.prev = nil
	n.next = nil
}

func (c *LRUCache) pushFront(n *lruNode) {
	n.next = c.head.next
	n.prev = c.head
	c.head.next.prev = n
	c.head.next = n
}

func (c *LRUCache) moveToFront(n *lruNode) {
	if c.head.next == n {
		return
	}
	c.removeNode(n)
	c.pushFront(n)
}

func (c *LRUCache) popTail() *lruNode {
	if c.tail.prev == c.head {
		return nil
	}
	n := c.tail.prev
	c.removeNode(n)
	return n
}

func (c *LRUCache) SetMaxBytes(maxBytes int64) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.maxBytes = maxBytes
	c.evict()
}

func (c *LRUCache) Get(key string) (*CacheItem, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if node, ok := c.items[key]; ok {
		c.moveToFront(node)
		return node.item, true
	}
	return nil, false
}

func (c *LRUCache) Contains(key string) bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	_, ok := c.items[key]
	return ok
}

func (c *LRUCache) Set(key string, item *CacheItem) {
	if item == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()

	item.Size = int64(len(item.Data))
	if c.maxBytes > 0 && item.Size > c.maxBytes {
		if node, ok := c.items[key]; ok {
			c.removeNode(node)
			delete(c.items, key)
			c.curBytes -= node.item.Size
		}
		return
	}

	if node, ok := c.items[key]; ok {
		c.moveToFront(node)
		c.curBytes -= node.item.Size
		node.item = item
		c.curBytes += item.Size
		c.evict()
		return
	}

	node := &lruNode{key: key, item: item}
	c.pushFront(node)
	c.items[key] = node
	c.curBytes += item.Size
	c.evict()
}

func (c *LRUCache) evict() {
	for c.maxBytes > 0 && c.curBytes > c.maxBytes && len(c.items) > 0 {
		node := c.popTail()
		if node == nil {
			break
		}
		delete(c.items, node.key)
		c.curBytes -= node.item.Size
	}
}

func (c *LRUCache) Delete(key string) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	if node, ok := c.items[key]; ok {
		c.removeNode(node)
		delete(c.items, key)
		c.curBytes -= node.item.Size
		return true
	}
	return false
}

func (c *LRUCache) Clear() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.items = make(map[string]*lruNode)
	c.head.next = c.tail
	c.tail.prev = c.head
	c.curBytes = 0
}

func (c *LRUCache) Stats() (int, int64) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return len(c.items), c.curBytes
}
