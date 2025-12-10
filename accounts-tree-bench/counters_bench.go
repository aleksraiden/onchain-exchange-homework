package main

import (
	"encoding/binary"
	"fmt"
	"runtime"
	"sync"
	"sync/atomic"
	"time"
	"unsafe"

	"github.com/zeebo/blake3"
)

// ================= TYPES =================

type Counters struct {
	GlobalNonce uint64
	BTCUSD_nonce  uint64
	ETHUSD_nonce  uint64
	SOLUSD_nonce  uint64
	TONUSD_nonce  uint64
	DOGEUSD_nonce uint64
	MONUSD_nonce  uint64
	USDTUSD_nonce uint64
	USDCUSD_nonce uint64
	XRPUSD_nonce  uint64
	ADAUSD_nonce  uint64
	AVAXUSD_nonce uint64
	DOTUSD_nonce  uint64
	LINKUSD_nonce uint64
	MATICUSD_nonce uint64
	LTCUSD_nonce  uint64
}

type UserData struct {
	UID      uint64
	Counters Counters
	Key      [8]byte // BigEndian(UID)
}

func (u *UserData) Hash() [32]byte {
	hasher := blake3.New()
	hasher.Write(u.Key[:])

	ptr := unsafe.Pointer(&u.Counters)
	size := unsafe.Sizeof(u.Counters)
	bytes := unsafe.Slice((*byte)(ptr), size)
	hasher.Write(bytes)

	var result [32]byte
	copy(result[:], hasher.Sum(nil))
	return result
}

// Node - узел дерева
type Node struct {
	Hash     [32]byte
	Children map[uint8]*Node
	UserData *UserData
	IsLeaf   bool
}

// SparseMerklePatriciaTree - дерево с Fine-Grained Locking
// Мы убираем глобальный RWMutex и добавляем шардированные локи для верхнего уровня
type SparseMerklePatriciaTree struct {
	Root      *Node
	// Accounts map больше не защищена глобальным локом.
	// Чтобы обеспечить потокобезопасность map без глобального лока, 
	// нам нужно либо использовать sync.Map (медленно), либо шардировать map,
	// либо (лучше всего) хранить данные ТОЛЬКО в листьях дерева.
	// В этой версии мы убираем дублирующую map Accounts, так как данные есть в UserData в листьях.
	// Это упрощает синхронизацию: доступ к данным = доступ к листу дерева.

	Cache     *ShardedLRUCache
	LeafArity int
	MaxDepth  int

	// Sharded Locks for the first level of the tree (Root children)
	// Key[0] determines the shard index (0..255)
	ShardLocks [256]sync.RWMutex
}

// ================= LRU CACHE (Sharded) =================

type LRUCache struct {
	capacity int
	cache    map[uint64]*cacheEntry
	head     *cacheEntry
	tail     *cacheEntry
	mu       sync.Mutex
}

type cacheEntry struct {
	key   uint64
	value *UserData
	prev  *cacheEntry
	next  *cacheEntry
}

func NewLRUCache(capacity int) *LRUCache {
	return &LRUCache{
		capacity: capacity,
		cache:    make(map[uint64]*cacheEntry),
	}
}

func (lru *LRUCache) Get(key uint64) (*UserData, bool) {
	lru.mu.Lock()
	defer lru.mu.Unlock()
	if entry, ok := lru.cache[key]; ok {
		lru.moveToFront(entry)
		return entry.value, true
	}
	return nil, false
}

func (lru *LRUCache) Put(key uint64, value *UserData) {
	lru.mu.Lock()
	defer lru.mu.Unlock()
	if entry, ok := lru.cache[key]; ok {
		entry.value = value
		lru.moveToFront(entry)
		return
	}
	newEntry := &cacheEntry{key: key, value: value}
	lru.cache[key] = newEntry
	lru.addToFront(newEntry)
	if len(lru.cache) > lru.capacity {
		lru.removeTail()
	}
}

func (lru *LRUCache) moveToFront(entry *cacheEntry) {
	if entry == lru.head { return }
	lru.remove(entry)
	lru.addToFront(entry)
}

func (lru *LRUCache) addToFront(entry *cacheEntry) {
	entry.next = lru.head
	entry.prev = nil
	if lru.head != nil { lru.head.prev = entry }
	lru.head = entry
	if lru.tail == nil { lru.tail = entry }
}

