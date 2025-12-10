package main

import (
	"crypto/rand"
	"encoding/binary"
	"fmt"
	"runtime"
	"sync"
	"sync/atomic"
	"time"
	"unsafe"

	"github.com/zeebo/blake3"
)

// AccountStatus - статус аккаунта
type AccountStatus uint8

const (
	StatusSystem AccountStatus = iota
	StatusBlocked
	StatusMM
	StatusAlgo
	StatusUser
)

func (s AccountStatus) String() string {
	return [...]string{"system", "blocked", "mm", "algo", "user"}[s]
}

// Account - структура аккаунта в листе дерева
type Account struct {
	UID       uint64
	Email     [64]byte
	Status    AccountStatus
	PublicKey [32]byte
	Key       [8]byte // BigEndian(UID)
}

// Hash возвращает blake3 хеш аккаунта
func (a *Account) Hash() [32]byte {
	hasher := blake3.New()
	hasher.Write(a.Key[:])
	hasher.Write(a.Email[:])
	hasher.Write([]byte{byte(a.Status)})
	hasher.Write(a.PublicKey[:])
	var result [32]byte
	copy(result[:], hasher.Sum(nil))
	return result
}

// Node - узел дерева
type Node struct {
	Hash     [32]byte
	Children map[uint8]*Node
	Account  *Account
	IsLeaf   bool
}

// SparseMerklePatriciaTree - основная структура дерева
type SparseMerklePatriciaTree struct {
	Root      *Node
	Accounts  map[uint64]*Account
	Cache     *ShardedLRUCache // Используем шардированный кеш
	LeafArity int
	MaxDepth  int
	mu        sync.RWMutex
}

// LRUCache - простой LRU кеш (один шард)
type LRUCache struct {
	capacity int
	cache    map[uint64]*cacheEntry
	head     *cacheEntry
	tail     *cacheEntry
	mu       sync.Mutex
}

type cacheEntry struct {
	key   uint64
	value *Account
	prev  *cacheEntry
	next  *cacheEntry
}

func NewLRUCache(capacity int) *LRUCache {
	return &LRUCache{
		capacity: capacity,
		cache:    make(map[uint64]*cacheEntry),
	}
}

func (lru *LRUCache) Get(key uint64) (*Account, bool) {
	lru.mu.Lock()
	defer lru.mu.Unlock() // defer имеет небольшой оверхед (~50ns), но для надежности оставим
	if entry, ok := lru.cache[key]; ok {
		lru.moveToFront(entry)
		return entry.value, true
	}
	return nil, false
}

func (lru *LRUCache) Put(key uint64, value *Account) {
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
	if entry == lru.head {
		return
	}
	lru.remove(entry)
	lru.addToFront(entry)
}

func (lru *LRUCache) addToFront(entry *cacheEntry) {
	entry.next = lru.head
	entry.prev = nil
	if lru.head != nil {
		lru.head.prev = entry
	}
	lru.head = entry
	if lru.tail == nil {
		lru.tail = entry
	}
}

func (lru *LRUCache) remove(entry *cacheEntry) {
	if entry.prev != nil {
		entry.prev.next = entry.next
	} else {
		lru.head = entry.next
	}
	if entry.next != nil {
		entry.next.prev = entry.prev
	} else {
		lru.tail = entry.prev
	}
}

func (lru *LRUCache) removeTail() {
	if lru.tail == nil {
		return
	}
	delete(lru.cache, lru.tail.key)
	lru.remove(lru.tail)
}

// ShardedLRUCache - обертка над несколькими LRUCache для снижения конкуренции
type ShardedLRUCache struct {
	shards    []*LRUCache
	shardMask uint64
}

// NewShardedLRUCache создает кеш с 2^power шардами (например power=8 -> 256 шардов)
func NewShardedLRUCache(totalCapacity int, power uint) *ShardedLRUCache {
	numShards := 1 << power
	shardCapacity := totalCapacity / numShards
	if shardCapacity < 1 {
		shardCapacity = 1
	}

	shards := make([]*LRUCache, numShards)
	for i := 0; i < numShards; i++ {
		shards[i] = NewLRUCache(shardCapacity)
	}

	return &ShardedLRUCache{
		shards:    shards,
		shardMask: uint64(numShards - 1),
	}
}

func (s *ShardedLRUCache) GetShard(key uint64) *LRUCache {
	// Простая хеш-функция для распределения.
	// Для UID можно использовать просто key & mask, если UID распределены равномерно.
	// Если UID идут подряд (0,1,2...), то это идеально размажет их по шардам.
	idx := key & s.shardMask
	return s.shards[idx]
}

func (s *ShardedLRUCache) Get(key uint64) (*Account, bool) {
	return s.GetShard(key).Get(key)
}

func (s *ShardedLRUCache) Put(key uint64, value *Account) {
	s.GetShard(key).Put(key, value)
}

