// snapshot_storage.go
package merkletree

import (
	"encoding/hex"
	"fmt"
	"sync"

	"github.com/cockroachdb/pebble/v2"
	"github.com/klauspost/compress/zstd"
	"github.com/vmihailenco/msgpack/v5"
)

// ============================================
// Константы для ключей в PebbleDB
// ============================================

const (
	// Префиксы для различных типов данных
	prefixMeta     = "meta:"
	prefixSnapshot = "snap:"
	prefixIndex    = "idx:ts:"
	
	// Ключи метаданных
	keyMetaFirst = "meta:first" // Первая версия снапшота
	keyMetaLast  = "meta:last"  // Последняя версия снапшота
	keyMetaCount = "meta:count" // Количество снапшотов
)

// ============================================
// SnapshotStorage - работа с хранилищем
// ============================================

// SnapshotStorage управляет хранением снапшотов в PebbleDB
type SnapshotStorage struct {
	db             *pebble.DB
	encoder        *zstd.Encoder // Переиспользуемый encoder для zstd
	decoder        *zstd.Decoder // Переиспользуемый decoder для zstd
	mu             sync.Mutex    // Защита для encoder/decoder
	compressionEnabled bool
}

// NewSnapshotStorage создает новое хранилище снапшотов
// dbPath - путь к директории PebbleDB
func NewSnapshotStorage(dbPath string, enableCompression bool) (*SnapshotStorage, error) {
	// Настройки PebbleDB для оптимальной производительности
	opts := &pebble.Options{
		// Размер кеша для горячих данных
		Cache: pebble.NewCache(64 << 20), // 64MB
		
		// Размер write buffer (больше = меньше flush операций)
		MemTableSize: 32 << 20, // 32MB
		
		// Количество уровней LSM дерева
		MaxOpenFiles: 1000,
	}
	
	// Открываем или создаем базу данных
	db, err := pebble.Open(dbPath, opts)
	if err != nil {
		return nil, fmt.Errorf("failed to open pebble db: %w", err)
	}
	
	storage := &SnapshotStorage{
		db:                 db,
		compressionEnabled: enableCompression,
	}
	
	// Инициализируем zstd encoder/decoder если compression включен
	if enableCompression {
		// Encoder с уровнем по умолчанию (хороший баланс скорость/сжатие)
		encoder, err := zstd.NewWriter(nil, zstd.WithEncoderLevel(zstd.SpeedDefault))
		if err != nil {
			db.Close()
			return nil, fmt.Errorf("failed to create zstd encoder: %w", err)
		}
		storage.encoder = encoder
		
		// Decoder с параллельной декомпрессией
		decoder, err := zstd.NewReader(nil, zstd.WithDecoderConcurrency(4))
		if err != nil {
			db.Close()
			return nil, fmt.Errorf("failed to create zstd decoder: %w", err)
		}
		storage.decoder = decoder
	}
	
	return storage, nil
}

// Close закрывает хранилище и освобождает ресурсы
func (s *SnapshotStorage) Close() error {
	if s.encoder != nil {
		s.encoder.Close()
	}
	if s.decoder != nil {
		s.decoder.Close()
	}
	return s.db.Close()
}

// ============================================
// Методы для работы со снапшотами
// ============================================

// SaveSnapshot сохраняет снапшот в базу данных
func (s *SnapshotStorage) SaveSnapshot(snapshot *Snapshot) error {
	// Сериализуем снапшот в MessagePack
	data, err := msgpack.Marshal(snapshot)
	if err != nil {
		return fmt.Errorf("failed to marshal snapshot: %w", err)
	}
	
	// Сжимаем данные если включено
	if s.compressionEnabled {
		data, err = s.compress(data)
		if err != nil {
			return fmt.Errorf("failed to compress snapshot: %w", err)
		}
	}
	
	// Создаем batch для атомарной записи всех данных
	batch := s.db.NewBatch()
	defer batch.Close()
	
	// Ключ снапшота: "snap:{version_hex}"
	snapshotKey := s.makeSnapshotKey(snapshot.Version)
	if err := batch.Set(snapshotKey, data, pebble.Sync); err != nil {
		return fmt.Errorf("failed to write snapshot: %w", err)
	}
	
	// Индекс по времени для быстрого поиска: "idx:ts:{timestamp}" -> version
	indexKey := s.makeIndexKey(snapshot.Timestamp)
	if err := batch.Set(indexKey, snapshot.Version[:], pebble.NoSync); err != nil {
		return fmt.Errorf("failed to write index: %w", err)
	}
	
	// Обновляем метаданные
	if err := s.updateMetadata(batch, snapshot.Version); err != nil {
		return fmt.Errorf("failed to update metadata: %w", err)
	}
	
	// Коммитим все изменения атомарно
	if err := batch.Commit(pebble.Sync); err != nil {
		return fmt.Errorf("failed to commit batch: %w", err)
	}
	
	return nil
}

