// Создайте файл hash_benchmark_test.go

package verkletree

import (
	"crypto/sha256"
	"crypto/sha512"
	"hash"
	"testing"
	
	"github.com/zeebo/blake3"
	"golang.org/x/crypto/sha3"
)

// BenchmarkHashFunctions сравнивает разные хеш-функции
func BenchmarkHashFunctions(t *testing.B) {
	// Размеры данных типичные для узлов дерева
	sizes := []struct {
		name string
		data []byte
	}{
		{"32B_single_hash", make([]byte, 32)},      // один хеш
		{"256B_small_node", make([]byte, 256)},     // маленький узел (8 хешей)
		{"1KB_medium_node", make([]byte, 1024)},    // средний узел (32 хеша)
		{"4KB_large_node", make([]byte, 4096)},     // большой узел (128 хешей)
		{"8KB_full_width128", make([]byte, 8192)},  // width=128, все заполнено
	}
	
	hashFuncs := []struct {
		name string
		fn   func([]byte) []byte
	}{
		{"blake3", func(data []byte) []byte {
			hasher := blake3.New()
			hasher.Write(data)
			return hasher.Sum(nil)
		}},
		{"sha256", func(data []byte) []byte {
			h := sha256.Sum256(data)
			return h[:]
		}},
		{"sha3_256", func(data []byte) []byte {
			h := sha3.Sum256(data)
			return h[:]
		}},
		{"sha512", func(data []byte) []byte {
			h := sha512.Sum512(data)
			return h[:]
		}},
	}
	
	for _, size := range sizes {
		for _, hashFunc := range hashFuncs {
			t.Run(size.name+"_"+hashFunc.name, func(b *testing.B) {
				b.SetBytes(int64(len(size.data)))
				
				for i := 0; i < b.N; i++ {
					_ = hashFunc.fn(size.data)
				}
			})
		}
	}
}

// BenchmarkNodeCommitment имитирует реальную работу дерева
func BenchmarkNodeCommitment(t *testing.B) {
	// Имитация коммитмента узла с width=128
	width := 128
	childHashes := make([][]byte, width)
	for i := 0; i < width; i++ {
		childHashes[i] = make([]byte, 32)
		// Заполняем случайными данными
		for j := 0; j < 32; j++ {
			childHashes[i][j] = byte(i + j)
		}
	}
	
	t.Run("blake3_node_commitment", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			hasher := blake3.New()
			for _, childHash := range childHashes {
				hasher.Write(childHash)
			}
			_ = hasher.Sum(nil)
		}
	})
	
	t.Run("sha256_node_commitment", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			hasher := sha256.New()
			for _, childHash := range childHashes {
				hasher.Write(childHash)
			}
			_ = hasher.Sum(nil)
		}
	})
	
	t.Run("sha3_256_node_commitment", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			hasher := sha3.New256()
			for _, childHash := range childHashes {
				hasher.Write(childHash)
			}
			_ = hasher.Sum(nil)
		}
	})
}

// BenchmarkTreePathHashing - хеширование пути в дереве
func BenchmarkTreePathHashing(t *testing.B) {
	depth := 6
	nodeData := make([]byte, 32) // один хеш на уровень
	
	hashers := []struct {
		name string
		fn   func() hash.Hash
	}{
		{"blake3", func() hash.Hash { return blake3.New() }},
		{"sha256", func() hash.Hash { return sha256.New() }},
		{"sha3_256", func() hash.Hash { return sha3.New256() }},
	}
	
	for _, hasher := range hashers {
		t.Run("path_"+hasher.name, func(b *testing.B) {
			for i := 0; i < b.N; i++ {
				// Имитация хеширования пути от листа до корня
				currentHash := nodeData
				for level := 0; level < depth; level++ {
					h := hasher.fn()
					h.Write(currentHash)
					currentHash = h.Sum(nil)
				}
			}
		})
	}
}
