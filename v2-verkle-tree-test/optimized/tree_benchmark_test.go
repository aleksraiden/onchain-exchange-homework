// optimized/tree_benchmark_test.go

package optimized

import (
	"encoding/json"
	"fmt"
	"math/big"
	"sync"
	"testing"
	
	kzg_bls12381 "github.com/consensys/gnark-crypto/ecc/bls12-381/kzg"
)

// Глобальный SRS для benchmarks
var (
	benchSRS     *kzg_bls12381.SRS
	benchSRSOnce sync.Once
)

func getBenchSRS(b *testing.B) *kzg_bls12381.SRS {
	benchSRSOnce.Do(func() {
		var err error
		benchSRS, err = kzg_bls12381.NewSRS(256, big.NewInt(12345))
		if err != nil {
			b.Fatalf("Failed to initialize SRS: %v", err)
		}
	})
	return benchSRS
}

// ============================================================
// INSERT BENCHMARKS
// ============================================================

// BenchmarkInsert - бенчмарк одиночной вставки
func BenchmarkInsert(b *testing.B) {
	srs := getBenchSRS(b)
	config := NewConfig(srs)
	tree, _ := New(config, nil)
	defer tree.Close()
	
	userData := &UserData{
		Balances: map[string]float64{"USD": 1000.0},
	}
	data, _ := json.Marshal(userData)
	
	b.ResetTimer()
	
	for i := 0; i < b.N; i++ {
		userID := fmt.Sprintf("bench_user_%d", i)
		tree.Insert(userID, data)
	}
	
	b.StopTimer()
	tree.WaitForCommit()
}

// BenchmarkBatchInsert8 - batch вставка 8 элементов
func BenchmarkBatchInsert8(b *testing.B) {
	benchmarkBatchInsert(b, 8)
}

// BenchmarkBatchInsert16 - batch вставка 16 элементов
func BenchmarkBatchInsert16(b *testing.B) {
	benchmarkBatchInsert(b, 16)
}

// BenchmarkBatchInsert32 - batch вставка 32 элементов
func BenchmarkBatchInsert32(b *testing.B) {
	benchmarkBatchInsert(b, 32)
}

// BenchmarkBatchInsert64 - batch вставка 64 элементов
func BenchmarkBatchInsert64(b *testing.B) {
	benchmarkBatchInsert(b, 64)
}

// BenchmarkBatchInsert128 - batch вставка 128 элементов
func BenchmarkBatchInsert128(b *testing.B) {
	benchmarkBatchInsert(b, 128)
}

func benchmarkBatchInsert(b *testing.B, batchSize int) {
	srs := getBenchSRS(b)
	config := NewConfig(srs)
	tree, _ := New(config, nil)
	defer tree.Close()
	
	// Подготовка данных
	batches := make([]*Batch, b.N)
	for i := 0; i < b.N; i++ {
		batch := tree.NewBatch()
		for j := 0; j < batchSize; j++ {
			userID := fmt.Sprintf("bench_batch_%d_user_%d", i, j)
			userData := &UserData{
				Balances: map[string]float64{"USD": float64(j * 100)},
			}
			data, _ := json.Marshal(userData)
			batch.Add(userID, data)
		}
		batches[i] = batch
	}
	
	b.ResetTimer()
	
	for i := 0; i < b.N; i++ {
		tree.CommitBatch(batches[i])
	}
	
	b.StopTimer()
	tree.WaitForCommit()
	
	// Метрики
	b.ReportMetric(float64(batchSize), "elements/batch")
	b.ReportMetric(float64(b.Elapsed().Nanoseconds())/float64(b.N)/float64(batchSize), "ns/element")
}

// ============================================================
// GET BENCHMARKS
// ============================================================