func (lru *LRUCache) remove(entry *cacheEntry) {
	if entry.prev != nil { entry.prev.next = entry.next } else { lru.head = entry.next }
	if entry.next != nil { entry.next.prev = entry.prev } else { lru.tail = entry.prev }
}

func (lru *LRUCache) removeTail() {
	if lru.tail == nil { return }
	delete(lru.cache, lru.tail.key)
	lru.remove(lru.tail)
}

type ShardedLRUCache struct {
	shards    []*LRUCache
	shardMask uint64
}

func NewShardedLRUCache(totalCapacity int, power uint) *ShardedLRUCache {
	numShards := 1 << power
	shardCapacity := totalCapacity / numShards
	if shardCapacity < 1 { shardCapacity = 1 }
	shards := make([]*LRUCache, numShards)
	for i := 0; i < numShards; i++ { shards[i] = NewLRUCache(shardCapacity) }
	return &ShardedLRUCache{shards: shards, shardMask: uint64(numShards - 1)}
}

func (s *ShardedLRUCache) Get(key uint64) (*UserData, bool) {
	return s.shards[key&s.shardMask].Get(key)
}

func (s *ShardedLRUCache) Put(key uint64, value *UserData) {
	s.shards[key&s.shardMask].Put(key, value)
}

// ================= TREE IMPLEMENTATION =================

func NewSparseMerklePatriciaTree(leafArity int, cacheSize int) *SparseMerklePatriciaTree {
	if leafArity == 0 { leafArity = 64 }
	maxDepth := 3
	shardedCache := NewShardedLRUCache(cacheSize, 8) // 256 shards

	// Pre-allocate Root children map to avoid race conditions on lazy init
	root := &Node{Children: make(map[uint8]*Node)}

	return &SparseMerklePatriciaTree{
		Root:      root,
		Cache:     shardedCache,
		LeafArity: leafArity,
		MaxDepth:  maxDepth,
	}
}

// Insert - теперь блокирует только нужный шард
func (tree *SparseMerklePatriciaTree) Insert(userData *UserData) {
	// Определяем шард по первому байту ключа (BigEndian UID)
	shardIdx := userData.Key[0]

	tree.ShardLocks[shardIdx].Lock()

	// Вставляем данные. Начинаем сразу с нужного ребенка корня,
	// так как Root сам по себе не меняется (только его map, который мы инициализировали).
	// Но wait, insertNode рекурсивный и ожидает Node.
	// Нам нужно безопасно получить/создать ребенка корня.

	// Так как Root.Children shared, доступ к нему тоже должен быть защищен.
	// Но мы договорились, что ShardLocks[i] защищает ветку i.
	// Значит Root.Children[i] управляется ShardLocks[i].

	// Получаем или создаем поддерево для этого шарда
	child := tree.Root.Children[shardIdx]
	if child == nil {
		child = &Node{Children: make(map[uint8]*Node)}
		tree.Root.Children[shardIdx] = child
	}

	// Вставляем рекурсивно, начиная с глубины 1 (так как depth 0 мы только что прошли)
	tree.insertNode(child, userData, 1)

	// После вставки обновляем хеш этого поддерева (ребенка корня)
	// Это важно для консистентности. Root хеш пересчитывается отдельно.
	// В текущей реализации insertNode считает хеши снизу вверх.
	// Нам нужно обновить хеш child.
	// insertNode обновляет хеши, но она работает "внутри" child.
	// Hash самого child обновится? Да, если insertNode возвращает хеш или обновляет переданный node.
	// Моя реализация insertNode обновляет node.Hash в конце.

	tree.ShardLocks[shardIdx].Unlock()

	tree.Cache.Put(userData.UID, userData)
}

