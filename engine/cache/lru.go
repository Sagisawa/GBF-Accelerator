package cache

import (
	"container/list"
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

type LRUCache struct {
	mu       sync.RWMutex
	maxBytes int64
	curBytes int64
	items    map[string]*list.Element
	evictList *list.List
}

type entry struct {
	key  string
	item *CacheItem
}

func NewLRUCache(maxBytes int64) *LRUCache {
	return &LRUCache{
		maxBytes:  maxBytes,
		items:     make(map[string]*list.Element),
		evictList: list.New(),
	}
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

	if elem, ok := c.items[key]; ok {
		c.evictList.MoveToFront(elem)
		return elem.Value.(*entry).item, true
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
	c.mu.Lock()
	defer c.mu.Unlock()

	item.Size = int64(len(item.Data))
	if elem, ok := c.items[key]; ok {
		c.evictList.MoveToFront(elem)
		oldEntry := elem.Value.(*entry)
		c.curBytes -= oldEntry.item.Size
		oldEntry.item = item
		c.curBytes += item.Size
		c.evict()
		return
	}

	ent := &entry{key: key, item: item}
	elem := c.evictList.PushFront(ent)
	c.items[key] = elem
	c.curBytes += item.Size
	c.evict()
}

func (c *LRUCache) evict() {
	for c.maxBytes > 0 && c.curBytes > c.maxBytes && c.evictList.Len() > 0 {
		back := c.evictList.Back()
		if back == nil {
			break
		}
		c.evictList.Remove(back)
		ent := back.Value.(*entry)
		delete(c.items, ent.key)
		c.curBytes -= ent.item.Size
	}
}

func (c *LRUCache) Clear() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.items = make(map[string]*list.Element)
	c.evictList.Init()
	c.curBytes = 0
}

func (c *LRUCache) Stats() (int, int64) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return len(c.items), c.curBytes
}
