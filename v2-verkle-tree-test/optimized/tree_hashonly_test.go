// optimized/tree_hashonly_test.go

package optimized

import (
	"encoding/json"
	"fmt"
	"runtime"
	"sync"
	"testing"
	"time"
)

// =============================================================================
// ФУНКЦИОНАЛЬНЫЕ ТЕСТЫ
// =============================================================================

// TestHashOnlyBasicOperations - базовые операции в режиме HashOnly
func TestHashOnlyBasicOperations(t *testing.T) {
	config := NewConfigHashOnly()
	tree, err := New(config, nil)
	if err != nil {
		t.Fatalf("Failed to create tree: %v", err)
	}
	defer tree.Close()

	// Вставка
	testData := []struct {
		userID string
		value  string
	}{
		{"user1", "data1"},
		{"user2", "data2"},
		{"user3", "data3"},
	}

	for _, td := range testData {
		userData := &UserData{
			Balances: map[string]float64{"USD": 100.0},
			Metadata: map[string]interface{}{"value": td.value},
		}
		data, _ := json.Marshal(userData)
		
		if err := tree.Insert(td.userID, data); err != nil {
			t.Errorf("Insert failed for %s: %v", td.userID, err)
		}
	}

	// Чтение
	for _, td := range testData {
		data, err := tree.Get(td.userID)
		if err != nil {
			t.Errorf("Get failed for %s: %v", td.userID, err)
		}
		if len(data) == 0 {
			t.Errorf("Empty data for %s", td.userID)
		}

		var userData UserData
		if err := json.Unmarshal(data, &userData); err != nil {
			t.Errorf("Unmarshal failed: %v", err)
		}
		
		if userData.Metadata["value"] != td.value {
			t.Errorf("Wrong value for %s: got %v, want %v", 
				td.userID, userData.Metadata["value"], td.value)
		}
	}

	// Получение root
	root := tree.GetRoot()
	if len(root) != 32 {
		t.Errorf("Invalid root hash length: %d", len(root))
	}

	finalRoot, err := tree.GetFinalRoot()
	if err != nil {
		t.Errorf("GetFinalRoot failed: %v", err)
	}
	if len(finalRoot) != 32 {
		t.Errorf("Invalid final root hash length: %d", len(finalRoot))
	}

	t.Logf("✅ HashOnly basic operations: PASS")
	t.Logf("   Root hash: %x...", root[:8])
}

// TestHashOnlyProofDisabled - проверка что proof'ы отключены
func TestHashOnlyProofDisabled(t *testing.T) {
	config := NewConfigHashOnly()
	tree, err := New(config, nil)
	if err != nil {
		t.Fatalf("Failed to create tree: %v", err)
	}
	defer tree.Close()

	// Вставляем данные
	tree.Insert("user1", []byte("data1"))

	// Пытаемся создать proof - должна быть ошибка
	_, err = tree.GenerateProof("user1")
	if err == nil {
		t.Error("GenerateProof should fail in HashOnly mode")
	}
	if err.Error() != "proof generation disabled in HashOnly mode" {
		t.Errorf("Wrong error message: %v", err)
	}

	// Пытаемся создать multi-proof - должна быть ошибка
	_, err = tree.GenerateMultiProof([]string{"user1"})
	if err == nil {
		t.Error("GenerateMultiProof should fail in HashOnly mode")
	}

	t.Logf("✅ HashOnly proof disabled: PASS")
}

// TestHashOnlyVsKZG - сравнение режимов
func TestHashOnlyVsKZG(t *testing.T) {
	size := 1000

	// HashOnly режим
	t.Run("HashOnly", func(t *testing.T) {
		config := NewConfigHashOnly()
		tree, _ := New(config, nil)
		defer tree.Close()

		start := time.Now()
		for i := 0; i < size; i++ {
			userID := fmt.Sprintf("user_%d", i)
			userData := &UserData{Balances: map[string]float64{"USD": float64(i)}}
			data, _ := json.Marshal(userData)
			tree.Insert(userID, data)
		}
		hashOnlyTime := time.Since(start)

		root := tree.GetRoot()
		t.Logf("HashOnly: %d inserts in %v (%.2f µs/op)", 
			size, hashOnlyTime, float64(hashOnlyTime)/float64(size)/1e3)
		t.Logf("Root: %x...", root[:8])
	})

	// KZG режим
	t.Run("KZG", func(t *testing.T) {
		srs := getTestSRS(t)
		config := NewConfig(srs)
		tree, _ := New(config, nil)
		defer tree.Close()

		start := time.Now()
		for i := 0; i < size; i++ {
			userID := fmt.Sprintf("user_%d", i)
			userData := &UserData{Balances: map[string]float64{"USD": float64(i)}}
			data, _ := json.Marshal(userData)
			tree.Insert(userID, data)
		}
		kzgTime := time.Since(start)

		root, _ := tree.GetFinalRoot()
		t.Logf("KZG: %d inserts in %v (%.2f µs/op)", 
			size, kzgTime, float64(kzgTime)/float64(size)/1e3)
		t.Logf("Root: %x...", root[:8])
	})
}

