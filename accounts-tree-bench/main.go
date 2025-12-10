package main

import (
	"crypto/rand"
	"encoding/binary"
	"fmt"
	"runtime"
	"sync"
//	"testing"
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
	UID       uint64        // Уникальный ID
	Email     [64]byte      // Email (64 байта)
	Status    AccountStatus // Статус аккаунта
	PublicKey [32]byte      // Публичный ключ (blake3 хеш)
	Key       [8]byte       // BigEndian(UID)
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
	mu       sync.RWMutex
}

type cacheEntry struct {
	key   uint64
	value *Account
	prev  *cacheEntry
	next  *cacheEntry
}

// NewLRUCache создает новый LRU кеш
func NewLRUCache(capacity int) *LRUCache {
	return &LRUCache{
		capacity: capacity,
		cache:    make(map[uint64]*cacheEntry),
	}
}

// Get получает значение из кеша
func (lru *LRUCache) Get(key uint64) (*Account, bool) {
	lru.mu.Lock()
	defer lru.mu.Unlock()
	if entry, ok := lru.cache[key]; ok {
		lru.moveToFront(entry)
		return entry.value, true
	}
	return nil, false
}

// Put добавляет значение в кеш
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
	maxDepth := 3 // Для ~11M аккаунтов
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
	if acc, ok := tree.Cache.Get(uid); ok {
		return acc, true
	}
	tree.mu.RLock()
	defer tree.mu.RUnlock()
	if acc, ok := tree.Accounts[uid]; ok {
		tree.Cache.Put(uid, acc)
		return acc, true
	}
	return nil, false
}

// GetBatch получает несколько аккаунтов
func (tree *SparseMerklePatriciaTree) GetBatch(uids []uint64) []*Account {
	result := make([]*Account, 0, len(uids))
	for _, uid := range uids {
		if acc, ok := tree.Get(uid); ok {
			result = append(result, acc)
		}
	}
	return result
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

// GenerateRandomAccount создает случайный аккаунт
func GenerateRandomAccount(uid uint64, status AccountStatus) *Account {
	acc := &Account{UID: uid, Status: status}
	binary.BigEndian.PutUint64(acc.Key[:], uid)
	_, _ = rand.Read(acc.Email[:])
	_, _ = rand.Read(acc.PublicKey[:])
	return acc
}

// EstimateMemory оценивает память дерева
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
	for _, e := range tree.Cache.cache {
		size += unsafe.Sizeof(*e)
	}
	return size
}

// printMemUsage печатает статистику памяти
func printMemUsage(prefix string) {
	var m runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&m)
	fmt.Printf("%s: Alloc = %v MB, HeapAlloc = %v MB, HeapObjects = %v\n",
		prefix, m.Alloc/1024/1024, m.HeapAlloc/1024/1024, m.HeapObjects)
}

// Бенчмарки (без изменений)