func (tree *SparseMerklePatriciaTree) insertNode(node *Node, userData *UserData, depth int) {
	if depth >= tree.MaxDepth-1 {
		if node.Children == nil { node.Children = make(map[uint8]*Node) }
		idx := userData.Key[depth]
		child := node.Children[idx]
		if child == nil {
			child = &Node{UserData: userData, IsLeaf: true}
			node.Children[idx] = child
		} else {
			child.UserData = userData
			child.IsLeaf = true
		}
		child.Hash = userData.Hash()
		return
	}

	if node.Children == nil { node.Children = make(map[uint8]*Node) }
	idx := userData.Key[depth]
	child := node.Children[idx]
	if child == nil {
		child = &Node{Children: make(map[uint8]*Node)}
		node.Children[idx] = child
	}

	tree.insertNode(child, userData, depth+1)

	// Пересчет хеша текущего узла после возврата из рекурсии (lazy/on-write hash update)
	// Это overhead на запись, но обеспечивает валидность хешей внутри шарда.
	// Можно оптимизировать: помечать "dirty" и пересчитывать только при ComputeRoot,
	// но для теста скорости Insert оставим так.

	// Упрощенный пересчет хеша узла (без сортировки ключей для скорости INSERTS?)
	// Нет, Merkle Tree требует детерминизма. Придется сортировать ключи.
	// Это замедлит Insert. Но мы распараллелили Insert, так что должно быть ок.
	tree.updateNodeHash(node)
}

func (tree *SparseMerklePatriciaTree) updateNodeHash(node *Node) {
	hasher := blake3.New()
	// Получаем ключи и сортируем (FIXME: аллокации при каждом Insert!)
	// Для оптимизации можно использовать pool для слайсов ключей.
	keys := make([]uint8, 0, len(node.Children))
	for k := range node.Children { keys = append(keys, k) }

	// Insertion sort for small arrays is fast
	for i := 1; i < len(keys); i++ {
		key := keys[i]
		j := i - 1
		for j >= 0 && keys[j] > key { keys[j+1] = keys[j]; j-- }
		keys[j+1] = key
	}

	for _, key := range keys {
		child := node.Children[key]
		hasher.Write([]byte{key})
		hasher.Write(child.Hash[:])
	}
	var res [32]byte
	copy(res[:], hasher.Sum(nil))
	node.Hash = res
}

// Get теперь тоже использует ShardLocks
func (tree *SparseMerklePatriciaTree) Get(uid uint64) (*UserData, bool) {
	if acc, ok := tree.Cache.Get(uid); ok { return acc, true }

	// Получаем ключ для определения шарда
	var key [8]byte
	binary.BigEndian.PutUint64(key[:], uid)
	shardIdx := key[0]

	tree.ShardLocks[shardIdx].RLock()
	// Ищем в дереве
	// В старой версии мы искали в map Accounts. Теперь её нет (для экономии памяти и сложности).
	// Идем по дереву. Это O(depth) = O(3) шага. Очень быстро.

	node := tree.Root.Children[shardIdx]
	if node == nil {
		tree.ShardLocks[shardIdx].RUnlock()
		return nil, false
	}

	// Depth 1..3
	for d := 1; d < tree.MaxDepth; d++ {
		// Leaf check?
		// В этой структуре данные только в листьях на дне (depth=MaxDepth-1) или раньше?
		// insertNode кладет данные в leaf.
		// Идем вниз.
		idx := key[d]
		node = node.Children[idx]
		if node == nil {
			tree.ShardLocks[shardIdx].RUnlock()
			return nil, false
		}
	}

	// Found leaf
	userData := node.UserData
	tree.ShardLocks[shardIdx].RUnlock()

	if userData != nil {
		tree.Cache.Put(uid, userData)
		return userData, true
	}
	return nil, false
}

// ComputeRoot собирает хеши шардов
func (tree *SparseMerklePatriciaTree) ComputeRoot() [32]byte {
	// Root хеш считается из хешей детей Root.children.
	// Дети защищены разными локами.
	// Чтобы получить консистентный снапшот, в идеале надо залочить всё.
	// Но для скорости мы можем пройтись последовательно (если допустима eventually consistency)
	// Или быстро залочить всё.

	// Залочим всё для честности снапшота
	for i := 0; i < 256; i++ {
		tree.ShardLocks[i].RLock()
	}
	defer func() {
		for i := 0; i < 256; i++ {
			tree.ShardLocks[i].RUnlock()
		}
	}()

	return tree.updateRootHashInternal()
}

func (tree *SparseMerklePatriciaTree) updateRootHashInternal() [32]byte {
	hasher := blake3.New()

	// Root.Children[0..255]
	// Сортировка не нужна, ключи 0..255, просто идем по порядку если они есть
	for i := 0; i < 256; i++ {
		idx := uint8(i)
		child := tree.Root.Children[idx]
		if child != nil {
			hasher.Write([]byte{idx})
			hasher.Write(child.Hash[:])
		}
	}
	var res [32]byte
	copy(res[:], hasher.Sum(nil))
	return res
}