// LoadSnapshot загружает снапшот по версии
// Если version == nil, загружает последний снапшот
func (s *SnapshotStorage) LoadSnapshot(version *[32]byte) (*Snapshot, error) {
	// Определяем версию для загрузки
	targetVersion := version
	if targetVersion == nil {
		// Загружаем последнюю версию
		lastVersion, err := s.getLastVersion()
		if err != nil {
			return nil, fmt.Errorf("failed to get last version: %w", err)
		}
		targetVersion = lastVersion
	}
	
	// Читаем данные из базы
	snapshotKey := s.makeSnapshotKey(*targetVersion)
	data, closer, err := s.db.Get(snapshotKey)
	if err != nil {
		if err == pebble.ErrNotFound {
			return nil, fmt.Errorf("snapshot not found: %x", targetVersion)
		}
		return nil, fmt.Errorf("failed to read snapshot: %w", err)
	}
	defer closer.Close()
	
	// Копируем данные т.к. они валидны только до closer.Close()
	dataCopy := make([]byte, len(data))
	copy(dataCopy, data)
	
	// Распаковываем если было сжатие
	if s.compressionEnabled {
		dataCopy, err = s.decompress(dataCopy)
		if err != nil {
			return nil, fmt.Errorf("failed to decompress snapshot: %w", err)
		}
	}
	
	// Десериализуем снапшот
	var snapshot Snapshot
	if err := msgpack.Unmarshal(dataCopy, &snapshot); err != nil {
		return nil, fmt.Errorf("failed to unmarshal snapshot: %w", err)
	}
	
	return &snapshot, nil
}

// DeleteSnapshot удаляет снапшот по версии
func (s *SnapshotStorage) DeleteSnapshot(version [32]byte) error {
	// Сначала загружаем снапшот для получения timestamp
	snapshot, err := s.LoadSnapshot(&version)
	if err != nil {
		return err
	}
	
	batch := s.db.NewBatch()
	defer batch.Close()
	
	// Удаляем основной снапшот
	snapshotKey := s.makeSnapshotKey(version)
	if err := batch.Delete(snapshotKey, pebble.Sync); err != nil {
		return fmt.Errorf("failed to delete snapshot: %w", err)
	}
	
	// Удаляем индекс по времени
	indexKey := s.makeIndexKey(snapshot.Timestamp)
	if err := batch.Delete(indexKey, pebble.NoSync); err != nil {
		return fmt.Errorf("failed to delete index: %w", err)
	}
	
	// Обновляем метаданные (декремент счетчика)
	if err := s.decrementCount(batch); err != nil {
		return fmt.Errorf("failed to update metadata: %w", err)
	}
	
	return batch.Commit(pebble.Sync)
}

// GetMetadata возвращает метаданные о снапшотах
func (s *SnapshotStorage) GetMetadata() (*SnapshotMetadata, error) {
	metadata := &SnapshotMetadata{}
	
	// Читаем первую версию
	firstData, closer, err := s.db.Get([]byte(keyMetaFirst))
	if err != nil && err != pebble.ErrNotFound {
		return nil, fmt.Errorf("failed to read first version: %w", err)
	}
	if err == nil {
		copy(metadata.FirstVersion[:], firstData)
		closer.Close()
	}
	
	// Читаем последнюю версию
	lastData, closer, err := s.db.Get([]byte(keyMetaLast))
	if err != nil && err != pebble.ErrNotFound {
		return nil, fmt.Errorf("failed to read last version: %w", err)
	}
	if err == nil {
		copy(metadata.LastVersion[:], lastData)
		closer.Close()
	}
	
	// Читаем счетчик
	countData, closer, err := s.db.Get([]byte(keyMetaCount))
	if err != nil && err != pebble.ErrNotFound {
		return nil, fmt.Errorf("failed to read count: %w", err)
	}
	if err == nil {
		if err := msgpack.Unmarshal(countData, &metadata.Count); err != nil {
			closer.Close()
			return nil, fmt.Errorf("failed to unmarshal count: %w", err)
		}
		closer.Close()
	}
	
	// Подсчитываем общий размер (итерируемся по всем снапшотам)
	iter, err := s.db.NewIter(&pebble.IterOptions{
		LowerBound: []byte(prefixSnapshot),
		UpperBound: []byte(prefixSnapshot + "\xff"),
	})
	if err != nil {
		return nil, fmt.Errorf("failed to create iterator: %w", err)
	}
	defer iter.Close()
	
	for iter.First(); iter.Valid(); iter.Next() {
		metadata.TotalSize += int64(len(iter.Value()))
	}
	
	return metadata, nil
}

