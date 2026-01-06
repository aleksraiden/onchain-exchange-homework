package trie

import (
	"bytes"
	"crypto/sha256"
	"testing"
)

// Hasher - простая хэш функция для тестов
func Hasher(data ...[]byte) []byte {
	hasher := sha256.New()
	for _, b := range data {
		hasher.Write(b)
	}
	return hasher.Sum(nil)
}

func TestTrieBasicOperations(t *testing.T) {
	// Создаем новое дерево
	trie := NewTrie(nil, Hasher)

	// Добавляем данные
	keys := [][]byte{
		Hasher([]byte("key1")),
		Hasher([]byte("key2")),
		Hasher([]byte("key3")),
	}
	values := [][]byte{
		[]byte("value1"),
		[]byte("value2"),
		[]byte("value3"),
	}

	// Обновляем дерево
	root, err := trie.Update(keys, values)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("Root after update: %x", root)

	// Коммитим изменения в память
	trie.Commit()

	// Получаем значения
	for i, key := range keys {
		val, err := trie.Get(key)
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(val, values[i]) {
			t.Fatalf("expected %s, got %s", values[i], val)
		}
	}

	// Проверяем Merkle proof
	ap, included, _, _, err := trie.MerkleProof(keys[0])
	if err != nil {
		t.Fatal(err)
	}
	if !included {
		t.Fatal("key should be included")
	}
	if !trie.VerifyInclusion(ap, keys[0], values[0]) {
		t.Fatal("failed to verify inclusion")
	}

	t.Logf("✓ Successfully stored and retrieved %d keys", len(keys))
}

func TestTrieDelete(t *testing.T) {
	trie := NewTrie(nil, Hasher)

	keys := [][]byte{Hasher([]byte("key1"))}
	values := [][]byte{[]byte("value1")}

	// Добавляем
	trie.Update(keys, values)
	trie.Commit()

	// Проверяем что есть
	val, _ := trie.Get(keys[0])
	if !bytes.Equal(val, values[0]) {
		t.Fatal("value not found")
	}

	// Удаляем (устанавливаем в DefaultLeaf)
	trie.Update(keys, [][]byte{DefaultLeaf})
	trie.Commit()

	// Проверяем что удалено
	val, _ = trie.Get(keys[0])
	if len(val) != 0 {
		t.Fatal("value should be deleted")
	}

	t.Log("✓ Delete operation successful")
}

func TestTrieStash(t *testing.T) {
	trie := NewTrie(nil, Hasher)

	keys := [][]byte{Hasher([]byte("key1"))}
	values := [][]byte{[]byte("value1")}

	// Добавляем и коммитим
	root1, _ := trie.Update(keys, values)
	trie.Commit()

	// Изменяем но не коммитим
	newValues := [][]byte{[]byte("value2")}
	trie.Update(keys, newValues)

	// Откатываем изменения
	err := trie.Stash(false)
	if err != nil {
		t.Fatal(err)
	}

	// Проверяем что вернулось старое значение
	if !bytes.Equal(trie.Root, root1) {
		t.Fatal("root should be reverted")
	}

	val, _ := trie.Get(keys[0])
	if !bytes.Equal(val, values[0]) {
		t.Fatal("value should be reverted")
	}

	t.Log("✓ Stash operation successful")
}
