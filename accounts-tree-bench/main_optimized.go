package main

import (
	"crypto/rand"
	"encoding/binary"
	"fmt"
	"runtime"
	"sync"
	"sync/atomic"
	"time"

	"github.com/zeebo/blake3"
)

// ================= CONSTANTS & TYPES =================

// AccountStatus - 1 байт
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

// OptimizedAccount - оптимизированная по памяти структура (Aligned)
// Убрали Email [64]byte, заменили на EmailHash [8]byte для компактности (симуляция внешней БД)
// Размер: 8 + 8 + 32 + 8 + 1 = 57 байт -> выравнивается до 64 байт (Cache Line friendly)
type OptimizedAccount struct {
	PublicKey [32]byte      // 32 байта
	UID       uint64        // 8 байт
	Key       [8]byte       // 8 байт (BigEndian UID)
	EmailHash uint64        // 8 байт (вместо полной строки)
	Status    AccountStatus // 1 байт
	_         [7]byte       // Padding для выравнивания до 64 байт
}

// FlatNode - узел для линеаризованного дерева (Flattened)
// Вместо map и указателей используем индексы и compact arrays
type FlatNode struct {
	Hash [32]byte
	// Compact sparse children storage
	// Для Arity 64 храним ключи и индексы в параллельных массивах.
	// Макс 64 ребенка. Для оптимизации используем fixed-size arrays или slices из пула.
	// Чтобы node был compact, используем slice headers, которые указывают на данные в Arena.
	ChildrenKeys    []byte   // Ключи (частичные пути)
	ChildrenIndices []uint32 // Индексы дочерних узлов в массиве nodes

	AccountIndex uint32 // Индекс аккаунта в массиве accounts (если IsLeaf)
	IsLeaf       bool
	_            [3]byte // Padding
}

// ================= ARENA ALLOCATOR =================

const (
	NodeBlockSize    = 1024 * 1024 // 1M nodes per block
	AccountBlockSize = 1024 * 1024 // 1M accounts per block
)

// NodeArena - управляет памятью для узлов
type NodeArena struct {
	blocks     [][]FlatNode
	activeBlock []FlatNode
	activeIndex int
	mu         sync.Mutex
}

func NewNodeArena() *NodeArena {
	return &NodeArena{
		blocks: make([][]FlatNode, 0, 16),
	}
}

// Alloc выделяет новый узел и возвращает его индекс (глобальный uint32 не подходит для >4B,
// но для теста 11M узлов uint32 (4 млрд) достаточно).
// Возвращаем указатель для удобства работы, но данные лежат плотно.
// В реальной flat-структуре мы бы оперировали индексами (blockID, localID).
// Для упрощения сделаем Alloc возвращающим *FlatNode, но гарантирующим локальность.
func (a *NodeArena) Alloc() *FlatNode {
	a.mu.Lock()
	defer a.mu.Unlock()

	if len(a.activeBlock) == 0 || a.activeIndex >= len(a.activeBlock) {
		// Allocate new block
		newBlock := make([]FlatNode, NodeBlockSize)
		a.blocks = append(a.blocks, newBlock)
		a.activeBlock = newBlock
		a.activeIndex = 0
	}

	node := &a.activeBlock[a.activeIndex]
	a.activeIndex++
	return node
}

// ================= FLAT TREE IMPLEMENTATION =================

type FlatTree struct {
	Root      *FlatNode
	Accounts  map[uint64]*OptimizedAccount // Map все еще нужна для O(1) доступа по UID
	NodeArena *NodeArena
	Cache     *ShardedLRUCache
	LeafArity int
	MaxDepth  int
	mu        sync.RWMutex
}

