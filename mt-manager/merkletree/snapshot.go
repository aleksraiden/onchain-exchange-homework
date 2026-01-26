package merkletree

import (
	"encoding/json"
	"fmt"
	"io"
	"time"
)

// SnapshotFormat формат снапшота
type SnapshotFormat string

const (
	SnapshotFormatBinary SnapshotFormat = "binary" // Бинарный формат
	SnapshotFormatJSON   SnapshotFormat = "json"   // JSON формат
	SnapshotFormatProto  SnapshotFormat = "proto"  // Protocol Buffers
)

// CompressionType тип сжатия
type CompressionType string

const (
	CompressionNone   CompressionType = "none"
	CompressionGzip   CompressionType = "gzip"
	CompressionZstd   CompressionType = "zstd"
	CompressionSnappy CompressionType = "snappy"
)

// SnapshotMetadata метаданные снапшота
type SnapshotMetadata struct {
	Version     uint64          `json:"version"`      // Версия снапшота
	Timestamp   int64           `json:"timestamp"`    // Unix timestamp создания
	TreeName    string          `json:"tree_name"`    // Имя дерева
	ItemCount   int             `json:"item_count"`   // Количество элементов
	NodeCount   int             `json:"node_count"`   // Количество узлов
	RootHash    [32]byte        `json:"root_hash"`    // Корневой хеш
	Format      SnapshotFormat  `json:"format"`       // Формат снапшота
	Compression CompressionType `json:"compression"`  // Тип сжатия
	Checksum    [32]byte        `json:"checksum"`     // Контрольная сумма
	Size        int64           `json:"size"`         // Размер в байтах
	MaxDepth    int             `json:"max_depth"`    // Глубина дерева
	CacheSize   int             `json:"cache_size"`   // Размер кеша
	CacheShards uint            `json:"cache_shards"` // Количество шардов
	Extra       map[string]any  `json:"extra"`        // Дополнительные данные
}

// SnapshotConfig конфигурация снапшота
type SnapshotConfig struct {
	Format         SnapshotFormat
	Compression    CompressionType
	IncludeCache   bool   // Включать ли кеш в снапшот
	IncludeMetrics bool   // Включать ли метрики
	Incremental    bool   // Инкрементальный снапшот (дельта)
	BaseVersion    uint64 // Базовая версия для инкрементального снапшота
}

// DefaultSnapshotConfig возвращает конфигурацию по умолчанию
func DefaultSnapshotConfig() *SnapshotConfig {
	return &SnapshotConfig{
		Format:         SnapshotFormatBinary,
		Compression:    CompressionZstd,
		IncludeCache:   false,
		IncludeMetrics: true,
		Incremental:    false,
	}
}

// Snapshotter интерфейс для создания снапшотов
type Snapshotter interface {
	// CreateSnapshot создает снапшот и возвращает метаданные
	CreateSnapshot(config *SnapshotConfig) (*SnapshotMetadata, error)

	// SaveSnapshot сохраняет снапшот в writer
	SaveSnapshot(w io.Writer, config *SnapshotConfig) error

	// LoadSnapshot загружает снапшот из reader
	LoadSnapshot(r io.Reader) error

	// ListSnapshots возвращает список доступных снапшотов
	ListSnapshots() ([]*SnapshotMetadata, error)

	// GetSnapshotMetadata возвращает метаданные снапшота
	GetSnapshotMetadata(version uint64) (*SnapshotMetadata, error)

	// DeleteSnapshot удаляет снапшот
	DeleteSnapshot(version uint64) error
}

// SnapshotStorage интерфейс для хранения снапшотов
type SnapshotStorage interface {
	// Save сохраняет снапшот
	Save(metadata *SnapshotMetadata, data []byte) error

	// Load загружает снапшот
	Load(version uint64) ([]byte, error)

	// List возвращает список снапшотов
	List() ([]*SnapshotMetadata, error)

	// Delete удаляет снапшот
	Delete(version uint64) error

	// GetMetadata возвращает метаданные
	GetMetadata(version uint64) (*SnapshotMetadata, error)
}

