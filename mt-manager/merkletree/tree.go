package merkletree

import (
	"io"
	"runtime"
	"sync"
	"sync/atomic"

	"github.com/zeebo/blake3"
)

const (
	// NodeBlockSize - размер блока для arena allocator
	NodeBlockSize = 1024 * 1024

	// SmallBatchThreshold - порог для маленьких батчей (используем простую блокировку)
	SmallBatchThreshold = 100

	// ParallelBatchThreshold - порог для параллельных батчей
	ParallelBatchThreshold = 500
)

// Tree представляет полностью оптимизированное Merkle-дерево
// Включает: lazy hashing, lock-free reads, adaptive locking, parallel batch
type Tree[T Hashable] struct {
	root       *Node[T]
	items      sync.Map      // Lock-free concurrent map для быстрых reads
	itemCount  atomic.Uint64 // Атомарный счетчик элементов
	arena      *Arena[T]
	cache      *ShardedCache[T]
	maxDepth   int
	mu         sync.RWMutex // Для структурных операций (Clear, small batches)

	// Lazy hashing - не пересчитываем хеши при каждой вставке
	dirtyNodes     atomic.Uint64 // Счетчик "грязных" узлов
	cachedRoot     [32]byte      // Кешированный корень
	rootCacheValid atomic.Bool   // Валиден ли кеш корня

	// Метрики производительности
	insertCount        atomic.Uint64
	getCount           atomic.Uint64
	cacheHits          atomic.Uint64
	cacheMisses        atomic.Uint64
	computeCount       atomic.Uint64
	batchCount         atomic.Uint64
	simpleBatchCount   atomic.Uint64 // Простые батчи (глобальная блокировка)
	parallelBatchCount atomic.Uint64 // Параллельные батчи (per-node locks)
}

// Node узел с fine-grained locking и lazy hashing
type Node[T Hashable] struct {
	Hash     [32]byte
	Children []*Node[T]
	Keys     []byte
	Value    T
	IsLeaf   bool
	mu       sync.RWMutex // Per-node lock для concurrent доступа
	dirty    atomic.Bool  // Требуется пересчет хеша
}

// New создает новое полностью оптимизированное Merkle-дерево
func New[T Hashable](cfg *Config) *Tree[T] {
	if cfg == nil {
		cfg = DefaultConfig()
	}

	arena := newArena[T]()
	root := arena.alloc()

	return &Tree[T]{
		root:     root,
		arena:    arena,
		cache:    newShardedCache[T](cfg.CacheSize, cfg.CacheShards),
		maxDepth: cfg.MaxDepth,
	}
}

// Insert вставляет элемент (одиночные вставки используют per-node locking)
func (t *Tree[T]) Insert(item T) {
	// Добавляем в sync.Map (lock-free операция)
	t.items.Store(item.ID(), item)
	t.itemCount.Add(1)

	// Добавляем в кеш
	t.cache.put(item.ID(), item)

	// Одиночные вставки обычно происходят параллельно из разных горутин
	// Используем per-node locking для максимального параллелизма
	t.insertNodeConcurrent(t.root, item, 0)

	// Помечаем корень грязным, но НЕ пересчитываем хеши (lazy)
	t.rootCacheValid.Store(false)
	t.insertCount.Add(1)
}

// InsertBatch оптимизированная пакетная вставка с адаптивной стратегией
func (t *Tree[T]) InsertBatch(items []T) {
	if len(items) == 0 {
		return
	}

	batchSize := len(items)

	// Адаптивная стратегия блокировок:
	// 1. Маленькие батчи (<100): глобальная блокировка (меньше overhead)
	// 2. Средние батчи (100-500): последовательно с per-node locks
	// 3. Большие батчи (>500): параллельно с per-node locks

	if batchSize < SmallBatchThreshold {
		t.insertBatchSimple(items)
		t.simpleBatchCount.Add(1)
	} else if batchSize >= ParallelBatchThreshold && runtime.NumCPU() > 1 {
		t.insertBatchParallel(items)
		t.parallelBatchCount.Add(1)
	} else {
		t.insertBatchSequential(items)
	}

	t.batchCount.Add(1)
	t.insertCount.Add(uint64(len(items)))
}

