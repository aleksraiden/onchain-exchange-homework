// snapshot.go
package merkletree

import (
	"fmt"
	"runtime"
	"sync"
	"time"

	"github.com/vmihailenco/msgpack/v5"
)

// ============================================
// Структуры данных
// ============================================

const (
	// Текущая версия схемы снапшотов
	CurrentSchemaVersion = 1
)

// Snapshot представляет полный снимок состояния Manager
type Snapshot struct {
	SchemaVersion int                      `msgpack:"schema_version"`
	Version       [32]byte                 `msgpack:"version"`        // GlobalRootHash
	Timestamp     int64                    `msgpack:"timestamp"`      // Unix timestamp
	TreeCount     int                      `msgpack:"tree_count"`     // Количество деревьев
	Trees         map[string]*TreeSnapshot `msgpack:"trees"`          // Данные деревьев
}

// TreeSnapshot представляет снимок одного дерева
type TreeSnapshot struct {
	TreeID    string   `msgpack:"tree_id"`    // Идентификатор дерева
	RootHash  [32]byte `msgpack:"root_hash"`  // Корневой хеш дерева
	ItemCount uint64   `msgpack:"item_count"` // Количество элементов
	Items     [][]byte `msgpack:"items"`      // Сериализованные элементы
}

// SnapshotMetadata хранит информацию о доступных снапшотах
type SnapshotMetadata struct {
	FirstVersion [32]byte `msgpack:"first_version"` // Первая версия
	LastVersion  [32]byte `msgpack:"last_version"`  // Последняя версия
	Count        int      `msgpack:"count"`         // Количество снапшотов
	TotalSize    int64    `msgpack:"total_size"`    // Общий размер на диске
}

// SnapshotOptions опции для создания снапшота
type SnapshotOptions struct {
	Async       bool // Создать в фоновом режиме (default: false)
	Compression bool // Использовать zstd сжатие (default: true)
	Workers     int  // Количество воркеров для параллельной сериализации (default: NumCPU)
}

// DefaultSnapshotOptions возвращает опции по умолчанию
func DefaultSnapshotOptions() *SnapshotOptions {
	return &SnapshotOptions{
		Async:       false,
		Compression: true,
		Workers:     runtime.NumCPU(),
	}
}

// SnapshotResult результат асинхронного создания снапшота
type SnapshotResult struct {
	Version  [32]byte      // Версия созданного снапшота
	Duration time.Duration // Время выполнения
	Size     int64         // Размер на диске
	Error    error         // Ошибка если есть
}

// ============================================
// SnapshotManager - управление снапшотами
// ============================================

// SnapshotManager управляет созданием и загрузкой снапшотов
type SnapshotManager[T Hashable] struct {
	storage *SnapshotStorage
	workers int
}

// NewSnapshotManager создает новый менеджер снапшотов
func NewSnapshotManager[T Hashable](dbPath string, enableCompression bool) (*SnapshotManager[T], error) {
	storage, err := NewSnapshotStorage(dbPath, enableCompression)
	if err != nil {
		return nil, err
	}
	
	return &SnapshotManager[T]{
		storage: storage,
		workers: runtime.NumCPU(),
	}, nil
}

// Close закрывает менеджер и освобождает ресурсы
func (sm *SnapshotManager[T]) Close() error {
	return sm.storage.Close()
}

// ============================================
// Создание снапшота
// ============================================