// TestHashOnlyBatch - батч операции
func TestHashOnlyBatch(t *testing.T) {
	config := NewConfigHashOnly()
	tree, err := New(config, nil)
	if err != nil {
		t.Fatalf("Failed to create tree: %v", err)
	}
	defer tree.Close()

	size := 5000
	batch := tree.NewBatch()

	start := time.Now()
	for i := 0; i < size; i++ {
		userID := fmt.Sprintf("batch_user_%d", i)
		userData := &UserData{Balances: map[string]float64{"USD": float64(i)}}
		data, _ := json.Marshal(userData)
		batch.Add(userID, data)
	}

	root, err := tree.CommitBatch(batch)
	if err != nil {
		t.Fatalf("CommitBatch failed: %v", err)
	}
	elapsed := time.Since(start)

	// Проверяем что данные доступны
	for i := 0; i < 10; i++ {
		userID := fmt.Sprintf("batch_user_%d", i)
		data, err := tree.Get(userID)
		if err != nil {
			t.Errorf("Get failed for %s: %v", userID, err)
		}
		if len(data) == 0 {
			t.Errorf("Empty data for %s", userID)
		}
	}

	t.Logf("✅ HashOnly batch: PASS")
	t.Logf("   %d elements in %v (%.2f µs/op)", size, elapsed, float64(elapsed)/float64(size)/1e3)
	t.Logf("   Root: %x...", root[:8])
}

// TestHashOnlyParallel - параллельные операции
func TestHashOnlyParallel(t *testing.T) {
	config := NewConfigHashOnly()
	tree, err := New(config, nil)
	if err != nil {
		t.Fatalf("Failed to create tree: %v", err)
	}
	defer tree.Close()

	workers := runtime.NumCPU()
	opsPerWorker := 500
	
	var wg sync.WaitGroup
	errChan := make(chan error, workers)

	start := time.Now()
	
	// Параллельная вставка
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func(workerID int) {
			defer wg.Done()
			batch := tree.NewBatch()
			for i := 0; i < opsPerWorker; i++ {
				userID := fmt.Sprintf("parallel_%d_%d", workerID, i)
				userData := &UserData{Balances: map[string]float64{"USD": float64(i)}}
				data, _ := json.Marshal(userData)
				batch.Add(userID, data)
			}
			if _, err := tree.CommitBatch(batch); err != nil {
				errChan <- err
			}
		}(w)
	}

	wg.Wait()
	close(errChan)
	elapsed := time.Since(start)

	// Проверяем ошибки
	for err := range errChan {
		t.Errorf("Parallel insert error: %v", err)
	}

	totalOps := workers * opsPerWorker
	t.Logf("✅ HashOnly parallel: PASS")
	t.Logf("   %d workers, %d ops each = %d total", workers, opsPerWorker, totalOps)
	t.Logf("   Time: %v (%.2f µs/op)", elapsed, float64(elapsed)/float64(totalOps)/1e3)

	// Параллельное чтение
	wg = sync.WaitGroup{}
	readErrors := 0
	var mu sync.Mutex

	start = time.Now()
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func(workerID int) {
			defer wg.Done()
			for i := 0; i < opsPerWorker/10; i++ {
				userID := fmt.Sprintf("parallel_%d_%d", workerID, i*10)
				_, err := tree.Get(userID)
				if err != nil {
					mu.Lock()
					readErrors++
					mu.Unlock()
				}
			}
		}(w)
	}
	wg.Wait()
	readTime := time.Since(start)

	if readErrors > 0 {
		t.Errorf("Read errors: %d", readErrors)
	}
	
	t.Logf("   Parallel reads: %v", readTime)
}