// insertBatchSimple для маленьких батчей - одна глобальная блокировка
func (t *Tree[T]) insertBatchSimple(items []T) {
	// Фаза 1: Добавляем в maps (без блокировок)
	for _, item := range items {
		t.items.Store(item.ID(), item)
		t.cache.put(item.ID(), item)
	}
	t.itemCount.Add(uint64(len(items)))

	// Фаза 2: Одна глобальная блокировка для всего батча
	t.mu.Lock()
	for _, item := range items {
		t.insertNodeSimple(t.root, item, 0)
	}
	t.mu.Unlock()

	t.rootCacheValid.Store(false)
}

// insertBatchSequential последовательная батчевая вставка с per-node locks
func (t *Tree[T]) insertBatchSequential(items []T) {
	// Фаза 1: Быстрое добавление в maps (без блокировок)
	for _, item := range items {
		t.items.Store(item.ID(), item)
		t.cache.put(item.ID(), item)
	}
	t.itemCount.Add(uint64(len(items)))

	// Фаза 2: Вставка в структуру дерева (per-node locking)
	for _, item := range items {
		t.insertNodeConcurrent(t.root, item, 0)
	}

	t.rootCacheValid.Store(false)
}

// insertBatchParallel параллельная батчевая вставка для больших батчей
func (t *Tree[T]) insertBatchParallel(items []T) {
	numWorkers := runtime.NumCPU()
	chunkSize := (len(items) + numWorkers - 1) / numWorkers

	var wg sync.WaitGroup

	// Фаза 1: Параллельно добавляем в maps
	for i := 0; i < numWorkers; i++ {
		start := i * chunkSize
		end := start + chunkSize
		if end > len(items) {
			end = len(items)
		}

		wg.Add(1)
		go func(chunk []T) {
			defer wg.Done()
			for _, item := range chunk {
				t.items.Store(item.ID(), item)
				t.cache.put(item.ID(), item)
			}
		}(items[start:end])
	}

	wg.Wait()
	t.itemCount.Add(uint64(len(items)))

	// Фаза 2: Параллельно вставляем в структуру (безопасно с per-node locks)
	for i := 0; i < numWorkers; i++ {
		start := i * chunkSize
		end := start + chunkSize
		if end > len(items) {
			end = len(items)
		}

		wg.Add(1)
		go func(chunk []T) {
			defer wg.Done()
			for _, item := range chunk {
				t.insertNodeConcurrent(t.root, item, 0)
			}
		}(items[start:end])
	}

	wg.Wait()
	t.rootCacheValid.Store(false)
}

// insertNodeSimple простая вставка БЕЗ per-node locks
// Вызывается только под глобальной блокировкой t.mu
func (t *Tree[T]) insertNodeSimple(node *Node[T], item T, depth int) {
	key := item.Key()

	if depth >= t.maxDepth-1 {
		// Уровень листьев
		idx := key[depth]

		// Ищем существующий ключ
		for i, k := range node.Keys {
			if k == idx {
				child := node.Children[i]
				child.Value = item
				child.Hash = item.Hash()
				child.dirty.Store(false)
				node.dirty.Store(true)
				t.dirtyNodes.Add(1)
				return
			}
		}

		// Создаем новый лист
		child := t.arena.alloc()
		child.IsLeaf = true
		child.Value = item
		child.Hash = item.Hash()
		node.Keys = append(node.Keys, idx)
		node.Children = append(node.Children, child)
		node.dirty.Store(true)
		t.dirtyNodes.Add(1)
		return
	}

	// Промежуточный узел
	idx := key[depth]
	for i, k := range node.Keys {
		if k == idx {
			t.insertNodeSimple(node.Children[i], item, depth+1)
			node.dirty.Store(true)
			t.dirtyNodes.Add(1)
			return
		}
	}

	// Новая ветка
	child := t.arena.alloc()
	node.Keys = append(node.Keys, idx)
	node.Children = append(node.Children, child)
	node.dirty.Store(true)
	t.dirtyNodes.Add(1)

	t.insertNodeSimple(child, item, depth+1)
}

