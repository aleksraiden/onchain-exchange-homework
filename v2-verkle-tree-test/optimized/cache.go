// optimized/cache.go

package optimized

import (
	"container/list"
	"sync"
)

// LRUCache - кэш для горячих узлов (5000 элементов)
type LRUCache struct {
	capacity int
	cache    map[string]*list.Element
	lruList  *list.List
	mu       sync.RWMutex
	
	// Статистика
	hits   uint64
	misses uint64
}

type cacheEntry struct {
	key  string
	node VerkleNode
}

// NewLRUCache создает новый LRU кэш
func NewLRUCache(capacity int) *LRUCache {
	return &LRUCache{
		capacity: capacity,
		cache:    make(map[string]*list.Element, capacity),
		lruList:  list.New(),
	}
}

// Get получает узел из кэша
func (c *LRUCache) Get(key string) (VerkleNode, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	
	if elem, found := c.cache[key]; found {
		c.lruList.MoveToFront(elem)
		c.hits++
		return elem.Value.(*cacheEntry).node, true
	}
	
	c.misses++
	return nil, false
}

// Put добавляет узел в кэш
func (c *LRUCache) Put(key string, node VerkleNode) {
	c.mu.Lock()
	defer c.mu.Unlock()
	
	if elem, found := c.cache[key]; found {
		c.lruList.MoveToFront(elem)
		elem.Value.(*cacheEntry).node = node
		return
	}
	
	if c.lruList.Len() >= c.capacity {
		oldest := c.lruList.Back()
		if oldest != nil {
			c.lruList.Remove(oldest)
			delete(c.cache, oldest.Value.(*cacheEntry).key)
		}
	}
	
	entry := &cacheEntry{key: key, node: node}
	elem := c.lruList.PushFront(entry)
	c.cache[key] = elem
}

// Invalidate удаляет узел из кэша
func (c *LRUCache) Invalidate(key string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	
	if elem, found := c.cache[key]; found {
		c.lruList.Remove(elem)
		delete(c.cache, key)
	}
}

// Stats возвращает статистику
func (c *LRUCache) Stats() (hits, misses uint64, hitRate float64) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	
	total := c.hits + c.misses
	if total > 0 {
		hitRate = float64(c.hits) / float64(total) * 100
	}
	return c.hits, c.misses, hitRate
}
