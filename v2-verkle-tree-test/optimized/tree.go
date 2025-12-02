// optimized/tree.go

package optimized

import (
	"fmt"
	"sync"
	"sync/atomic"
)

// VerkleTree - оптимизированное Verkle дерево
type VerkleTree struct {
	// Корень дерева (фиксированная глубина 8)
	root *InternalNode
	
	// Конфигурация
	config *Config
	
	// Хранилище (Pebble DB, может быть nil для in-memory)
	dataStore Storage
	
	// ✅ Thread-safe индекс листьев (sync.Map для параллельного доступа)
	nodeIndex sync.Map // map[string]*LeafNode
	
	// LRU cache для горячих узлов (5000 элементов)
	cache *LRUCache
	
	// Async commit workers
	commitQueue chan *commitTask
	commitWG    sync.WaitGroup
	commitInProgress atomic.Bool
	pendingRoot []byte
	
	// Worker pool для параллельных операций
	workerPool *WorkerPool
	
	// Мьютекс для потокобезопасности дерева
	mu sync.RWMutex
	
	// ✅ Отдельный мьютекс для модификации структуры дерева
	treeMu sync.Mutex
}

// commitTask - задача на асинхронный commit
type commitTask struct {
	node       *InternalNode
	resultChan chan []byte
}

// New создает новое оптимизированное Verkle дерево
func New(config *Config, dataStore Storage) (*VerkleTree, error) {
	if config == nil {
		return nil, fmt.Errorf("config cannot be nil")
	}
	
	// Создаем корневой узел (depth=0)
	root := NewInternalNode(0)
	
	tree := &VerkleTree{
		root:        root,
		config:      config,
		dataStore:   dataStore,
		cache:       NewLRUCache(config.CacheSize),
		commitQueue: make(chan *commitTask, 100),
		workerPool:  NewWorkerPool(config.Workers),
	}
	
	// Запускаем async commit workers
	if config.AsyncMode {
		for i := 0; i < config.Workers; i++ {
			go tree.commitWorker()
		}
	}
	
	return tree, nil
}

// getNodeIndex - оптимизированный метод с pre-computed mask
func (vt *VerkleTree) getNodeIndex(byteValue byte) int {
	return int(byteValue) & vt.config.NodeMask
}

// Insert вставляет данные пользователя в дерево
func (vt *VerkleTree) Insert(userID string, data []byte) error {
	if len(data) > MaxValueSize {
		return ErrValueTooLarge
	}
	
	// Hash user ID
	userIDHash := HashUserID(userID)
	
	// Сохраняем в Pebble DB если есть (можно параллельно)
	if vt.dataStore != nil {
		dataKey := fmt.Sprintf("data:%x", userIDHash)
		if err := vt.dataStore.Put([]byte(dataKey), data); err != nil {
			return fmt.Errorf("failed to save to storage: %w", err)
		}
	}
	
	// ✅ Лочим только на время модификации дерева
	vt.treeMu.Lock()
	err := vt.insert(userIDHash, data)
	vt.treeMu.Unlock()
	
	return err
}

// insert - внутренняя функция вставки (требует treeMu.Lock())
func (vt *VerkleTree) insert(userIDHash [32]byte, data []byte) error {
	// Создаем stem (первые 31 байт)
	var stem [StemSize]byte
	copy(stem[:], userIDHash[:StemSize])
	
	// Проходим по дереву до глубины TreeDepth-1
	node := vt.root
	
	for depth := 0; depth < TreeDepth-1; depth++ {
		index := vt.getNodeIndex(stem[depth])
		
		if node.children[index] == nil {
			// Создаем новый внутренний узел
			node.children[index] = NewInternalNode(depth + 1)
		}
		
		// Переходим к следующему уровню
		if internalNode, ok := node.children[index].(*InternalNode); ok {
			node.SetDirty(true)
			node = internalNode
		} else {
			// Достигли листа раньше - конфликт, нужно расширить
			return vt.expandLeaf(node, index, depth)
		}
	}
	
	// Создаем или обновляем листовой узел
	leafIndex := vt.getNodeIndex(stem[TreeDepth-1])
	dataKey := fmt.Sprintf("data:%x", userIDHash)
	
	cacheKey := string(userIDHash[:])
	
	if node.children[leafIndex] == nil {
		// Создаем новый лист
		leaf := NewLeafNode(userIDHash, stem, dataKey)
		leaf.inMemoryData = data
		leaf.hasData = true
		leaf.SetDirty(true)
		
		node.children[leafIndex] = leaf
		vt.nodeIndex.Store(cacheKey, leaf) // ✅ Thread-safe
		
		// Кэшируем горячий узел
		vt.cache.Put(cacheKey, leaf)
	} else {
		// Обновляем существующий лист
		if leaf, ok := node.children[leafIndex].(*LeafNode); ok {
			leaf.inMemoryData = data
			leaf.hasData = true
			leaf.SetDirty(true)
			
			// Инвалидируем кэш при обновлении
			vt.cache.Invalidate(cacheKey)
			vt.cache.Put(cacheKey, leaf)
		}
	}
	
	// Помечаем путь как dirty (Lazy commit)
	vt.markDirtyPath(stem)
	
	return nil
}

// expandLeaf расширяет лист в поддерево при коллизии
func (vt *VerkleTree) expandLeaf(parentNode *InternalNode, index int, depth int) error {
	oldLeaf := parentNode.children[index].(*LeafNode)
	
	// Создаем новый внутренний узел
	newInternal := NewInternalNode(depth + 1)
	
	// Вычисляем новый индекс для старого листа
	newIndex := vt.getNodeIndex(oldLeaf.stem[depth+1])
	newInternal.children[newIndex] = oldLeaf
	
	// Заменяем лист на внутренний узел
	parentNode.children[index] = newInternal
	parentNode.SetDirty(true)
	
	return nil
}