// CreateSnapshot создает снапшот TreeManager
// Блокирует TreeManager только на время получения списка деревьев (<500µs)
func (sm *SnapshotManager[T]) CreateSnapshot(mgr *TreeManager[T], opts *SnapshotOptions) ([32]byte, error) {
	if opts == nil {
		opts = DefaultSnapshotOptions()
	}
	
	startTime := time.Now()
	
	// Фаза 1: Получаем snapshot деревьев (минимальная блокировка)
	treeSnapshots, err := sm.captureTreeSnapshots(mgr)
	if err != nil {
		return [32]byte{}, fmt.Errorf("failed to capture tree snapshots: %w", err)
	}
	
	// Фаза 2: Вычисляем GlobalRoot
	version := mgr.ComputeGlobalRoot()
	
	// Фаза 3: Параллельная сериализация деревьев (БЕЗ блокировок TreeManager)
	serializedTrees, err := sm.serializeTreesParallel(treeSnapshots, opts.Workers)
	if err != nil {
		return [32]byte{}, fmt.Errorf("failed to serialize trees: %w", err)
	}
	
	// Фаза 4: Создаем объект Snapshot
	snapshot := &Snapshot{
		SchemaVersion: CurrentSchemaVersion,
		Version:       version,
		Timestamp:     time.Now().Unix(),
		TreeCount:     len(serializedTrees),
		Trees:         serializedTrees,
	}
	
	// Фаза 5: Сохраняем в базу данных
	if err := sm.storage.SaveSnapshot(snapshot); err != nil {
		return [32]byte{}, fmt.Errorf("failed to save snapshot: %w", err)
	}
	
	duration := time.Since(startTime)
	
	// Логируем успешное создание
	sm.logSnapshotCreated(version, len(serializedTrees), duration)
	
	return version, nil
}

// CreateSnapshotAsync создает снапшот асинхронно в фоне
// Возвращает канал для получения результата
func (sm *SnapshotManager[T]) CreateSnapshotAsync(mgr *TreeManager[T], opts *SnapshotOptions) <-chan SnapshotResult {
	resultChan := make(chan SnapshotResult, 1)
	
	go func() {
		defer close(resultChan)
		
		startTime := time.Now()
		version, err := sm.CreateSnapshot(mgr, opts)
		duration := time.Since(startTime)
		
		// Получаем размер снапшота
		var size int64
		if err == nil {
			if metadata, metaErr := sm.storage.GetMetadata(); metaErr == nil {
				size = metadata.TotalSize
			}
		}
		
		resultChan <- SnapshotResult{
			Version:  version,
			Duration: duration,
			Size:     size,
			Error:    err,
		}
	}()
	
	return resultChan
}

// ============================================
// Загрузка снапшота
// ============================================

// LoadSnapshot загружает снапшот в TreeManager
// Если version == nil, загружает последний снапшот
// Блокирует TreeManager только на время замены деревьев (~10µs)
func (sm *SnapshotManager[T]) LoadSnapshot(mgr *TreeManager[T], version *[32]byte) error {
	startTime := time.Now()
	
	// Фаза 1: Загружаем снапшот из базы (БЕЗ блокировки TreeManager)
	snapshot, err := sm.storage.LoadSnapshot(version)
	if err != nil {
		return fmt.Errorf("failed to load snapshot: %w", err)
	}
	
	// Проверяем версию схемы
	if snapshot.SchemaVersion != CurrentSchemaVersion {
		return fmt.Errorf("unsupported schema version: %d (expected %d)", 
			snapshot.SchemaVersion, CurrentSchemaVersion)
	}
	
	// Фаза 2: Параллельная десериализация деревьев (БЕЗ блокировки TreeManager)
	newTrees, err := sm.deserializeTreesParallel(mgr, snapshot)
	if err != nil {
		return fmt.Errorf("failed to deserialize trees: %w", err)
	}
	
	// Фаза 3: Короткая блокировка для замены деревьев
	mgr.mu.Lock()
	mgr.trees = newTrees
	mgr.mu.Unlock()
	
	duration := time.Since(startTime)
	
	// Логируем успешную загрузку
	sm.logSnapshotLoaded(snapshot.Version, len(newTrees), duration)
	
	return nil
}

// ============================================
// Вспомогательные методы для метаданных
// ============================================

// GetMetadata возвращает метаданные о снапшотах
func (sm *SnapshotManager[T]) GetMetadata() (*SnapshotMetadata, error) {
	return sm.storage.GetMetadata()
}

// ListVersions возвращает список всех доступных версий
func (sm *SnapshotManager[T]) ListVersions() ([][32]byte, error) {
	return sm.storage.ListVersions()
}

// DeleteSnapshot удаляет снапшот по версии
func (sm *SnapshotManager[T]) DeleteSnapshot(version [32]byte) error {
	return sm.storage.DeleteSnapshot(version)
}

