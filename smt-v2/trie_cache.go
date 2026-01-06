/**
 * @file
 * @copyright MIT License
 */

package trie

import (
	"sync"
)

// MemoryStore - простое хранилище в памяти заменяющее db.DB
type MemoryStore struct {
	data map[string][]byte
	lock sync.RWMutex
}

func NewMemoryStore() *MemoryStore {
	return &MemoryStore{
		data: make(map[string][]byte),
	}
}

func (m *MemoryStore) Get(key []byte) []byte {
	m.lock.RLock()
	defer m.lock.RUnlock()
	val, exists := m.data[string(key)]
	if !exists {
		return nil
	}
	// Возвращаем копию чтобы избежать проблем с concurrent access
	result := make([]byte, len(val))
	copy(result, val)
	return result
}

func (m *MemoryStore) Set(key, value []byte) {
	m.lock.Lock()
	defer m.lock.Unlock()
	m.data[string(key)] = value
}

func (m *MemoryStore) Delete(key []byte) {
	m.lock.Lock()
	defer m.lock.Unlock()
	delete(m.data, string(key))
}

// CacheDB содержит все узлы дерева в памяти
type CacheDB struct {
	// liveCache содержит первые уровни дерева (узлы с 2 не-default детьми)
	liveCache map[Hash][][]byte
	liveMux   sync.RWMutex

	// updatedNodes содержит узлы которые будут сохранены
	updatedNodes map[Hash][][]byte
	updatedMux   sync.RWMutex

	// nodesToRevert будут удалены при revert
	nodesToRevert [][]byte
	revertMux     sync.RWMutex

	// lock для CacheDB
	lock sync.RWMutex

	// Store - хранилище в памяти
	Store *MemoryStore
}

// commit добавляет updatedNodes в хранилище
func (c *CacheDB) commit() {
	c.updatedMux.Lock()
	defer c.updatedMux.Unlock()

	for key, batch := range c.updatedNodes {
		c.Store.Set(key[:], c.serializeBatch(batch))
	}
}

// serializeBatch сериализует 2D [][]byte в []byte
func (c *CacheDB) serializeBatch(batch [][]byte) []byte {
	serialized := make([]byte, 4)
	if batch[0][0] == 1 {
		// batch узел это shortcut
		bitSet(serialized, 31)
	}

	for i := 1; i < 31; i++ {
		if len(batch[i]) != 0 {
			bitSet(serialized, i-1)
			serialized = append(serialized, batch[i]...)
		}
	}
	return serialized
}
