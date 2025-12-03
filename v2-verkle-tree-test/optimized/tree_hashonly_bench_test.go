// optimized/tree_hashonly_bench_test.go

package optimized

import (
	"encoding/json"
	"fmt"
	"testing"
)

// BenchmarkHashOnlyInsertOptimized - оптимизированная версия с переиспользованием
func BenchmarkHashOnlyInsertOptimized(b *testing.B) {
	config := NewConfigHashOnly()
	tree, _ := New(config, nil)
	defer tree.Close()

	// Предварительно сериализуем данные
	userData := &UserData{Balances: map[string]float64{"USD": 100.0}}
	data, _ := json.Marshal(userData)

	b.ResetTimer()
	b.ReportAllocs()
	
	for i := 0; i < b.N; i++ {
		userID := fmt.Sprintf("bench_user_%d", i)
		tree.Insert(userID, data)
	}
	
	b.ReportMetric(float64(b.N)/b.Elapsed().Seconds(), "ops/sec")
}

// BenchmarkBatchSizes - детальный анализ размеров батча
func BenchmarkBatchSizes(b *testing.B) {
	sizes := []int{10, 50, 100, 500, 1000, 5000, 10000}
	
	for _, size := range sizes {
		b.Run(fmt.Sprintf("HashOnly_N=%d", size), func(b *testing.B) {
			config := NewConfigHashOnly()
			tree, _ := New(config, nil)
			defer tree.Close()

			// Предварительная подготовка данных
			userIDs := make([]string, size)
			dataItems := make([][]byte, size)
			
			for i := 0; i < size; i++ {
				userIDs[i] = fmt.Sprintf("bench_%d", i)
				userData := &UserData{Balances: map[string]float64{"USD": float64(i)}}
				dataItems[i], _ = json.Marshal(userData)
			}

			b.ResetTimer()
			b.ReportAllocs()
			
			for i := 0; i < b.N; i++ {
				batch := tree.NewBatch()
				for j := 0; j < size; j++ {
					batch.Add(userIDs[j], dataItems[j])
				}
				tree.CommitBatch(batch)
			}
			
			opsPerSec := float64(size*b.N) / b.Elapsed().Seconds()
			b.ReportMetric(opsPerSec, "inserts/sec")
		})
	}
}

// BenchmarkMemoryFootprint - анализ памяти
func BenchmarkMemoryFootprint(b *testing.B) {
	sizes := []int{1000, 10000, 100000}
	
	for _, size := range sizes {
		b.Run(fmt.Sprintf("N=%d", size), func(b *testing.B) {
			b.ReportAllocs()
			
			for i := 0; i < b.N; i++ {
				config := NewConfigHashOnly()
				tree, _ := New(config, nil)
				
				batch := tree.NewBatch()
				for j := 0; j < size; j++ {
					userID := fmt.Sprintf("mem_%d", j)
					userData := &UserData{Balances: map[string]float64{"USD": float64(j)}}
					data, _ := json.Marshal(userData)
					batch.Add(userID, data)
				}
				tree.CommitBatch(batch)
				tree.Close()
			}
		})
	}
}

// BenchmarkCacheEfficiency - эффективность кэша
func BenchmarkCacheEfficiency(b *testing.B) {
	config := NewConfigHashOnly()
	tree, _ := New(config, nil)
	defer tree.Close()

	// Заполняем дерево
	size := 10000
	batch := tree.NewBatch()
	for i := 0; i < size; i++ {
		userID := fmt.Sprintf("cache_%d", i)
		userData := &UserData{Balances: map[string]float64{"USD": float64(i)}}
		data, _ := json.Marshal(userData)
		batch.Add(userID, data)
	}
	tree.CommitBatch(batch)

	// Бенчмарк горячего кэша
	b.Run("HotCache", func(b *testing.B) {
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			// Читаем одни и те же элементы (должны быть в кэше)
			userID := fmt.Sprintf("cache_%d", i%100)
			tree.Get(userID)
		}
	})

	// Бенчмарк холодного кэша
	b.Run("ColdCache", func(b *testing.B) {
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			// Читаем случайные элементы
			userID := fmt.Sprintf("cache_%d", (i*7919)%size)
			tree.Get(userID)
		}
	})

	// Статистика кэша после тестов
	stats := tree.Stats()
	b.Logf("Cache stats: hits=%v, misses=%v, hit_rate=%.2f%%",
		stats["cache_hits"], stats["cache_misses"], 
		stats["cache_hit_rate"].(float64)*100)
}

