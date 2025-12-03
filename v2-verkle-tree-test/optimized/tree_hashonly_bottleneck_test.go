// optimized/tree_hashonly_bottleneck_test.go

package optimized

import (
	"encoding/json"
	"fmt"
	"runtime"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// BenchmarkLockContention - измеряем contention на мьютексах
func BenchmarkLockContention(b *testing.B) {
	config := NewConfigHashOnly()
	tree, _ := New(config, nil)
	defer tree.Close()

	// Заполняем дерево
	batch := tree.NewBatch()
	for i := 0; i < 10000; i++ {
		userID := fmt.Sprintf("lock_%d", i)
		userData := &UserData{Balances: map[string]float64{"USD": float64(i)}}
		data, _ := json.Marshal(userData)
		batch.Add(userID, data)
	}
	tree.CommitBatch(batch)

	b.Run("ParallelReads", func(b *testing.B) {
		b.RunParallel(func(pb *testing.PB) {
			i := 0
			for pb.Next() {
				userID := fmt.Sprintf("lock_%d", i%10000)
				tree.Get(userID)
				i++
			}
		})
	})

	b.Run("ParallelWrites", func(b *testing.B) {
		var counter int64
		b.RunParallel(func(pb *testing.PB) {
			for pb.Next() {
				idx := atomic.AddInt64(&counter, 1)
				userID := fmt.Sprintf("write_%d", idx)
				userData := &UserData{Balances: map[string]float64{"USD": float64(idx)}}
				data, _ := json.Marshal(userData)
				tree.Insert(userID, data)
			}
		})
	})

	b.Run("MixedReadWrite", func(b *testing.B) {
		var counter int64
		b.RunParallel(func(pb *testing.PB) {
			i := 0
			for pb.Next() {
				if i%10 < 8 { // 80% reads
					userID := fmt.Sprintf("lock_%d", i%10000)
					tree.Get(userID)
				} else { // 20% writes
					idx := atomic.AddInt64(&counter, 1)
					userID := fmt.Sprintf("mixed_%d", idx)
					userData := &UserData{Balances: map[string]float64{"USD": float64(idx)}}
					data, _ := json.Marshal(userData)
					tree.Insert(userID, data)
				}
				i++
			}
		})
	})
}

// BenchmarkBatchVsParallelInserts - сравнение батча и параллельных Insert
func BenchmarkBatchVsParallelInserts(b *testing.B) {
	size := 1000

	b.Run("SingleBatch", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			config := NewConfigHashOnly()
			tree, _ := New(config, nil)
			
			batch := tree.NewBatch()
			for j := 0; j < size; j++ {
				userID := fmt.Sprintf("single_%d", j)
				userData := &UserData{Balances: map[string]float64{"USD": float64(j)}}
				data, _ := json.Marshal(userData)
				batch.Add(userID, data)
			}
			tree.CommitBatch(batch)
			tree.Close()
		}
		
		b.ReportMetric(float64(size*b.N)/b.Elapsed().Seconds(), "inserts/sec")
	})

	b.Run("MultipleBatches", func(b *testing.B) {
		workers := runtime.NumCPU()
		batchSize := size / workers
		
		for i := 0; i < b.N; i++ {
			config := NewConfigHashOnly()
			tree, _ := New(config, nil)
			
			var wg sync.WaitGroup
			for w := 0; w < workers; w++ {
				wg.Add(1)
				go func(workerID int) {
					defer wg.Done()
					batch := tree.NewBatch()
					for j := 0; j < batchSize; j++ {
						userID := fmt.Sprintf("multi_%d_%d", workerID, j)
						userData := &UserData{Balances: map[string]float64{"USD": float64(j)}}
						data, _ := json.Marshal(userData)
						batch.Add(userID, data)
					}
					tree.CommitBatch(batch)
				}(w)
			}
			wg.Wait()
			tree.Close()
		}
		
		b.ReportMetric(float64(size*b.N)/b.Elapsed().Seconds(), "inserts/sec")
	})

	b.Run("ParallelInserts", func(b *testing.B) {
		workers := runtime.NumCPU()
		
		for i := 0; i < b.N; i++ {
			config := NewConfigHashOnly()
			tree, _ := New(config, nil)
			
			var wg sync.WaitGroup
			var counter int64
			
			for w := 0; w < workers; w++ {
				wg.Add(1)
				go func() {
					defer wg.Done()
					for {
						idx := atomic.AddInt64(&counter, 1)
						if int(idx) > size {
							break
						}
						userID := fmt.Sprintf("parallel_%d", idx)
						userData := &UserData{Balances: map[string]float64{"USD": float64(idx)}}
						data, _ := json.Marshal(userData)
						tree.Insert(userID, data)
					}
				}()
			}
			wg.Wait()
			tree.Close()
		}
		
		b.ReportMetric(float64(size*b.N)/b.Elapsed().Seconds(), "inserts/sec")
	})
}