// insertNodeConcurrent вставка с per-node locking (для параллельного доступа)
func (t *Tree[T]) insertNodeConcurrent(node *Node[T], item T, depth int) {
	key := item.Key()

	// Блокируем ТОЛЬКО текущий узел
	node.mu.Lock()

	if depth >= t.maxDepth-1 {
		// Уровень листьев
		idx := key[depth]

		// Ищем существующий ключ
		for i, k := range node.Keys {
			if k == idx {
				child := node.Children[i]
				node.mu.Unlock() // Освобождаем родителя

				// Обновляем лист
				child.mu.Lock()
				child.Value = item
				child.Hash = item.Hash()
				child.dirty.Store(false)
				child.mu.Unlock()

				node.dirty.Store(true)
				t.dirtyNodes.Add(1)
				return
			}
		}

		// Создаем новый лист
		child := t.arena.alloc()
		child.IsLeaf = true
		child.Value = item
		child.Hash = item.Hash()
		node.Keys = append(node.Keys, idx)
		node.Children = append(node.Children, child)
		node.dirty.Store(true)
		t.dirtyNodes.Add(1)
		node.mu.Unlock()
		return
	}

	// Промежуточный узел
	idx := key[depth]
	for i, k := range node.Keys {
		if k == idx {
			child := node.Children[i]
			node.mu.Unlock() // Освобождаем родителя ПЕРЕД рекурсией
			t.insertNodeConcurrent(child, item, depth+1)
			node.dirty.Store(true)
			t.dirtyNodes.Add(1)
			return
		}
	}

	// Новая ветка
	child := t.arena.alloc()
	node.Keys = append(node.Keys, idx)
	node.Children = append(node.Children, child)
	node.dirty.Store(true)
	t.dirtyNodes.Add(1)

	node.mu.Unlock() // Освобождаем перед рекурсией
	t.insertNodeConcurrent(child, item, depth+1)
}

// Get возвращает элемент по ID (полностью lock-free для cache hits)
func (t *Tree[T]) Get(id uint64) (T, bool) {
	t.getCount.Add(1)

	// Проверка кеша (lock-free)
	if item, ok := t.cache.get(id); ok {
		t.cacheHits.Add(1)
		return item, true
	}

	// Поиск в sync.Map (lock-free)
	if val, ok := t.items.Load(id); ok {
		item := val.(T)
		t.cache.put(id, item)
		t.cacheMisses.Add(1)
		return item, true
	}

	var zero T
	t.cacheMisses.Add(1)
	return zero, false
}

// ComputeRoot вычисляет корневой хеш (с использованием кеша)
func (t *Tree[T]) ComputeRoot() [32]byte {
	// Проверяем кеш корня
	if t.rootCacheValid.Load() {
		return t.cachedRoot
	}

	root := t.computeNodeHash(t.root, 0)

	// Сохраняем в кеш
	t.cachedRoot = root
	t.rootCacheValid.Store(true)
	t.dirtyNodes.Store(0)
	t.computeCount.Add(1)

	return root
}

// ComputeRootParallel параллельное вычисление корня для больших деревьев
func (t *Tree[T]) ComputeRootParallel() [32]byte {
	// Проверяем кеш
	if t.rootCacheValid.Load() {
		return t.cachedRoot
	}

	root := t.computeNodeHashParallel(t.root, 0)

	t.cachedRoot = root
	t.rootCacheValid.Store(true)
	t.dirtyNodes.Store(0)
	t.computeCount.Add(1)

	return root
}

