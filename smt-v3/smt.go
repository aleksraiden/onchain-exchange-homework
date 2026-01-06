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
	ShardCount = 256
	
	// Минимальное кол-во ключей для запуска горутины при коммите
	ParallelThreshold = 2048
)

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
// Определена глобально, чтобы быть доступной и в Commit, и в updateBatchParallel.
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

	// NEW: Буфер для отложенных изменений (Lazy Batching)
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

// Get теперь проверяет pendingUpdates перед тем как лезть в дерево
func (s *SMT) Get(key []byte) ([]byte, error) {
	s.lock.RLock()
	defer s.lock.RUnlock()

	// 1. Сначала ищем в незакоммиченных изменениях
	if val, ok := s.pendingUpdates[toArray(key)]; ok {
		if val == nil {
			return nil, errors.New("key deleted (pending)")
		}
		return val, nil
	}

	// 2. Ищем в дереве
	return s.get(s.root, key, 0)
}

// Set добавляет ключ в буфер (O(1)). Хеширование не происходит.
// Для применения изменений нужно вызвать Commit().
func (s *SMT) Set(key, value []byte) error {
	s.lock.Lock()
	defer s.lock.Unlock()
	s.pendingUpdates[toArray(key)] = value
	return nil
}

// Commit применяет все накопленные изменения, пересчитывает хеши
// и обновляет корень дерева за один проход.
// Commit применяет изменения с использованием параллелизма и партиционирования
func (s *SMT) Commit() ([]byte, error) {
	s.lock.Lock()
	defer s.lock.Unlock()

	if len(s.pendingUpdates) == 0 {
		return s.root, nil
	}

	// Аллоцируем один большой буфер сразу
	batch := make([]kv, 0, len(s.pendingUpdates))
	for k, v := range s.pendingUpdates {
		batch = append(batch, kv{k, v})
	}

	// 2. Запускаем рекурсивное обновление
	newRoot, err := s.updateBatchParallel(s.root, batch, 0)
	if err != nil {
		return nil, err
	}

	s.root = newRoot
	s.pendingUpdates = make(map[[32]byte][]byte)
	
	return s.root, nil
}

// Update (Atomic) - для обратной совместимости.
// Работает как Set + Commit для одного ключа.
func (s *SMT) Update(key, value []byte) ([]byte, error) {
	s.lock.Lock()
	defer s.lock.Unlock()
	
	// Используем updateBatch напрямую для одного ключа, минуя pending map
	keys := [][32]byte{toArray(key)}
	values := [][]byte{value}
	
	newRoot, err := s.updateBatch(s.root, keys, values, 0)
	if err != nil {
		return nil, err
	}
	s.root = newRoot
	return s.root, nil
}

// updateBatch - Рекурсивная функция для пакетного обновления
func (s *SMT) updateBatch(root []byte, keys [][32]byte, values [][]byte, depth int) ([]byte, error) {
	// Базовый случай: пустой батч
	if len(keys) == 0 {
		return root, nil
	}

	// 1. Если пришли в пустой узел
	if bytes.Equal(root, EmptyHash) {
		// Оптимизация: если ключ всего один, создаем Shortcut
		if len(keys) == 1 {
			if values[0] == nil {
				return EmptyHash, nil // Удаление
			}
			return s.storeLeaf(keys[0][:], values[0])
		}
		// Если ключей много, продолжаем делить их (виртуально создаем ветви)
		// Переходим к логике Internal (ниже), считая текущий узел пустым Internal
	} else {
		// Загружаем текущий узел (если он не пустой)
		data, err := s.loadNode(root)
		if err != nil {
			return nil, err
		}

		// 2. Если текущий узел - Лист (Shortcut)
		if data[0] == NodeTypeLeaf {
			storedKey := toArray(data[1 : 1+KeySize])
			storedVal := data[1+KeySize:]

			// Проверяем, есть ли storedKey в нашем батче обновлений
			inBatch := false
			for _, k := range keys {
				if k == storedKey {
					inBatch = true
					break
				}
			}

			// Если storedKey НЕТ в батче, мы должны добавить его к текущей обработке,
			// чтобы он либо сохранился где-то внизу, либо сдвинулся.
			// Если он ЕСТЬ в батче, то новое значение из батча его перезапишет.
			if !inBatch {
				// Добавляем старый лист в набор ключей для распределения
				// Важно сохранить порядок сортировки, но это сложно внутри рекурсии.
				// Проще всего: просто распределить его в left/right бакеты ниже.
				// Мы сделаем это вручную при разделении.
				
				// Хак: добавляем его в keys/values и позволим логике разделения (ниже)
				// отправить его в нужную ветку.
				keys = append(keys, storedKey)
				values = append(values, storedVal)
			}
			// Теперь считаем этот узел "виртуально" пустым (Internal) и распределяем все ключи
		}
	}

	// 3. Логика Internal (или разбитого Leaf/Empty)
	// Нам нужно разделить keys и values на левые и правые
	
	// Оптимизация: зная что keys отсортированы, мы могли бы найти точку раздела бинарным поиском,
	// но так как сортировка идет по байтам, а мы смотрим биты, порядок может не совпадать на глубине.
	// Поэтому просто итерируем.
	
	var lKeys, rKeys [][32]byte
	var lVals, rVals [][]byte

	// Аллокацию можно оптимизировать, но пока делаем просто
	lKeys = make([][32]byte, 0, len(keys)/2)
	lVals = make([][]byte, 0, len(keys)/2)
	rKeys = make([][32]byte, 0, len(keys)/2)
	rVals = make([][]byte, 0, len(keys)/2)

	for i, k := range keys {
		if bitIsSet(k[:], depth) {
			rKeys = append(rKeys, k)
			rVals = append(rVals, values[i])
		} else {
			lKeys = append(lKeys, k)
			lVals = append(lVals, values[i])
		}
	}
	
	// Получаем текущих детей (если узел существовал и был Internal)
	var leftHash, rightHash []byte
	if !bytes.Equal(root, EmptyHash) {
		data, _ := s.loadNode(root) // Ошибку проверили выше
		if data[0] == NodeTypeInternal {
			leftHash = data[1 : 1+HashSize]
			rightHash = data[1+HashSize : 1+HashSize*2]
		}
	}
	if leftHash == nil { leftHash = EmptyHash }
	if rightHash == nil { rightHash = EmptyHash }

	// Рекурсия
	newLeft, err := s.updateBatch(leftHash, lKeys, lVals, depth+1)
	if err != nil { return nil, err }
	
	newRight, err := s.updateBatch(rightHash, rKeys, rVals, depth+1)
	if err != nil { return nil, err }

	// 4. Схлопывание (Collapse Optimization)
	if bytes.Equal(newLeft, EmptyHash) && !bytes.Equal(newRight, EmptyHash) {
		rightNode, err := s.loadNode(newRight)
		if err == nil && rightNode[0] == NodeTypeLeaf {
			return newRight, nil // Move up
		}
	} else if bytes.Equal(newRight, EmptyHash) && !bytes.Equal(newLeft, EmptyHash) {
		leftNode, err := s.loadNode(newLeft)
		if err == nil && leftNode[0] == NodeTypeLeaf {
			return newLeft, nil // Move up
		}
	} else if bytes.Equal(newLeft, EmptyHash) && bytes.Equal(newRight, EmptyHash) {
		return EmptyHash, nil
	}

	return s.storeInternal(newLeft, newRight)
}

