// node_cache.go

package verkletree

import (
	"container/list"
	"sync"
)

// NodeCache - LRU кэш для горячих узлов
type NodeCache struct {
	// capacity - максимальное количество элементов в кэше
	capacity int
	
	// cache - мапа для O(1) доступа
	cache map[string]*list.Element
	
	// lruList - двусвязный список для LRU политики
	lruList *list.List
	
	// mu - мьютекс для потокобезопасности
	mu sync.RWMutex
	
	// Статистика
	hits   uint64
	misses uint64
}

// cacheEntry - элемент кэша
type cacheEntry struct {
	key  string
	node VerkleNode
}

// NewNodeCache создает новый LRU кэш
// capacity - количество узлов (рекомендуется 1000-10000)
func NewNodeCache(capacity int) *NodeCache {
	if capacity <= 0 {
		capacity = 1000 // По умолчанию
	}
	
	return &NodeCache{
		capacity: capacity,
		cache:    make(map[string]*list.Element, capacity),
		lruList:  list.New(),
		hits:     0,
		misses:   0,
	}
}

// Get получает узел из кэша
// Возвращает (node, true) если найден, (nil, false) если нет
func (nc *NodeCache) Get(key string) (VerkleNode, bool) {
	nc.mu.Lock()
	defer nc.mu.Unlock()
	
	if elem, found := nc.cache[key]; found {
		// Перемещаем в начало списка (most recently used)
		nc.lruList.MoveToFront(elem)
		nc.hits++
		
		entry := elem.Value.(*cacheEntry)
		return entry.node, true
	}
	
	nc.misses++
	return nil, false
}

// Put добавляет узел в кэш
func (nc *NodeCache) Put(key string, node VerkleNode) {
	nc.mu.Lock()
	defer nc.mu.Unlock()
	
	// Если уже есть - обновляем и перемещаем в начало
	if elem, found := nc.cache[key]; found {
		nc.lruList.MoveToFront(elem)
		entry := elem.Value.(*cacheEntry)
		entry.node = node
		return
	}
	
	// Если кэш полон - удаляем самый старый (LRU)
	if nc.lruList.Len() >= nc.capacity {
		oldest := nc.lruList.Back()
		if oldest != nil {
			nc.lruList.Remove(oldest)
			oldEntry := oldest.Value.(*cacheEntry)
			delete(nc.cache, oldEntry.key)
		}
	}
	
	// Добавляем новый элемент в начало
	entry := &cacheEntry{
		key:  key,
		node: node,
	}
	elem := nc.lruList.PushFront(entry)
	nc.cache[key] = elem
}

// Invalidate удаляет узел из кэша (при обновлении)
func (nc *NodeCache) Invalidate(key string) {
	nc.mu.Lock()
	defer nc.mu.Unlock()
	
	if elem, found := nc.cache[key]; found {
		nc.lruList.Remove(elem)
		delete(nc.cache, key)
	}
}

// Clear очищает весь кэш
func (nc *NodeCache) Clear() {
	nc.mu.Lock()
	defer nc.mu.Unlock()
	
	nc.cache = make(map[string]*list.Element, nc.capacity)
	nc.lruList = list.New()
	nc.hits = 0
	nc.misses = 0
}

// Stats возвращает статистику кэша
func (nc *NodeCache) Stats() map[string]interface{} {
	nc.mu.RLock()
	defer nc.mu.RUnlock()
	
	total := nc.hits + nc.misses
	hitRate := 0.0
	if total > 0 {
		hitRate = float64(nc.hits) / float64(total) * 100
	}
	
	return map[string]interface{}{
		"capacity":  nc.capacity,
		"size":      nc.lruList.Len(),
		"hits":      nc.hits,
		"misses":    nc.misses,
		"hit_rate":  hitRate,
		"occupancy": float64(nc.lruList.Len()) / float64(nc.capacity) * 100,
	}
}

// GetHitRate возвращает процент попаданий в кэш
func (nc *NodeCache) GetHitRate() float64 {
	nc.mu.RLock()
	defer nc.mu.RUnlock()
	
	total := nc.hits + nc.misses
	if total == 0 {
		return 0
	}
	return float64(nc.hits) / float64(total) * 100
}