// NewSparseMerklePatriciaTree создает новое дерево
func NewSparseMerklePatriciaTree(leafArity int, cacheSize int) *SparseMerklePatriciaTree {
	if leafArity == 0 {
		leafArity = 64
	}
	maxDepth := 3

	// Используем 256 шардов (2^8) для LRU кеша.
	// Это должно практически устранить конкуренцию при < 64 ядрах.
	shardedCache := NewShardedLRUCache(cacheSize, 8)

	return &SparseMerklePatriciaTree{
		Root:      &Node{Children: make(map[uint8]*Node)},
		Accounts:  make(map[uint64]*Account),
		Cache:     shardedCache,
		LeafArity: leafArity,
		MaxDepth:  maxDepth,
	}
}

// Insert добавляет или обновляет аккаунт
func (tree *SparseMerklePatriciaTree) Insert(account *Account) {
	// Блокировка дерева нужна только для обновления основной структуры
	tree.mu.Lock()
	tree.Accounts[account.UID] = account
	// Обновляем структуру дерева...
	tree.insertNode(tree.Root, account, 0)
	tree.mu.Unlock() // Разблокируем дерево как можно раньше

	// Кеш можно обновить ВНЕ глобальной блокировки дерева!
	// ShardedLRUCache потокобезопасен сам по себе.
	tree.Cache.Put(account.UID, account)
}

func (tree *SparseMerklePatriciaTree) insertNode(node *Node, account *Account, depth int) {
	if depth >= tree.MaxDepth-1 {
		if node.Children == nil {
			node.Children = make(map[uint8]*Node)
		}
		idx := account.Key[depth]
		child := node.Children[idx]
		if child == nil {
			child = &Node{Account: account, IsLeaf: true}
			node.Children[idx] = child
		} else {
			child.Account = account
			child.IsLeaf = true
		}
		child.Hash = account.Hash()
		return
	}
	if node.Children == nil {
		node.Children = make(map[uint8]*Node)
	}
	idx := account.Key[depth]
	child := node.Children[idx]
	if child == nil {
		child = &Node{Children: make(map[uint8]*Node)}
		node.Children[idx] = child
	}
	tree.insertNode(child, account, depth+1)
}

// Get получает аккаунт по UID
func (tree *SparseMerklePatriciaTree) Get(uid uint64) (*Account, bool) {
	// 1. Быстрый путь: Sharded LRU кеш (lock-free относительно других шардов)
	if acc, ok := tree.Cache.Get(uid); ok {
		return acc, true
	}

	// 2. Медленный путь: RLock дерева
	tree.mu.RLock()
	acc, ok := tree.Accounts[uid]
	tree.mu.RUnlock()

	if ok {
		tree.Cache.Put(uid, acc)
		return acc, true
	}
	return nil, false
}

// ComputeRoot вычисляет корневой хеш
func (tree *SparseMerklePatriciaTree) ComputeRoot() [32]byte {
	tree.mu.RLock()
	defer tree.mu.RUnlock()
	return tree.computeNodeHash(tree.Root)
}

func (tree *SparseMerklePatriciaTree) computeNodeHash(node *Node) [32]byte {
	if node == nil {
		return [32]byte{}
	}
	if node.IsLeaf && node.Account != nil {
		return node.Account.Hash()
	}
	hasher := blake3.New()
	keys := make([]uint8, 0, len(node.Children))
	for k := range node.Children {
		keys = append(keys, k)
	}
	// Сортировка для детерминизма
	for i := 1; i < len(keys); i++ {
		key := keys[i]
		j := i - 1
		for j >= 0 && keys[j] > key {
			keys[j+1] = keys[j]
			j--
		}
		keys[j+1] = key
	}
	for _, key := range keys {
		childHash := tree.computeNodeHash(node.Children[key])
		hasher.Write([]byte{key})
		hasher.Write(childHash[:])
	}
	var result [32]byte
	copy(result[:], hasher.Sum(nil))
	node.Hash = result
	return result
}

func GenerateRandomAccount(uid uint64, status AccountStatus) *Account {
	acc := &Account{UID: uid, Status: status}
	binary.BigEndian.PutUint64(acc.Key[:], uid)
	_, _ = rand.Read(acc.Email[:])
	_, _ = rand.Read(acc.PublicKey[:])
	return acc
}

func (tree *SparseMerklePatriciaTree) EstimateMemory() uintptr {
	tree.mu.RLock()
	defer tree.mu.RUnlock()
	var size uintptr
	size += unsafe.Sizeof(*tree)
	for _, acc := range tree.Accounts {
		size += unsafe.Sizeof(*acc)
	}
	visited := make(map[*Node]struct{})
	var walk func(n *Node)
	walk = func(n *Node) {
		if n == nil || visited[n] != struct{}{} {
			return
		}
		visited[n] = struct{}{}
		size += unsafe.Sizeof(*n) + unsafe.Sizeof(n.Children)
		for _, child := range n.Children {
			walk(child)
		}
	}
	walk(tree.Root)

	// Оценка памяти шардированного кеша
	size += unsafe.Sizeof(*tree.Cache)
	size += unsafe.Sizeof(tree.Cache.shards) // slice header
	// + сами структуры LRUCache
	size += uintptr(len(tree.Cache.shards)) * unsafe.Sizeof(LRUCache{})
	return size
}

