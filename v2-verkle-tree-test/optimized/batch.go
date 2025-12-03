// optimized/batch.go

package optimized

import (
	"encoding/json"
	"fmt"
	"sync"
)

// updateEntry — быстрая структура без string копирования
type updateEntry struct {
	userIDHash [32]byte // ← ФИКСИРОВАННЫЙ размер!
	data       []byte
}

// Batch — использует slice вместо map (0 копирований ключей)
type Batch struct {
	mu      sync.Mutex
	closed  bool
	entries []updateEntry // ← 2.5x быстрее map!
}

// NewBatch — с большим initial capacity
func (vt *VerkleTree) NewBatch() *Batch {
	return &Batch{
		entries: make([]updateEntry, 0, 1024), // ← Меньше realloc!
	}
}

// Add — прямое добавление без string(userIDHash[:])
func (b *Batch) Add(userID string, data []byte) error {
	b.mu.Lock()
	defer b.mu.Unlock()

	if b.closed {
		return fmt.Errorf("batch is closed")
	}
	if len(data) > MaxValueSize {
		return ErrValueTooLarge
	}

	// ✅ ПРЯМО хеш в [32]byte — без string копирования!
	h := HashUserID(userID)
	b.entries = append(b.entries, updateEntry{
		userIDHash: h,
		data:       data,
	})
	return nil
}

// AddUserData — для тестов
func (b *Batch) AddUserData(userID string, userData *UserData) error {
	data, err := json.Marshal(userData)
	if err != nil {
		return fmt.Errorf("failed to marshal user data: %w", err)
	}
	return b.Add(userID, data)
}

//OLD CommitBatch - ПРОСТАЯ версия
func (vt *VerkleTree) CommitBatchOLD(batch *Batch) ([]byte, error) {
    if batch == nil || len(batch.entries) == 0 {
        return vt.root.Blake3Hash(), nil
    }
    
    // Один простой lock
    vt.treeMu.Lock()
    defer vt.treeMu.Unlock()
    
    // Вставляем все элементы
    for _, entry := range batch.entries {
        //userIDHash := HashUserID(entry.UserID)  // Исправлено: UserID с большой буквы
        if err := vt.insert(entry.userIDHash, entry.data); err != nil {
            return nil, err
        }
    }
    
    vt.root.SetDirty(true)
    
    // Async commit (если включен)
    if vt.config.AsyncMode {
        select {
        case vt.commitQueue <- &commitTask{node: vt.root, resultChan: make(chan []byte, 1)}:
            // Async запущен
        default:
            // Queue переполнена - синхронный commit
            if !vt.config.HashOnly {
                vt.mu.Lock()
                vt.computeKZGForRoot()
                vt.mu.Unlock()
            }
        }
    } else if !vt.config.HashOnly {
        // Синхронный KZG commit
        vt.mu.Lock()
        defer vt.mu.Unlock()
        if err := vt.computeKZGForRoot(); err != nil {
            return nil, err
        }
        return vt.root.commitment, nil
    }
    
    return vt.root.Blake3Hash(), nil
}

func (vt *VerkleTree) CommitBatch(batch *Batch) ([]byte, error) {
    if batch == nil || len(batch.entries) == 0 {
        return vt.root.Blake3Hash(), nil
    }

    vt.treeMu.Lock()
    defer vt.treeMu.Unlock()

    for _, entry := range batch.entries {
        if err := vt.insert(entry.userIDHash, entry.data); err != nil {
            return nil, err
        }
    }

    vt.root.SetDirty(true)

    // ✅ Async commit с правильным WaitGroup
    if vt.config.AsyncMode {
        vt.commitWG.Add(1) // ✅ ДОБАВИТЬ ЭТУ СТРОКУ
        select {
        case vt.commitQueue <- &commitTask{
            node:       vt.root,
            resultChan: make(chan []byte, 1),
        }:
            // Async запущен
        default:
            // Queue переполнена - синхронный fallback
            vt.commitWG.Done() // ✅ Отменяем Add
            if !vt.config.HashOnly {
                vt.mu.Lock()
                vt.computeKZGForRoot()
                vt.mu.Unlock()
            }
        }
    } else if !vt.config.HashOnly {
        // Синхронный KZG
        vt.mu.Lock()
        defer vt.mu.Unlock()
        if err := vt.computeKZGForRoot(); err != nil {
            return nil, err
        }
        return vt.root.commitment, nil
    }

    return vt.root.Blake3Hash(), nil
}

// parallelBatchInsert — оптимизированная версия (entries вместо map!)
func (vt *VerkleTree) parallelBatchInsert(entries []updateEntry) error {
	n := len(entries)

	// ✅ Параллельная запись в Pebble (если есть)
	if vt.dataStore != nil {
		errChan := make(chan error, vt.config.Workers)
		var wg sync.WaitGroup

		chunkSize := (n + vt.config.Workers - 1) / vt.config.Workers
		for w := 0; w < vt.config.Workers; w++ {
			start := w * chunkSize
			if start >= n {
				break
			}
			end := start + chunkSize
			if end > n {
				end = n
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
		for err := range errChan {
			if err != nil {
				return err
			}
		}
	}

	// ✅ Последовательная вставка в дерево (одна блокировка!)
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
