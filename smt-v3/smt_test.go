package main

import (
	"bytes"
	"crypto/rand"
	"testing"
	
	mrand "math/rand"
)

// Helper: генерация случайного ключа
func randomKey() []byte {
	k := make([]byte, 32)
	rand.Read(k)
	return k
}

// === ФУНКЦИОНАЛЬНЫЕ ТЕСТЫ ===

// TestCRUD покрывает полный жизненный цикл ключа (используя Atomic Update)
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

// TestBatchingLogic проверяет работу отложенного коммита (Set + Commit)
func TestBatchingLogic(t *testing.T) {
	store := NewMemoryStore()
	smt := NewSMT(store, 100)

	key1 := hash([]byte("batch_k1"))
	val1 := []byte("batch_v1")
	key2 := hash([]byte("batch_k2"))
	val2 := []byte("batch_v2")

	// 1. Делаем Set. Root НЕ должен измениться.
	emptyRoot := make([]byte, 32)
	copy(emptyRoot, smt.Root())

	smt.Set(key1, val1)
	smt.Set(key2, val2)

	if !bytes.Equal(smt.Root(), emptyRoot) {
		t.Fatal("Root changed before Commit!")
	}

	// 2. Проверяем, что Get видит незакоммиченные данные (pending)
	got, err := smt.Get(key1)
	if err != nil || !bytes.Equal(got, val1) {
		t.Fatalf("Get fail on pending data. Got %s, err %v", got, err)
	}

	// 3. Commit. Root должен измениться.
	newRoot, err := smt.Commit()
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Equal(newRoot, emptyRoot) {
		t.Fatal("Root did not change after Commit")
	}

	// 4. Проверяем данные после коммита
	got, err = smt.Get(key2)
	if err != nil || !bytes.Equal(got, val2) {
		t.Fatal("Get failed after Commit")
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

	keyA := make([]byte, 32) // все нули
	keyB := make([]byte, 32)
	keyB[0] = 128            // первый бит 1

	// 1. Вставка A
	smt.Update(keyA, []byte("A"))
	rootA := make([]byte, 32)
	copy(rootA, smt.Root())

	// 2. Вставка B (Дерево разрастается)
	smt.Update(keyB, []byte("B"))

	if bytes.Equal(smt.Root(), rootA) {
		t.Fatal("Root must change")
	}

	// 3. Удаление B (Дерево должно схлопнуться обратно)
	smt.Update(keyB, nil)

	if !bytes.Equal(smt.Root(), rootA) {
		t.Fatalf("Collapse failed. Root should match state before B insert.\nExpected: %x\nGot:      %x", rootA, smt.Root())
	}
}

func TestDeepPrefixCollision(t *testing.T) {
	store := NewMemoryStore()
	smt := NewSMT(store, 0)

	// Создаем два ключа, одинаковые везде, кроме последнего байта
	key1 := make([]byte, 32) // Все нули
	key2 := make([]byte, 32) // Все нули...
	key2[31] = 1             // ...кроме конца

	val1 := []byte("value1")
	val2 := []byte("value2")

	smt.Update(key1, val1)
	smt.Update(key2, val2)

	// Проверяем, что оба ключа на месте
	got1, _ := smt.Get(key1)
	got2, _ := smt.Get(key2)

	if !bytes.Equal(got1, val1) || !bytes.Equal(got2, val2) {
		t.Fatal("Failed to retrieve deep collision keys")
	}

	// Проверяем удаление одного из них (должен произойти Collapse)
	smt.Update(key1, nil)
	
	got2after, _ := smt.Get(key2)
	if !bytes.Equal(got2after, val2) {
		t.Fatal("Remaining key corrupted after deep sibling deletion")
	}
	
	// Проверяем, что удаленный ключ исчез
	if _, err := smt.Get(key1); err == nil {
		t.Fatal("Deleted key still exists")
	}
}

func TestFullNode(t *testing.T) {
	store := NewMemoryStore()
	smt := NewSMT(store, 0)

	// Вставляем 256 ключей, которые отличаются только первым байтом.
	// Это создаст один корневой узел с 256 детьми.
	for i := 0; i < 256; i++ {
		key := make([]byte, 32)
		key[0] = byte(i) // 0x00, 0x01 ... 0xFF
		val := []byte{byte(i)}
		smt.Update(key, val)
	}

	// Проверяем чтение всех 256 ключей
	for i := 0; i < 256; i++ {
		key := make([]byte, 32)
		key[0] = byte(i)
		got, err := smt.Get(key)
		if err != nil {
			t.Fatalf("Failed to get key index %d", i)
		}
		if got[0] != byte(i) {
			t.Fatalf("Value mismatch at index %d", i)
		}
	}
}

func TestPersistence(t *testing.T) {
	// 1. Создаем дерево и пишем данные
	store := NewMemoryStore()
	smt1 := NewSMT(store, 0)
	
	key := hash([]byte("persistance_key"))
	val := []byte("persistance_val")
	
	smt1.Update(key, val)
	rootHash := smt1.Root()

	// 2. "Выбрасываем" smt1 и создаем smt2 НА ТОМ ЖЕ store
	smt2 := NewSMT(store, 0)
	
	// ВАЖНО: В реальном коде нужен метод smt.SetRoot(rootHash)
	// Здесь хак для теста (т.к. мы в том же пакете main)
	smt2.root = rootHash 

	// 3. Пытаемся прочитать данные
	got, err := smt2.Get(key)
	if err != nil {
		t.Fatal("Failed to load key from persistent store")
	}
	if !bytes.Equal(got, val) {
		t.Fatal("Value corrupted after reload")
	}
}

func TestFuzz(t *testing.T) {
	store := NewMemoryStore()
	smt := NewSMT(store, 100)
	
	// Эталон данных (Ground Truth)
	mirror := make(map[[32]byte][]byte)
	
	// Набор ключей для теста
	keys := make([][32]byte, 100)
	for i := range keys {
		k := randomKey()
		copy(keys[i][:], k)
	}

	// 10,000 случайных операций
	nOps := 10000
	for i := 0; i < nOps; i++ {
		// Выбираем случайный ключ и операцию
		idx := mrand.Intn(len(keys))
		keyArr := keys[idx]
		keySlice := keyArr[:]
		
		op := mrand.Intn(100)
		
		if op < 50 { 
			// 50% UPDATE / INSERT
			val := make([]byte, 8)
			mrand.Read(val)
			
			// Обновляем SMT
			if _, err := smt.Update(keySlice, val); err != nil {
				t.Fatalf("Update failed iter %d: %v", i, err)
			}
			// Обновляем эталон
			mirror[keyArr] = val
			
		} else if op < 80 {
			// 30% GET
			got, err := smt.Get(keySlice)
			expected, exists := mirror[keyArr]
			
			if exists {
				if err != nil {
					t.Fatalf("Get failed iter %d: key should exist", i)
				}
				if !bytes.Equal(got, expected) {
					t.Fatalf("Value mismatch iter %d. Want %x, Got %x", i, expected, got)
				}
			} else {
				if err == nil {
					t.Fatalf("Get succeeded iter %d: key should NOT exist", i)
				}
			}
			
		} else {
			// 20% DELETE
			if _, err := smt.Update(keySlice, nil); err != nil {
				t.Fatalf("Delete failed iter %d: %v", i, err)
			}
			delete(mirror, keyArr)
		}
	}
}

// === БЕНЧМАРКИ ОПЕРАЦИЙ ===

// 1. BenchmarkInsertNew: Вставка АБСОЛЮТНО НОВЫХ ключей (Immediate Update).
func BenchmarkOperation_Insert_New(b *testing.B) {
	store := NewMemoryStore()
	smt := NewSMT(store, 10000)
	val := []byte("payload")
	
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
func BenchmarkOperation_Update_Existing(b *testing.B) {
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
	for i := 0; i < b.N; i++ {
		key := keys[i % count]
		smt.Update(key, newVal)
	}
}

// 3. BenchmarkGetExisting: Чтение существующих ключей
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
func BenchmarkOperation_Get_NonExisting(b *testing.B) {
	store := NewMemoryStore()
	smt := NewSMT(store, 10000)
	for i := 0; i < 1000; i++ {
		smt.Update(randomKey(), []byte("data"))
	}

	keys := make([][]byte, b.N)
	for i := 0; i < b.N; i++ {
		keys[i] = randomKey()
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		smt.Get(keys[i])
	}
}

// 5. BenchmarkDelete: Удаление.
func BenchmarkOperation_Delete_Existing(b *testing.B) {
	batchSize := 10_000
	val := []byte("trash")
	
	b.ResetTimer()
	
	for i := 0; i < b.N; {
		b.StopTimer()
		store := NewMemoryStore()
		smt := NewSMT(store, 10000)
		keys := make([][]byte, batchSize)
		for k := 0; k < batchSize; k++ {
			keys[k] = randomKey()
			smt.Update(keys[k], val)
		}
		b.StartTimer()
		
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

// 6. BenchmarkFullBuild_100K: Построение дерева по одному (старый метод).
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

// 7. BenchmarkBatchInsert_100K: Построение дерева через Set + Commit (НОВЫЙ МЕТОД).
// Ожидается значительное ускорение по сравнению с BenchmarkFullBuild_100K.
func BenchmarkBatchInsert_100K(b *testing.B) {
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
		
		// Фаза 1: Set (просто в память)
		for _, k := range keys {
			smt.Set(k, val)
		}
		// Фаза 2: Commit (один проход по дереву)
		smt.Commit()
	}
}

// BenchmarkParallelGet проверяет масштабируемость чтения
func BenchmarkParallelGet(b *testing.B) {
	count := 100_000
	store := NewMemoryStore()
	smt := NewSMT(store, 100000) 
	keys := make([][]byte, count)
	val := []byte("data")
	
	// Используем быстрый Commit для подготовки теста
	for i := 0; i < count; i++ {
		keys[i] = randomKey()
		smt.Set(keys[i], val)
	}
	smt.Commit()

	b.ResetTimer()
	
	b.RunParallel(func(pb *testing.PB) {
		i := 0
		for pb.Next() {
			key := keys[i % count]
			_, err := smt.Get(key)
			if err != nil {
				b.Fatal(err)
			}
			i++
		}
	})
}