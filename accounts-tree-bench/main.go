package main

import (
	"crypto/rand"
	"encoding/binary"
	"fmt"
	"runtime"
	"sync"
	"testing"
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
}

// Hash возвращает blake3 хеш аккаунта
func (a *Account) Hash() [32]byte {
	hasher := blake3.New()

	// Сериализуем аккаунт для хеширования
	_ = binary.Write(hasher, binary.LittleEndian, a.UID)
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
	Accounts  map[uint64]*Account // Все аккаунты по UID
	Cache     *LRUCache           // LRU кеш для быстрого доступа
	LeafArity int                 // Количество листьев на нижнем уровне
    MaxDepth  int  				  // Максимальная глубина дерева
	mu        sync.RWMutex        // Мьютекс для потокобезопасности
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
		leafArity = 16 // По умолчанию
	}
	
	// Автоматически выбираем глубину
    maxDepth := 4
    if leafArity >= 64 {
        maxDepth = 3
    }
    if leafArity >= 256 {
        maxDepth = 2
    }

	return &SparseMerklePatriciaTree{
		Root:      &Node{Children: make(map[uint8]*Node)},
		Accounts:  make(map[uint64]*Account),
		Cache:     NewLRUCache(cacheSize),
		LeafArity: leafArity,
		MaxDepth:  maxDepth,
	}
}

// Insert добавляет или обновляет аккаунт в дереве
func (tree *SparseMerklePatriciaTree) Insert(account *Account) {
	tree.mu.Lock()
	defer tree.mu.Unlock()

	tree.Accounts[account.UID] = account
	tree.Cache.Put(account.UID, account)

	// Вставляем в дерево по пути на основе UID
	tree.insertNode(tree.Root, account, 0)
}

// insertNode рекурсивно вставляет узел
func (tree *SparseMerklePatriciaTree) insertNode(node *Node, account *Account, depth int) {
	// Определяем индекс на основе UID и глубины (байт на уровень)
	shift := uint(56 - depth*8)
	index := uint8((account.UID >> shift) & 0xFF)

	// Если достигли нужной глубины для листьев
	if depth >= tree.MaxDepth {
		if node.Children == nil {
			node.Children = make(map[uint8]*Node)
		}
		if node.Children[index] == nil {
			node.Children[index] = &Node{
				Account: account,
				IsLeaf:  true,
				Hash:    account.Hash(),
			}
		} else {
			node.Children[index].Account = account
			node.Children[index].Hash = account.Hash()
		}
		return
	}

	// Создаем промежуточный узел если нужно
	if node.Children == nil {
		node.Children = make(map[uint8]*Node)
	}
	if node.Children[index] == nil {
		node.Children[index] = &Node{Children: make(map[uint8]*Node)}
	}

	tree.insertNode(node.Children[index], account, depth+1)
}