// BenchmarkGet - бенчмарк чтения (cache hit)
func BenchmarkGet(b *testing.B) {
	srs := getBenchSRS(b)
	config := NewConfig(srs)
	tree, _ := New(config, nil)
	defer tree.Close()
	
	// Вставляем данные
	numUsers := 1000
	userIDs := make([]string, numUsers)
	
	for i := 0; i < numUsers; i++ {
		userID := fmt.Sprintf("get_bench_user_%d", i)
		userIDs[i] = userID
		
		userData := &UserData{
			Balances: map[string]float64{"USD": float64(i * 100)},
		}
		data, _ := json.Marshal(userData)
		tree.Insert(userID, data)
	}
	
	tree.WaitForCommit()
	
	b.ResetTimer()
	
	for i := 0; i < b.N; i++ {
		userID := userIDs[i%numUsers]
		tree.Get(userID)
	}
}

// BenchmarkGetColdCache - бенчмарк чтения (cache miss)
func BenchmarkGetColdCache(b *testing.B) {
	srs := getBenchSRS(b)
	config := NewConfig(srs)
	tree, _ := New(config, nil)
	defer tree.Close()
	
	// Вставляем много данных (больше чем размер кэша)
	numUsers := 10000
	userIDs := make([]string, numUsers)
	
	batch := tree.NewBatch()
	for i := 0; i < numUsers; i++ {
		userID := fmt.Sprintf("cold_bench_user_%d", i)
		userIDs[i] = userID
		
		userData := &UserData{
			Balances: map[string]float64{"USD": float64(i * 100)},
		}
		data, _ := json.Marshal(userData)
		batch.Add(userID, data)
	}
	
	tree.CommitBatch(batch)
	tree.WaitForCommit()
	
	b.ResetTimer()
	
	for i := 0; i < b.N; i++ {
		// Случайный доступ (холодный кэш)
		userID := userIDs[(i*7919)%numUsers] // Prime number для "случайности"
		tree.Get(userID)
	}
}

// ============================================================
// PROOF GENERATION BENCHMARKS
// ============================================================

// BenchmarkProofGeneration - генерация single proof
func BenchmarkProofGeneration(b *testing.B) {
	srs := getBenchSRS(b)
	config := NewConfig(srs)
	tree, _ := New(config, nil)
	defer tree.Close()
	
	// Вставляем данные
	numUsers := 1000
	userIDs := make([]string, numUsers)
	
	batch := tree.NewBatch()
	for i := 0; i < numUsers; i++ {
		userID := fmt.Sprintf("proof_bench_user_%d", i)
		userIDs[i] = userID
		
		userData := &UserData{
			Balances: map[string]float64{"USD": float64(i * 100)},
		}
		data, _ := json.Marshal(userData)
		batch.Add(userID, data)
	}
	
	tree.CommitBatch(batch)
	tree.WaitForCommit()
	
	b.ResetTimer()
	
	for i := 0; i < b.N; i++ {
		userID := userIDs[i%numUsers]
		tree.GenerateProof(userID)
	}
}

// BenchmarkProofGenerationParallel - параллельная генерация proofs
func BenchmarkProofGenerationParallel(b *testing.B) {
	srs := getBenchSRS(b)
	config := NewConfig(srs)
	tree, _ := New(config, nil)
	defer tree.Close()
	
	// Вставляем данные
	numUsers := 1000
	userIDs := make([]string, numUsers)
	
	batch := tree.NewBatch()
	for i := 0; i < numUsers; i++ {
		userID := fmt.Sprintf("parallel_proof_user_%d", i)
		userIDs[i] = userID
		
		userData := &UserData{
			Balances: map[string]float64{"USD": float64(i * 100)},
		}
		data, _ := json.Marshal(userData)
		batch.Add(userID, data)
	}
	
	tree.CommitBatch(batch)
	tree.WaitForCommit()
	
	b.ResetTimer()
	
	b.RunParallel(func(pb *testing.PB) {
		i := 0
		for pb.Next() {
			userID := userIDs[i%numUsers]
			tree.GenerateProof(userID)
			i++
		}
	})
}

// ============================================================
// BUNDLED MULTI-PROOF BENCHMARKS
// ============================================================

// BenchmarkBundledProof10 - bundled proof для 10 пользователей
func BenchmarkBundledProof10(b *testing.B) {
	benchmarkBundledProof(b, 10)
}

