package main

import (
	"bytes"
	"errors"
	"sync"

	"github.com/zeebo/blake3"
)

const (
	HashSize = 32
	KeySize  = 32
)

// Типы узлов для сериализации
const (
	NodeTypeInternal = 0
	NodeTypeLeaf     = 1
)

var (
	// EmptyHash - хеш пустого узла (нули)
	EmptyHash = make([]byte, HashSize)
)

// Store - интерфейс для хранения узлов (в памяти или БД)
type Store interface {
	Get(key []byte) ([]byte, error)
	Set(key, value []byte) error
	Delete(key []byte) error
}

// MemoryStore - простая реализация хранилища в памяти
type MemoryStore struct {
	sync.RWMutex
	data map[string][]byte
}

func NewMemoryStore() *MemoryStore {
	return &MemoryStore{data: make(map[string][]byte)}
}
func (m *MemoryStore) Get(k []byte) ([]byte, error) {
	m.RLock()
	defer m.RUnlock()
	if v, ok := m.data[string(k)]; ok {
		return v, nil
	}
	return nil, errors.New("not found")
}
func (m *MemoryStore) Set(k, v []byte) error {
	m.Lock()
	defer m.Unlock()
	m.data[string(k)] = v
	return nil
}
func (m *MemoryStore) Delete(k []byte) error {
	m.Lock()
	defer m.Unlock()
	delete(m.data, string(k))
	return nil
}

// SMT - Sparse Merkle Trie с оптимизацией shortcuts
type SMT struct {
	store Store
	root  []byte
	lock  sync.RWMutex
}

func NewSMT(store Store) *SMT {
	return &SMT{
		store: store,
		root:  EmptyHash,
	}
}

// Root возвращает текущий корень
func (s *SMT) Root() []byte {
	s.lock.RLock()
	defer s.lock.RUnlock()
	return s.root
}

// Get возвращает значение по ключу
func (s *SMT) Get(key []byte) ([]byte, error) {
	s.lock.RLock()
	defer s.lock.RUnlock()
	return s.get(s.root, key, 0)
}

func (s *SMT) get(root []byte, key []byte, depth int) ([]byte, error) {
	if bytes.Equal(root, EmptyHash) {
		return nil, errors.New("key not found")
	}

	data, err := s.store.Get(root)
	if err != nil {
		return nil, err
	}

	if data[0] == NodeTypeLeaf {
		// Это Shortcut узел. Проверяем, совпадает ли ключ.
		storedKey := data[1 : 1+KeySize]
		if bytes.Equal(storedKey, key) {
			return data[1+KeySize:], nil
		}
		return nil, errors.New("key not found (shortcut mismatch)")
	}

	// Internal узел
	leftHash := data[1 : 1+HashSize]
	rightHash := data[1+HashSize : 1+HashSize*2]

	if bitIsSet(key, depth) {
		return s.get(rightHash, key, depth+1)
	}
	return s.get(leftHash, key, depth+1)
}

// Update обновляет или вставляет значение. Если value == nil, удаляет ключ.
func (s *SMT) Update(key, value []byte) ([]byte, error) {
	s.lock.Lock()
	defer s.lock.Unlock()

	newRoot, err := s.update(s.root, key, value, 0)
	if err != nil {
		return nil, err
	}
	s.root = newRoot
	return s.root, nil
}

