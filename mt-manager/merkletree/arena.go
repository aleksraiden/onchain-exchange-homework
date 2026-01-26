package merkletree

import (
	"sync"
	"sync/atomic"
)

//const NodeBlockSize = 1024 * 1024

// Arena аллокатор узлов дерева с пулом для переиспользования
type Arena[T Hashable] struct {
	blocks       [][]Node[T]
	activeBlock  []Node[T]
	activeIndex  int
	blockSize    int
	mu           sync.Mutex
	allocCounter atomic.Uint64
	nodePool     sync.Pool // Пул для переиспользования
}

// newArena создает новую арену с пулом
func newArena[T Hashable]() *Arena[T] {
	arena := &Arena[T]{
		blocks:    make([][]Node[T], 0, 16),
		blockSize: NodeBlockSize,
	}

	// Инициализируем пул
	arena.nodePool = sync.Pool{
		New: func() interface{} {
			return &Node[T]{}
		},
	}

	arena.allocNewBlock()
	return arena
}

// alloc выделяет узел из арены
func (a *Arena[T]) alloc() *Node[T] {
	a.mu.Lock()
	defer a.mu.Unlock()

	if a.activeIndex >= a.blockSize {
		a.allocNewBlock()
	}

	node := &a.activeBlock[a.activeIndex]
	a.activeIndex++
	a.allocCounter.Add(1)

	return node
}

// allocFromPool попытка взять из пула (опциональная оптимизация)
func (a *Arena[T]) allocFromPool() *Node[T] {
	if node := a.nodePool.Get().(*Node[T]); node != nil {
		// Очищаем узел
		node.Hash = [32]byte{}
		node.Children = node.Children[:0]
		node.Keys = node.Keys[:0]
		node.IsLeaf = false
		node.dirty.Store(false)
		return node
	}
	return a.alloc()
}

// free возвращает узел в пул
func (a *Arena[T]) free(node *Node[T]) {
	a.nodePool.Put(node)
}

// allocNewBlock выделяет новый блок (должен вызываться под блокировкой)
func (a *Arena[T]) allocNewBlock() {
	newBlock := make([]Node[T], a.blockSize)
	a.blocks = append(a.blocks, newBlock)
	a.activeBlock = newBlock
	a.activeIndex = 0
}

// allocated возвращает количество аллоцированных узлов
func (a *Arena[T]) allocated() int {
	return int(a.allocCounter.Load())
}

// reset сбрасывает арену для переиспользования
func (a *Arena[T]) reset() {
	a.mu.Lock()
	defer a.mu.Unlock()

	// Очищаем блоки
	a.blocks = make([][]Node[T], 0, 16)
	a.allocNewBlock()
	a.allocCounter.Store(0)
}