// BenchmarkBundledProof50 - bundled proof для 50 пользователей
func BenchmarkBundledProof50(b *testing.B) {
	benchmarkBundledProof(b, 50)
}

// BenchmarkBundledProof100 - bundled proof для 100 пользователей
func BenchmarkBundledProof100(b *testing.B) {
	benchmarkBundledProof(b, 100)
}

func benchmarkBundledProof(b *testing.B, numUsers int) {
	srs := getBenchSRS(b)
	config := NewConfig(srs)
	tree, _ := New(config, nil)
	defer tree.Close()
	
	// Вставляем данные
	totalUsers := numUsers * 10
	userIDs := make([]string, totalUsers)
	
	batch := tree.NewBatch()
	for i := 0; i < totalUsers; i++ {
		userID := fmt.Sprintf("bundled_bench_%d_user_%d", numUsers, i)
		userIDs[i] = userID
		
		userData := &UserData{
			Balances: map[string]float64{"USD": float64(i * 100)},
		}
		data, _ := json.Marshal(userData)
		batch.Add(userID, data)
	}
	
	tree.CommitBatch(batch)
	tree.WaitForCommit()
	
	proofUsers := userIDs[:numUsers]
	
	b.ResetTimer()
	
	for i := 0; i < b.N; i++ {
		tree.GenerateMultiProof(proofUsers)
	}
	
	b.ReportMetric(float64(numUsers), "users/proof")
	b.ReportMetric(float64(b.Elapsed().Nanoseconds())/float64(b.N)/float64(numUsers), "ns/user")
}

// ============================================================
// PROOF VERIFICATION BENCHMARKS
// ============================================================

// BenchmarkProofVerification - верификация single proof
func BenchmarkProofVerification(b *testing.B) {
	srs := getBenchSRS(b)
	config := NewConfig(srs)
	tree, _ := New(config, nil)
	defer tree.Close()
	
	// Вставляем данные
	numUsers := 100
	proofs := make([]*Proof, numUsers)
	
	batch := tree.NewBatch()
	for i := 0; i < numUsers; i++ {
		userID := fmt.Sprintf("verify_bench_user_%d", i)
		
		userData := &UserData{
			Balances: map[string]float64{"USD": float64(i * 100)},
		}
		data, _ := json.Marshal(userData)
		batch.Add(userID, data)
	}
	
	tree.CommitBatch(batch)
	tree.WaitForCommit()
	
	// Генерируем proofs
	for i := 0; i < numUsers; i++ {
		userID := fmt.Sprintf("verify_bench_user_%d", i)
		proof, _ := tree.GenerateProof(userID)
		proofs[i] = proof
	}
	
	b.ResetTimer()
	
	for i := 0; i < b.N; i++ {
		proof := proofs[i%numUsers]
		VerifySingleProof(proof, config)
	}
}

// BenchmarkBundledProofVerification - верификация bundled proof
func BenchmarkBundledProofVerification(b *testing.B) {
	srs := getBenchSRS(b)
	config := NewConfig(srs)
	tree, _ := New(config, nil)
	defer tree.Close()
	
	// Вставляем данные
	numUsers := 50
	userIDs := make([]string, numUsers)
	
	batch := tree.NewBatch()
	for i := 0; i < numUsers; i++ {
		userID := fmt.Sprintf("bundled_verify_user_%d", i)
		userIDs[i] = userID
		
		userData := &UserData{
			Balances: map[string]float64{"USD": float64(i * 100)},
		}
		data, _ := json.Marshal(userData)
		batch.Add(userID, data)
	}
	
	tree.CommitBatch(batch)
	tree.WaitForCommit()
	
	// Генерируем bundled proof
	bundledProof, _ := tree.GenerateMultiProof(userIDs)
	
	b.ResetTimer()
	
	for i := 0; i < b.N; i++ {
		VerifyBundledProof(bundledProof, config)
	}
	
	b.ReportMetric(float64(numUsers), "users/proof")
}

// ============================================================
// COMPARISON BENCHMARKS
// ============================================================