// ============================================
// Внутренние методы - Capture
// ============================================

// treeDataSnapshot временная структура для хранения данных дерева
type treeDataSnapshot[T Hashable] struct {
	treeID   string
	rootHash [32]byte
	items    []T
}

// captureTreeSnapshots получает snapshot всех деревьев
// Использует минимальную блокировку TreeManager
func (sm *SnapshotManager[T]) captureTreeSnapshots(mgr *TreeManager[T]) ([]*treeDataSnapshot[T], error) {
	// Получаем список деревьев с минимальной блокировкой
	mgr.mu.RLock()
	treeIDs := make([]string, 0, len(mgr.trees))
	treeRefs := make([]*Tree[T], 0, len(mgr.trees))
	
	for id, tree := range mgr.trees {
		treeIDs = append(treeIDs, id)
		treeRefs = append(treeRefs, tree)
	}
	mgr.mu.RUnlock()
	
	// Собираем данные из каждого дерева
	snapshots := make([]*treeDataSnapshot[T], len(treeRefs))
	
	for i, tree := range treeRefs {
		snapshot, err := sm.captureTreeData(tree, treeIDs[i])
		if err != nil {
			return nil, fmt.Errorf("failed to capture tree %s: %w", treeIDs[i], err)
		}
		snapshots[i] = snapshot
	}
	
	return snapshots, nil
}

// captureTreeData получает данные из одного дерева
// Использует lock-free Range для минимальной блокировки
func (sm *SnapshotManager[T]) captureTreeData(tree *Tree[T], treeID string) (*treeDataSnapshot[T], error) {
	// Получаем rootHash (может быть закеширован)
	rootHash := tree.ComputeRoot()
	
	// Собираем все элементы через lock-free Range
	items := make([]T, 0, tree.Size())
	
	tree.items.Range(func(key, value interface{}) bool {
		item := value.(T)
		items = append(items, item)
		return true // продолжаем итерацию
	})
	
	return &treeDataSnapshot[T]{
		treeID:   treeID,
		rootHash: rootHash,
		items:    items,
	}, nil
}

// ============================================
// Внутренние методы - Параллельная сериализация
// ============================================

// serializeTreesParallel сериализует деревья параллельно
func (sm *SnapshotManager[T]) serializeTreesParallel(
	treeSnapshots []*treeDataSnapshot[T],
	workers int,
) (map[string]*TreeSnapshot, error) {
	
	if workers <= 0 {
		workers = runtime.NumCPU()
	}
	
	// Каналы для воркеров
	jobs := make(chan *treeDataSnapshot[T], len(treeSnapshots))
	results := make(chan *serializationResult, len(treeSnapshots))
	
	// Запускаем воркеров
	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go sm.serializationWorker(jobs, results, &wg)
	}
	
	// Отправляем задачи
	for _, snapshot := range treeSnapshots {
		jobs <- snapshot
	}
	close(jobs)
	
	// Ждем завершения всех воркеров
	wg.Wait()
	close(results)
	
	// Собираем результаты
	serializedTrees := make(map[string]*TreeSnapshot, len(treeSnapshots))
	for result := range results {
		if result.err != nil {
			return nil, fmt.Errorf("serialization error for tree %s: %w", result.treeID, result.err)
		}
		serializedTrees[result.treeID] = result.treeSnapshot
	}
	
	return serializedTrees, nil
}

// serializationResult результат сериализации одного дерева
type serializationResult struct {
	treeID       string
	treeSnapshot *TreeSnapshot
	err          error
}

// serializationWorker воркер для параллельной сериализации
func (sm *SnapshotManager[T]) serializationWorker(
	jobs <-chan *treeDataSnapshot[T],
	results chan<- *serializationResult,
	wg *sync.WaitGroup,
) {
	defer wg.Done()
	
	for snapshot := range jobs {
		treeSnapshot, err := sm.serializeTree(snapshot)
		results <- &serializationResult{
			treeID:       snapshot.treeID,
			treeSnapshot: treeSnapshot,
			err:          err,
		}
	}
}

