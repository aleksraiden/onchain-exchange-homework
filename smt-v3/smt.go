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
	// Количество шардов для кэша (2^8 = 256).
	// Используем первый байт ключа для маршрутизации.
	ShardCount = 256
)

// Типы узлов
const (
	NodeTypeInternal = 0
	NodeTypeLeaf     = 1
)

var (
	EmptyHash = make([]byte, HashSize)

	hasherPool = sync.Pool{
		New: func() interface{} {
			return blake3.New()
		},
	}
)

func toArray(b []byte) [32]byte {
	var a [32]byte
	copy(a[:], b)
	return a
}

// --- Store ---

type Store interface {
	Get(key []byte) ([]byte, error)
	Set(key, value []byte) error
	Delete(key []byte) error
}

type MemoryStore struct {
	sync.RWMutex
	data map[[32]byte][]byte
}

func NewMemoryStore() *MemoryStore {
	return &MemoryStore{data: make(map[[32]byte][]byte)}
}

func (m *MemoryStore) Get(k []byte) ([]byte, error) {
	m.RLock()
	defer m.RUnlock()
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

// --- Sharded LRU Cache ---

type lruNode struct {
	key   [32]byte
	value []byte
	prev  *lruNode
	next  *lruNode
}

var lruNodePool = sync.Pool{
	New: func() interface{} {
		return &lruNode{}
	},
}

// cacheShard - это отдельный независимый LRU кэш со своим мьютексом
type cacheShard struct {
	capacity int
	items    map[[32]byte]*lruNode
	head     *lruNode
	tail     *lruNode
	lock     sync.Mutex
}

func newCacheShard(capacity int) *cacheShard {
	return &cacheShard{
		capacity: capacity,
		items:    make(map[[32]byte]*lruNode, capacity),
	}
}

// ShardedNodeCache - обертка, содержащая 256 шардов
type ShardedNodeCache struct {
	shards [ShardCount]*cacheShard
}

func NewShardedNodeCache(totalCapacity int) *ShardedNodeCache {
	sc := &ShardedNodeCache{}
	// Распределяем общую емкость поровну между шардами
	shardCap := totalCapacity / ShardCount
	if shardCap < 1 {
		shardCap = 1
	}
	for i := 0; i < ShardCount; i++ {
		sc.shards[i] = newCacheShard(shardCap)
	}
	return sc
}

// Get выбирает шард по первому байту ключа
func (sc *ShardedNodeCache) Get(key []byte) ([]byte, bool) {
	// key[0] работает как идеальный балансировщик для криптографических хешей
	shard := sc.shards[key[0]]
	
	shard.lock.Lock()
	defer shard.lock.Unlock()

	// Внутри шарда логика старая (toArray делаем тут, чтобы не делать снаружи)
	k := toArray(key)
	if node, ok := shard.items[k]; ok {
		shard.moveToFront(node)
		return node.value, true
	}
	return nil, false
}

func (sc *ShardedNodeCache) Add(key, value []byte) {
	shard := sc.shards[key[0]]
	
	shard.lock.Lock()
	defer shard.lock.Unlock()

	k := toArray(key)

	if node, ok := shard.items[k]; ok {
		node.value = value
		shard.moveToFront(node)
		return
	}

	if len(shard.items) >= shard.capacity {
		shard.removeTail()
	}

	node := lruNodePool.Get().(*lruNode)
	node.key = k
	node.value = value
	node.prev = nil
	node.next = nil

	shard.items[k] = node
	shard.addToFront(node)
}

// Внутренние методы шарда (без блокировок, т.к. вызываются под локом шарда)
func (c *cacheShard) moveToFront(node *lruNode) {
	if c.head == node {
		return
	}
	if node.prev != nil {
		node.prev.next = node.next
	}
	if node.next != nil {
		node.next.prev = node.prev
	}
	if node == c.tail && node.prev != nil {
		c.tail = node.prev
	}
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

func (c *cacheShard) addToFront(node *lruNode) {
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

func (c *cacheShard) removeTail() {
	if c.tail == nil {
		return
	}
	oldTail := c.tail
	delete(c.items, oldTail.key)

	if c.tail.prev != nil {
		c.tail = c.tail.prev
		c.tail.next = nil
	} else {
		c.head = nil
		c.tail = nil
	}

	oldTail.prev = nil
	oldTail.next = nil
	oldTail.value = nil
	lruNodePool.Put(oldTail)
}

// --- SMT Implementation ---

type SMT struct {
	store Store
	cache *ShardedNodeCache // Используем шардированный кэш
	root  []byte
	lock  sync.RWMutex
}

func NewSMT(store Store, cacheSize int) *SMT {
	s := &SMT{
		store: store,
		root:  EmptyHash,
	}
	if cacheSize > 0 {
		s.cache = NewShardedNodeCache(cacheSize)
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

func hash(data ...[]byte) []byte {
	hasher := hasherPool.Get().(*blake3.Hasher)
	defer hasherPool.Put(hasher)
	hasher.Reset()
	for _, d := range data {
		hasher.Write(d)
	}
	return hasher.Sum(nil)
}