// updateBatchParallel - Параллельная версия с In-Place Partitioning
func (s *SMT) updateBatchParallel(root []byte, batch []kv, depth int) ([]byte, error) {
	// Базовый случай: пусто
	if len(batch) == 0 {
		return root, nil
	}

	// 1. Обработка пустых узлов и Shortcuts (как раньше)
	if bytes.Equal(root, EmptyHash) {
		if len(batch) == 1 {
			if batch[0].v == nil {
				return EmptyHash, nil
			}
			return s.storeLeaf(batch[0].k[:], batch[0].v)
		}
	} else {
		data, err := s.loadNode(root)
		if err != nil { return nil, err }

		if data[0] == NodeTypeLeaf {
			storedKey := toArray(data[1 : 1+KeySize])
			storedVal := data[1+KeySize:]

			// Проверяем наличие в батче линейным поиском (т.к. батч тут обычно маленький)
			// Если батч огромный, партиционирование уже развело ключи, так что коллизий мало
			found := false
			for i := range batch {
				if batch[i].k == storedKey {
					found = true
					break
				}
			}

			if !found {
				// Добавляем текущий лист в батч для перераспределения
				batch = append(batch, kv{storedKey, storedVal})
			}
		}
	}

	// 2. IN-PLACE PARTITIONING (Вместо аллокации lKeys/rKeys)
	// Мы переставляем элементы в slice так, чтобы слева были биты 0, справа 1.
	// Возвращаем индекс разделителя.
	splitIdx := partition(batch, depth)

	// Разделяем слайс (без копирования, просто view)
	leftBatch := batch[:splitIdx]
	rightBatch := batch[splitIdx:]

	var leftHash, rightHash []byte
	if !bytes.Equal(root, EmptyHash) {
		data, _ := s.loadNode(root)
		if data[0] == NodeTypeInternal {
			leftHash = data[1 : 1+HashSize]
			rightHash = data[1+HashSize : 1+HashSize*2]
		}
	}
	if leftHash == nil { leftHash = EmptyHash }
	if rightHash == nil { rightHash = EmptyHash }

	var newLeft, newRight []byte
	var errL, errR error

	// 3. PARALLEL EXECUTION
	// Если работы много, запускаем в параллель
	if len(batch) > ParallelThreshold {
		var wg sync.WaitGroup
		wg.Add(2)

		go func() {
			defer wg.Done()
			newLeft, errL = s.updateBatchParallel(leftHash, leftBatch, depth+1)
		}()
		
		go func() {
			defer wg.Done()
			newRight, errR = s.updateBatchParallel(rightHash, rightBatch, depth+1)
		}()

		wg.Wait()
	} else {
		// Иначе последовательно (чтобы не спамить горутинами на мелких ветках)
		newLeft, errL = s.updateBatchParallel(leftHash, leftBatch, depth+1)
		if errL == nil {
			newRight, errR = s.updateBatchParallel(rightHash, rightBatch, depth+1)
		}
	}

	if errL != nil { return nil, errL }
	if errR != nil { return nil, errR }

	// 4. Схлопывание (Collapse)
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

// partition переставляет элементы: бит 0 -> влево, бит 1 -> вправо.
// Возвращает индекс первого элемента с битом 1 (или len(batch), если их нет).
// Работает за O(N), 0 allocs.
func partition(batch []kv, depth int) int {
	l, r := 0, len(batch)-1
	
	for l <= r {
		// Проверяем бит левого элемента
		if !bitIsSet(batch[l].k[:], depth) {
			l++
			continue
		}
		
		// Проверяем бит правого элемента
		if bitIsSet(batch[r].k[:], depth) {
			r--
			continue
		}
		
		// Если слева 1, а справа 0 -> меняем местами
		batch[l], batch[r] = batch[r], batch[l]
		l++
		r--
	}
	
	// l теперь указывает на начало правой части (где бит 1)
	return l
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