// LRU Cache Implementation (Same as before)
type LRUCache struct {
	capacity int
	cache    map[uint64]*cacheEntry
	head     *cacheEntry
	tail     *cacheEntry
	mu       sync.Mutex
}
type cacheEntry struct {
	key   uint64
	value *OptimizedAccount
	prev  *cacheEntry
	next  *cacheEntry
}
func NewLRUCache(capacity int) *LRUCache {
	return &LRUCache{capacity: capacity, cache: make(map[uint64]*cacheEntry)}
}
func (lru *LRUCache) Get(key uint64) (*OptimizedAccount, bool) {
	lru.mu.Lock(); defer lru.mu.Unlock()
	if entry, ok := lru.cache[key]; ok {
		lru.moveToFront(entry); return entry.value, true
	}
	return nil, false
}
func (lru *LRUCache) Put(key uint64, value *OptimizedAccount) {
	lru.mu.Lock(); defer lru.mu.Unlock()
	if entry, ok := lru.cache[key]; ok {
		entry.value = value; lru.moveToFront(entry); return
	}
	newEntry := &cacheEntry{key: key, value: value}
	lru.cache[key] = newEntry; lru.addToFront(newEntry)
	if len(lru.cache) > lru.capacity { lru.removeTail() }
}
func (lru *LRUCache) moveToFront(entry *cacheEntry) {
	if entry == lru.head { return }
	lru.remove(entry); lru.addToFront(entry)
}
func (lru *LRUCache) addToFront(entry *cacheEntry) {
	entry.next = lru.head; entry.prev = nil
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
	delete(lru.cache, lru.tail.key); lru.remove(lru.tail)
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
func (s *ShardedLRUCache) Get(key uint64) (*OptimizedAccount, bool) {
	return s.shards[key&s.shardMask].Get(key)
}
func (s *ShardedLRUCache) Put(key uint64, value *OptimizedAccount) {
	s.shards[key&s.shardMask].Put(key, value)
}

// NewFlatTree создает оптимизированное дерево
func NewFlatTree(leafArity int, cacheSize int) *FlatTree {
	arena := NewNodeArena()
	root := arena.Alloc()

	// Pre-allocate children capacity for root to avoid re-allocs
	root.ChildrenKeys = make([]byte, 0, 64)
	root.ChildrenIndices = make([]uint32, 0, 64)

	return &FlatTree{
		Root:      root,
		Accounts:  make(map[uint64]*OptimizedAccount, 11_000_000), // Hint size
		NodeArena: arena,
		Cache:     NewShardedLRUCache(cacheSize, 8), // 256 shards
		LeafArity: leafArity,
		MaxDepth:  3,
	}
}

// Insert optimized
func (tree *FlatTree) Insert(account *OptimizedAccount) {
	tree.mu.Lock()
	tree.Accounts[account.UID] = account
	tree.insertNode(tree.Root, account, 0)
	tree.mu.Unlock()

	tree.Cache.Put(account.UID, account)
}

func (tree *FlatTree) insertNode(node *FlatNode, account *OptimizedAccount, depth int) {
	if depth >= tree.MaxDepth-1 {
		// Leaf level
		idx := account.Key[depth]

		// Linear scan for child (fast for small arrays, <64 items)
		// FlatNode uses parallel arrays: ChildrenKeys[i] corresponds to ChildrenIndices[i]
		found := false
		for i, key := range node.ChildrenKeys {
			if key == idx {
				// Child exists, update it
				// Note: ChildrenIndices here stores POINTERS to nodes in Arena conceptually.
				// But since we pass *FlatNode directly in recursion, we need to map back.
				// In this simplified FlatTree, we don't store "index", we store pointers in recursion
				// but structure keeps structure flat. 
				// Wait, FlatNode definition has `ChildrenIndices []uint32`.
				// We need to resolve uint32 index to *FlatNode. 
				// To make this simple and fast without implementing full index-pointer arithmetic 
				// in this demo, let's cheat slightly: 
				// We will use `ChildrenNodes []*FlatNode` for speed in this demo version, 
				// but allocated from Arena. Full index-based addressing requires passing Arena pointer everywhere.
				// Let's stick to Arena allocation but use slice of pointers for children to keep code simple.
				// REAL Flattening would use indices.
				found = true

				// In a full implementation, we would fetch node by index.
				// For this hybrid demo to show Slab Allocator benefit:
				// We need to change FlatNode definition slightly to hold pointers 
				// OR implement lookup. Let's change FlatNode to use pointers for simplicity of code
				// BUT keep them allocated from Arena (Slab).
				break
			}
			_ = i
		}

		if !found {
			// Create new leaf node from Arena
			child := tree.NodeArena.Alloc()
			child.IsLeaf = true
			child.Hash = account.Hash()
			// AccountIndex logic omitted, we store account in map/leaf

			node.ChildrenKeys = append(node.ChildrenKeys, idx)
			// node.ChildrenIndices = append(node.ChildrenIndices, ... ) 
			// See note above: implementing pure index-based tree in one file is complex.
			// Let's refactor FlatNode to use Pointers but allocated from Slab.
		}
		return
	}
	// ... (Recursive logic similar to standard tree but using arrays)
}

// RE-IMPLEMENTATION WITH POINTERS BUT SLAB ALLOCATION
// This gives 80% of performance benefit (locality) with 20% of code complexity.

type SlabNode struct {
	Hash     [32]byte
	Children []*SlabNode // Slice of pointers, but pointers point to Arena memory
	Keys     []byte      // Parallel array of keys
	Account  *OptimizedAccount
	IsLeaf   bool
}

type SlabTree struct {
	Root      *SlabNode
	Accounts  map[uint64]*OptimizedAccount
	Arena     *SlabArena
	Cache     *ShardedLRUCache
	MaxDepth  int
	mu        sync.RWMutex
}

type SlabArena struct {
	blocks     [][]SlabNode
	activeBlock []SlabNode
	activeIndex int
	mu         sync.Mutex
}

func NewSlabArena() *SlabArena {
	return &SlabArena{blocks: make([][]SlabNode, 0, 16)}
}

func (a *SlabArena) Alloc() *SlabNode {
	a.mu.Lock()
	defer a.mu.Unlock()
	if len(a.activeBlock) == 0 || a.activeIndex >= len(a.activeBlock) {
		newBlock := make([]SlabNode, NodeBlockSize)
		a.blocks = append(a.blocks, newBlock)
		a.activeBlock = newBlock
		a.activeIndex = 0
	}
	node := &a.activeBlock[a.activeIndex]
	a.activeIndex++
	return node
}

func NewSlabTree(cacheSize int) *SlabTree {
	arena := NewSlabArena()
	root := arena.Alloc()
	return &SlabTree{
		Root:     root,
		Accounts: make(map[uint64]*OptimizedAccount, 11_000_000),
		Arena:    arena,
		Cache:    NewShardedLRUCache(cacheSize, 8),
		MaxDepth: 3,
	}
}

func (tree *SlabTree) Insert(account *OptimizedAccount) {
	tree.mu.Lock()
	tree.Accounts[account.UID] = account
	tree.insertNode(tree.Root, account, 0)
	tree.mu.Unlock()
	tree.Cache.Put(account.UID, account)
}

func (tree *SlabTree) insertNode(node *SlabNode, account *OptimizedAccount, depth int) {
	if depth >= tree.MaxDepth-1 {
		idx := account.Key[depth]
		// Linear search in compact arrays (Cache friendly)
		for i, key := range node.Keys {
			if key == idx {
				child := node.Children[i]
				child.Account = account
				child.Hash = account.Hash()
				return
			}
		}
		// Not found, alloc new
		child := tree.Arena.Alloc()
		child.IsLeaf = true
		child.Account = account
		child.Hash = account.Hash()

		node.Keys = append(node.Keys, idx)
		node.Children = append(node.Children, child)
		return
	}

	idx := account.Key[depth]
	for i, key := range node.Keys {
		if key == idx {
			tree.insertNode(node.Children[i], account, depth+1)
			return
		}
	}

	child := tree.Arena.Alloc()
	node.Keys = append(node.Keys, idx)
	node.Children = append(node.Children, child)
	tree.insertNode(child, account, depth+1)
}

func (tree *SlabTree) Get(uid uint64) (*OptimizedAccount, bool) {
	if acc, ok := tree.Cache.Get(uid); ok { return acc, true }
	tree.mu.RLock()
	acc, ok := tree.Accounts[uid]
	tree.mu.RUnlock()
	if ok { tree.Cache.Put(uid, acc); return acc, true }
	return nil, false
}

func (tree *SlabTree) ComputeRoot() [32]byte {
	tree.mu.RLock(); defer tree.mu.RUnlock()
	return tree.computeNodeHash(tree.Root)
}

func (tree *SlabTree) computeNodeHash(node *SlabNode) [32]byte {
	if node == nil { return [32]byte{} }
	if node.IsLeaf && node.Account != nil { return node.Account.Hash() }

	hasher := blake3.New()

	// Need to sort children by key for deterministic hash
	// Since we store them in insertion order, we need to copy and sort indices
	count := len(node.Keys)
	if count == 0 { return [32]byte{} }

	indices := make([]int, count)
	for i := 0; i < count; i++ { indices[i] = i }

	// Sort indices based on keys (Bubble sort is fine for <64 items and zero allocs if optimized,
	// but here we just do simple sort)
	for i := 0; i < count-1; i++ {
		for j := 0; j < count-i-1; j++ {
			if node.Keys[indices[j]] > node.Keys[indices[j+1]] {
				indices[j], indices[j+1] = indices[j+1], indices[j]
			}
		}
	}

	for _, idx := range indices {
		childHash := tree.computeNodeHash(node.Children[idx])
		hasher.Write([]byte{node.Keys[idx]})
		hasher.Write(childHash[:])
	}

	var result [32]byte
	copy(result[:], hasher.Sum(nil))
	return result
}

func (a *OptimizedAccount) Hash() [32]byte {
	hasher := blake3.New()
	hasher.Write(a.Key[:])
	binary.Write(hasher, binary.BigEndian, a.EmailHash) // Hash 8 bytes instead of 64
	hasher.Write([]byte{byte(a.Status)})
	hasher.Write(a.PublicKey[:])
	var res [32]byte
	copy(res[:], hasher.Sum(nil))
	return res
}

func GenerateOptimizedAccount(uid uint64, status AccountStatus) *OptimizedAccount {
	acc := &OptimizedAccount{UID: uid, Status: status}
	binary.BigEndian.PutUint64(acc.Key[:], uid)
	// Fake email hash
	acc.EmailHash = uid ^ 0xCAFEBABE
	_, _ = rand.Read(acc.PublicKey[:])
	return acc
}

// Helper for benchmarks
func runConcurrentReadTest(tree *SlabTree, numReaders int, duration time.Duration, totalAccounts uint64) {
	var ops atomic.Uint64
	done := make(chan struct{})
	for i := 0; i < numReaders; i++ {
		go func() {
			rngState := uint64(time.Now().UnixNano()) + uint64(i)*12345
			for {
				select {
				case <-done: return
				default:
					rngState ^= rngState << 13; rngState ^= rngState >> 7; rngState ^= rngState << 17
					tree.Get(rngState % totalAccounts)
					ops.Add(1)
				}
			}
		}()
	}
	time.Sleep(duration); close(done)
	opsSec := float64(ops.Load()) / duration.Seconds()
	fmt.Printf("   Readers: %d | Speed: %.0f ops/sec\n", numReaders, opsSec)
}

func main() {
	runtime.GOMAXPROCS(runtime.NumCPU())
	fmt.Println("=== Highly Optimized Slab-Allocated Merkle Tree ===")
	fmt.Println("Features: Arena Allocation, Struct Padding, Compact Children Arrays")

	tree := NewSlabTree(100000)

	fmt.Println("1. Filling 11M accounts...")
	start := time.Now()
	for i := uint64(0); i < 11_000_000; i++ {
		tree.Insert(GenerateOptimizedAccount(i, StatusUser))
	}
	fmt.Printf("   Time: %v\n", time.Since(start))

	fmt.Println("2. Root Computation...")
	start = time.Now()
	root := tree.ComputeRoot()
	fmt.Printf("   Root: %x | Time: %v\n", root[:16], time.Since(start))

	fmt.Println("3. Concurrent Read Test (Pure Read)...")
	for _, t := range []int{1, 32} {
		runConcurrentReadTest(tree, t, 2*time.Second, 11_000_000)
	}

	// Memory stats
	var m runtime.MemStats
	runtime.ReadMemStats(&m)
	fmt.Printf("4. Heap Alloc: %v MB\n", m.HeapAlloc/1024/1024)
}
