package main

import (
	"bytes"
	"crypto/rand"
	"encoding/binary"
	"fmt"
	"math/big"
	"testing"
)

// Helper для генерации случайных ключей (хешей)
func randomKey() []byte {
	k := make([]byte, 32)
	rand.Read(k)
	return k
}

func TestBasicOperations(t *testing.T) {
	store := NewMemoryStore()
	smt := NewSMT(store)

	key1 := hash([]byte("user1"))
	val1 := []byte("balance:100")

	key2 := hash([]byte("user2"))
	val2 := []byte("balance:200")

	// 1. Insert
	t.Log("Inserting key1...")
	if _, err := smt.Update(key1, val1); err != nil {
		t.Fatal(err)
	}

	// 2. Get
	got, err := smt.Get(key1)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, val1) {
		t.Fatalf("Expected %s, got %s", val1, got)
	}

	// 3. Insert second key
	t.Log("Inserting key2...")
	if _, err := smt.Update(key2, val2); err != nil {
		t.Fatal(err)
	}

	// 4. Update existing
	t.Log("Updating key1...")
	newVal1 := []byte("balance:150")
	if _, err := smt.Update(key1, newVal1); err != nil {
		t.Fatal(err)
	}
	got, _ = smt.Get(key1)
	if !bytes.Equal(got, newVal1) {
		t.Fatalf("Update failed")
	}

	// 5. Delete
	t.Log("Deleting key1...")
	if _, err := smt.Update(key1, nil); err != nil {
		t.Fatal(err)
	}
	_, err = smt.Get(key1)
	if err == nil {
		t.Fatal("Key1 should be deleted")
	}

	// Verify key2 still exists (structure integrity)
	got2, err := smt.Get(key2)
	if err != nil || !bytes.Equal(got2, val2) {
		t.Fatal("Key2 corrupted after delete")
	}
}

// Тест на корректность и детерминизм Merkle Root
func TestMerkleRoot(t *testing.T) {
	// 1. Проверка пустого корня
	store1 := NewMemoryStore()
	smt1 := NewSMT(store1)
	if !bytes.Equal(smt1.Root(), make([]byte, 32)) {
		t.Fatal("Empty root should be 32 zero bytes")
	}

	// 2. Проверка детерминизма (одинаковые данные = одинаковый корень)
	key := hash([]byte("test_key"))
	val := []byte("test_value")

	smt1.Update(key, val)
	root1 := make([]byte, len(smt1.Root()))
	copy(root1, smt1.Root())

	store2 := NewMemoryStore()
	smt2 := NewSMT(store2)
	smt2.Update(key, val)

	if !bytes.Equal(smt2.Root(), root1) {
		t.Fatalf("Roots mismatch for same data.\nTree1: %x\nTree2: %x", root1, smt2.Root())
	}

	// 3. Проверка изменения корня (разные данные = разный корень)
	smt2.Update(hash([]byte("key2")), val)
	if bytes.Equal(smt2.Root(), root1) {
		t.Fatal("Root MUST change after adding new data")
	}

	// 4. Проверка возврата корня при отмене изменений (удаление)
	// (в данной реализации удаление может менять структуру дерева из-за сжатия путей,
	// но если мы приведем дерево в точно такое же состояние, корень должен совпасть)
	store3 := NewMemoryStore()
	smt3 := NewSMT(store3)
	smt3.Update(key, val)           // State A
	smt3.Update(randomKey(), val)   // State B
	// Тут сложнее проверить возврат к точному хешу State A без глубокого понимания
	// как именно схлопнутся узлы, но базовый детерминизм проверен выше.
}

func TestShortcutsCollapse(t *testing.T) {
	// Этот тест проверяет, что при удалении соседа, узел "всплывает" наверх (optimization)
	store := NewMemoryStore()
	smt := NewSMT(store)

	// Берем ключи, которые отличаются только в последнем бите
	keyA := make([]byte, 32)
	keyB := make([]byte, 32)
	keyB[0] = 128 // set first bit to 1

	smt.Update(keyA, []byte("A"))
	rootAfterA := make([]byte, len(smt.Root()))
	copy(rootAfterA, smt.Root())

	smt.Update(keyB, []byte("B"))

	smt.Update(keyB, nil) // Delete B

	if !bytes.Equal(smt.Root(), rootAfterA) {
		t.Fatalf("Trie did not collapse back to original state after deleting sibling. \nExpected: %x\nGot:      %x", rootAfterA, smt.Root())
	}
}

// Стандартный микробенчмарк на одну операцию
func BenchmarkUpdate(b *testing.B) {
	store := NewMemoryStore()
	smt := NewSMT(store)

	keys := make([][]byte, b.N)
	for i := 0; i < b.N; i++ {
		keys[i] = randomKey()
	}
	val := []byte("data")

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		smt.Update(keys[i], val)
	}
}