// BenchmarkSingleVsBundled - сравнение single vs bundled (50 пользователей)
func BenchmarkSingleVsBundled(b *testing.B) {
	srs := getBenchSRS(b)
	config := NewConfig(srs)
	
	numUsers := 50
	
	b.Run("Single_Generation", func(b *testing.B) {
		tree, _ := New(config, nil)
		defer tree.Close()
		
		userIDs := make([]string, numUsers)
		batch := tree.NewBatch()
		for i := 0; i < numUsers; i++ {
			userID := fmt.Sprintf("single_comp_user_%d", i)
			userIDs[i] = userID
			
			userData := &UserData{
				Balances: map[string]float64{"USD": float64(i * 100)},
			}
			data, _ := json.Marshal(userData)
			batch.Add(userID, data)
		}
		tree.CommitBatch(batch)
		tree.WaitForCommit()
		
		b.ResetTimer()
		
		for i := 0; i < b.N; i++ {
			for _, userID := range userIDs {
				tree.GenerateProof(userID)
			}
		}
	})
	
	b.Run("Bundled_Generation", func(b *testing.B) {
		tree, _ := New(config, nil)
		defer tree.Close()
		
		userIDs := make([]string, numUsers)
		batch := tree.NewBatch()
		for i := 0; i < numUsers; i++ {
			userID := fmt.Sprintf("bundled_comp_user_%d", i)
			userIDs[i] = userID
			
			userData := &UserData{
				Balances: map[string]float64{"USD": float64(i * 100)},
			}
			data, _ := json.Marshal(userData)
			batch.Add(userID, data)
		}
		tree.CommitBatch(batch)
		tree.WaitForCommit()
		
		b.ResetTimer()
		
		for i := 0; i < b.N; i++ {
			tree.GenerateMultiProof(userIDs)
		}
	})
}

// ============================================================
// CACHE BENCHMARKS
// ============================================================

// BenchmarkCacheHitRate - измерение эффективности кэша
func BenchmarkCacheHitRate(b *testing.B) {
	srs := getBenchSRS(b)
	config := NewConfig(srs)
	tree, _ := New(config, nil)
	defer tree.Close()
	
	// Горячие данные (меньше размера кэша)
	hotUsers := 100
	userIDs := make([]string, hotUsers)
	
	batch := tree.NewBatch()
	for i := 0; i < hotUsers; i++ {
		userID := fmt.Sprintf("cache_user_%d", i)
		userIDs[i] = userID
		
		userData := &UserData{
			Balances: map[string]float64{"USD": float64(i * 100)},
		}
		data, _ := json.Marshal(userData)
		batch.Add(userID, data)
	}
	
	tree.CommitBatch(batch)
	tree.WaitForCommit()
	
	b.ResetTimer()
	
	// Частый доступ к горячим данным
	for i := 0; i < b.N; i++ {
		userID := userIDs[i%hotUsers]
		tree.Get(userID)
	}
	
	b.StopTimer()
	
	stats := tree.Stats()
	hitRate := stats["cache_hit_rate"].(float64)
	b.ReportMetric(hitRate, "%_cache_hit")
}

// ============================================================
// FULL WORKFLOW BENCHMARK
// ============================================================

// BenchmarkFullWorkflow - полный цикл: insert -> proof -> verify
func BenchmarkFullWorkflow(b *testing.B) {
	srs := getBenchSRS(b)
	config := NewConfig(srs)
	
	b.ResetTimer()
	
	for i := 0; i < b.N; i++ {
		b.StopTimer()
		
		tree, _ := New(config, nil)
		
		// Insert
		userID := fmt.Sprintf("workflow_user_%d", i)
		userData := &UserData{
			Balances: map[string]float64{"USD": 1000.0},
		}
		data, _ := json.Marshal(userData)
		
		b.StartTimer()
		tree.Insert(userID, data)
		tree.WaitForCommit()
		
		// Generate proof
		proof, _ := tree.GenerateProof(userID)
		
		// Verify proof
		VerifySingleProof(proof, config)
		
		b.StopTimer()
		tree.Close()
	}
}