// TreeSnapshot снапшот дерева
type TreeSnapshot[T Hashable] struct {
	tree           *Tree[T]
	storage        SnapshotStorage
	currentVersion uint64
	snapshots      map[uint64]*SnapshotMetadata
}

// NewTreeSnapshot создает новый snapshotter для дерева
func NewTreeSnapshot[T Hashable](tree *Tree[T], storage SnapshotStorage) *TreeSnapshot[T] {
	return &TreeSnapshot[T]{
		tree:      tree,
		storage:   storage,
		snapshots: make(map[uint64]*SnapshotMetadata),
	}
}

// CreateSnapshot создает снапшот (TODO: реализация)
func (ts *TreeSnapshot[T]) CreateSnapshot(config *SnapshotConfig) (*SnapshotMetadata, error) {
	if config == nil {
		config = DefaultSnapshotConfig()
	}

	// Получаем текущее состояние дерева
	stats := ts.tree.GetStats()
	rootHash := ts.tree.ComputeRoot()

	// Создаем метаданные
	ts.currentVersion++
	metadata := &SnapshotMetadata{
		Version:     ts.currentVersion,
		Timestamp:   time.Now().Unix(),
		ItemCount:   stats.TotalItems,
		NodeCount:   stats.AllocatedNodes,
		RootHash:    rootHash,
		Format:      config.Format,
		Compression: config.Compression,
		Extra:       make(map[string]any),
	}

	// TODO: Реализовать сериализацию дерева
	// - Обход всех узлов
	// - Сериализация в выбранный формат
	// - Сжатие
	// - Вычисление контрольной суммы

	ts.snapshots[metadata.Version] = metadata

	return metadata, nil
}

// SaveSnapshot сохраняет снапшот в writer (TODO: реализация)
func (ts *TreeSnapshot[T]) SaveSnapshot(w io.Writer, config *SnapshotConfig) error {
	metadata, err := ts.CreateSnapshot(config)
	if err != nil {
		return fmt.Errorf("failed to create snapshot: %w", err)
	}

	// TODO: Реализовать запись в writer
	// 1. Записать метаданные
	// 2. Записать данные дерева
	// 3. Записать контрольную сумму

	// Заглушка: пишем только метаданные в JSON
	encoder := json.NewEncoder(w)
	return encoder.Encode(metadata)
}

// LoadSnapshot загружает снапшот из reader (TODO: реализация)
func (ts *TreeSnapshot[T]) LoadSnapshot(r io.Reader) error {
	// TODO: Реализовать загрузку из reader
	// 1. Прочитать метаданные
	// 2. Проверить формат и версию
	// 3. Прочитать данные
	// 4. Проверить контрольную сумму
	// 5. Десериализовать дерево
	// 6. Восстановить состояние

	// Заглушка: читаем только метаданные
	var metadata SnapshotMetadata
	decoder := json.NewDecoder(r)
	if err := decoder.Decode(&metadata); err != nil {
		return fmt.Errorf("failed to decode metadata: %w", err)
	}

	ts.snapshots[metadata.Version] = &metadata

	return nil
}

// ListSnapshots возвращает список снапшотов
func (ts *TreeSnapshot[T]) ListSnapshots() ([]*SnapshotMetadata, error) {
	if ts.storage != nil {
		return ts.storage.List()
	}

	// Из памяти
	snapshots := make([]*SnapshotMetadata, 0, len(ts.snapshots))
	for _, metadata := range ts.snapshots {
		snapshots = append(snapshots, metadata)
	}

	return snapshots, nil
}

// GetSnapshotMetadata возвращает метаданные снапшота
func (ts *TreeSnapshot[T]) GetSnapshotMetadata(version uint64) (*SnapshotMetadata, error) {
	if ts.storage != nil {
		return ts.storage.GetMetadata(version)
	}

	metadata, exists := ts.snapshots[version]
	if !exists {
		return nil, fmt.Errorf("snapshot version %d not found", version)
	}

	return metadata, nil
}

// DeleteSnapshot удаляет снапшот
func (ts *TreeSnapshot[T]) DeleteSnapshot(version uint64) error {
	if ts.storage != nil {
		return ts.storage.Delete(version)
	}

	delete(ts.snapshots, version)
	return nil
}