// ListVersions возвращает список всех доступных версий
func (s *SnapshotStorage) ListVersions() ([][32]byte, error) {
	var versions [][32]byte
	
	// Итерируемся по всем снапшотам
	iter, err := s.db.NewIter(&pebble.IterOptions{
		LowerBound: []byte(prefixSnapshot),
		UpperBound: []byte(prefixSnapshot + "\xff"),
	})
	if err != nil {
		return nil, fmt.Errorf("failed to create iterator: %w", err)
	}
	defer iter.Close()
	
	for iter.First(); iter.Valid(); iter.Next() {
		// Извлекаем version из ключа "snap:{version_hex}"
		key := string(iter.Key())
		versionHex := key[len(prefixSnapshot):]
		
		versionBytes, err := hex.DecodeString(versionHex)
		if err != nil {
			continue // Пропускаем некорректные ключи
		}
		
		if len(versionBytes) != 32 {
			continue
		}
		
		var version [32]byte
		copy(version[:], versionBytes)
		versions = append(versions, version)
	}
	
	return versions, nil
}

// ============================================
// Вспомогательные методы
// ============================================

// makeSnapshotKey создает ключ для снапшота
func (s *SnapshotStorage) makeSnapshotKey(version [32]byte) []byte {
	return []byte(fmt.Sprintf("%s%x", prefixSnapshot, version))
}

// makeIndexKey создает ключ для индекса по времени
func (s *SnapshotStorage) makeIndexKey(timestamp int64) []byte {
	return []byte(fmt.Sprintf("%s%d", prefixIndex, timestamp))
}

// compress сжимает данные через zstd (thread-safe)
func (s *SnapshotStorage) compress(data []byte) ([]byte, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	
	// EncodeAll создает новый слайс и сжимает данные
	compressed := s.encoder.EncodeAll(data, make([]byte, 0, len(data)/2))
	return compressed, nil
}

// decompress распаковывает данные (thread-safe)
func (s *SnapshotStorage) decompress(data []byte) ([]byte, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	
	// DecodeAll распаковывает в новый слайс
	decompressed, err := s.decoder.DecodeAll(data, nil)
	if err != nil {
		return nil, fmt.Errorf("zstd decompression failed: %w", err)
	}
	return decompressed, nil
}

// getLastVersion возвращает последнюю версию снапшота
func (s *SnapshotStorage) getLastVersion() (*[32]byte, error) {
	data, closer, err := s.db.Get([]byte(keyMetaLast))
	if err != nil {
		if err == pebble.ErrNotFound {
			return nil, fmt.Errorf("no snapshots found")
		}
		return nil, err
	}
	defer closer.Close()
	
	if len(data) != 32 {
		return nil, fmt.Errorf("invalid version size: %d", len(data))
	}
	
	var version [32]byte
	copy(version[:], data)
	return &version, nil
}

// updateMetadata обновляет метаданные после сохранения снапшота
func (s *SnapshotStorage) updateMetadata(batch *pebble.Batch, version [32]byte) error {
	// Проверяем, это первый снапшот?
	_, closer, err := s.db.Get([]byte(keyMetaFirst))
	isFirst := err == pebble.ErrNotFound
	if closer != nil {
		closer.Close()
	}
	
	if isFirst {
		// Устанавливаем первую версию
		if err := batch.Set([]byte(keyMetaFirst), version[:], pebble.NoSync); err != nil {
			return err
		}
	}
	
	// Всегда обновляем последнюю версию
	if err := batch.Set([]byte(keyMetaLast), version[:], pebble.NoSync); err != nil {
		return err
	}
	
	// Инкрементируем счетчик
	return s.incrementCount(batch)
}

// incrementCount увеличивает счетчик снапшотов
func (s *SnapshotStorage) incrementCount(batch *pebble.Batch) error {
	count := 0
	
	// Читаем текущий счетчик
	countData, closer, err := s.db.Get([]byte(keyMetaCount))
	if err != nil && err != pebble.ErrNotFound {
		return err
	}
	if err == nil {
		msgpack.Unmarshal(countData, &count)
		closer.Close()
	}
	
	// Инкрементируем
	count++
	
	// Сохраняем
	newCountData, err := msgpack.Marshal(count)
	if err != nil {
		return err
	}
	
	return batch.Set([]byte(keyMetaCount), newCountData, pebble.NoSync)
}

// decrementCount уменьшает счетчик снапшотов
func (s *SnapshotStorage) decrementCount(batch *pebble.Batch) error {
	count := 0
	
	countData, closer, err := s.db.Get([]byte(keyMetaCount))
	if err != nil && err != pebble.ErrNotFound {
		return err
	}
	if err == nil {
		msgpack.Unmarshal(countData, &count)
		closer.Close()
	}
	
	// Декрементируем
	if count > 0 {
		count--
	}
	
	newCountData, err := msgpack.Marshal(count)
	if err != nil {
		return err
	}
	
	return batch.Set([]byte(keyMetaCount), newCountData, pebble.NoSync)
}