// TestHashOnlyStats - статистика
func TestHashOnlyStats(t *testing.T) {
	config := NewConfigHashOnly()
	tree, err := New(config, nil)
	if err != nil {
		t.Fatalf("Failed to create tree: %v", err)
	}
	defer tree.Close()

	// Вставляем данные
	size := 1000
	batch := tree.NewBatch()
	for i := 0; i < size; i++ {
		userID := fmt.Sprintf("stats_user_%d", i)
		userData := &UserData{Balances: map[string]float64{"USD": float64(i)}}
		data, _ := json.Marshal(userData)
		batch.Add(userID, data)
	}
	tree.CommitBatch(batch)

	// Читаем для кэша
	for i := 0; i < 100; i++ {
		userID := fmt.Sprintf("stats_user_%d", i*10)
		tree.Get(userID)
	}

	stats := tree.Stats()
	
	t.Logf("✅ HashOnly stats:")
	t.Logf("   Node count:     %v", stats["node_count"])
	t.Logf("   Cache hits:     %v", stats["cache_hits"])
	t.Logf("   Cache misses:   %v", stats["cache_misses"])
	t.Logf("   Cache hit rate: %.2f%%", stats["cache_hit_rate"].(float64)*100)
	t.Logf("   Workers:        %v", stats["workers"])
	t.Logf("   Cache size:     %v", stats["cache_size"])

	if stats["node_count"].(int) != size {
		t.Errorf("Wrong node count: got %v, want %d", stats["node_count"], size)
	}
}

// =============================================================================
// БЕНЧМАРКИ
// =============================================================================

// BenchmarkHashOnlyInsert - бенчмарк вставки в режиме HashOnly
func BenchmarkHashOnlyInsert(b *testing.B) {
	config := NewConfigHashOnly()
	tree, _ := New(config, nil)
	defer tree.Close()

	userData := &UserData{Balances: map[string]float64{"USD": 100.0}}
	data, _ := json.Marshal(userData)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		userID := fmt.Sprintf("bench_user_%d", i)
		tree.Insert(userID, data)
	}
}

// BenchmarkKZGInsert - бенчмарк вставки в режиме KZG
func BenchmarkKZGInsert(b *testing.B) {
	srs := getTestSRS(&testing.T{})
	config := NewConfig(srs)
	tree, _ := New(config, nil)
	defer tree.Close()

	userData := &UserData{Balances: map[string]float64{"USD": 100.0}}
	data, _ := json.Marshal(userData)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		userID := fmt.Sprintf("bench_user_%d", i)
		tree.Insert(userID, data)
	}
}

// BenchmarkHashOnlyBatch - бенчмарк батч вставки HashOnly
func BenchmarkHashOnlyBatch(b *testing.B) {
	sizes := []int{100, 1000, 10000}
	
	for _, size := range sizes {
		b.Run(fmt.Sprintf("N=%d", size), func(b *testing.B) {
			config := NewConfigHashOnly()
			tree, _ := New(config, nil)
			defer tree.Close()

			// Подготавливаем данные
			batches := make([]*Batch, b.N)
			for i := 0; i < b.N; i++ {
				batch := tree.NewBatch()
				for j := 0; j < size; j++ {
					userID := fmt.Sprintf("bench_%d_%d", i, j)
					userData := &UserData{Balances: map[string]float64{"USD": float64(j)}}
					data, _ := json.Marshal(userData)
					batch.Add(userID, data)
				}
				batches[i] = batch
			}

			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				tree.CommitBatch(batches[i])
			}
		})
	}
}

// BenchmarkKZGBatch - бенчмарк батч вставки KZG
func BenchmarkKZGBatch(b *testing.B) {
	sizes := []int{100, 1000, 10000}
	
	for _, size := range sizes {
		b.Run(fmt.Sprintf("N=%d", size), func(b *testing.B) {
			srs := getTestSRS(&testing.T{})
			config := NewConfig(srs)
			tree, _ := New(config, nil)
			defer tree.Close()

			// Подготавливаем данные
			batches := make([]*Batch, b.N)
			for i := 0; i < b.N; i++ {
				batch := tree.NewBatch()
				for j := 0; j < size; j++ {
					userID := fmt.Sprintf("bench_%d_%d", i, j)
					userData := &UserData{Balances: map[string]float64{"USD": float64(j)}}
					data, _ := json.Marshal(userData)
					batch.Add(userID, data)
				}
				batches[i] = batch
			}

			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				tree.CommitBatch(batches[i])
			}
		})
	}
}

// BenchmarkHashOnlyGet - бенчмарк чтения HashOnly
func BenchmarkHashOnlyGet(b *testing.B) {
	config := NewConfigHashOnly()
	tree, _ := New(config, nil)
	defer tree.Close()

	// Заполняем дерево
	size := 10000
	batch := tree.NewBatch()
	for i := 0; i < size; i++ {
		userID := fmt.Sprintf("get_user_%d", i)
		userData := &UserData{Balances: map[string]float64{"USD": float64(i)}}
		data, _ := json.Marshal(userData)
		batch.Add(userID, data)
	}
	tree.CommitBatch(batch)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		userID := fmt.Sprintf("get_user_%d", i%size)
		tree.Get(userID)
	}
}