// markDirtyPath помечает весь путь как dirty (для Lazy commit)
func (vt *VerkleTree) markDirtyPath(stem [StemSize]byte) {
	node := vt.root
	node.SetDirty(true)
	
	for depth := 0; depth < TreeDepth-1; depth++ {
		index := vt.getNodeIndex(stem[depth])
		
		if node.children[index] != nil {
			node.children[index].SetDirty(true)
			
			if internalNode, ok := node.children[index].(*InternalNode); ok {
				node = internalNode
			} else {
				break
			}
		}
	}
}

// Get получает данные пользователя
func (vt *VerkleTree) Get(userID string) ([]byte, error) {
	userIDHash := HashUserID(userID)
	cacheKey := string(userIDHash[:])
	
	// Проверяем LRU cache сначала (горячие узлы)
	if cachedNode, found := vt.cache.Get(cacheKey); found {
		if leaf, ok := cachedNode.(*LeafNode); ok && leaf.hasData {
			// Cache HIT!
			if vt.dataStore == nil {
				return leaf.inMemoryData, nil
			}
			
			// Читаем из Pebble
			data, err := vt.dataStore.Get([]byte(leaf.dataKey))
			if err != nil {
				return nil, err
			}
			return data, nil
		}
	}
	
	// Cache MISS - ищем в sync.Map
	value, exists := vt.nodeIndex.Load(cacheKey)
	if !exists {
		return nil, ErrKeyNotFound
	}
	
	leaf := value.(*LeafNode)
	if !leaf.hasData {
		return nil, ErrKeyNotFound
	}
	
	// Кэшируем найденный узел
	vt.cache.Put(cacheKey, leaf)
	
	// Возвращаем данные
	if vt.dataStore == nil {
		return leaf.inMemoryData, nil
	}
	
	data, err := vt.dataStore.Get([]byte(leaf.dataKey))
	if err != nil {
		return nil, err
	}
	
	return data, nil
}

// GetRoot возвращает root hash (Blake3 temporary или KZG финальный)
func (vt *VerkleTree) GetRoot() []byte {
	vt.mu.RLock()
	defer vt.mu.RUnlock()
	
	// Если async commit в процессе - возвращаем pending root
	if vt.commitInProgress.Load() && vt.pendingRoot != nil {
		return vt.pendingRoot
	}
	
	return vt.root.Hash()
}

// GetFinalRoot возвращает финальный KZG root (ждет если нужно)
func (vt *VerkleTree) GetFinalRoot() ([]byte, error) {
	// Ждем завершения async commits
	vt.commitWG.Wait()
	
	vt.mu.RLock()
	defer vt.mu.RUnlock()
	
	// Вычисляем KZG commitment для root (Lazy KZG)
	if vt.root.IsDirty() {
		vt.mu.RUnlock()
		vt.mu.Lock()
		
		if err := vt.computeKZGForRoot(); err != nil {
			vt.mu.Unlock()
			vt.mu.RLock()
			return nil, err
		}
		
		vt.mu.Unlock()
		vt.mu.RLock()
	}
	
	return vt.root.commitment, nil
}

// computeKZGForRoot вычисляет KZG commitment только для root
func (vt *VerkleTree) computeKZGForRoot() error {
	// Используем memory pool для fr.Element
	values := GetFrElementSlice()
	defer PutFrElementSlice(values)
	
	// Собираем хеши детей
	for i := 0; i < NodeWidth; i++ {
		if vt.root.children[i] == nil {
			values[i].SetZero()
		} else {
			hash := vt.root.children[i].Hash()
			values[i] = hashToFieldElement(hash)
		}
	}
	
	// Вычисляем KZG commitment
	commitment, err := ComputeKZGCommitment(values, vt.config.KZGConfig)
	if err != nil {
		return err
	}
	
	vt.root.commitment = commitment
	vt.root.SetDirty(false)
	
	return nil
}

// commitWorker - фоновый воркер для async commits
func (vt *VerkleTree) commitWorker() {
	for task := range vt.commitQueue {
		// Вычисляем KZG commitment
		vt.mu.Lock()
		err := vt.computeKZGForRoot()
		vt.mu.Unlock()
		
		if err == nil {
			task.resultChan <- vt.root.commitment
		} else {
			task.resultChan <- nil
		}
		
		close(task.resultChan)
		vt.commitWG.Done()
	}
}

// Close закрывает дерево и останавливает воркеры
func (vt *VerkleTree) Close() error {
	// Ждем завершения async commits
	vt.commitWG.Wait()
	
	// Закрываем очередь
	close(vt.commitQueue)
	
	// Останавливаем worker pool
	vt.workerPool.Stop()
	
	// Закрываем storage
	if vt.dataStore != nil {
		return vt.dataStore.Close()
	}
	
	return nil
}

// Stats возвращает статистику дерева
func (vt *VerkleTree) Stats() map[string]interface{} {
	// Подсчитываем элементы в sync.Map
	nodeCount := 0
	vt.nodeIndex.Range(func(key, value interface{}) bool {
		nodeCount++
		return true
	})
	
	hits, misses, hitRate := vt.cache.Stats()
	
	return map[string]interface{}{
		"depth":              TreeDepth,
		"width":              NodeWidth,
		"node_count":         nodeCount,
		"workers":            vt.config.Workers,
		"cache_size":         vt.config.CacheSize,
		"cache_hits":         hits,
		"cache_misses":       misses,
		"cache_hit_rate":     hitRate,
		"lazy_commit":        vt.config.LazyCommit,
		"async_mode":         vt.config.AsyncMode,
		"commit_in_progress": vt.commitInProgress.Load(),
	}
}
