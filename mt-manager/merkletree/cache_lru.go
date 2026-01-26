package merkletree

import (
	"sync"
)

// lruCache простой LRU кеш
type lruCache[T Hashable] struct {
	cache    map[uint64]*lruNode[T] // Используем lruNode
	head     *lruNode[T]
	tail     *lruNode[T]
	capacity int
	mu       sync.Mutex
}

// lruNode узел двусвязного списка
type lruNode[T Hashable] struct {
	id    uint64
	value T
	prev  *lruNode[T]
	next  *lruNode[T]
}

// newLRUCache создает новый LRU кеш
func newLRUCache[T Hashable](capacity int) *lruCache[T] {
	return &lruCache[T]{
		cache:    make(map[uint64]*lruNode[T], capacity),
		capacity: capacity,
	}
}

// put добавляет элемент в кеш
func (c *lruCache[T]) put(id uint64, item T) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if node, exists := c.cache[id]; exists {
		node.value = item
		c.moveToHead(node)
		return
	}

	node := &lruNode[T]{
		id:    id,
		value: item,
	}

	c.cache[id] = node
	c.addToHead(node)

	if len(c.cache) > c.capacity {
		c.removeTail()
	}
}

// get получает элемент из кеша
func (c *lruCache[T]) get(id uint64) (T, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if node, exists := c.cache[id]; exists {
		c.moveToHead(node)
		return node.value, true
	}

	var zero T
	return zero, false
}

// addToHead добавляет узел в начало списка
func (c *lruCache[T]) addToHead(node *lruNode[T]) {
	node.next = c.head
	node.prev = nil

	if c.head != nil {
		c.head.prev = node
	}

	c.head = node

	if c.tail == nil {
		c.tail = node
	}
}

// moveToHead перемещает узел в начало списка
func (c *lruCache[T]) moveToHead(node *lruNode[T]) {
	if node == c.head {
		return
	}

	c.removeNode(node)
	c.addToHead(node)
}

// removeNode удаляет узел из списка
func (c *lruCache[T]) removeNode(node *lruNode[T]) {
	if node.prev != nil {
		node.prev.next = node.next
	} else {
		c.head = node.next
	}

	if node.next != nil {
		node.next.prev = node.prev
	} else {
		c.tail = node.prev
	}
}

// delete удаляет элемент из кеша
func (c *lruCache[T]) delete(id uint64) {
	c.mu.Lock()
	defer c.mu.Unlock()
	
	node, exists := c.cache[id]
	if !exists {
		return
	}
	
	// Удаляем из map
	delete(c.cache, id)
	
	// Удаляем из списка
	c.removeNode(node)
}

// removeTail удаляет последний элемент
func (c *lruCache[T]) removeTail() {
	if c.tail == nil {
		return
	}

	delete(c.cache, c.tail.id)
	c.removeNode(c.tail)
}

// clear очищает кеш
func (c *lruCache[T]) clear() {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.cache = make(map[uint64]*lruNode[T], c.capacity) // lruNode везде
	c.head = nil
	c.tail = nil
}

// Resize изменяет размер кеша
func (c *lruCache[T]) Resize(newCapacity int) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if newCapacity < c.capacity {
		// Уменьшаем - удаляем лишние элементы
		for len(c.cache) > newCapacity {
			c.removeTail()
		}
	}

	c.capacity = newCapacity
}

// size возвращает текущий размер кеша
func (c *lruCache[T]) size() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.cache)
}