// BenchmarkKZGGet - бенчмарк чтения KZG
func BenchmarkKZGGet(b *testing.B) {
	srs := getTestSRS(&testing.T{})
	config := NewConfig(srs)
	tree, _ := New(config, nil)
	defer tree.Close()

	// Заполняем дерево
	size := 10000
	batch := tree.NewBatch()
	for i := 0; i < size; i++ {
		userID := fmt.Sprintf("get_user_%d", i)
		userData := &UserData{Balances: map[string]float64{"USD": float64(i)}}
		data, _ := json.Marshal(userData)
		batch.Add(userID, data)
	}
	tree.CommitBatch(batch)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		userID := fmt.Sprintf("get_user_%d", i%size)
		tree.Get(userID)
	}
}

// BenchmarkHashOnlyGetRoot - бенчмарк получения root hash
func BenchmarkHashOnlyGetRoot(b *testing.B) {
	config := NewConfigHashOnly()
	tree, _ := New(config, nil)
	defer tree.Close()

	// Заполняем дерево
	batch := tree.NewBatch()
	for i := 0; i < 1000; i++ {
		userID := fmt.Sprintf("root_user_%d", i)
		userData := &UserData{Balances: map[string]float64{"USD": float64(i)}}
		data, _ := json.Marshal(userData)
		batch.Add(userID, data)
	}
	tree.CommitBatch(batch)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		tree.GetRoot()
	}
}

// BenchmarkKZGGetFinalRoot - бенчмарк получения KZG root
func BenchmarkKZGGetFinalRoot(b *testing.B) {
	srs := getTestSRS(&testing.T{})
	config := NewConfig(srs)
	tree, _ := New(config, nil)
	defer tree.Close()

	// Заполняем дерево
	batch := tree.NewBatch()
	for i := 0; i < 1000; i++ {
		userID := fmt.Sprintf("root_user_%d", i)
		userData := &UserData{Balances: map[string]float64{"USD": float64(i)}}
		data, _ := json.Marshal(userData)
		batch.Add(userID, data)
	}
	tree.CommitBatch(batch)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		tree.GetFinalRoot()
	}
}

// BenchmarkHashOnlyParallelInsert - параллельная вставка HashOnly
func BenchmarkHashOnlyParallelInsert(b *testing.B) {
	config := NewConfigHashOnly()
	tree, _ := New(config, nil)
	defer tree.Close()

	workers := runtime.NumCPU()
	
	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		i := 0
		for pb.Next() {
			userID := fmt.Sprintf("parallel_user_%d", i)
			userData := &UserData{Balances: map[string]float64{"USD": float64(i)}}
			data, _ := json.Marshal(userData)
			tree.Insert(userID, data)
			i++
		}
	})
	
	b.ReportMetric(float64(workers), "workers")
}

// BenchmarkKZGParallelInsert - параллельная вставка KZG
func BenchmarkKZGParallelInsert(b *testing.B) {
	srs := getTestSRS(&testing.T{})
	config := NewConfig(srs)
	tree, _ := New(config, nil)
	defer tree.Close()

	workers := runtime.NumCPU()
	
	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		i := 0
		for pb.Next() {
			userID := fmt.Sprintf("parallel_user_%d", i)
			userData := &UserData{Balances: map[string]float64{"USD": float64(i)}}
			data, _ := json.Marshal(userData)
			tree.Insert(userID, data)
			i++
		}
	})
	
	b.ReportMetric(float64(workers), "workers")
}

// BenchmarkComparison - сравнительный бенчмарк
func BenchmarkComparison(b *testing.B) {
	sizes := []int{100, 1000, 10000}
	
	for _, size := range sizes {
		b.Run(fmt.Sprintf("HashOnly_N=%d", size), func(b *testing.B) {
			config := NewConfigHashOnly()
			
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				tree, _ := New(config, nil)
				batch := tree.NewBatch()
				
				for j := 0; j < size; j++ {
					userID := fmt.Sprintf("user_%d", j)
					userData := &UserData{Balances: map[string]float64{"USD": float64(j)}}
					data, _ := json.Marshal(userData)
					batch.Add(userID, data)
				}
				
				tree.CommitBatch(batch)
				tree.GetRoot()
				tree.Close()
			}
		})
		
		b.Run(fmt.Sprintf("KZG_N=%d", size), func(b *testing.B) {
			srs := getTestSRS(&testing.T{})
			config := NewConfig(srs)
			
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				tree, _ := New(config, nil)
				batch := tree.NewBatch()
				
				for j := 0; j < size; j++ {
					userID := fmt.Sprintf("user_%d", j)
					userData := &UserData{Balances: map[string]float64{"USD": float64(j)}}
					data, _ := json.Marshal(userData)
					batch.Add(userID, data)
				}
				
				tree.CommitBatch(batch)
				tree.GetFinalRoot()
				tree.Close()
			}
		})
	}
}
