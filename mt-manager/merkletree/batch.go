package merkletree

import (
	"fmt"
	"sync"
)

// BatchMode режим батча
type BatchMode int

const (
	BatchModeNone BatchMode = iota
	BatchModeActive
)

// TreeBatch представляет батч для дерева
type TreeBatch[T Hashable] struct {
	tree   *Tree[T]
	buffer []T
	active bool
	mu     sync.Mutex
}

// BeginBatch начинает батч для дерева
func (t *Tree[T]) BeginBatch() *TreeBatch[T] {
	return &TreeBatch[T]{
		tree:   t,
		buffer: make([]T, 0, 1000),
		active: true,
	}
}

// Insert добавляет элемент в батч
func (b *TreeBatch[T]) Insert(item T) error {
	b.mu.Lock()
	defer b.mu.Unlock()

	if !b.active {
		return fmt.Errorf("батч не активен")
	}

	b.buffer = append(b.buffer, item)
	return nil
}

// InsertMany добавляет несколько элементов в батч
func (b *TreeBatch[T]) InsertMany(items []T) error {
	b.mu.Lock()
	defer b.mu.Unlock()

	if !b.active {
		return fmt.Errorf("батч не активен")
	}

	b.buffer = append(b.buffer, items...)
	return nil
}

// Size возвращает размер буфера
func (b *TreeBatch[T]) Size() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return len(b.buffer)
}

// Commit применяет все изменения из батча
func (b *TreeBatch[T]) Commit() error {
	b.mu.Lock()
	defer b.mu.Unlock()

	if !b.active {
		return fmt.Errorf("батч не активен")
	}

	// Применяем все изменения одним батчем
	b.tree.InsertBatch(b.buffer)

	b.active = false
	b.buffer = nil

	return nil
}

// Rollback отменяет изменения в батче
func (b *TreeBatch[T]) Rollback() {
	b.mu.Lock()
	defer b.mu.Unlock()

	b.active = false
	b.buffer = nil
}

// IsActive проверяет, активен ли батч
func (b *TreeBatch[T]) IsActive() bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.active
}

// ManagerBatch представляет батч для менеджера
type ManagerBatch[T Hashable] struct {
	manager     *TreeManager[T]
	treeBatches map[string]*TreeBatch[T]
	active      bool
	mu          sync.Mutex
}

// BeginBatch начинает батч для менеджера
func (m *TreeManager[T]) BeginBatch() *ManagerBatch[T] {
	return &ManagerBatch[T]{
		manager:     m,
		treeBatches: make(map[string]*TreeBatch[T]),
		active:      true,
	}
}

// InsertToTree добавляет элемент в дерево в батч-режиме
func (mb *ManagerBatch[T]) InsertToTree(treeName string, item T) error {
	mb.mu.Lock()
	defer mb.mu.Unlock()

	if !mb.active {
		return fmt.Errorf("батч не активен")
	}

	// Получаем или создаем батч для дерева
	treeBatch, exists := mb.treeBatches[treeName]
	if !exists {
		tree, ok := mb.manager.GetTree(treeName)
		if !ok {
			return fmt.Errorf("дерево '%s' не найдено", treeName)
		}

		treeBatch = tree.BeginBatch()
		mb.treeBatches[treeName] = treeBatch
	}

	return treeBatch.Insert(item)
}

// InsertManyToTree добавляет несколько элементов в дерево
func (mb *ManagerBatch[T]) InsertManyToTree(treeName string, items []T) error {
	mb.mu.Lock()
	defer mb.mu.Unlock()

	if !mb.active {
		return fmt.Errorf("батч не активен")
	}

	// Получаем или создаем батч для дерева
	treeBatch, exists := mb.treeBatches[treeName]
	if !exists {
		tree, ok := mb.manager.GetTree(treeName)
		if !ok {
			return fmt.Errorf("дерево '%s' не найдено", treeName)
		}

		treeBatch = tree.BeginBatch()
		mb.treeBatches[treeName] = treeBatch
	}

	return treeBatch.InsertMany(items)
}

// GetBatchSize возвращает размер батча для дерева
func (mb *ManagerBatch[T]) GetBatchSize(treeName string) int {
	mb.mu.Lock()
	defer mb.mu.Unlock()

	if treeBatch, exists := mb.treeBatches[treeName]; exists {
		return treeBatch.Size()
	}

	return 0
}