// Get получает аккаунт по UID (сначала проверяет кеш)
func (tree *SparseMerklePatriciaTree) Get(uid uint64) (*Account, bool) {
	// Сначала кеш
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

// GetBatch получает несколько аккаунтов за раз
func (tree *SparseMerklePatriciaTree) GetBatch(uids []uint64) []*Account {
	result := make([]*Account, 0, len(uids))

	for _, uid := range uids {
		if acc, ok := tree.Get(uid); ok {
			result = append(result, acc)
		}
	}

	return result
}

// ComputeRoot вычисляет корневой хеш дерева
func (tree *SparseMerklePatriciaTree) ComputeRoot() [32]byte {
	tree.mu.RLock()
	defer tree.mu.RUnlock()

	return tree.computeNodeHash(tree.Root)
}

// computeNodeHash рекурсивно вычисляет хеш узла
func (tree *SparseMerklePatriciaTree) computeNodeHash(node *Node) [32]byte {
	if node == nil {
		var zero [32]byte
		return zero
	}

	if node.IsLeaf && node.Account != nil {
		return node.Account.Hash()
	}

	hasher := blake3.New()

	// Сортируем ключи для детерминированного хеша
	keys := make([]uint8, 0, len(node.Children))
	for k := range node.Children {
		keys = append(keys, k)
	}

	// Простая сортировка вставками для малых массивов
	for i := 1; i < len(keys); i++ {
		key := keys[i]
		j := i - 1
		for j >= 0 && keys[j] > key {
			keys[j+1] = keys[j]
			j--
		}
		keys[j+1] = key
	}

	// Хешируем дочерние узлы
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

// GenerateRandomAccount генерирует случайный аккаунт
func GenerateRandomAccount(uid uint64, status AccountStatus) *Account {
	acc := &Account{
		UID:    uid,
		Status: status,
	}

	// Случайный email
	_, _ = rand.Read(acc.Email[:])

	// Случайный публичный ключ
	_, _ = rand.Read(acc.PublicKey[:])

	return acc
}

// EstimateMemory грубо оценивает память, занимаемую деревом
func (tree *SparseMerklePatriciaTree) EstimateMemory() uintptr {
	tree.mu.RLock()
	defer tree.mu.RUnlock()

	var size uintptr

	// Структура дерева
	size += unsafe.Sizeof(*tree)

	// Все аккаунты
	for _, acc := range tree.Accounts {
		size += unsafe.Sizeof(*acc)
	}

	// Узлы дерева (обход с защитой от повторов)
	visited := make(map[*Node]struct{})
	var walk func(n *Node)
	walk = func(n *Node) {
		if n == nil {
			return
		}
		if _, ok := visited[n]; ok {
			return
		}
		visited[n] = struct{}{}

		size += unsafe.Sizeof(*n)
		size += unsafe.Sizeof(n.Children) // дескриптор map

		for _, child := range n.Children {
			walk(child)
		}
	}

	walk(tree.Root)

	// LRU cache (структура + entries)
	size += unsafe.Sizeof(*tree.Cache)
	for _, e := range tree.Cache.cache {
		size += unsafe.Sizeof(*e)
	}

	return size
}

// printMemUsage печатает статистику памяти рантайма
func printMemUsage(prefix string) {
	var m runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&m) // [web:17][web:19]

	fmt.Printf("%s: Alloc = %v MB, HeapAlloc = %v MB, HeapObjects = %v\n",
		prefix,
		m.Alloc/1024/1024,
		m.HeapAlloc/1024/1024,
		m.HeapObjects)
}

// ================= БЕНЧМАРКИ =================

func BenchmarkTreeInsertion(b *testing.B) {
	tree := NewSparseMerklePatriciaTree(64, 100000)

	accounts := make([]*Account, b.N)
	for i := 0; i < b.N; i++ {
		accounts[i] = GenerateRandomAccount(uint64(i), StatusUser)
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		tree.Insert(accounts[i])
	}
}

func BenchmarkTreeGet(b *testing.B) {
	tree := NewSparseMerklePatriciaTree(64, 100000)

	for i := 0; i < 1000000; i++ {
		status := StatusSystem
		if i >= 1000000 {
			status = StatusUser
		}
		tree.Insert(GenerateRandomAccount(uint64(i), status))
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		uid := uint64(i % 1000000)
		tree.Get(uid)
	}
}

func BenchmarkTreeGetBatch16(b *testing.B)  { benchmarkGetBatch(b, 16) }
func BenchmarkTreeGetBatch32(b *testing.B)  { benchmarkGetBatch(b, 32) }
func BenchmarkTreeGetBatch64(b *testing.B)  { benchmarkGetBatch(b, 64) }
func BenchmarkTreeGetBatch128(b *testing.B) { benchmarkGetBatch(b, 128) }

func benchmarkGetBatch(b *testing.B, batchSize int) {
	tree := NewSparseMerklePatriciaTree(64, 100000)

	totalAccounts := 1000000
	for i := 0; i < totalAccounts; i++ {
		status := StatusSystem
		if i >= 1000000 {
			status = StatusUser
		}
		tree.Insert(GenerateRandomAccount(uint64(i), status))
	}

	batches := make([][]uint64, b.N)
	for i := 0; i < b.N; i++ {
		batch := make([]uint64, batchSize)
		for j := 0; j < batchSize; j++ {
			batch[j] = uint64((i*batchSize + j) % totalAccounts)
		}
		batches[i] = batch
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		tree.GetBatch(batches[i])
	}
}

func BenchmarkTreeRootComputation(b *testing.B) {
	tree := NewSparseMerklePatriciaTree(64, 100000)

	for i := 0; i < 100000; i++ {
		tree.Insert(GenerateRandomAccount(uint64(i), StatusUser))
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		tree.ComputeRoot()
	}
}

func BenchmarkTreeUpdate1000(b *testing.B) {
	tree := NewSparseMerklePatriciaTree(64, 100000)

	for i := 0; i < 1000000; i++ {
		tree.Insert(GenerateRandomAccount(uint64(i), StatusUser))
	}

	updates := make([][]*Account, b.N)
	for i := 0; i < b.N; i++ {
		batch := make([]*Account, 1000)
		for j := 0; j < 1000; j++ {
			uid := uint64((i*1000 + j) % 1000000)
			batch[j] = GenerateRandomAccount(uid, StatusAlgo)
		}
		updates[i] = batch
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		for _, acc := range updates[i] {
			tree.Insert(acc)
		}
		tree.ComputeRoot()
	}
}

func BenchmarkTreeUpdate1000Insert5000(b *testing.B) {
	tree := NewSparseMerklePatriciaTree(64, 100000)

	baseSize := 1000000
	for i := 0; i < baseSize; i++ {
		tree.Insert(GenerateRandomAccount(uint64(i), StatusUser))
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		for j := 0; j < 1000; j++ {
			uid := uint64(j % baseSize)
			tree.Insert(GenerateRandomAccount(uid, StatusMM))
		}

		for j := 0; j < 5000; j++ {
			uid := uint64(baseSize + i*5000 + j)
			tree.Insert(GenerateRandomAccount(uid, StatusUser))
		}

		tree.ComputeRoot()
	}
}

// ================= main =================

func main() {
	fmt.Println("=== Sparse Variable-Arity Merkle-Patricia Tree Benchmark ===")
	fmt.Println()

	tree := NewSparseMerklePatriciaTree(64, 100000)

	printMemUsage("Before fill")

	// 1. Генерация 1M системных аккаунтов
	start := time.Now()
	fmt.Println("1. Генерация 1M системных аккаунтов...")
	for i := uint64(0); i < 1_000_000; i++ {
		tree.Insert(GenerateRandomAccount(i, StatusSystem))
	}
	fmt.Printf("   Время: %v\n", time.Since(start))
	printMemUsage("After 1M system")

	// 2. Генерация 10M случайных пользователей
	start = time.Now()
	fmt.Println("2. Генерация 10M случайных пользователей...")
	for i := uint64(1_000_000); i < 11_000_000; i++ {
		statusIdx := i % 4
		status := []AccountStatus{StatusUser, StatusMM, StatusAlgo, StatusBlocked}[statusIdx]
		tree.Insert(GenerateRandomAccount(i, status))
	}
	fmt.Printf("   Время: %v\n", time.Since(start))
	fmt.Printf("   Всего аккаунтов: %d\n", len(tree.Accounts))
	printMemUsage("After 11M total")

	// Оценка памяти структурно
	estimated := tree.EstimateMemory()
	fmt.Printf("   Оценка памяти по структурам: %.2f MB\n",
		float64(estimated)/1024.0/1024.0)

	// 3. Вычисление root hash
	fmt.Println()
	fmt.Println("3. Вычисление root hash...")
	start = time.Now()
	rootHash := tree.ComputeRoot()
	elapsed := time.Since(start)
	fmt.Printf("   Root Hash (первые 16 байт): %x\n", rootHash[:16])
	fmt.Printf("   Время: %v\n", elapsed)

	// 4. Обновление 1000 случайных аккаунтов
	fmt.Println()
	fmt.Println("4. Обновление 1000 случайных аккаунтов...")
	start = time.Now()
	for i := 0; i < 1000; i++ {
		uid := uint64(i * 11_000)
		tree.Insert(GenerateRandomAccount(uid, StatusAlgo))
	}
	rootHash = tree.ComputeRoot()
	elapsed = time.Since(start)
	fmt.Printf("   Новый Root Hash (первые 16 байт): %x\n", rootHash[:16])
	fmt.Printf("   Время (обновление + пересчет): %v\n", elapsed)

	// 5. Обновление 1000 + добавление 5000 новых
	fmt.Println()
	fmt.Println("5. Обновление 1000 + добавление 5000 новых аккаунтов...")
	start = time.Now()
	for i := 0; i < 1000; i++ {
		uid := uint64(i * 10_000)
		tree.Insert(GenerateRandomAccount(uid, StatusMM))
	}
	for i := uint64(11_000_000); i < 11_005_000; i++ {
		tree.Insert(GenerateRandomAccount(i, StatusUser))
	}
	rootHash = tree.ComputeRoot()
	elapsed = time.Since(start)
	fmt.Printf("   Новый Root Hash (первые 16 байт): %x\n", rootHash[:16])
	fmt.Printf("   Время (обновление + вставка + пересчет): %v\n", elapsed)
	fmt.Printf("   Всего аккаунтов: %d\n", len(tree.Accounts))
	printMemUsage("After updates")

	// 6. Тест получения случайного аккаунта
	fmt.Println()
	fmt.Println("6. Тест получения случайного аккаунта (1M операций)...")
	start = time.Now()
	hits := 0
	for i := 0; i < 1_000_000; i++ {
		uid := uint64(i % 11_005_000)
		if _, ok := tree.Get(uid); ok {
			hits++
		}
	}
	elapsed = time.Since(start)
	fmt.Printf("   Время: %v\n", elapsed)
	fmt.Printf("   Скорость: %.0f ops/sec\n", float64(1_000_000)/elapsed.Seconds())
	fmt.Printf("   Cache hits: %d/%d\n", hits, 1_000_000)

	// 7. Батч чтение
	fmt.Println()
	fmt.Println("7. Тест батч чтения...")
	batchSizes := []int{16, 32, 64, 128}
	iterations := 10_000

	for _, size := range batchSizes {
		start = time.Now()
		for i := 0; i < iterations; i++ {
			uids := make([]uint64, size)
			for j := 0; j < size; j++ {
				uids[j] = uint64((i*size + j) % 11_005_000)
			}
			tree.GetBatch(uids)
		}
		elapsed = time.Since(start)
		totalOps := iterations * size
		fmt.Printf("   Batch %d: %v (%.0f ops/sec)\n",
			size, elapsed, float64(totalOps)/elapsed.Seconds())
	}
	
	
	
	fmt.Println("=== Тест разных конфигураций дерева ===\n")
    
    arities := []int{16, 64, 128, 256}
    
    for _, arity := range arities {
        fmt.Printf("--- Тестирование LeafArity = %d ---\n", arity)
        
        tree := NewSparseMerklePatriciaTree(arity, 100000)
        
        // Заполняем 1M аккаунтов для теста
        start := time.Now()
        for i := uint64(0); i < 1_000_000; i++ {
            tree.Insert(GenerateRandomAccount(i, StatusUser))
        }
        insertTime := time.Since(start)
        
        // Тест root computation
        start = time.Now()
        tree.ComputeRoot()
        rootTime := time.Since(start)
        
        // Оценка памяти
        memUsed := tree.EstimateMemory()
        
        fmt.Printf("  Вставка 1M: %v (%.0f ops/sec)\n", 
            insertTime, float64(1_000_000)/insertTime.Seconds())
        fmt.Printf("  Root hash: %v\n", rootTime)
        fmt.Printf("  Память: %.2f MB\n", float64(memUsed)/1024.0/1024.0)
        fmt.Printf("  MaxDepth: %d\n\n", tree.MaxDepth)
    }

	fmt.Println()
	fmt.Println("=== Тесты завершены ===")
	fmt.Println()
	fmt.Println("Для запуска бенчмарков Go используйте:")
	fmt.Println("  go test -bench=. -benchmem -benchtime=10s")
}
