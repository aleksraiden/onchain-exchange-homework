// optimized/nodes.go

package optimized

import (
//	"encoding/binary"
	"github.com/zeebo/blake3"
)

// InternalNode - внутренний узел (фиксированная ширина 128)
type InternalNode struct {
	children   [NodeWidth]VerkleNode // Фиксированный массив!
	commitment []byte                 // Blake3 hash (32 bytes)
	depth      int
	dirty      bool
}

// NewInternalNode создает новый внутренний узел
func NewInternalNode(depth int) *InternalNode {
	return &InternalNode{
		depth: depth,
		dirty: true,
	}
}

// Hash возвращает blake3 хеш узла (commitment или вычисляет)
func (n *InternalNode) Hash() []byte {
	if n.commitment == nil {
		// Быстрый Blake3 хеш детей
		hasher := blake3.New()
		for i := 0; i < NodeWidth; i++ {
			if n.children[i] == nil {
				hasher.Write(make([]byte, 32)) // Нулевой хеш
			} else {
				hasher.Write(n.children[i].Hash())
			}
		}
		n.commitment = hasher.Sum(nil)
		n.dirty = false
	}
	return n.commitment
}

// IsLeaf возвращает false
func (n *InternalNode) IsLeaf() bool {
	return false
}

// IsDirty возвращает флаг dirty
func (n *InternalNode) IsDirty() bool {
	return n.dirty
}

// SetDirty устанавливает флаг dirty
func (n *InternalNode) SetDirty(dirty bool) {
	n.dirty = dirty
	if dirty {
		n.commitment = nil
	}
}

// LeafNode - листовой узел (содержит данные пользователя)
type LeafNode struct {
	userIDHash   [32]byte  // Хеш ID пользователя
	stem         [StemSize]byte // Префикс ключа
	dataKey      string    // Ключ для Pebble DB
	inMemoryData []byte    // Данные в памяти (если DB == nil)
	commitment   []byte    // Blake3 хеш userIDHash
	dirty        bool
	hasData      bool
}

// NewLeafNode создает новый листовой узел
func NewLeafNode(userIDHash [32]byte, stem [StemSize]byte, dataKey string) *LeafNode {
	// Commitment = blake3(userIDHash)
	hasher := blake3.New()
	hasher.Write(userIDHash[:])
	commitment := hasher.Sum(nil)
	
	return &LeafNode{
		userIDHash: userIDHash,
		stem:       stem,
		dataKey:    dataKey,
		commitment: commitment,
		dirty:      false,
		hasData:    false,
	}
}

// Hash возвращает blake3 хеш userIDHash
func (n *LeafNode) Hash() []byte {
	if n.commitment == nil {
		hasher := blake3.New()
		hasher.Write(n.userIDHash[:])
		n.commitment = hasher.Sum(nil)
	}
	return n.commitment
}

// IsLeaf возвращает true
func (n *LeafNode) IsLeaf() bool {
	return true
}

// IsDirty возвращает флаг dirty
func (n *LeafNode) IsDirty() bool {
	return n.dirty
}

// SetDirty устанавливает флаг dirty
func (n *LeafNode) SetDirty(dirty bool) {
	n.dirty = dirty
}

// Blake3Hash ВСЕГДА вычисляет Blake3 хеш (игнорируя cached commitment)
func (n *InternalNode) Blake3Hash() []byte {
	hasher := blake3.New()
	for i := 0; i < NodeWidth; i++ {
		if n.children[i] == nil {
			hasher.Write(make([]byte, 32))
		} else {
			// Рекурсивно вызываем Blake3Hash для детей
			if child, ok := n.children[i].(*InternalNode); ok {
				hasher.Write(child.Blake3Hash())
			} else {
				hasher.Write(n.children[i].Hash())
			}
		}
	}
	return hasher.Sum(nil)
}

// Blake3Hash для LeafNode просто возвращает Hash
func (n *LeafNode) Blake3Hash() []byte {
	return n.Hash()
}