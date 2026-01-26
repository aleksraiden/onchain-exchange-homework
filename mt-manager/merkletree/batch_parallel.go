package merkletree

import (
	"fmt"
	"runtime"
	"sync"
)

// ParallelBatch батч с параллельной обработкой
type ParallelBatch[T Hashable] struct {
	manager  *TreeManager[T]
	buffers  map[string][]T
	mu       sync.Mutex
	workerMu []sync.Mutex // По одному мьютексу на дерево
	active   bool
}

// BeginParallelBatch начинает параллельный батч
func (m *TreeManager[T]) BeginParallelBatch() *ParallelBatch[T] {
	return &ParallelBatch[T]{
		manager:  m,
		buffers:  make(map[string][]T),
		workerMu: make([]sync.Mutex, 0),
		active:   true,
	}
}

// InsertToTree добавление в буфер (lock-free)
func (pb *ParallelBatch[T]) InsertToTree(treeName string, item T) error {
	pb.mu.Lock()
	if !pb.active {
		pb.mu.Unlock()
		return fmt.Errorf("батч не активен")
	}

	pb.buffers[treeName] = append(pb.buffers[treeName], item)
	pb.mu.Unlock()

	return nil
}

// Commit параллельный коммит
func (pb *ParallelBatch[T]) Commit() error {
	pb.mu.Lock()
	if !pb.active {
		pb.mu.Unlock()
		return fmt.Errorf("батч не активен")
	}

	// Копируем буферы
	buffers := make(map[string][]T, len(pb.buffers))
	for name, items := range pb.buffers {
		buffers[name] = items
	}
	pb.mu.Unlock()

	// Параллельная обработка
	numWorkers := runtime.NumCPU()
	if len(buffers) < numWorkers {
		numWorkers = len(buffers)
	}

	type workItem struct {
		treeName string
		items    []T
	}

	workChan := make(chan workItem, len(buffers))
	errChan := make(chan error, len(buffers))

	var wg sync.WaitGroup

	// Запускаем воркеры
	for i := 0; i < numWorkers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()

			for work := range workChan {
				tree, exists := pb.manager.GetTree(work.treeName)
				if !exists {
					errChan <- fmt.Errorf("дерево '%s' не найдено", work.treeName)
					continue
				}

				// Батчевая вставка
				tree.InsertBatch(work.items)
			}
		}()
	}

	// Отправляем работу
	for name, items := range buffers {
		workChan <- workItem{
			treeName: name,
			items:    items,
		}
	}

	close(workChan)
	wg.Wait()
	close(errChan)

	// Проверяем ошибки
	for err := range errChan {
		pb.active = false
		return err
	}

	pb.active = false
	pb.buffers = nil

	return nil
}