// ManagerSnapshot снапшот менеджера деревьев
type ManagerSnapshot[T Hashable] struct {
	manager        *TreeManager[T]
	storage        SnapshotStorage
	currentVersion uint64
	snapshots      map[uint64]*ManagerSnapshotMetadata
}

// ManagerSnapshotMetadata метаданные снапшота менеджера
type ManagerSnapshotMetadata struct {
	SnapshotMetadata
	TreeCount    int                          `json:"tree_count"`    // Количество деревьев
	TreeMetadata map[string]*SnapshotMetadata `json:"tree_metadata"` // Метаданные каждого дерева
	GlobalRoot   [32]byte                     `json:"global_root"`   // Глобальный корень
}

// NewManagerSnapshot создает snapshotter для менеджера
func NewManagerSnapshot[T Hashable](manager *TreeManager[T], storage SnapshotStorage) *ManagerSnapshot[T] {
	return &ManagerSnapshot[T]{
		manager:   manager,
		storage:   storage,
		snapshots: make(map[uint64]*ManagerSnapshotMetadata),
	}
}

// CreateSnapshot создает снапшот всех деревьев (TODO: реализация)
func (ms *ManagerSnapshot[T]) CreateSnapshot(config *SnapshotConfig) (*ManagerSnapshotMetadata, error) {
	if config == nil {
		config = DefaultSnapshotConfig()
	}

	// Получаем состояние менеджера
	stats := ms.manager.GetTotalStats()
	globalRoot := ms.manager.ComputeGlobalRoot()

	ms.currentVersion++

	metadata := &ManagerSnapshotMetadata{
		SnapshotMetadata: SnapshotMetadata{
			Version:     ms.currentVersion,
			Timestamp:   time.Now().Unix(),
			ItemCount:   stats.TotalItems,
			NodeCount:   stats.TotalNodes,
			RootHash:    globalRoot,
			Format:      config.Format,
			Compression: config.Compression,
			Extra:       make(map[string]any),
		},
		TreeCount:    stats.TreeCount,
		TreeMetadata: make(map[string]*SnapshotMetadata),
		GlobalRoot:   globalRoot,
	}

	// TODO: Создать снапшоты для каждого дерева
	// for name, tree := range ms.manager.trees {
	//     treeSnapshot := NewTreeSnapshot(tree, nil)
	//     treeMeta, _ := treeSnapshot.CreateSnapshot(config)
	//     metadata.TreeMetadata[name] = treeMeta
	// }

	ms.snapshots[metadata.Version] = metadata

	return metadata, nil
}

// SaveSnapshot сохраняет снапшот менеджера (TODO: реализация)
func (ms *ManagerSnapshot[T]) SaveSnapshot(w io.Writer, config *SnapshotConfig) error {
	metadata, err := ms.CreateSnapshot(config)
	if err != nil {
		return err
	}

	// TODO: Реализовать сохранение всех деревьев

	encoder := json.NewEncoder(w)
	return encoder.Encode(metadata)
}

// LoadSnapshot загружает снапшот менеджера (TODO: реализация)
func (ms *ManagerSnapshot[T]) LoadSnapshot(r io.Reader) error {
	// TODO: Реализовать загрузку всех деревьев

	var metadata ManagerSnapshotMetadata
	decoder := json.NewDecoder(r)
	if err := decoder.Decode(&metadata); err != nil {
		return err
	}

	ms.snapshots[metadata.Version] = &metadata

	return nil
}

// String форматированный вывод метаданных
func (m *SnapshotMetadata) String() string {
	return fmt.Sprintf(`Snapshot v%d:
  Timestamp: %s
  Items: %d, Nodes: %d
  Root: %x
  Format: %s, Compression: %s
  Size: %d bytes`,
		m.Version,
		time.Unix(m.Timestamp, 0).Format(time.RFC3339),
		m.ItemCount,
		m.NodeCount,
		m.RootHash[:16],
		m.Format,
		m.Compression,
		m.Size)
}
