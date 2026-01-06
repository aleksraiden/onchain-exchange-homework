package main

import (
	"bytes"
	"crypto/rand"
	"testing"
)

// Helper: генерация случайного ключа
func randomKey() []byte {
	k := make([]byte, 32)
	rand.Read(k)
	return k
}

// === ФУНКЦИОНАЛЬНЫЕ ТЕСТЫ ===

// TestCRUD покрывает полный жизненный цикл ключа
func TestCRUD(t *testing.T) {
	store := NewMemoryStore()
	smt := NewSMT(store, 100) // Включаем кэш

	key := hash([]byte("test_key"))
	val1 := []byte("value_1")
	val2 := []byte("value_2")

	// 1. INSERT
	if _, err := smt.Update(key, val1); err != nil {
		t.Fatalf("Insert failed: %v", err)
	}
	got, err := smt.Get(key)
	if err != nil || !bytes.Equal(got, val1) {
		t.Fatalf("Get after Insert failed. Expected %s, got %s", val1, got)
	}

	// 2. UPDATE (Overwrite)
	if _, err := smt.Update(key, val2); err != nil {
		t.Fatalf("Update failed: %v", err)
	}
	got, err = smt.Get(key)
	if err != nil || !bytes.Equal(got, val2) {
		t.Fatalf("Get after Update failed. Expected %s, got %s", val2, got)
	}

	// 3. DELETE
	if _, err := smt.Update(key, nil); err != nil {
		t.Fatalf("Delete failed: %v", err)
	}
	_, err = smt.Get(key)
	if err == nil {
		t.Fatal("Get after Delete should fail, but it returned a value")
	}
}