// ================= HELPERS & TEST RUNNERS (Same as before) =================

func GenerateRandomUserData(uid uint64) *UserData {
	ud := &UserData{UID: uid}
	binary.BigEndian.PutUint64(ud.Key[:], uid)
	ud.Counters.GlobalNonce = uid
	ud.Counters.BTCUSD_nonce = uid + 1
	return ud
}

func getCounterValue(counters *Counters, idx int) uint64 {
	ptr := unsafe.Pointer(counters)
	arr := (*[16]uint64)(ptr)
	return arr[idx]
}

func incrementCounter(counters *Counters, idx int) {
	ptr := unsafe.Pointer(counters)
	arr := (*[16]uint64)(ptr)
	arr[idx]++
}

func runReadCounterTest(tree *SparseMerklePatriciaTree, numReaders int, duration time.Duration, totalAccounts uint64) {
	var ops atomic.Uint64
	done := make(chan struct{})
	for i := 0; i < numReaders; i++ {
		go func() {
			seed := uint64(time.Now().UnixNano()) + uint64(i)*777
			rngState := seed
			for {
				select {
				case <-done: return
				default:
					rngState ^= rngState << 13; rngState ^= rngState >> 7; rngState ^= rngState << 17
					uid := rngState % totalAccounts
					user, ok := tree.Get(uid)
					if ok {
						val := getCounterValue(&user.Counters, int(rngState % 16))
						_ = val
					}
					ops.Add(1)
				}
			}
		}()
	}
	time.Sleep(duration); close(done)
	opsSec := float64(ops.Load()) / duration.Seconds()
	fmt.Printf("   Readers: %d | Speed: %.0f ops/sec\n", numReaders, opsSec)
}

func runMixedTest(tree *SparseMerklePatriciaTree, numWorkers int, duration time.Duration, totalAccounts uint64) {
	var ops atomic.Uint64
	done := make(chan struct{})
	for i := 0; i < numWorkers; i++ {
		go func() {
			seed := uint64(time.Now().UnixNano()) + uint64(i)*999
			rngState := seed
			for {
				select {
				case <-done: return
				default:
					rngState ^= rngState << 13; rngState ^= rngState >> 7; rngState ^= rngState << 17
					uid := rngState % totalAccounts
					decision := rngState % 100
					if decision < 70 {
						user, ok := tree.Get(uid)
						if ok { _ = getCounterValue(&user.Counters, int(rngState % 16)) }
					} else {
						user, ok := tree.Get(uid)
						if ok {
							newUser := *user
							incrementCounter(&newUser.Counters, int(rngState % 16))
							tree.Insert(&newUser)
						}
					}
					ops.Add(1)
				}
			}
		}()
	}
	time.Sleep(duration); close(done)
	opsSec := float64(ops.Load()) / duration.Seconds()
	fmt.Printf("   Workers: %d (70%% R, 30%% W) | Speed: %.0f ops/sec\n", numWorkers, opsSec)
}

func main() {
	runtime.GOMAXPROCS(runtime.NumCPU())
	fmt.Println("=== Fine-Grained Locking Merkle Tree Benchmark ===")
	fmt.Printf("CPUs: %d | Leaf Arity: 64 | Shards: 256\n", runtime.NumCPU())

	tree := NewSparseMerklePatriciaTree(64, 100000)

	fmt.Println("1. Filling 2M accounts...")
	start := time.Now()
	for i := uint64(0); i < 2_000_000; i++ {
		tree.Insert(GenerateRandomUserData(i))
	}
	fmt.Printf("   Time: %v\n", time.Since(start))

	fmt.Println("\n2. Test: Read Random Counter (Pure Read)...")
	for _, t := range []int{1, 4, 16, 32} {
		runReadCounterTest(tree, t, 2*time.Second, 2_000_000)
	}

	fmt.Println("\n3. Test: Mixed Load (70% Read Counter, 30% Increment)...")
	for _, t := range []int{1, 4, 16, 32} {
		runMixedTest(tree, t, 2*time.Second, 2_000_000)
	}

	fmt.Println("\n4. Final Root Computation...")
	start = time.Now()
	root := tree.ComputeRoot()
	fmt.Printf("   Root: %x | Time: %v\n", root[:16], time.Since(start))
}