// ================= TEST HELPERS =================

func runConcurrentReadTest(tree *SparseMerklePatriciaTree, numReaders int, duration time.Duration, totalAccounts uint64) {
	var ops atomic.Uint64
	done := make(chan struct{})

	for i := 0; i < numReaders; i++ {
		go func() {
			// Локальный генератор случайных чисел для каждого потока (PCG)
			// Это важно, чтобы rand не был узким местом
			seed := uint64(time.Now().UnixNano()) + uint64(i)*12345
			rngState := seed

			for {
				select {
				case <-done:
					return
				default:
					// Xorshift подобный быстрый RNG
					rngState ^= rngState << 13
					rngState ^= rngState >> 7
					rngState ^= rngState << 17

					uid := rngState % totalAccounts
					tree.Get(uid)
					ops.Add(1)
				}
			}
		}()
	}

	time.Sleep(duration)
	close(done)

	totalOps := ops.Load()
	opsPerSec := float64(totalOps) / duration.Seconds()
	fmt.Printf("   Readers: %d | Total Ops: %d | Speed: %.0f ops/sec (Aggregate)\n", 
		numReaders, totalOps, opsPerSec)
	fmt.Printf("   Avg per reader: %.0f ops/sec\n", opsPerSec/float64(numReaders))
}

func runConcurrentReadWriteTest(tree *SparseMerklePatriciaTree, numReaders int, duration time.Duration, totalAccounts uint64) {
	var readOps atomic.Uint64
	var writeOps atomic.Uint64
	done := make(chan struct{})

	// Писатель (1 поток)
	go func() {
		rngState := uint64(time.Now().UnixNano())
		for {
			select {
			case <-done:
				return
			default:
				rngState ^= rngState << 13
				rngState ^= rngState >> 7
				rngState ^= rngState << 17
				uid := rngState % totalAccounts

				tree.Insert(GenerateRandomAccount(uid, StatusAlgo))
				writeOps.Add(1)
				// Небольшая задержка, чтобы симулировать реальную нагрузку,
				// иначе один писатель может забить канал памяти.
				// В HFT write ops обычно намного меньше read ops.
				for k := 0; k < 100; k++ { } // busy wait ~few ns
			}
		}
	}()

	// Читатели
	for i := 0; i < numReaders; i++ {
		go func() {
			rngState := uint64(time.Now().UnixNano()) + uint64(i)*99999
			for {
				select {
				case <-done:
					return
				default:
					rngState ^= rngState << 13
					rngState ^= rngState >> 7
					rngState ^= rngState << 17
					uid := rngState % totalAccounts

					tree.Get(uid)
					readOps.Add(1)
				}
			}
		}()
	}

	time.Sleep(duration)
	close(done)

	rOps := readOps.Load()
	wOps := writeOps.Load()

	fmt.Printf("   Readers: %d + 1 Writer\n", numReaders)
	fmt.Printf("   Read Speed:  %.0f ops/sec\n", float64(rOps)/duration.Seconds())
	fmt.Printf("   Write Speed: %.0f ops/sec\n", float64(wOps)/duration.Seconds())
	fmt.Printf("   Total Speed: %.0f ops/sec\n", float64(rOps+wOps)/duration.Seconds())
}

// ================= MAIN =================

func main() {
	runtime.GOMAXPROCS(runtime.NumCPU())

	fmt.Println("=== Sharded LRU Sparse Merkle Tree Benchmark ===")
	fmt.Printf("CPUs available: %d\n", runtime.NumCPU())
	fmt.Printf("Cache Shards: 256\n\n")

	tree := NewSparseMerklePatriciaTree(64, 100000)

	// 1. Подготовка данных
	fmt.Println("Preparing data (1M accounts)...")
	start := time.Now()
	for i := uint64(0); i < 1_000_000; i++ {
		tree.Insert(GenerateRandomAccount(i, StatusUser))
	}
	fmt.Printf("Data loaded in %v\n\n", time.Since(start))

	// Тест 10: Многопоточное чтение (Pure Read Scalability)
	fmt.Println("10. Тест масштабируемости чтения (Pure Read)...")
	threadCounts := []int{1, 2, 4, 8, 16, 32}
	for _, threads := range threadCounts {
		if threads > runtime.NumCPU()*4 {
			break 
		}
		runConcurrentReadTest(tree, threads, 2*time.Second, 1_000_000)
	}
	fmt.Println()

	// Тест 11: Чтение под нагрузкой записи
	fmt.Println("11. Тест чтения при активной записи (Readers + 1 Writer)...")
	fmt.Println("   Adding more accounts up to 2M for this test...")
	for i := uint64(1_000_000); i < 2_000_000; i++ {
		tree.Insert(GenerateRandomAccount(i, StatusUser))
	}

	for _, threads := range threadCounts {
		if threads > runtime.NumCPU()*4 {
			break
		}
		runConcurrentReadWriteTest(tree, threads, 2*time.Second, 2_000_000)
		fmt.Println("   ---")
	}

	fmt.Println("\n=== Тесты завершены ===")
}