// update - рекурсивная функция вставки с логикой "Shortcuts"
func (s *SMT) update(root []byte, key, value []byte, depth int) ([]byte, error) {
	// 1. Если пришли в пустой узел
	if bytes.Equal(root, EmptyHash) {
		if value == nil {
			return EmptyHash, nil // Удаление несуществующего
		}
		// Создаем новый Shortcut (Leaf) здесь
		return s.storeLeaf(key, value)
	}

	// Загружаем текущий узел
	data, err := s.store.Get(root)
	if err != nil {
		return nil, err
	}

	// 2. Если текущий узел - Лист (Shortcut)
	if data[0] == NodeTypeLeaf {
		storedKey := data[1 : 1+KeySize]
		
		// А. Ключи совпадают -> Обновляем значение
		if bytes.Equal(storedKey, key) {
			if value == nil {
				return EmptyHash, nil // Удаляем лист, возвращаем пустоту
			}
			return s.storeLeaf(key, value) // Обновляем данные
		}

		// Б. Ключи разные -> Коллизия shortcut. Нужно "разбить" shortcut.
		// Мы превращаем текущий лист в ветку и спускаем оба ключа ниже.
		// Важно: storedValue нам нужно, чтобы перезаписать старый лист ниже.
		storedValue := data[1+KeySize:]
		
		// Если удаляем (value == nil), то мы просто не добавляем новый ключ,
		// но старый shortcut нам трогать не надо было... стоп.
		// Если ключи не равны и мы удаляем KEY, то старый ключ STORED_KEY остается как был.
		if value == nil {
			return root, nil
		}

		// Создаем новый внутренний узел, который разделит storedKey и key
		return s.splitAndInsert(storedKey, storedValue, key, value, depth)
	}

	// 3. Если текущий узел - Внутренний (Internal)
	leftHash := data[1 : 1+HashSize]
	rightHash := data[1+HashSize : 1+HashSize*2]

	var newLeft, newRight []byte

	if bitIsSet(key, depth) {
		// Идем направо
		newRight, err = s.update(rightHash, key, value, depth+1)
		if err != nil { return nil, err }
		newLeft = leftHash
	} else {
		// Идем налево
		newLeft, err = s.update(leftHash, key, value, depth+1)
		if err != nil { return nil, err }
		newRight = rightHash
	}

	// OPTIMIZATION: Move Up Shortcut (Collapsing)
	// Если один ребенок пустой, а второй - Лист, мы можем поднять Лист наверх.
	if bytes.Equal(newLeft, EmptyHash) && !bytes.Equal(newRight, EmptyHash) {
		// Проверяем, является ли правый ребенок Листом
		rightNode, err := s.store.Get(newRight)
		if err == nil && rightNode[0] == NodeTypeLeaf {
			return newRight, nil // Поднимаем shortcut наверх!
		}
	} else if bytes.Equal(newRight, EmptyHash) && !bytes.Equal(newLeft, EmptyHash) {
		// Проверяем, является ли левый ребенок Листом
		leftNode, err := s.store.Get(newLeft)
		if err == nil && leftNode[0] == NodeTypeLeaf {
			return newLeft, nil // Поднимаем shortcut наверх!
		}
	} else if bytes.Equal(newLeft, EmptyHash) && bytes.Equal(newRight, EmptyHash) {
		// Оба пустые -> этот узел тоже становится пустым
		return EmptyHash, nil
	}

	// Иначе сохраняем как обычный внутренний узел
	return s.storeInternal(newLeft, newRight)
}

// splitAndInsert рекурсивно создает внутренние узлы, пока ключи не разойдутся
func (s *SMT) splitAndInsert(key1, val1, key2, val2 []byte, depth int) ([]byte, error) {
	// Если достигли дна (маловероятно с хешами blake3), просто перезаписываем
	if depth >= KeySize*8 {
		return s.storeLeaf(key2, val2)
	}

	bit1 := bitIsSet(key1, depth)
	bit2 := bitIsSet(key2, depth)

	var left, right []byte

	if bit1 == bit2 {
		// Бит совпадает, копаем глубже
		subNode, err := s.splitAndInsert(key1, val1, key2, val2, depth+1)
		if err != nil { return nil, err }
		
		if bit1 {
			left, right = EmptyHash, subNode // Оба пошли направо
		} else {
			left, right = subNode, EmptyHash // Оба пошли налево
		}
	} else {
		// Биты разошлись! Создаем два листа на этом уровне.
		node1, err := s.storeLeaf(key1, val1)
		if err != nil { return nil, err }
		node2, err := s.storeLeaf(key2, val2)
		if err != nil { return nil, err }

		if bit1 { // key1 -> Right, key2 -> Left
			left, right = node2, node1
		} else {  // key1 -> Left, key2 -> Right
			left, right = node1, node2
		}
	}

	return s.storeInternal(left, right)
}

// Helpers для хеширования и хранения

func (s *SMT) storeLeaf(key, value []byte) ([]byte, error) {
	// Format: [1][Key][Value]
	data := make([]byte, 1+KeySize+len(value))
	data[0] = NodeTypeLeaf
	copy(data[1:], key)
	copy(data[1+KeySize:], value)
	
	h := hash(data)
	err := s.store.Set(h, data)
	return h, err
}

func (s *SMT) storeInternal(left, right []byte) ([]byte, error) {
	// Format: [0][LeftHash][RightHash]
	data := make([]byte, 1+HashSize*2)
	data[0] = NodeTypeInternal
	copy(data[1:], left)
	copy(data[1+HashSize:], right)

	h := hash(data)
	err := s.store.Set(h, data)
	return h, err
}

// bitIsSet возвращает true, если бит на позиции idx равен 1
func bitIsSet(key []byte, idx int) bool {
	byteIdx := idx / 8
	bitIdx := 7 - (idx % 8)
	return (key[byteIdx]>>bitIdx)&1 == 1
}

// hash использует BLAKE3
func hash(data ...[]byte) []byte {
	hasher := blake3.New()
	for _, d := range data {
		hasher.Write(d)
	}
	return hasher.Sum(nil)
}