func main() {
	fmt.Println("=== Sparse Merkle Tree Benchmark (BigEndian UID key) ===")
	fmt.Println()

	tree := NewSparseMerklePatriciaTree(64, 100000)

	printMemUsage("Before fill")

	// 1-2. Генерация аккаунтов
	fmt.Println("1. Генерация 1M системных аккаунтов...")
	start := time.Now()
	for i := uint64(0); i < 1_000_000; i++ {
		tree.Insert(GenerateRandomAccount(i, StatusSystem))
	}
	fmt.Printf("   Время: %v\n", time.Since(start))
	printMemUsage("After 1M system")

	fmt.Println("2. Генерация 10M случайных пользователей...")
	start = time.Now()
	for i := uint64(1_000_000); i < 11_000_000; i++ {
		tree.Insert(GenerateRandomAccount(i, []AccountStatus{StatusUser, StatusMM, StatusAlgo, StatusBlocked}[i%4]))
	}
	fmt.Printf("   Время: %v\n", time.Since(start))
	fmt.Printf("   Всего аккаунтов: %d\n", len(tree.Accounts))
	printMemUsage("After 11M total")
	fmt.Printf("   Оценка памяти по структурам: %.2f MB\n", float64(tree.EstimateMemory())/1024.0/1024.0)
	
	// 3-5. Тесты хеширования и обновлений
	fmt.Println("\n3. Вычисление root hash...")
	start = time.Now()
	rootHash := tree.ComputeRoot()
	fmt.Printf("   Root Hash: %x\n   Время: %v\n", rootHash[:16], time.Since(start))

	fmt.Println("\n4. Обновление 1000 случайных аккаунтов...")
	start = time.Now()
	for i := 0; i < 1000; i++ {
		tree.Insert(GenerateRandomAccount(uint64(i*11000), StatusAlgo))
	}
	rootHash = tree.ComputeRoot()
	fmt.Printf("   Новый Root Hash: %x\n   Время (обновление + пересчет): %v\n", rootHash[:16], time.Since(start))

	fmt.Println("\n5. Обновление 1000 + добавление 5000 новых...")
	start = time.Now()
	for i := 0; i < 1000; i++ {
		tree.Insert(GenerateRandomAccount(uint64(i*10000), StatusMM))
	}
	baseUIDAfterBulk := uint64(11_000_000)
	for i := uint64(0); i < 5000; i++ {
		tree.Insert(GenerateRandomAccount(baseUIDAfterBulk+i, StatusUser))
	}
	rootHash = tree.ComputeRoot()
	fmt.Printf("   Новый Root Hash: %x\n   Время: %v\n   Всего аккаунтов: %d\n", rootHash[:16], time.Since(start), len(tree.Accounts))
	printMemUsage("After updates")

	// 6. Тест одиночного чтения
	fmt.Println("\n6. Тест получения случайного аккаунта (1M операций)...")
	start = time.Now()
	const singleReads = 1_000_000
	for i := 0; i < singleReads; i++ {
		_, _ = tree.Get(uint64(i % 11_005_000))
	}
	elapsed := time.Since(start)
	avgUs := float64(elapsed.Nanoseconds()) / float64(singleReads) / 1000.0
	fmt.Printf("   Время: %v\n   Скорость: %.0f ops/sec\n   Среднее на чтение: %.3f µs\n",
		elapsed, float64(singleReads)/elapsed.Seconds(), avgUs)

	// 7. Тест батч-чтения
	fmt.Println("\n7. Тест батч чтения...")
	for _, size := range []int{16, 32, 64, 128} {
		start = time.Now()
		const iterations = 10_000
		for i := 0; i < iterations; i++ {
			uids := make([]uint64, size)
			for j := 0; j < size; j++ {
				uids[j] = uint64((i*size + j) % 11_005_000)
			}
			_ = tree.GetBatch(uids)
		}
		elapsed := time.Since(start)
		totalOps := iterations * size
		avgUsPerAcc := float64(elapsed.Nanoseconds()) / float64(totalOps) / 1000.0
		fmt.Printf("   Batch %d: %v (%.0f ops/sec), среднее на аккаунт: %.3f µs\n",
			size, elapsed, float64(totalOps)/elapsed.Seconds(), avgUsPerAcc)
	}

	// 8. НОВЫЙ ТЕСТ: Среднее время вставки нового аккаунта
	fmt.Println("\n8. Тест вставки одного нового аккаунта (10k операций)...")
	const newInsertions = 10_000
	var totalInsertTime time.Duration
	baseUIDForNew := uint64(len(tree.Accounts))
	for i := 0; i < newInsertions; i++ {
		newAcc := GenerateRandomAccount(baseUIDForNew+uint64(i), StatusUser)
		start = time.Now()
		tree.Insert(newAcc)
		totalInsertTime += time.Since(start)
	}
	avgInsertUs := float64(totalInsertTime.Nanoseconds()) / float64(newInsertions) / 1000.0
	fmt.Printf("   Всего аккаунтов после вставки: %d\n", len(tree.Accounts))
	fmt.Printf("   Среднее время на одну вставку: %.3f µs\n", avgInsertUs)

	// 9. НОВЫЙ ТЕСТ: Среднее время модификации существующего аккаунта
	fmt.Println("\n9. Тест модификации одного существующего аккаунта (10k операций)...")
	const modifications = 10_000
	var totalModTime time.Duration
	for i := 0; i < modifications; i++ {
		modUID := uint64((i * 1000) % 11_000_000)
		modAcc := GenerateRandomAccount(modUID, StatusBlocked)
		start = time.Now()
		tree.Insert(modAcc) // Insert на существующий UID = модификация
		totalModTime += time.Since(start)
	}
	avgModUs := float64(totalModTime.Nanoseconds()) / float64(modifications) / 1000.0
	fmt.Printf("   Среднее время на одну модификацию: %.3f µs\n", avgModUs)
	
	fmt.Println("\n=== Тесты завершены ===")
}
