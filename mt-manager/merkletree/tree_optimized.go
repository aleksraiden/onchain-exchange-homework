package merkletree

import (
	"github.com/zeebo/blake3"
	"sync"
	"sync/atomic"
)

// OptimizedTree дерево с отложенным вычислением хешей
type OptimizedTree[T Hashable] struct {
	root           *OptimizedNode[T]
	items          map[uint64]T
	arena          *Arena[T]
	cache          *ShardedCache[T]
	maxDepth       int
	mu             sync.RWMutex
	dirtyNodes     atomic.Uint64 // Счетчик "грязных" узлов
	rootDirty      atomic.Bool   // Флаг: корень требует пересчета
	cachedRoot     [32]byte      // Кешированный корень
	rootCacheValid atomic.Bool   // Валиден ли кеш корня
}

// OptimizedNode узел с lazy hashing
type OptimizedNode[T Hashable] struct {
	Hash     [32]byte
	Children []*OptimizedNode[T]
	Keys     []byte
	Value    T
	IsLeaf   bool
	dirty    atomic.Bool // Требуется пересчет хеша
}

// NewOptimizedTree создает оптимизированное дерево
func NewOptimizedTree[T Hashable](cfg *Config) *OptimizedTree[T] {
	if cfg == nil {
		cfg = DefaultConfig()
	}

	arena := newArena[T]()
	root := &OptimizedNode[T]{}

	return &OptimizedTree[T]{
		root:     root,
		items:    make(map[uint64]T),
		arena:    arena,
		cache:    newShardedCache[T](cfg.CacheSize, cfg.CacheShards),
		maxDepth: cfg.MaxDepth,
	}
}

// Insert вставка БЕЗ немедленного пересчета хешей
func (t *OptimizedTree[T]) Insert(item T) {
	t.mu.Lock()
	defer t.mu.Unlock()

	t.items[item.ID()] = item
	t.insertNode(t.root, item, 0)
	t.cache.put(item.ID(), item)

	// Помечаем корень как грязный, но НЕ пересчитываем
	t.rootCacheValid.Store(false)
}

// insertNode вставка с пометкой dirty
func (t *OptimizedTree[T]) insertNode(node *OptimizedNode[T], item T, depth int) {
	key := item.Key()

	if depth >= t.maxDepth-1 {
		// Лист
		idx := key[depth]

		for i, k := range node.Keys {
			if k == idx {
				child := node.Children[i]
				child.Value = item
				child.Hash = item.Hash()
				child.dirty.Store(false)
				node.dirty.Store(true) // Помечаем родителя
				t.dirtyNodes.Add(1)
				return
			}
		}

		// Новый лист
		child := &OptimizedNode[T]{
			IsLeaf: true,
			Value:  item,
			Hash:   item.Hash(),
		}
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
			t.insertNode(node.Children[i], item, depth+1)
			node.dirty.Store(true)
			t.dirtyNodes.Add(1)
			return
		}
	}

	// Новая ветка
	child := &OptimizedNode[T]{}
	node.Keys = append(node.Keys, idx)
	node.Children = append(node.Children, child)
	node.dirty.Store(true)
	t.dirtyNodes.Add(1)

	t.insertNode(child, item, depth+1)
}

// ComputeRoot вычисление только при запросе
func (t *OptimizedTree[T]) ComputeRoot() [32]byte {
	// Проверяем кеш
	if t.rootCacheValid.Load() {
		return t.cachedRoot
	}

	t.mu.RLock()
	root := t.computeNodeHash(t.root)
	t.mu.RUnlock()

	// Сохраняем в кеш
	t.cachedRoot = root
	t.rootCacheValid.Store(true)
	t.dirtyNodes.Store(0)

	return root
}

// computeNodeHash вычисление с проверкой dirty
func (t *OptimizedTree[T]) computeNodeHash(node *OptimizedNode[T]) [32]byte {
	if node == nil {
		return [32]byte{}
	}

	// Если не грязный - возвращаем кешированный хеш
	if !node.dirty.Load() && node.Hash != [32]byte{} {
		return node.Hash
	}

	if node.IsLeaf {
		if any(node.Value) != nil {
			node.Hash = node.Value.Hash()
			node.dirty.Store(false)
			return node.Hash
		}
		return [32]byte{}
	}

	count := len(node.Keys)
	if count == 0 {
		return [32]byte{}
	}

	// Индексы для сортировки
	indices := make([]int, count)
	for i := range indices {
		indices[i] = i
	}

	// Пузырьковая сортировка (для небольших массивов быстрее)
	for i := 0; i < count-1; i++ {
		for j := i + 1; j < count; j++ {
			if node.Keys[indices[i]] > node.Keys[indices[j]] {
				indices[i], indices[j] = indices[j], indices[i]
			}
		}
	}

	// Хешируем
	hasher := blake3.New()
	for _, idx := range indices {
		childHash := t.computeNodeHash(node.Children[idx])
		hasher.Write([]byte{node.Keys[idx]})
		hasher.Write(childHash[:])
	}

	copy(node.Hash[:], hasher.Sum(nil))
	node.dirty.Store(false)

	return node.Hash
}

// GetDirtyNodeCount возвращает количество грязных узлов
func (t *OptimizedTree[T]) GetDirtyNodeCount() uint64 {
	return t.dirtyNodes.Load()
}

// Get без изменений
func (t *OptimizedTree[T]) Get(id uint64) (T, bool) {
	if item, ok := t.cache.get(id); ok {
		return item, true
	}

	t.mu.RLock()
	item, ok := t.items[id]
	t.mu.RUnlock()

	if ok {
		t.cache.put(id, item)
	}

	return item, ok
}

// Size возвращает размер
func (t *OptimizedTree[T]) Size() int {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return len(t.items)
}

// InsertBatch оптимизированная пакетная вставка
func (t *OptimizedTree[T]) InsertBatch(items []T) {
	t.mu.Lock()
	defer t.mu.Unlock()

	for _, item := range items {
		t.items[item.ID()] = item
		t.insertNode(t.root, item, 0)
		t.cache.put(item.ID(), item)
	}

	t.rootCacheValid.Store(false)
}
