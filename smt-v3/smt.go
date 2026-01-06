package main

import (
	"bytes"
	"errors"
	"sync"

	"github.com/zeebo/blake3"
)

const (
	HashSize = 32
	KeySize  = 32
)

// Типы узлов для сериализации
const (
	NodeTypeInternal = 0
	NodeTypeLeaf     = 1
)

var (
	// EmptyHash - хеш пустого узла (нули)
	EmptyHash = make([]byte, HashSize)
	
	// ОПТИМИЗАЦИЯ 1: Пул хешеров для переиспользования
	hasherPool = sync.Pool{
		New: func() interface{} {
			return blake3.New()
		},
	}
)

// Store - интерфейс для хранения узлов
type Store interface {
	Get(key []byte) ([]byte, error)
	Set(key, value []byte) error
	Delete(key []byte) error
}

// --- Helper для конвертации ---
// Превращает срез в массив без аллокации (копирование на стеке)
func toArray(b []byte) [32]byte {
	var a [32]byte
	copy(a[:], b)
	return a
}

// MemoryStore - простая реализация хранилища в памяти
type MemoryStore struct {
	sync.RWMutex
	data map[[32]byte][]byte // Изменено с string на [32]byte
}

func NewMemoryStore() *MemoryStore {
	return &MemoryStore{data: make(map[[32]byte][]byte)}
}

func (m *MemoryStore) Get(k []byte) ([]byte, error) {
	m.RLock()
	defer m.RUnlock()
	// toArray не вызывает аллокации в куче
	if v, ok := m.data[toArray(k)]; ok {
		return v, nil
	}
	return nil, errors.New("not found")
}

func (m *MemoryStore) Set(k, v []byte) error {
	m.Lock()
	defer m.Unlock()
	m.data[toArray(k)] = v
	return nil
}

func (m *MemoryStore) Delete(k []byte) error {
	m.Lock()
	defer m.Unlock()
	delete(m.data, toArray(k))
	return nil
}

// --- LRU Cache Optimized (Custom List + Pool) ---

// lruNode - узел двусвязного списка.
// Мы не используем container/list, чтобы иметь строгую типизацию и переиспользовать узлы.
type lruNode struct {
	key   [32]byte
	value []byte
	prev  *lruNode
	next  *lruNode
}

// Пул для узлов списка кэша, чтобы снизить нагрузку на GC
var lruNodePool = sync.Pool{
	New: func() interface{} {
		return &lruNode{}
	},
}

type NodeCache struct {
	capacity int
	items    map[[32]byte]*lruNode
	head     *lruNode // MRU (Most Recently Used)
	tail     *lruNode // LRU (Least Recently Used)
	lock     sync.Mutex
}

func NewNodeCache(capacity int) *NodeCache {
	return &NodeCache{
		capacity: capacity,
		items:    make(map[[32]byte]*lruNode, capacity),
	}
}

func (c *NodeCache) Get(key []byte) ([]byte, bool) {
	c.lock.Lock()
	defer c.lock.Unlock()

	k := toArray(key)
	if node, ok := c.items[k]; ok {
		c.moveToFront(node)
		return node.value, true
	}
	return nil, false
}

func (c *NodeCache) Add(key, value []byte) {
	c.lock.Lock()
	defer c.lock.Unlock()

	k := toArray(key)

	// Если элемент уже есть, обновляем значение и двигаем вперед
	if node, ok := c.items[k]; ok {
		node.value = value
		c.moveToFront(node)
		return
	}

	// Если кэш полон, удаляем хвост (LRU)
	if len(c.items) >= c.capacity {
		c.removeTail()
	}

	// Создаем новый узел (берем из пула)
	node := lruNodePool.Get().(*lruNode)
	node.key = k
	node.value = value
	node.prev = nil
	node.next = nil

	c.items[k] = node
	c.addToFront(node)
}

// --- Внутренние методы управления списком ---

