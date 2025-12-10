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
	Cache     *LRUCache
	LeafArity int
	MaxDepth  int
	mu        sync.RWMutex
}

// LRUCache - простой LRU кеш
type LRUCache struct {
	capacity int
	cache    map[uint64]*cacheEntry
	head     *cacheEntry
	tail     *cacheEntry
	mu       sync.Mutex // Используем Mutex для кеша (быстрее RWMutex на мелких операциях)
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
	defer lru.mu.Unlock()
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

// NewSparseMerklePatriciaTree создает новое дерево
func NewSparseMerklePatriciaTree(leafArity int, cacheSize int) *SparseMerklePatriciaTree {
	if leafArity == 0 {
		leafArity = 64
	}
	maxDepth := 3
	return &SparseMerklePatriciaTree{
		Root:      &Node{Children: make(map[uint8]*Node)},
		Accounts:  make(map[uint64]*Account),
		Cache:     NewLRUCache(cacheSize),
		LeafArity: leafArity,
		MaxDepth:  maxDepth,
	}
}

// Insert добавляет или обновляет аккаунт
func (tree *SparseMerklePatriciaTree) Insert(account *Account) {
	tree.mu.Lock()
	defer tree.mu.Unlock()
	tree.Accounts[account.UID] = account
	tree.Cache.Put(account.UID, account)
	tree.insertNode(tree.Root, account, 0)
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
	// 1. Быстрый путь: LRU кеш
	if acc, ok := tree.Cache.Get(uid); ok {
		return acc, true
	}

	// 2. Медленный путь: RLock дерева
	tree.mu.RLock()
	// Важно: RUnlock нельзя делать defer, если мы хотим обновить кеш ПОСЛЕ чтения
	// Но для простоты оставим defer, а Put в кеш сделаем после, это безопасно
	// так как кеш имеет свой мьютекс.
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
	size += unsafe.Sizeof(*tree.Cache)
	return size
}

// ================= TEST HELPERS =================

func runConcurrentReadTest(tree *SparseMerklePatriciaTree, numReaders int, duration time.Duration, totalAccounts uint64) {
	var ops atomic.Uint64
	done := make(chan struct{})

	// Запускаем читателей
	for i := 0; i < numReaders; i++ {
		go func() {
			for {
				select {
				case <-done:
					return
				default:
					// Читаем случайный аккаунт
					// Используем простой генератор псевдослучайных чисел для скорости теста
					// (в реальном коде rand.Intn, тут простая арифметика с atomic тоже ок, но лучше локальный rand)
					// Чтобы не блокироваться на глобальном rand lock, используем время или простой счетчик
					// Для теста просто возьмем i и ops.

					// Симуляция random access:
					// В реальном бенчмарке лучше использовать PCG или SplitMix локально,
					// но для простоты возьмем простое хеширование текущего времени
					uid := uint64(time.Now().UnixNano()) % totalAccounts
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
		for {
			select {
			case <-done:
				return
			default:
				// Обновляем существующий
				uid := uint64(time.Now().UnixNano()) % totalAccounts
				tree.Insert(GenerateRandomAccount(uid, StatusAlgo))
				writeOps.Add(1)
				// Небольшая пауза чтобы не забивать Lock на 100% (симуляция реальной работы движка)
				time.Sleep(10 * time.Microsecond)
			}
		}
	}()

	// Читатели
	for i := 0; i < numReaders; i++ {
		go func() {
			for {
				select {
				case <-done:
					return
				default:
					uid := uint64(time.Now().UnixNano()) % totalAccounts
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
	// Устанавливаем GOMAXPROCS для использования всех ядер
	runtime.GOMAXPROCS(runtime.NumCPU())

	fmt.Println("=== Sparse Merkle Tree Concurrent Benchmark ===")
	fmt.Printf("CPUs available: %d\n\n", runtime.NumCPU())

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
		if threads > runtime.NumCPU()*2 {
			break // Нет смысла запускать слишком много горутин
		}
		runConcurrentReadTest(tree, threads, 2*time.Second, 1_000_000)
	}
	fmt.Println()

	// Тест 11: Чтение под нагрузкой записи (Readers vs Writer)
	fmt.Println("11. Тест чтения при активной записи (Readers + 1 Writer)...")
	// Обновим дерево до 10M для реалистичности
	fmt.Println("   Adding more accounts up to 2M for this test...")
	for i := uint64(1_000_000); i < 2_000_000; i++ {
		tree.Insert(GenerateRandomAccount(i, StatusUser))
	}

	for _, threads := range threadCounts {
		if threads > runtime.NumCPU()*2 {
			break
		}
		runConcurrentReadWriteTest(tree, threads, 2*time.Second, 2_000_000)
		fmt.Println("   ---")
	}

	fmt.Println("\n=== Тесты завершены ===")
}
