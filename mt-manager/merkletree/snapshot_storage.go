package merkletree

import (
	"fmt"
	"os"
	"path/filepath"
	"sync"
)

// MemorySnapshotStorage хранилище в памяти (для тестов)
type MemorySnapshotStorage struct {
	snapshots map[uint64][]byte
	metadata  map[uint64]*SnapshotMetadata
	mu        sync.RWMutex
}

// NewMemorySnapshotStorage создает in-memory хранилище
func NewMemorySnapshotStorage() *MemorySnapshotStorage {
	return &MemorySnapshotStorage{
		snapshots: make(map[uint64][]byte),
		metadata:  make(map[uint64]*SnapshotMetadata),
	}
}

// Save сохраняет снапшот в память
func (m *MemorySnapshotStorage) Save(metadata *SnapshotMetadata, data []byte) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.snapshots[metadata.Version] = data
	m.metadata[metadata.Version] = metadata

	return nil
}

// Load загружает снапшот из памяти
func (m *MemorySnapshotStorage) Load(version uint64) ([]byte, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	data, exists := m.snapshots[version]
	if !exists {
		return nil, fmt.Errorf("snapshot version %d not found", version)
	}

	return data, nil
}

// List возвращает список снапшотов
func (m *MemorySnapshotStorage) List() ([]*SnapshotMetadata, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	list := make([]*SnapshotMetadata, 0, len(m.metadata))
	for _, meta := range m.metadata {
		list = append(list, meta)
	}

	return list, nil
}

// Delete удаляет снапшот
func (m *MemorySnapshotStorage) Delete(version uint64) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	delete(m.snapshots, version)
	delete(m.metadata, version)

	return nil
}

// GetMetadata возвращает метаданные
func (m *MemorySnapshotStorage) GetMetadata(version uint64) (*SnapshotMetadata, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	meta, exists := m.metadata[version]
	if !exists {
		return nil, fmt.Errorf("snapshot version %d not found", version)
	}

	return meta, nil
}

// FileSnapshotStorage хранилище на диске (TODO: полная реализация)
type FileSnapshotStorage struct {
	basePath string
	mu       sync.RWMutex
}

// NewFileSnapshotStorage создает файловое хранилище
func NewFileSnapshotStorage(basePath string) (*FileSnapshotStorage, error) {
	// Создаем директорию если не существует
	if err := os.MkdirAll(basePath, 0755); err != nil {
		return nil, fmt.Errorf("failed to create base path: %w", err)
	}

	return &FileSnapshotStorage{
		basePath: basePath,
	}, nil
}

// Save сохраняет снапшот на диск (TODO: реализация)
func (f *FileSnapshotStorage) Save(metadata *SnapshotMetadata, data []byte) error {
	f.mu.Lock()
	defer f.mu.Unlock()

	// TODO: Реализовать сохранение на диск
	// - Создать файл snapshot_v{version}.{format}
	// - Записать метаданные
	// - Записать данные
	// - Fsync для durability

	filename := filepath.Join(f.basePath, fmt.Sprintf("snapshot_v%d.dat", metadata.Version))
	_ = filename

	return fmt.Errorf("not implemented yet")
}

// Load загружает снапшот с диска (TODO: реализация)
func (f *FileSnapshotStorage) Load(version uint64) ([]byte, error) {
	f.mu.RLock()
	defer f.mu.RUnlock()

	// TODO: Реализовать загрузку с диска

	return nil, fmt.Errorf("not implemented yet")
}

// List возвращает список снапшотов (TODO: реализация)
func (f *FileSnapshotStorage) List() ([]*SnapshotMetadata, error) {
	// TODO: Сканировать директорию и читать метаданные

	return nil, fmt.Errorf("not implemented yet")
}

// Delete удаляет снапшот (TODO: реализация)
func (f *FileSnapshotStorage) Delete(version uint64) error {
	// TODO: Удалить файл с диска

	return fmt.Errorf("not implemented yet")
}

// GetMetadata возвращает метаданные (TODO: реализация)
func (f *FileSnapshotStorage) GetMetadata(version uint64) (*SnapshotMetadata, error) {
	// TODO: Прочитать только метаданные из файла

	return nil, fmt.Errorf("not implemented yet")
}

// S3SnapshotStorage хранилище в S3 (TODO: реализация)
type S3SnapshotStorage struct {
	bucket string
	prefix string
	// s3Client *s3.Client
}

// NewS3SnapshotStorage создает S3 хранилище
func NewS3SnapshotStorage(bucket, prefix string) *S3SnapshotStorage {
	return &S3SnapshotStorage{
		bucket: bucket,
		prefix: prefix,
	}
}

// Методы интерфейса SnapshotStorage для S3 (TODO: реализация)
func (s *S3SnapshotStorage) Save(metadata *SnapshotMetadata, data []byte) error {
	return fmt.Errorf("S3 storage not implemented yet")
}

func (s *S3SnapshotStorage) Load(version uint64) ([]byte, error) {
	return nil, fmt.Errorf("S3 storage not implemented yet")
}

func (s *S3SnapshotStorage) List() ([]*SnapshotMetadata, error) {
	return nil, fmt.Errorf("S3 storage not implemented yet")
}

func (s *S3SnapshotStorage) Delete(version uint64) error {
	return fmt.Errorf("S3 storage not implemented yet")
}

func (s *S3SnapshotStorage) GetMetadata(version uint64) (*SnapshotMetadata, error) {
	return nil, fmt.Errorf("S3 storage not implemented yet")
}