func (c *NodeCache) moveToFront(node *lruNode) {
	if c.head == node {
		return
	}

	// Удаляем из текущей позиции
	if node.prev != nil {
		node.prev.next = node.next
	}
	if node.next != nil {
		node.next.prev = node.prev
	}
	if node == c.tail && node.prev != nil {
		c.tail = node.prev
	}

	// Вставляем в голову
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

func (c *NodeCache) addToFront(node *lruNode) {
	if c.head == nil {
		c.head = node
		c.tail = node
		return
	}
	node.next = c.head
	node.prev = nil
	c.head.prev = node
	c.head = node
}

func (c *NodeCache) removeTail() {
	if c.tail == nil {
		return
	}
	
	oldTail := c.tail
	
	// Удаляем из map
	delete(c.items, oldTail.key)

	// Обновляем список
	if c.tail.prev != nil {
		c.tail = c.tail.prev
		c.tail.next = nil
	} else {
		// Список стал пустым
		c.head = nil
		c.tail = nil
	}

	// Очищаем и возвращаем узел в пул
	oldTail.prev = nil
	oldTail.next = nil
	oldTail.value = nil // Важно занулить ссылки, чтобы GC мог собрать данные (value)
	lruNodePool.Put(oldTail)
} 

// --- SMT Implementation ---

type SMT struct {
	store Store
	cache *NodeCache
	root  []byte
	lock  sync.RWMutex
}

// NewSMT создает новое дерево.
// cacheSize <= 0 отключает кэширование.
// cacheSize > 0 включает LRU кэш указанного размера.
func NewSMT(store Store, cacheSize int) *SMT {
	s := &SMT{
		store: store,
		root:  EmptyHash,
	}
	if cacheSize > 0 {
		s.cache = NewNodeCache(cacheSize)
	}
	return s
}

func (s *SMT) Root() []byte {
	s.lock.RLock()
	defer s.lock.RUnlock()
	return s.root
}

func (s *SMT) Get(key []byte) ([]byte, error) {
	s.lock.RLock()
	defer s.lock.RUnlock()
	return s.get(s.root, key, 0)
}

func (s *SMT) get(root []byte, key []byte, depth int) ([]byte, error) {
	if bytes.Equal(root, EmptyHash) {
		return nil, errors.New("key not found")
	}

	data, err := s.loadNode(root)
	if err != nil {
		return nil, err
	}

	if data[0] == NodeTypeLeaf {
		storedKey := data[1 : 1+KeySize]
		if bytes.Equal(storedKey, key) {
			return data[1+KeySize:], nil
		}
		return nil, errors.New("key not found (shortcut mismatch)")
	}

	leftHash := data[1 : 1+HashSize]
	rightHash := data[1+HashSize : 1+HashSize*2]

	if bitIsSet(key, depth) {
		return s.get(rightHash, key, depth+1)
	}
	return s.get(leftHash, key, depth+1)
}

func (s *SMT) Update(key, value []byte) ([]byte, error) {
	s.lock.Lock()
	defer s.lock.Unlock()

	newRoot, err := s.update(s.root, key, value, 0)
	if err != nil {
		return nil, err
	}
	s.root = newRoot
	return s.root, nil
}

func (s *SMT) update(root []byte, key, value []byte, depth int) ([]byte, error) {
	if bytes.Equal(root, EmptyHash) {
		if value == nil {
			return EmptyHash, nil
		}
		return s.storeLeaf(key, value)
	}

	data, err := s.loadNode(root)
	if err != nil {
		return nil, err
	}

	if data[0] == NodeTypeLeaf {
		storedKey := data[1 : 1+KeySize]
		
		if bytes.Equal(storedKey, key) {
			if value == nil {
				return EmptyHash, nil
			}
			return s.storeLeaf(key, value)
		}

		storedValue := data[1+KeySize:]
		if value == nil {
			return root, nil
		}

		return s.splitAndInsert(storedKey, storedValue, key, value, depth)
	}

	// Internal
	leftHash := data[1 : 1+HashSize]
	rightHash := data[1+HashSize : 1+HashSize*2]

	var newLeft, newRight []byte

	if bitIsSet(key, depth) {
		newRight, err = s.update(rightHash, key, value, depth+1)
		if err != nil { return nil, err }
		newLeft = leftHash
	} else {
		newLeft, err = s.update(leftHash, key, value, depth+1)
		if err != nil { return nil, err }
		newRight = rightHash
	}

	// Optimization: Move Up Shortcut
	if bytes.Equal(newLeft, EmptyHash) && !bytes.Equal(newRight, EmptyHash) {
		rightNode, err := s.loadNode(newRight)
		if err == nil && rightNode[0] == NodeTypeLeaf {
			return newRight, nil
		}
	} else if bytes.Equal(newRight, EmptyHash) && !bytes.Equal(newLeft, EmptyHash) {
		leftNode, err := s.loadNode(newLeft)
		if err == nil && leftNode[0] == NodeTypeLeaf {
			return newLeft, nil
		}
	} else if bytes.Equal(newLeft, EmptyHash) && bytes.Equal(newRight, EmptyHash) {
		return EmptyHash, nil
	}

	return s.storeInternal(newLeft, newRight)
}

func (s *SMT) splitAndInsert(key1, val1, key2, val2 []byte, depth int) ([]byte, error) {
	if depth >= KeySize*8 {
		return s.storeLeaf(key2, val2)
	}

	bit1 := bitIsSet(key1, depth)
	bit2 := bitIsSet(key2, depth)

	var left, right []byte

	if bit1 == bit2 {
		subNode, err := s.splitAndInsert(key1, val1, key2, val2, depth+1)
		if err != nil { return nil, err }
		
		if bit1 {
			left, right = EmptyHash, subNode
		} else {
			left, right = subNode, EmptyHash
		}
	} else {
		node1, err := s.storeLeaf(key1, val1)
		if err != nil { return nil, err }
		node2, err := s.storeLeaf(key2, val2)
		if err != nil { return nil, err }

		if bit1 {
			left, right = node2, node1
		} else {
			left, right = node1, node2
		}
	}

	return s.storeInternal(left, right)
}

// Helpers

func (s *SMT) loadNode(hash []byte) ([]byte, error) {
	if s.cache != nil {
		if val, found := s.cache.Get(hash); found {
			return val, nil
		}
	}
	
	val, err := s.store.Get(hash)
	if err != nil {
		return nil, err
	}

	if s.cache != nil {
		s.cache.Add(hash, val)
	}
	return val, nil
}

func (s *SMT) storeLeaf(key, value []byte) ([]byte, error) {
	data := make([]byte, 1+KeySize+len(value))
	data[0] = NodeTypeLeaf
	copy(data[1:], key)
	copy(data[1+KeySize:], value)
	
	h := hash(data)
	
	if err := s.store.Set(h, data); err != nil {
		return nil, err
	}
	if s.cache != nil {
		s.cache.Add(h, data)
	}
	
	return h, nil
}

func (s *SMT) storeInternal(left, right []byte) ([]byte, error) {
	data := make([]byte, 1+HashSize*2)
	data[0] = NodeTypeInternal
	copy(data[1:], left)
	copy(data[1+HashSize:], right)

	h := hash(data)
	
	if err := s.store.Set(h, data); err != nil {
		return nil, err
	}
	if s.cache != nil {
		s.cache.Add(h, data)
	}

	return h, nil
}

func bitIsSet(key []byte, idx int) bool {
	byteIdx := idx / 8
	bitIdx := 7 - (idx % 8)
	return (key[byteIdx]>>bitIdx)&1 == 1
}

// hash - ОПТИМИЗИРОВАННАЯ ВЕРСИЯ С sync.Pool
func hash(data ...[]byte) []byte {
	// 1. Берем хешер из пула (без аллокации структуры)
	hasher := hasherPool.Get().(*blake3.Hasher)
	// 2. Обязательно возвращаем обратно
	defer hasherPool.Put(hasher)
	
	// 3. Сбрасываем состояние хешера (обязательно при переиспользовании)
	hasher.Reset()
	
	for _, d := range data {
		hasher.Write(d)
	}
	
	// 4. Возвращаем хеш
	return hasher.Sum(nil)
}