// GetTotalBatchSize возвращает общий размер всех батчей
func (mb *ManagerBatch[T]) GetTotalBatchSize() int {
	mb.mu.Lock()
	defer mb.mu.Unlock()

	total := 0
	for _, treeBatch := range mb.treeBatches {
		total += treeBatch.Size()
	}

	return total
}

// GetAffectedTrees возвращает список деревьев в батче
func (mb *ManagerBatch[T]) GetAffectedTrees() []string {
	mb.mu.Lock()
	defer mb.mu.Unlock()

	trees := make([]string, 0, len(mb.treeBatches))
	for name := range mb.treeBatches {
		trees = append(trees, name)
	}

	return trees
}

// Commit применяет все изменения во всех деревьях и пересчитывает хеши
func (mb *ManagerBatch[T]) Commit() error {
	mb.mu.Lock()
	defer mb.mu.Unlock()

	if !mb.active {
		return fmt.Errorf("батч не активен")
	}

	// Применяем изменения во всех деревьях параллельно
	var wg sync.WaitGroup
	errors := make(chan error, len(mb.treeBatches))

	for treeName, treeBatch := range mb.treeBatches {
		wg.Add(1)
		go func(name string, batch *TreeBatch[T]) {
			defer wg.Done()
			if err := batch.Commit(); err != nil {
				errors <- fmt.Errorf("ошибка commit дерева '%s': %w", name, err)
			}
		}(treeName, treeBatch)
	}

	wg.Wait()
	close(errors)

	// Проверяем ошибки
	for err := range errors {
		mb.active = false
		return err
	}

	mb.active = false
	mb.treeBatches = nil

	return nil
}

// CommitWithRoots применяет изменения и возвращает новые корни всех затронутых деревьев
func (mb *ManagerBatch[T]) CommitWithRoots() (map[string][32]byte, error) {
	mb.mu.Lock()
	if !mb.active {
		mb.mu.Unlock()
		return nil, fmt.Errorf("батч не активен")
	}

	// Сохраняем список затронутых деревьев ДО коммита
	affectedTrees := make([]string, 0, len(mb.treeBatches))
	for name := range mb.treeBatches {
		affectedTrees = append(affectedTrees, name)
	}
	mb.mu.Unlock()

	// Применяем изменения
	if err := mb.Commit(); err != nil {
		return nil, err
	}

	// Получаем корни всех затронутых деревьев
	roots := make(map[string][32]byte, len(affectedTrees))

	for _, treeName := range affectedTrees {
		if root, err := mb.manager.GetTreeRoot(treeName); err == nil {
			roots[treeName] = root
		}
	}

	return roots, nil
}

// CommitWithGlobalRoot применяет изменения и возвращает новый глобальный корень
func (mb *ManagerBatch[T]) CommitWithGlobalRoot() ([32]byte, error) {
	mb.mu.Lock()
	if !mb.active {
		mb.mu.Unlock()
		return [32]byte{}, fmt.Errorf("батч не активен")
	}
	mb.mu.Unlock()

	// Применяем изменения
	if err := mb.Commit(); err != nil {
		return [32]byte{}, err
	}

	// Вычисляем новый глобальный корень
	return mb.manager.ComputeGlobalRoot(), nil
}

// Rollback отменяет все изменения
func (mb *ManagerBatch[T]) Rollback() {
	mb.mu.Lock()
	defer mb.mu.Unlock()

	for _, treeBatch := range mb.treeBatches {
		treeBatch.Rollback()
	}

	mb.active = false
	mb.treeBatches = nil
}

// IsActive проверяет, активен ли батч
func (mb *ManagerBatch[T]) IsActive() bool {
	mb.mu.Lock()
	defer mb.mu.Unlock()
	return mb.active
}

// BatchStats статистика батча
type BatchStats struct {
	TotalItems    int            // Всего элементов в батче
	AffectedTrees int            // Количество затронутых деревьев
	TreeSizes     map[string]int // Размеры батчей по деревьям
}

// GetStats возвращает статистику батча
func (mb *ManagerBatch[T]) GetStats() BatchStats {
	mb.mu.Lock()
	defer mb.mu.Unlock()

	stats := BatchStats{
		AffectedTrees: len(mb.treeBatches),
		TreeSizes:     make(map[string]int),
	}

	for name, batch := range mb.treeBatches {
		size := batch.Size()
		stats.TreeSizes[name] = size
		stats.TotalItems += size
	}

	return stats
}
