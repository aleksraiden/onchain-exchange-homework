package main

import (
	"errors"
	"sync"

	"github.com/cockroachdb/pebble"
)

// PebbleStore реализует интерфейс Store поверх PebbleDB
type PebbleStore struct {
	db *pebble.DB
	// batch используется для накопления записей перед сбросом на диск
	batch *pebble.Batch
	mu    sync.RWMutex
}

func NewPebbleStore(path string) (*PebbleStore, error) {
	db, err := pebble.Open(path, &pebble.Options{})
	if err != nil {
		return nil, err
	}
	return &PebbleStore{
		db: db,
	}, nil
}

func (s *PebbleStore) Close() error {
	if s.batch != nil {
		s.batch.Close()
	}
	return s.db.Close()
}

// Get читает данные.
// ВАЖНО: Pebble возвращает данные, которые нельзя использовать после закрытия итератора/closer.
// Поэтому мы делаем копию.
func (s *PebbleStore) Get(key []byte) ([]byte, error) {
	// Сначала проверяем текущий батч (если мы внутри транзакции)
	s.mu.RLock()
	if s.batch != nil {
		val, closer, err := s.batch.Get(key)
		if err == nil {
			// Нашли в батче, копируем и возвращаем
			ret := make([]byte, len(val))
			copy(ret, val)
			closer.Close()
			s.mu.RUnlock()
			return ret, nil
		}
	}
	s.mu.RUnlock()

	// Если нет в батче, читаем из БД
	val, closer, err := s.db.Get(key)
	if err != nil {
		if errors.Is(err, pebble.ErrNotFound) {
			return nil, errors.New("not found")
		}
		return nil, err
	}
	defer closer.Close()

	// Делаем копию данных
	ret := make([]byte, len(val))
	copy(ret, val)
	return ret, nil
}

// Set пишет данные.
// Если активен батч - пишет в него, иначе сразу в БД.
func (s *PebbleStore) Set(key, value []byte) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.batch != nil {
		return s.batch.Set(key, value, pebble.NoSync)
	}
	return s.db.Set(key, value, pebble.Sync)
}

func (s *PebbleStore) Delete(key []byte) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.batch != nil {
		return s.batch.Delete(key, pebble.NoSync)
	}
	return s.db.Delete(key, pebble.Sync)
}

// --- Управление Транзакциями (Батчинг) ---

// StartBatch начинает накопление изменений (вызывать перед SMT.Commit)
func (s *PebbleStore) StartBatch() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.batch == nil {
		s.batch = s.db.NewBatch()
	}
}

// FinishBatch сбрасывает изменения на диск (вызывать после SMT.Commit)
func (s *PebbleStore) FinishBatch() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	
	if s.batch != nil {
		err := s.batch.Commit(pebble.Sync)
		if err != nil {
			return err
		}
		s.batch = nil
	}
	return nil
}

// --- Работа со Снапшотами (Корнями) ---

// SaveRoot сохраняет текущий Root Hash под именем (тегом).
// Например tag="latest" или tag="block_100500"
func (s *PebbleStore) SaveRoot(tag string, root []byte) error {
	key := []byte("root_" + tag)
	return s.db.Set(key, root, pebble.Sync)
}

// LoadRoot загружает Root Hash по тегу
func (s *PebbleStore) LoadRoot(tag string) ([]byte, error) {
	key := []byte("root_" + tag)
	val, closer, err := s.db.Get(key)
	if err != nil {
		return nil, err
	}
	defer closer.Close()
	
	ret := make([]byte, len(val))
	copy(ret, val)
	return ret, nil
}