// TestWorkerBottleneck - детальный анализ bottleneck
func TestWorkerBottleneck(t *testing.T) {
	config := NewConfigHashOnly()
	tree, _ := New(config, nil)
	defer tree.Close()

	workerCounts := []int{1, 2, 4, 8, 16}
	opsPerWorker := 1000
	
	t.Logf("\n🔍 Worker Bottleneck Analysis:")
	t.Logf("%-10s %-15s %-15s %-15s", "Workers", "Total Time", "Ops/Sec", "Speedup")
	t.Logf("%s", "---------------------------------------------------------------")
	
	var baselineTime time.Duration
	
	for _, workers := range workerCounts {
		config := NewConfigHashOnly()
		config.Workers = workers
		tree, _ := New(config, nil)
		
		start := time.Now()
		
		var wg sync.WaitGroup
		for w := 0; w < workers; w++ {
			wg.Add(1)
			go func(workerID int) {
				defer wg.Done()
				batch := tree.NewBatch()
				for i := 0; i < opsPerWorker; i++ {
					userID := fmt.Sprintf("worker_%d_%d", workerID, i)
					userData := &UserData{Balances: map[string]float64{"USD": float64(i)}}
					data, _ := json.Marshal(userData)
					batch.Add(userID, data)
				}
				tree.CommitBatch(batch)
			}(w)
		}
		wg.Wait()
		
		elapsed := time.Since(start)
		totalOps := workers * opsPerWorker
		opsPerSec := float64(totalOps) / elapsed.Seconds()
		
		if workers == 1 {
			baselineTime = elapsed
		}
		
		speedup := float64(baselineTime) / float64(elapsed)
		
		t.Logf("%-10d %-15v %-15.0f %-15.2fx", workers, elapsed, opsPerSec, speedup)
		
		tree.Close()
	}
	
	t.Logf("\n💡 Ideal speedup for %d workers should be ~%dx", 
		workerCounts[len(workerCounts)-1], workerCounts[len(workerCounts)-1])
}

// BenchmarkOptimalBatchSize - находим оптимальный размер батча
func BenchmarkOptimalBatchSize(b *testing.B) {
	sizes := []int{1, 10, 50, 100, 200, 500, 1000, 2000, 5000}
	
	for _, size := range sizes {
		b.Run(fmt.Sprintf("N=%d", size), func(b *testing.B) {
			config := NewConfigHashOnly()
			tree, _ := New(config, nil)
			defer tree.Close()
			
			// Подготовка данных
			userIDs := make([]string, size)
			dataItems := make([][]byte, size)
			
			for i := 0; i < size; i++ {
				userIDs[i] = fmt.Sprintf("opt_%d", i)
				userData := &UserData{Balances: map[string]float64{"USD": float64(i)}}
				dataItems[i], _ = json.Marshal(userData)
			}
			
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				batch := tree.NewBatch()
				for j := 0; j < size; j++ {
					batch.Add(userIDs[j], dataItems[j])
				}
				tree.CommitBatch(batch)
			}
			
			nsPerInsert := float64(b.Elapsed().Nanoseconds()) / float64(size*b.N)
			b.ReportMetric(nsPerInsert, "ns/insert")
			b.ReportMetric(1e9/nsPerInsert, "inserts/sec")
		})
	}
}
