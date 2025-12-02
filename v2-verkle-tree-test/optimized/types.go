// optimized/types.go

package optimized

import (
	"errors"
)

// Ошибки
var (
	ErrKeyNotFound    = errors.New("key not found")
	ErrInvalidKey     = errors.New("invalid key")
	ErrValueTooLarge  = errors.New("value too large (max 8KB)")
	ErrInvalidProof   = errors.New("invalid proof")
)

// VerkleNode - интерфейс для узлов дерева
type VerkleNode interface {
	// Hash возвращает blake3 хеш узла
	Hash() []byte
	
	// IsLeaf проверяет, является ли узел листом
	IsLeaf() bool
	
	// IsDirty проверяет, нужно ли пересчитать commitment
	IsDirty() bool
	
	// SetDirty помечает узел как измененный
	SetDirty(dirty bool)
}

// UserData - данные пользователя (пример структуры)
type UserData struct {
	Balances  map[string]float64     `json:"balances"`
	Metadata  map[string]interface{} `json:"metadata,omitempty"`
	Timestamp int64                  `json:"timestamp,omitempty"`
}

// Storage - интерфейс для хранилища (Pebble)
type Storage interface {
	Get(key []byte) ([]byte, error)
	Put(key []byte, value []byte) error
	Delete(key []byte) error
	Close() error
}