// BenchmarkWorkerScaling - масштабирование воркеров
func BenchmarkWorkerScaling(b *testing.B) {
	workerCounts := []int{1, 2, 4, 8, 16}
	
	for _, workers := range workerCounts {
		b.Run(fmt.Sprintf("Workers=%d", workers), func(b *testing.B) {
			config := NewConfigHashOnly()
			config.Workers = workers
			tree, _ := New(config, nil)
			defer tree.Close()

			opsPerWorker := 1000
			
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				batch := tree.NewBatch()
				for j := 0; j < opsPerWorker; j++ {
					userID := fmt.Sprintf("worker_%d_%d", i, j)
					userData := &UserData{Balances: map[string]float64{"USD": float64(j)}}
					data, _ := json.Marshal(userData)
					batch.Add(userID, data)
				}
				tree.CommitBatch(batch)
			}
			
			opsPerSec := float64(opsPerWorker*b.N) / b.Elapsed().Seconds()
			b.ReportMetric(opsPerSec, "ops/sec")
			b.ReportMetric(opsPerSec/float64(workers), "ops/sec/worker")
		})
	}
}

// BenchmarkSequentialVsBatch - сравнение последовательной вставки и батча
func BenchmarkSequentialVsBatch(b *testing.B) {
	size := 1000
	
	b.Run("Sequential", func(b *testing.B) {
		config := NewConfigHashOnly()
		tree, _ := New(config, nil)
		defer tree.Close()
		
		userData := &UserData{Balances: map[string]float64{"USD": 100.0}}
		data, _ := json.Marshal(userData)
		
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			for j := 0; j < size; j++ {
				userID := fmt.Sprintf("seq_%d_%d", i, j)
				tree.Insert(userID, data)
			}
		}
	})
	
	b.Run("Batch", func(b *testing.B) {
		config := NewConfigHashOnly()
		tree, _ := New(config, nil)
		defer tree.Close()
		
		userData := &UserData{Balances: map[string]float64{"USD": 100.0}}
		data, _ := json.Marshal(userData)
		
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			batch := tree.NewBatch()
			for j := 0; j < size; j++ {
				userID := fmt.Sprintf("batch_%d_%d", i, j)
				batch.Add(userID, data)
			}
			tree.CommitBatch(batch)
		}
	})
}

// BenchmarkGetPatterns - паттерны чтения
func BenchmarkGetPatterns(b *testing.B) {
	config := NewConfigHashOnly()
	tree, _ := New(config, nil)
	defer tree.Close()

	// Заполняем дерево
	size := 10000
	batch := tree.NewBatch()
	for i := 0; i < size; i++ {
		userID := fmt.Sprintf("get_%d", i)
		userData := &UserData{Balances: map[string]float64{"USD": float64(i)}}
		data, _ := json.Marshal(userData)
		batch.Add(userID, data)
	}
	tree.CommitBatch(batch)

	// Последовательное чтение
	b.Run("Sequential", func(b *testing.B) {
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			userID := fmt.Sprintf("get_%d", i%size)
			tree.Get(userID)
		}
	})

	// Случайное чтение
	b.Run("Random", func(b *testing.B) {
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			userID := fmt.Sprintf("get_%d", (i*7919)%size)
			tree.Get(userID)
		}
	})

	// Локальное чтение (locality)
	b.Run("Locality", func(b *testing.B) {
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			base := (i / 100) * 100
			userID := fmt.Sprintf("get_%d", (base+i%100)%size)
			tree.Get(userID)
		}
	})
}

// BenchmarkRootHashComputation - вычисление root hash
func BenchmarkRootHashComputation(b *testing.B) {
	sizes := []int{100, 1000, 10000}
	
	for _, size := range sizes {
		b.Run(fmt.Sprintf("N=%d", size), func(b *testing.B) {
			config := NewConfigHashOnly()
			tree, _ := New(config, nil)
			
			// Заполняем дерево
			batch := tree.NewBatch()
			for i := 0; i < size; i++ {
				userID := fmt.Sprintf("root_%d", i)
				userData := &UserData{Balances: map[string]float64{"USD": float64(i)}}
				data, _ := json.Marshal(userData)
				batch.Add(userID, data)
			}
			tree.CommitBatch(batch)
			
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				tree.GetRoot()
			}
			
			tree.Close()
		})
	}
}