// TestDeleteEdgeCases проверяет граничные случаи удаления
func TestDeleteEdgeCases(t *testing.T) {
	store := NewMemoryStore()
	smt := NewSMT(store, 100)

	key := hash([]byte("key1"))
	val := []byte("data")

	// 1. Удаление из пустого дерева
	newRoot, err := smt.Update(key, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(newRoot, make([]byte, 32)) {
		t.Fatal("Root should remain empty")
	}

	// 2. Удаление несуществующего ключа в непустом дереве
	otherKey := hash([]byte("key2"))
	smt.Update(otherKey, val) // Add data
	rootBefore := make([]byte, 32)
	copy(rootBefore, smt.Root())

	smt.Update(key, nil) // Try delete non-existing
	if !bytes.Equal(smt.Root(), rootBefore) {
		t.Fatal("Root changed after deleting non-existing key")
	}

	// 3. Insert -> Delete -> Insert (проверка на "зомби" данные)
	smt.Update(key, val) // Insert
	smt.Update(key, nil) // Delete
	
	// Проверяем, что кэш или стор не отдают старое
	if _, err := smt.Get(key); err == nil {
		t.Fatal("Key should be gone")
	}
	
	smt.Update(key, val) // Insert again
	got, _ := smt.Get(key)
	if !bytes.Equal(got, val) {
		t.Fatal("Re-insertion failed")
	}
}

// TestDeleteCollapse проверяет, что дерево физически уменьшается
// (превращает Internal ноды обратно в Leaf/Shortcuts)
func TestDeleteCollapse(t *testing.T) {
	store := NewMemoryStore()
	smt := NewSMT(store, 0) // Без кэша, чтобы проверить состояние store

	// Генерируем два ключа, отличающихся только последним битом (глубокая коллизия)
	// Для blake3 это сложно подобрать вручную, поэтому используем детерминированные значения для теста логики
	// Но так как у нас есть реализация bitIsSet, мы просто возьмем
	// два ключа, которые точно разделятся.
	
	keyA := make([]byte, 32) // все нули
	keyB := make([]byte, 32)
	keyB[0] = 128            // первый бит 1 (10000000...)

	// 1. Вставка A
	smt.Update(keyA, []byte("A"))
	rootA := make([]byte, 32)
	copy(rootA, smt.Root())

	// 2. Вставка B (Дерево разрастается: Root -> Internal -> (LeafA, LeafB))
	smt.Update(keyB, []byte("B"))

	if bytes.Equal(smt.Root(), rootA) {
		t.Fatal("Root must change")
	}

	// 3. Удаление B (Дерево должно схлопнуться обратно: Root -> LeafA)
	smt.Update(keyB, nil)

	if !bytes.Equal(smt.Root(), rootA) {
		t.Fatalf("Collapse failed. Root should match state before B insert.\nExpected: %x\nGot:      %x", rootA, smt.Root())
	}
}

// === БЕНЧМАРКИ ОПЕРАЦИЙ ===

// 1. BenchmarkInsertNew: Вставка АБСОЛЮТНО НОВЫХ ключей.
// Это самая "тяжелая" вставка, т.к. вызывает аллокации и рост структуры.
func BenchmarkOperation_Insert_New(b *testing.B) {
	store := NewMemoryStore()
	smt := NewSMT(store, 10000)
	val := []byte("payload")
	
	// Предгенерация ключей, чтобы не тратить время в таймере
	keys := make([][]byte, b.N)
	for i := 0; i < b.N; i++ {
		keys[i] = randomKey()
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		smt.Update(keys[i], val)
	}
}

// 2. BenchmarkUpdateExisting: Перезапись СУЩЕСТВУЮЩИХ ключей.
// Структура дерева не меняется, меняются только значения в листьях и пересчитываются хеши.
func BenchmarkOperation_Update_Existing(b *testing.B) {
	// Setup: заполняем дерево 10K элементами
	count := 10_000
	store := NewMemoryStore()
	smt := NewSMT(store, 10000)
	keys := make([][]byte, count)
	val := []byte("initial")
	
	for i := 0; i < count; i++ {
		keys[i] = randomKey()
		smt.Update(keys[i], val)
	}

	newVal := []byte("updated_value")
	
	b.ResetTimer()
	// В цикле постоянно обновляем одни и те же ключи по кругу
	for i := 0; i < b.N; i++ {
		key := keys[i % count]
		smt.Update(key, newVal)
	}
}

// 3. BenchmarkGetExisting: Чтение существующих ключей (с кэшем)
func BenchmarkOperation_Get_Existing(b *testing.B) {
	count := 10_000
	store := NewMemoryStore()
	smt := NewSMT(store, 10000)
	keys := make([][]byte, count)
	val := []byte("data")
	
	for i := 0; i < count; i++ {
		keys[i] = randomKey()
		smt.Update(keys[i], val)
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		smt.Get(keys[i % count])
	}
}

// 4. BenchmarkGetNonExisting: Попытка чтения несуществующих ключей.
// Должно быть быстро, если дерево разреженное (быстрый отказ).
func BenchmarkOperation_Get_NonExisting(b *testing.B) {
	store := NewMemoryStore()
	smt := NewSMT(store, 10000)
	// Заполняем немного, чтобы дерево не было пустым
	for i := 0; i < 1000; i++ {
		smt.Update(randomKey(), []byte("data"))
	}

	keys := make([][]byte, b.N)
	for i := 0; i < b.N; i++ {
		keys[i] = randomKey() // Вероятность коллизии с существующими почти 0
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		smt.Get(keys[i])
	}
}

// 5. BenchmarkDelete: Удаление существующих ключей.
// Самый сложный бенчмарк, т.к. дерево пустеет.
// Мы заполняем дерево пачками, а потом замеряем время удаления.
func BenchmarkOperation_Delete_Existing(b *testing.B) {
	// Чтобы замер был честным, мы не можем удалить ключ дважды.
	// Поэтому стратегия: StopTimer -> Наполнить дерево -> StartTimer -> Удалить всё.
	
	batchSize := 10_000
	val := []byte("trash")
	
	b.ResetTimer()
	
	for i := 0; i < b.N; {
		b.StopTimer()
		
		// Создаем новое дерево и наполняем ключами
		store := NewMemoryStore()
		smt := NewSMT(store, 10000)
		keys := make([][]byte, batchSize)
		for k := 0; k < batchSize; k++ {
			keys[k] = randomKey()
			smt.Update(keys[k], val)
		}
		
		b.StartTimer()
		
		// Замеряем удаление
		// Важно: мы инкрементим i внутри, но цикл ограничен batchSize или остатком b.N
		limit := batchSize
		if b.N - i < batchSize {
			limit = b.N - i
		}
		
		for k := 0; k < limit; k++ {
			smt.Update(keys[k], nil)
		}
		
		i += limit
	}
}

// 6. BenchmarkMerkleRoot: Полное построение (уже было, но для полноты картины)
func BenchmarkFullBuild_100K(b *testing.B) {
	n := 100_000
	keys := make([][]byte, n)
	for i := 0; i < n; i++ {
		keys[i] = randomKey()
	}
	val := []byte("data")

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		b.StopTimer()
		store := NewMemoryStore()
		smt := NewSMT(store, 10000)
		b.StartTimer()
		
		for _, k := range keys {
			smt.Update(k, val)
		}
	}
}

// BenchmarkParallelGet проверяет масштабируемость чтения в несколько потоков
func BenchmarkParallelGet(b *testing.B) {
	count := 100_000
	store := NewMemoryStore()
	// Создаем SMT с большим кэшем, чтобы работать из памяти
	smt := NewSMT(store, 100000) 
	keys := make([][]byte, count)
	val := []byte("data")
	
	// Подготовка данных
	for i := 0; i < count; i++ {
		keys[i] = randomKey()
		smt.Update(keys[i], val)
	}

	b.ResetTimer()
	
	// Запускаем параллельные горутины
	b.RunParallel(func(pb *testing.PB) {
		i := 0
		for pb.Next() {
			// Читаем случайные ключи по кругу
			key := keys[i % count]
			_, err := smt.Get(key)
			if err != nil {
				b.Fatal(err)
			}
			i++
		}
	})
}