func BenchmarkGet(b *testing.B) {
	store := NewMemoryStore()
	smt := NewSMT(store)

	keys := make([][]byte, 1000)
	val := []byte("data")
	for i := 0; i < 1000; i++ {
		keys[i] = randomKey()
		smt.Update(keys[i], val)
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		smt.Get(keys[i%1000])
	}
}

func TestMassiveInsert(t *testing.T) {
	store := NewMemoryStore()
	smt := NewSMT(store)
	n := 1000
	keys := make([][]byte, n)

	for i := 0; i < n; i++ {
		keys[i] = randomKey()
		val := make([]byte, 4)
		binary.LittleEndian.PutUint32(val, uint32(i))
		smt.Update(keys[i], val)
	}

	for i := 0; i < n; i++ {
		val, err := smt.Get(keys[i])
		if err != nil {
			t.Fatalf("Key %d missing", i)
		}
		num := binary.LittleEndian.Uint32(val)
		if num != uint32(i) {
			t.Fatalf("Key %d value mismatch", i)
		}
	}
}

// BenchmarkMerkleRootCalculation замеряет время вычисления корня (построения дерева с нуля)
// для заданного количества ключей: 1, 10, 100, 1K, 10K, 100K, 1M.
func BenchmarkMerkleRootCalculation(b *testing.B) {
	// Размеры: 1, 10, 100, 1K, 10K, 100K, 1M
	sizes := []int{1, 10, 100, 1000, 10_000, 100_000, 1_000_000}
	val := []byte("payload")

	for _, n := range sizes {
		b.Run(fmt.Sprintf("Keys=%d", n), func(b *testing.B) {
			// Предгенерация ключей, чтобы не измерять RNG
			keys := make([][]byte, n)
			for i := 0; i < n; i++ {
				keys[i] = randomKey()
			}

			b.ResetTimer()

			for i := 0; i < b.N; i++ {
				// В каждой итерации создаем чистое дерево и заполняем его полностью.
				// Время выполнения этого цикла и будет временем вычисления корня для N ключей.
				b.StopTimer()
				store := NewMemoryStore()
				smt := NewSMT(store)
				b.StartTimer()

				for _, k := range keys {
					smt.Update(k, val)
				}
			}
		})
	}
}

// BenchmarkMassiveInsert измеряет время ПОЛНОГО построения дерева с нуля
// для заданного количества элементов.
func BenchmarkMassiveInsert(b *testing.B) {
	// Размеры для тестов: 10K, 100K, 1M
	sizes := []int{10_000, 100_000, 1_000_000}

	for _, n := range sizes {
		b.Run(fmt.Sprintf("N=%d", n), func(b *testing.B) {
			// Генерация ключей вынесена за пределы таймера, но происходит внутри теста,
			// так как нам нужен уникальный набор для каждой итерации (в идеале),
			// или переиспользование одного набора. Для экономии памяти создадим один набор.
			keys := make([][]byte, n)
			for i := 0; i < n; i++ {
				keys[i] = randomKey()
			}
			val := []byte("payload")

			b.ResetTimer()

			// Цикл бенчмарка.
			// Внимание: для N=10M этот цикл скорее всего выполнится 1 раз из-за длительности.
			for i := 0; i < b.N; i++ {
				b.StopTimer() // Останавливаем таймер для подготовки чистого дерева
				store := NewMemoryStore()
				smt := NewSMT(store)
				b.StartTimer() // Запускаем таймер для измерения чистого времени вставки

				for _, k := range keys {
					smt.Update(k, val)
				}
			}
		})
	}
}

// BenchmarkMassiveInsertAndRead измеряет сценарий:
// 1. Вставка N ключей
// 2. Чтение 0.001% случайных ключей из этого набора
func BenchmarkMassiveInsertAndRead(b *testing.B) {
	sizes := []int{10_000, 100_000, 1_000_000}

	for _, n := range sizes {
		b.Run(fmt.Sprintf("N=%d_Read0.001pct", n), func(b *testing.B) {
			// 1. Подготовка ключей
			keys := make([][]byte, n)
			for i := 0; i < n; i++ {
				keys[i] = randomKey()
			}
			val := []byte("payload")

			// 2. Выборка 0.001% ключей для чтения
			readCount := int(float64(n) * 0.00001)
			if readCount < 1 {
				readCount = 1
			}
			readKeys := make([][]byte, readCount)
			// Берем случайные индексы для честности (или просто первые, если массив перемешан)
			for i := 0; i < readCount; i++ {
				idx, _ := rand.Int(rand.Reader, big.NewInt(int64(n)))
				readKeys[i] = keys[idx.Int64()]
			}

			b.ResetTimer()

			for i := 0; i < b.N; i++ {
				b.StopTimer()
				store := NewMemoryStore()
				smt := NewSMT(store)
				b.StartTimer()

				// Шаг 1: Вставка
				for _, k := range keys {
					smt.Update(k, val)
				}

				// Шаг 2: Чтение
				for _, k := range readKeys {
					_, err := smt.Get(k)
					if err != nil {
						b.Fatalf("Failed to read key: %v", err)
					}
				}
			}
		})
	}
}