// serializeTree сериализует одно дерево
func (sm *SnapshotManager[T]) serializeTree(snapshot *treeDataSnapshot[T]) (*TreeSnapshot, error) {
	// Сериализуем каждый элемент
	serializedItems := make([][]byte, len(snapshot.items))
	
	for i, item := range snapshot.items {
		data, err := msgpack.Marshal(item)
		if err != nil {
			return nil, fmt.Errorf("failed to marshal item %d: %w", i, err)
		}
		serializedItems[i] = data
	}
	
	return &TreeSnapshot{
		TreeID:    snapshot.treeID,
		RootHash:  snapshot.rootHash,
		ItemCount: uint64(len(snapshot.items)),
		Items:     serializedItems,
	}, nil
}

// ============================================
// Внутренние методы - Параллельная десериализация
// ============================================

// deserializeTreesParallel десериализует деревья параллельно
func (sm *SnapshotManager[T]) deserializeTreesParallel(
	mgr *TreeManager[T],
	snapshot *Snapshot,
) (map[string]*Tree[T], error) {
	
	workers := sm.workers
	if workers <= 0 {
		workers = runtime.NumCPU()
	}
	
	// Определяем структуру job
	type jobData struct {
		treeID       string
		treeSnapshot *TreeSnapshot
	}
	
	// Каналы для воркеров
	jobs := make(chan jobData, len(snapshot.Trees))
	results := make(chan *deserializationResult[T], len(snapshot.Trees))
	
	// Запускаем воркеров (встроенная горутина)
	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {  // ✅ ВСТРОЕННАЯ ГОРУТИНА - убрали вызов sm.deserializationWorker
			defer wg.Done()
			for job := range jobs {
				tree, err := sm.deserializeTree(mgr, job.treeID, job.treeSnapshot)
				results <- &deserializationResult[T]{
					treeID: job.treeID,
					tree:   tree,
					err:    err,
				}
			}
		}()
	}
	
	// Отправляем задачи
	for treeID, treeSnapshot := range snapshot.Trees {
		jobs <- jobData{treeID, treeSnapshot}
	}
	close(jobs)
	
	// Ждем завершения
	wg.Wait()
	close(results)
	
	// Собираем результаты
	newTrees := make(map[string]*Tree[T], len(snapshot.Trees))
	for result := range results {
		if result.err != nil {
			return nil, fmt.Errorf("deserialization error for tree %s: %w", result.treeID, result.err)
		}
		newTrees[result.treeID] = result.tree
	}
	
	return newTrees, nil
}

// deserializationResult результат десериализации одного дерева
type deserializationResult[T Hashable] struct {
	treeID string
	tree   *Tree[T]
	err    error
}

// deserializeTree десериализует одно дерево
func (sm *SnapshotManager[T]) deserializeTree(
	mgr *TreeManager[T],
	treeID string,
	treeSnapshot *TreeSnapshot,
) (*Tree[T], error) {
	
	// Создаем новое дерево с конфигурацией из TreeManager
	tree := New[T](mgr.config)
	tree.name = treeID  // Устанавливаем имя дерева
	
	// Десериализуем элементы
	items := make([]T, treeSnapshot.ItemCount)
	for i, data := range treeSnapshot.Items {
		var item T
		if err := msgpack.Unmarshal(data, &item); err != nil {
			return nil, fmt.Errorf("failed to unmarshal item %d: %w", i, err)
		}
		items[i] = item
	}
	
	// Вставляем все элементы батчем
	tree.InsertBatch(items)
	
	return tree, nil
}

// ============================================
// Логирование (для отладки)
// ============================================

func (sm *SnapshotManager[T]) logSnapshotCreated(version [32]byte, treeCount int, duration time.Duration) {
	// Можно добавить логирование через structured logger
	// log.Info("snapshot created",
	//     "version", hex.EncodeToString(version[:]),
	//     "trees", treeCount,
	//     "duration", duration)
}

func (sm *SnapshotManager[T]) logSnapshotLoaded(version [32]byte, treeCount int, duration time.Duration) {
	// log.Info("snapshot loaded",
	//     "version", hex.EncodeToString(version[:]),
	//     "trees", treeCount,
	//     "duration", duration)
}
