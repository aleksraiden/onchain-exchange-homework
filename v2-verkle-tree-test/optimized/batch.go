// optimized/batch.go

package optimized

import (
	"encoding/json"
	"fmt"
	"sync"
)

// Batch - структура для batch операций
type Batch struct {
	updates map[string][]byte // userIDHash -> data
	mu      sync.Mutex
	closed  bool
}

// NewBatch создает новый batch
func (vt *VerkleTree) NewBatch() *Batch {
	return &Batch{
		updates: make(map[string][]byte, 1000),
		closed:  false,
	}
}

// Add добавляет данные в batch
func (b *Batch) Add(userID string, data []byte) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	
	if b.closed {
		return fmt.Errorf("batch is closed")
	}
	
	if len(data) > MaxValueSize {
		return ErrValueTooLarge
	}
	
	userIDHash := HashUserID(userID)
	b.updates[string(userIDHash[:])] = data
	
	return nil
}

// AddUserData добавляет структурированные данные
func (b *Batch) AddUserData(userID string, userData *UserData) error {
	data, err := json.Marshal(userData)
	if err != nil {
		return fmt.Errorf("failed to marshal user data: %w", err)
	}
	
	return b.Add(userID, data)
}

// CommitBatch применяет batch с async/parallel оптимизациями
func (vt *VerkleTree) CommitBatch(batch *Batch) ([]byte, error) {
	batch.mu.Lock()
	batch.closed = true
	updates := batch.updates
	batch.mu.Unlock()
	
	if len(updates) == 0 {
		vt.mu.RLock()
		root := vt.root.Hash()
		vt.mu.RUnlock()
		return root, nil
	}
	
	// ✅ Параллельная вставка (если workers >= 4 и batch >= 100)
	if vt.config.Workers >= 4 && len(updates) >= 100 {
		if err := vt.parallelBatchInsert(updates); err != nil {
			return nil, err
		}
	} else {
		// Последовательная вставка для малых batch
		vt.treeMu.Lock()
		for userIDHashStr, data := range updates {
			var userIDHash [32]byte
			copy(userIDHash[:], []byte(userIDHashStr))
			
			if err := vt.insert(userIDHash, data); err != nil {
				vt.treeMu.Unlock()
				return nil, err
			}
		}
		vt.treeMu.Unlock()
	}
	
	// Async commit с temporary root
	if vt.config.AsyncMode {
		return vt.asyncCommit()
	}
	
	// Синхронный commit (только Blake3, Lazy KZG)
	vt.mu.RLock()
	root := vt.root.Hash()
	vt.mu.RUnlock()
	
	return root, nil
}

// parallelBatchInsert - МАКСИМАЛЬНО параллельная вставка
func (vt *VerkleTree) parallelBatchInsert(updates map[string][]byte) error {
	type updateEntry struct {
		userIDHash [32]byte
		data       []byte
	}
	
	entries := make([]updateEntry, 0, len(updates))
	for userIDHashStr, data := range updates {
		var userIDHash [32]byte
		copy(userIDHash[:], []byte(userIDHashStr))
		entries = append(entries, updateEntry{userIDHash, data})
	}
	
	// ✅ СТРАТЕГИЯ: Параллельная запись в Pebble + последовательная модификация дерева
	
	// Фаза 1: Параллельная запись в Pebble (если есть)
	if vt.dataStore != nil {
		errChan := make(chan error, len(entries))
		var wg sync.WaitGroup
		
		chunkSize := (len(entries) + vt.config.Workers - 1) / vt.config.Workers
		
		for w := 0; w < vt.config.Workers; w++ {
			start := w * chunkSize
			if start >= len(entries) {
				break
			}
			
			end := start + chunkSize
			if end > len(entries) {
				end = len(entries)
			}
			
			wg.Add(1)
			go func(chunk []updateEntry) {
				defer wg.Done()
				
				for _, entry := range chunk {
					dataKey := fmt.Sprintf("data:%x", entry.userIDHash)
					if err := vt.dataStore.Put([]byte(dataKey), entry.data); err != nil {
						errChan <- err
						return
					}
				}
			}(entries[start:end])
		}
		
		wg.Wait()
		close(errChan)
		
		// Проверяем ошибки Pebble
		for err := range errChan {
			if err != nil {
				return err
			}
		}
	}
	
	// Фаза 2: Быстрая последовательная вставка в дерево
	// (один treeMu.Lock на весь batch - минимальное время блокировки)
	vt.treeMu.Lock()
	for _, entry := range entries {
		if err := vt.insert(entry.userIDHash, entry.data); err != nil {
			vt.treeMu.Unlock()
			return err
		}
	}
	vt.treeMu.Unlock()
	
	return nil
}

// asyncCommit - асинхронный commit с temporary Blake3 root
func (vt *VerkleTree) asyncCommit() ([]byte, error) {
	vt.mu.RLock()
	// Быстрый temporary root (Blake3)
	tempRoot := vt.root.Hash()
	vt.mu.RUnlock()
	
	vt.pendingRoot = tempRoot
	
	// Создаем задачу на async KZG commit
	resultChan := make(chan []byte, 1)
	task := &commitTask{
		node:       vt.root,
		resultChan: resultChan,
	}
	
	vt.commitWG.Add(1)
	vt.commitInProgress.Store(true)
	
	// Отправляем в очередь (неблокирующая)
	select {
	case vt.commitQueue <- task:
		// Задача в очереди
	default:
		// Очередь переполнена - выполняем синхронно
		vt.commitWG.Done()
		vt.commitInProgress.Store(false)
		
		vt.mu.Lock()
		if err := vt.computeKZGForRoot(); err != nil {
			vt.mu.Unlock()
			return nil, err
		}
		finalRoot := vt.root.commitment
		vt.mu.Unlock()
		
		return finalRoot, nil
	}
	
	// Фоновое обновление финального root
	go func() {
		finalRoot := <-resultChan
		vt.mu.Lock()
		if finalRoot != nil {
			vt.root.commitment = finalRoot
		}
		vt.commitInProgress.Store(false)
		vt.mu.Unlock()
	}()
	
	// Возвращаем temporary root СРАЗУ
	return tempRoot, nil
}

// WaitForCommit ждет завершения всех async commits
func (vt *VerkleTree) WaitForCommit() {
	vt.commitWG.Wait()
}
