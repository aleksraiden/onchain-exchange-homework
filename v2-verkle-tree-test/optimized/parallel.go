// optimized/parallel.go

package optimized

import (
	"runtime"
	"sync"
)

// WorkerPool - пул воркеров для параллельных операций
type WorkerPool struct {
	workers   int
	taskQueue chan func()
	wg        sync.WaitGroup
	stopped   bool
	mu        sync.Mutex
}

// NewWorkerPool создает новый пул воркеров
func NewWorkerPool(workers int) *WorkerPool {
	if workers == 0 {
		workers = runtime.GOMAXPROCS(0)
	}
	if workers < MinWorkers {
		workers = MinWorkers
	}
	
	pool := &WorkerPool{
		workers:   workers,
		taskQueue: make(chan func(), workers*10), // Буфер
		stopped:   false,
	}
	
	// Запускаем воркеры
	for i := 0; i < workers; i++ {
		pool.wg.Add(1)
		go pool.worker()
	}
	
	return pool
}

// worker - фоновый воркер
func (wp *WorkerPool) worker() {
	defer wp.wg.Done()
	
	for task := range wp.taskQueue {
		task()
	}
}

// Submit отправляет задачу в пул
func (wp *WorkerPool) Submit(task func()) {
	wp.mu.Lock()
	if wp.stopped {
		wp.mu.Unlock()
		return
	}
	wp.mu.Unlock()
	
	wp.taskQueue <- task
}

// Stop останавливает пул воркеров
func (wp *WorkerPool) Stop() {
	wp.mu.Lock()
	if wp.stopped {
		wp.mu.Unlock()
		return
	}
	wp.stopped = true
	wp.mu.Unlock()
	
	close(wp.taskQueue)
	wp.wg.Wait()
}

// ParallelExecute выполняет функцию параллельно для каждого элемента
func (wp *WorkerPool) ParallelExecute(count int, fn func(index int) error) error {
	if count == 0 {
		return nil
	}
	
	errChan := make(chan error, count)
	var wg sync.WaitGroup
	
	chunkSize := (count + wp.workers - 1) / wp.workers
	
	for w := 0; w < wp.workers; w++ {
		start := w * chunkSize
		if start >= count {
			break
		}
		
		end := start + chunkSize
		if end > count {
			end = count
		}
		
		wg.Add(1)
		wp.Submit(func() {
			defer wg.Done()
			
			for i := start; i < end; i++ {
				if err := fn(i); err != nil {
					errChan <- err
					return
				}
			}
		})
	}
	
	wg.Wait()
	close(errChan)
	
	// Проверяем ошибки
	for err := range errChan {
		if err != nil {
			return err
		}
	}
	
	return nil
}