// computeNodeHash рекурсивное вычисление с per-node locking
func (t *Tree[T]) computeNodeHash(node *Node[T], depth int) [32]byte {
	if node == nil {
		return [32]byte{}
	}

	// Read lock только для этого узла
	node.mu.RLock()

	// Если узел не грязный - возвращаем кеш
	if !node.dirty.Load() && node.Hash != [32]byte{} {
		hash := node.Hash
		node.mu.RUnlock()
		return hash
	}

	// Для листьев
	if node.IsLeaf {
		var hash [32]byte
		if any(node.Value) != nil {
			hash = node.Value.Hash()
		}
		node.mu.RUnlock()

		// Обновляем узел
		node.mu.Lock()
		node.Hash = hash
		node.dirty.Store(false)
		node.mu.Unlock()

		return hash
	}

	count := len(node.Keys)
	if count == 0 {
		node.mu.RUnlock()
		return [32]byte{}
	}

	// Копируем данные под read lock
	indices := make([]int, count)
	for i := range indices {
		indices[i] = i
	}

	keys := make([]byte, count)
	copy(keys, node.Keys)
	children := make([]*Node[T], count)
	copy(children, node.Children)

	node.mu.RUnlock() // Освобождаем рано, чтобы разрешить параллельный доступ

	// Сортировка индексов
	for i := 0; i < count-1; i++ {
		for j := i + 1; j < count; j++ {
			if keys[indices[i]] > keys[indices[j]] {
				indices[i], indices[j] = indices[j], indices[i]
			}
		}
	}

	// Вычисляем хеши детей БЕЗ блокировки родителя
	hasher := blake3.New()
	for _, idx := range indices {
		childHash := t.computeNodeHash(children[idx], depth+1)
		hasher.Write([]byte{keys[idx]})
		hasher.Write(childHash[:])
	}

	var result [32]byte
	copy(result[:], hasher.Sum(nil))

	// Обновляем узел
	node.mu.Lock()
	node.Hash = result
	node.dirty.Store(false)
	node.mu.Unlock()

	return result
}

// computeNodeHashParallel параллельное вычисление для верхних уровней
func (t *Tree[T]) computeNodeHashParallel(node *Node[T], depth int) [32]byte {
	if node == nil {
		return [32]byte{}
	}

	node.mu.RLock()

	if !node.dirty.Load() && node.Hash != [32]byte{} {
		hash := node.Hash
		node.mu.RUnlock()
		return hash
	}

	if node.IsLeaf {
		var hash [32]byte
		if any(node.Value) != nil {
			hash = node.Value.Hash()
		}
		node.mu.RUnlock()

		node.mu.Lock()
		node.Hash = hash
		node.dirty.Store(false)
		node.mu.Unlock()

		return hash
	}

	count := len(node.Keys)
	if count == 0 {
		node.mu.RUnlock()
		return [32]byte{}
	}

	// Параллелизм только на верхних уровнях с достаточным количеством детей
	useParallel := depth < 2 && count >= 4 && runtime.NumCPU() > 1

	indices := make([]int, count)
	for i := range indices {
		indices[i] = i
	}

	keys := make([]byte, count)
	copy(keys, node.Keys)
	children := make([]*Node[T], count)
	copy(children, node.Children)

	node.mu.RUnlock()

	// Сортировка
	for i := 0; i < count-1; i++ {
		for j := i + 1; j < count; j++ {
			if keys[indices[i]] > keys[indices[j]] {
				indices[i], indices[j] = indices[j], indices[i]
			}
		}
	}

	// Вычисляем хеши детей
	childHashes := make([][32]byte, count)

	if useParallel {
		// Параллельно
		var wg sync.WaitGroup
		for i, idx := range indices {
			wg.Add(1)
			go func(i, idx int) {
				defer wg.Done()
				childHashes[i] = t.computeNodeHashParallel(children[idx], depth+1)
			}(i, idx)
		}
		wg.Wait()
	} else {
		// Последовательно
		for i, idx := range indices {
			childHashes[i] = t.computeNodeHash(children[idx], depth+1)
		}
	}

	// Хешируем результат
	hasher := blake3.New()
	for i, idx := range indices {
		hasher.Write([]byte{keys[idx]})
		hasher.Write(childHashes[i][:])
	}

	var result [32]byte
	copy(result[:], hasher.Sum(nil))

	node.mu.Lock()
	node.Hash = result
	node.dirty.Store(false)
	node.mu.Unlock()

	return result
}

// Size возвращает количество элементов (атомарное чтение)
func (t *Tree[T]) Size() int {
	return int(t.itemCount.Load())
}

