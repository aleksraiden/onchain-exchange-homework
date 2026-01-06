package main

import (
	"bytes"
	"encoding/binary"
	"errors"
	"math/bits"
	"sort"
	"sync"

	"github.com/zeebo/blake3"
)

const (
	HashSize   = 32
	KeySize    = 32
	ShardCount = 256
	// Минимальное кол-во ключей для запуска горутины при коммите
	ParallelThreshold = 2048
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

// kv - вспомогательная структура для передачи пары ключ-значение.
type kv struct {
	k [32]byte
	v []byte
}

// --- Helper ---

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

type ShardedNodeCache struct {
	shards [ShardCount]*cacheShard
}

func NewShardedNodeCache(totalCapacity int) *ShardedNodeCache {
	sc := &ShardedNodeCache{}
	shardCap := totalCapacity / ShardCount
	if shardCap < 1 {
		shardCap = 1
	}
	for i := 0; i < ShardCount; i++ {
		sc.shards[i] = newCacheShard(shardCap)
	}
	return sc
}

func (sc *ShardedNodeCache) Get(key []byte) ([]byte, bool) {
	shard := sc.shards[key[0]]
	shard.lock.Lock()
	defer shard.lock.Unlock()

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
	cache *ShardedNodeCache
	root  []byte
	lock  sync.RWMutex

	pendingUpdates map[[32]byte][]byte
}

func NewSMT(store Store, cacheSize int) *SMT {
	s := &SMT{
		store:          store,
		root:           EmptyHash,
		pendingUpdates: make(map[[32]byte][]byte),
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

	if val, ok := s.pendingUpdates[toArray(key)]; ok {
		if val == nil {
			return nil, errors.New("key deleted (pending)")
		}
		return val, nil
	}

	return s.get(s.root, toArray(key), 0)
}

func (s *SMT) Set(key, value []byte) error {
	s.lock.Lock()
	defer s.lock.Unlock()
	s.pendingUpdates[toArray(key)] = value
	return nil
}

func (s *SMT) Commit() ([]byte, error) {
	s.lock.Lock()
	defer s.lock.Unlock()

	if len(s.pendingUpdates) == 0 {
		return s.root, nil
	}

	batch := make([]kv, 0, len(s.pendingUpdates))
	for k, v := range s.pendingUpdates {
		batch = append(batch, kv{k, v})
	}

	// Сортировка важна: она группирует ключи по префиксам байтов,
	// что позволяет updateBatchParallel эффективно нарезать диапазоны.
	sort.Slice(batch, func(i, j int) bool {
		return bytes.Compare(batch[i].k[:], batch[j].k[:]) < 0
	})

	newRoot, err := s.updateBatchParallel(s.root, batch, 0)
	if err != nil {
		return nil, err
	}

	s.root = newRoot
	s.pendingUpdates = make(map[[32]byte][]byte)
	
	return s.root, nil
}

func (s *SMT) Update(key, value []byte) ([]byte, error) {
	s.lock.Lock()
	defer s.lock.Unlock()
	
	batch := []kv{{toArray(key), value}}
	
	newRoot, err := s.updateBatchParallel(s.root, batch, 0)
	if err != nil {
		return nil, err
	}
	s.root = newRoot
	return s.root, nil
}

// LoadRoot устанавливает корень дерева (для загрузки из БД)
func (s *SMT) LoadRoot(root []byte) {
	s.lock.Lock()
	defer s.lock.Unlock()
	// Копируем, чтобы избежать внешних изменений
	s.root = make([]byte, len(root))
	copy(s.root, root)
}

// updateBatchParallel для Arity-256
// depth увеличивается на 1 при каждом шаге (всего макс 32 шага)
// updateBatchParallel для Arity-256
// depth увеличивается на 1 при каждом шаге (всего макс 32 шага)
func (s *SMT) updateBatchParallel(root []byte, batch []kv, depth int) ([]byte, error) {
	if len(batch) == 0 {
		return root, nil
	}

	// 1. Guard: Reached max depth (Leaf creation)
	// Если мы здесь, значит мы прошли весь путь (32 байта).
	// Если ключей несколько, берем последний (Overwrite).
	if depth >= KeySize {
		last := batch[len(batch)-1]
		if last.v == nil {
			return EmptyHash, nil
		}
		return s.storeLeaf(last.k[:], last.v)
	}

	// 2. Обработка пустого узла (Creation)
	if bytes.Equal(root, EmptyHash) {
		// Оптимизация: 1 ключ -> создаем Leaf
		if len(batch) == 1 {
			if batch[0].v == nil {
				return EmptyHash, nil
			}
			return s.storeLeaf(batch[0].k[:], batch[0].v)
		}
		// Если ключей много, пропускаем шаг загрузки узла и сразу идем к распределению (Step 4)
		// Мы как бы создаем новый Internal узел с нуля.
		goto Distribution
	}

	// 3. Обработка существующего узла (Update)
	{
		data, err := s.loadNode(root)
		if err != nil { return nil, err }

		if data[0] == NodeTypeLeaf {
			storedKey := toArray(data[1 : 1+KeySize])
			storedVal := data[1+KeySize:]

			// Проверяем, есть ли этот ключ уже в батче (перезапись)
			found := false
			for i := range batch {
				if batch[i].k == storedKey {
					found = true
					break
				}
			}
			// Если ключа нет в батче (это "сосед" или коллизия), добавляем его для перераспределения
			if !found {
				batch = append(batch, kv{storedKey, storedVal})
				// Обязательно пересортируем, так как порядок важен для Distribution
				sort.Slice(batch, func(i, j int) bool {
					return bytes.Compare(batch[i].k[:], batch[j].k[:]) < 0
				})
			}
		}
	}

	Distribution:
	// 4. Загружаем детей (если это был Internal) или создаем пустых
	children := [256][]byte{}
	for i := 0; i < 256; i++ { children[i] = EmptyHash }

	if !bytes.Equal(root, EmptyHash) {
		data, _ := s.loadNode(root)
		if data[0] == NodeTypeInternal {
			// [Type 1] [Bitmap 32] [Hash 32]...
			var bitmap [4]uint64
			for i := 0; i < 4; i++ {
				bitmap[i] = binary.LittleEndian.Uint64(data[1+i*8:])
			}
			offset := 33
			for i := 0; i < 256; i++ {
				segment := i / 64
				bit := i % 64
				if (bitmap[segment]>>bit)&1 == 1 {
					children[i] = data[offset : offset+HashSize]
					offset += HashSize
				}
			}
		}
	}

	// 5. Распределение по 256 ведрам
	var wg sync.WaitGroup
	useParallel := len(batch) > ParallelThreshold
	errChan := make(chan error, 256)

	start := 0
	for start < len(batch) {
		childIdx := int(batch[start].k[depth]) // Теперь здесь безопасно благодаря проверке в начале
		end := start + 1
		for end < len(batch) && int(batch[end].k[depth]) == childIdx {
			end++
		}
		
		subBatch := batch[start:end]
		childRoot := children[childIdx]
		
		if useParallel {
			wg.Add(1)
			go func(idx int, r []byte, b []kv) {
				defer wg.Done()
				res, err := s.updateBatchParallel(r, b, depth+1)
				if err != nil {
					errChan <- err
				} else {
					children[idx] = res
				}
			}(childIdx, childRoot, subBatch)
		} else {
			res, err := s.updateBatchParallel(childRoot, subBatch, depth+1)
			if err != nil { return nil, err }
			children[childIdx] = res
		}
		start = end
	}

	if useParallel {
		wg.Wait()
		close(errChan)
		if len(errChan) > 0 {
			return nil, <-errChan
		}
	}

	// 6. Схлопывание (Collapse) и Сохранение
	activeCount := 0
	lastChildIdx := -1
	
	for i := 0; i < 256; i++ {
		if !bytes.Equal(children[i], EmptyHash) {
			activeCount++
			lastChildIdx = i
		}
	}

	if activeCount == 0 {
		return EmptyHash, nil
	}
	
	if activeCount == 1 {
		child := children[lastChildIdx]
		data, err := s.loadNode(child)
		if err == nil && data[0] == NodeTypeLeaf {
			return child, nil
		}
	}

	return s.storeInternal(children)
}

func (s *SMT) get(root []byte, key [32]byte, depth int) ([]byte, error) {
	if bytes.Equal(root, EmptyHash) {
		return nil, errors.New("key not found")
	}

	data, err := s.loadNode(root)
	if err != nil {
		return nil, err
	}

	if data[0] == NodeTypeLeaf {
		storedKey := toArray(data[1 : 1+KeySize])
		if storedKey == key {
			return data[1+KeySize:], nil
		}
		return nil, errors.New("key not found (shortcut mismatch)")
	}

	// Internal Node Arity-256
	// [Type 1] [Bitmap 32] [Hash...]
	idx := uint8(key[depth])
	
	// Проверяем бит в битмапе
	segmentIdx := idx / 64
	bitIdx := idx % 64
	
	start := 1 + (int(segmentIdx) * 8)
	segment := binary.LittleEndian.Uint64(data[start : start+8])
	
	if (segment>>bitIdx)&1 == 0 {
		return nil, errors.New("key not found (branch empty)")
	}
	
	// Считаем смещение через POPCNT
	childIdx := 0
	// Полные сегменты до нас
	for i := 0; i < int(segmentIdx); i++ {
		sStart := 1 + (i * 8)
		seg := binary.LittleEndian.Uint64(data[sStart : sStart+8])
		childIdx += bits.OnesCount64(seg)
	}
	// Биты в текущем сегменте
	mask := (uint64(1) << bitIdx) - 1
	childIdx += bits.OnesCount64(segment & mask)
	
	offset := 33 + (childIdx * HashSize)
	childHash := data[offset : offset+HashSize]
	
	return s.get(childHash, key, depth+1)
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

// storeInternal собирает узел Arity-256
func (s *SMT) storeInternal(children [256][]byte) ([]byte, error) {
	var bitmap [4]uint64
	activeChildren := 0
	
	// 1. Формируем битмапу
	for i := 0; i < 256; i++ {
		if !bytes.Equal(children[i], EmptyHash) {
			segment := i / 64
			bit := i % 64
			bitmap[segment] |= (1 << bit)
			activeChildren++
		}
	}
	
	// 2. Аллоцируем буфер
	// Type(1) + Bitmap(32) + Hashes(N*32)
	size := 1 + 32 + (activeChildren * HashSize)
	data := make([]byte, size)
	
	data[0] = NodeTypeInternal
	
	// Записываем битмапу
	for i := 0; i < 4; i++ {
		binary.LittleEndian.PutUint64(data[1+i*8:], bitmap[i])
	}
	
	// Записываем хеши
	offset := 33
	for i := 0; i < 256; i++ {
		segment := i / 64
		bit := i % 64
		if (bitmap[segment]>>bit)&1 == 1 {
			copy(data[offset:], children[i])
			offset += HashSize
		}
	}

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
	// Эта функция больше не нужна для Arity-256, но можно оставить для совместимости тестов, если нужно.
	// В новой логике не используется.
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