// GetAllItems возвращает все элементы дерева
func (t *Tree[T]) GetAllItems() []T {
	items := make([]T, 0, t.itemCount.Load())

	t.items.Range(func(key, value interface{}) bool {
		items = append(items, value.(T))
		return true
	})

	return items
}

// GetDirtyNodeCount возвращает количество грязных узлов
func (t *Tree[T]) GetDirtyNodeCount() uint64 {
	return t.dirtyNodes.Load()
}

// Clear очищает дерево для переиспользования
func (t *Tree[T]) Clear() {
	t.mu.Lock()
	defer t.mu.Unlock()

	t.items = sync.Map{}
	t.itemCount.Store(0)
	t.arena.reset()
	t.root = t.arena.alloc()
	t.cache.clear()
	t.rootCacheValid.Store(false)
	t.dirtyNodes.Store(0)
}

// Stats статистика дерева
type Stats struct {
	TotalItems       int
	AllocatedNodes   int
	CacheSize        int
	DirtyNodes       uint64
	InsertCount      uint64
	GetCount         uint64
	CacheHits        uint64
	CacheMisses      uint64
	CacheHitRate     float64
	ComputeCount     uint64
	BatchCount       uint64
	SimpleBatches    uint64 // Батчи с глобальной блокировкой
	ParallelBatches  uint64 // Батчи с параллелизмом
	SequentialBatches uint64 // Батчи последовательные с per-node locks
}

// GetStats возвращает полную статистику дерева
func (t *Tree[T]) GetStats() Stats {
	hits := t.cacheHits.Load()
	misses := t.cacheMisses.Load()
	hitRate := 0.0
	if hits+misses > 0 {
		hitRate = float64(hits) / float64(hits+misses) * 100
	}

	batchCount := t.batchCount.Load()
	simpleBatches := t.simpleBatchCount.Load()
	parallelBatches := t.parallelBatchCount.Load()
	sequentialBatches := batchCount - simpleBatches - parallelBatches

	return Stats{
		TotalItems:        int(t.itemCount.Load()),
		AllocatedNodes:    t.arena.allocated(),
		CacheSize:         t.cache.size(),
		DirtyNodes:        t.dirtyNodes.Load(),
		InsertCount:       t.insertCount.Load(),
		GetCount:          t.getCount.Load(),
		CacheHits:         hits,
		CacheMisses:       misses,
		CacheHitRate:      hitRate,
		ComputeCount:      t.computeCount.Load(),
		BatchCount:        batchCount,
		SimpleBatches:     simpleBatches,
		ParallelBatches:   parallelBatches,
		SequentialBatches: sequentialBatches,
	}
}

// CacheStats статистика кеша
type CacheStats struct {
	Size     int
	Capacity int
	Usage    float64
}

// CacheStats возвращает статистику кеша
func (t *Tree[T]) CacheStats() CacheStats {
	capacity := t.getCacheCapacity()
	size := t.cache.size()
	usage := 0.0
	if capacity > 0 {
		usage = float64(size) / float64(capacity) * 100
	}

	return CacheStats{
		Size:     size,
		Capacity: capacity,
		Usage:    usage,
	}
}

// getCacheCapacity возвращает емкость кеша
func (t *Tree[T]) getCacheCapacity() int {
	capacity := 0
	for _, shard := range t.cache.shards {
		shard.mu.Lock()
		capacity += shard.capacity
		shard.mu.Unlock()
	}
	return capacity
}

// GetSnapshotter возвращает snapshotter для дерева
func (t *Tree[T]) GetSnapshotter(storage SnapshotStorage) Snapshotter {
	return NewTreeSnapshot(t, storage)
}

// SaveToSnapshot создает и сохраняет снапшот
func (t *Tree[T]) SaveToSnapshot(w io.Writer, config *SnapshotConfig) error {
	snapshotter := NewTreeSnapshot(t, nil)
	return snapshotter.SaveSnapshot(w, config)
}

// LoadFromSnapshot загружает дерево из снапшота
func (t *Tree[T]) LoadFromSnapshot(r io.Reader) error {
	snapshotter := NewTreeSnapshot(t, nil)
	return snapshotter.LoadSnapshot(r)
}
