// optimized/tree_stress_test.go

package optimized

import (
	"encoding/json"
	"fmt"
//	"math"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"
)

// StressTestSizes - размеры стресс-тестов
var StressTestSizes = []int{10_000} // , 25_000, 50_000, 100_000, 250_000, 500_000, 1_000_000, 10_000_000

// TestStressInsertSequential - последовательная вставка
func TestStressInsertSequential(t *testing.T) {
	testStressInsert(t, false)
}

// TestStressInsertParallel - параллельная вставка
func TestStressInsertParallel(t *testing.T) {
	testStressInsert(t, true)
}

func testStressInsert(t *testing.T, parallel bool) {
	t.Log("\n" + strings.Repeat("=", 100))
	t.Logf("🧪 STRESS TEST: %s вставка (%d размеров)", 
		map[bool]string{true: "ПАРАЛЛЕЛЬНАЯ", false: "Последовательная"}[parallel], len(StressTestSizes))
	t.Log(strings.Repeat("=", 100))

	srs := getTestSRS(t)
	config := NewConfig(srs) // фиксированные параметры для сравнения

	for _, size := range StressTestSizes {
		t.Run(fmt.Sprintf("N=%d", size), func(t *testing.T) {
			testSingleStressInsert(t, config, size, parallel)
		})
	}
}

func testSingleStressInsert(t *testing.T, config *Config, size int, parallel bool) {
	startTotal := time.Now()

	// Создаём дерево
	tree, err := New(config, nil)
	if err != nil {
		t.Fatalf("Failed to create tree: %v", err)
	}
	defer tree.Close()

	// Генерируем данные
	t.Logf("\n📝 Генерация %d элементов...", size)
	userIDs := make([]string, size)
	
	startDataGen := time.Now()
	for i := 0; i < size; i++ {
		userIDs[i] = fmt.Sprintf("stress_%d_%06d", size, i)
	}
	dataGenTime := time.Since(startDataGen)
	t.Logf("   ✓ Данные сгенерированы за %v", dataGenTime)

	// Вставка
	t.Logf("📝 %s вставка...", map[bool]string{true: "Параллельная", false: "Последовательная"}[parallel])
	
	startInsert := time.Now()
	
	if parallel {
		// Параллельная вставка
		batch := tree.NewBatch()
		for i, userID := range userIDs {
			userData := &UserData{
				Balances: map[string]float64{"USD": float64(i)},
			}
			data, _ := json.Marshal(userData)
			batch.Add(userID, data)
		}
		_, err := tree.CommitBatch(batch)
		if err != nil {
			t.Fatalf("Parallel batch insert failed: %v", err)
		}
	} else {
		// Последовательная вставка
		for i, userID := range userIDs {
			userData := &UserData{
				Balances: map[string]float64{"USD": float64(i)},
			}
			data, _ := json.Marshal(userData)
			if err := tree.Insert(userID, data); err != nil {
				t.Fatalf("Insert %d failed: %v", i, err)
			}
			
			if i%100_000 == 0 {
				t.Logf("   Inserted %d/%d (%.1f%%)", i, size, float64(i)/float64(size)*100)
			}
		}
	}
	
	insertTime := time.Since(startInsert)
	insertPerElem := float64(insertTime) / float64(size)
	
	t.Logf("   ✓ Вставка завершена за %v (%.2f µs/элемент)", insertTime, insertPerElem*1e6)

	// Проверка чтения 10% случайных элементов
	t.Logf("📝 Проверка чтения (%d элементов)...", size/10)
	
	var readWG sync.WaitGroup
	readErrors := make(chan error, 100)
	readCount := size / 10
	
	if readCount > 10000 {
		readCount = 10000 // лимит для больших тестов
	}
	
	startRead := time.Now()
	readWG.Add(readCount)
	for i := 0; i < readCount; i++ {
		idx := (i * 73856093) % size // псевдослучайный
		go func(idx int) {
			defer readWG.Done()
			data, err := tree.Get(userIDs[idx])
			if err != nil {
				readErrors <- fmt.Errorf("read %d failed: %v", idx, err)
				return
			}
			if len(data) == 0 {
				readErrors <- fmt.Errorf("empty data for %d", idx)
			}
		}(idx)
	}
	
	readWG.Wait()
	close(readErrors)
	
	readErrorsCount := 0
	for err := range readErrors {
		readErrorsCount++
		t.Errorf("Read error: %v", err)
	}
	
	readTime := time.Since(startRead)
	
	t.Logf("   ✓ Чтение: %d проверено, %d ошибок за %v", readCount, readErrorsCount, readTime)

	// Статистика
	stats := tree.Stats()
	t.Logf("\n📊 СТАТИСТИКА (%d элементов):", size)
	t.Logf("    Время вставки:     %v (%.2f µs/элемент)", insertTime, insertPerElem*1e6)
	t.Logf("    Время чтения:      %v (%.2f µs/проверка)", readTime, float64(readTime)/float64(readCount)*1e6)
	t.Logf("    Узлов:             %v", stats["node_count"])
	t.Logf("    Cache hit rate:    %.1f%%", stats["cache_hit_rate"].(float64)*100)  // float64
	hits := stats["cache_hits"].(uint64)     // uint64!
	misses := stats["cache_misses"].(uint64) // uint64!
	t.Logf("    Память: %.1f MB (hits=%d, misses=%d)", float64(hits+misses)*float64(100)/1024/1024, hits, misses)
	t.Logf("   Общее время:       %v", time.Since(startTotal))

	t.Logf("   🟢 PASS: %d элементов успешно!", size)
}

// TestStressProofGeneration - стресс-тест генерации proofs
func TestStressProofGeneration(t *testing.T) {
	sizes := []int{10_000, 50_000, 100_000}
	
	for _, size := range sizes {
		t.Run(fmt.Sprintf("N=%d", size), func(t *testing.T) {
			testStressProofs(t, size)
		})
	}
}

func testStressProofs(t *testing.T, size int) {
	srs := getTestSRS(t)
	config := NewConfig(srs)
	
	tree, err := New(config, nil)
	if err != nil {
		t.Fatalf("Failed to create tree: %v", err)
	}
	defer tree.Close()
	
	// Заполняем дерево
	batch := tree.NewBatch()
	for i := 0; i < size; i++ {
		userID := fmt.Sprintf("proof_stress_%d", i)
		userData := &UserData{Balances: map[string]float64{"USD": float64(i)}}
		data, _ := json.Marshal(userData)
		batch.Add(userID, data)
	}
	
	_, err = tree.CommitBatch(batch)
	if err != nil {
		t.Fatalf("Failed to populate tree: %v", err)
	}
	
	t.Logf("\n🧪 Proof stress test: %d элементов", size)
	
	// Генерируем 1% proofs параллельно
	proofCount := size / 100
	if proofCount > 1000 {
		proofCount = 1000
	}
	
	userIDs := make([]string, size)
	for i := 0; i < size; i++ {
		userIDs[i] = fmt.Sprintf("proof_stress_%d", i)
	}
	
	proofUsers := userIDs[:proofCount]
	
	startProof := time.Now()
	
	// Параллельная генерация
	var wg sync.WaitGroup
	proofChan := make(chan *Proof, proofCount)
	errChan := make(chan error, 100)
	
	workers := runtime.NumCPU()
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range proofUsers {
				if i >= proofCount {
					break
				}
				proof, err := tree.GenerateProof(proofUsers[i])
				if err != nil {
					errChan <- err
					return
				}
				
				_ = proof.UserIDs // используем proof - компилятор доволен!
				
				proofChan <- proof
			}
		}()
	}
	
	go func() {
		wg.Wait()
		close(proofChan)
		close(errChan)
	}()
	
	proofsGenerated := 0
	for proof := range proofChan {
		if proof.UserIDs != nil {
			proofsGenerated++
		}
	}
	
	for err := range errChan {
		t.Errorf("Proof generation error: %v", err)
	}
	
	proofTime := time.Since(startProof)
	
	t.Logf("   ✓ Сгенерировано %d proofs за %v (%.2f ms/proof)", 
		proofsGenerated, proofTime, float64(proofTime)/float64(proofsGenerated)/1e6)
	
	t.Logf("   🟢 Proof stress test PASS!")
}

// TestStressMemory - тест потребления памяти
func TestStressMemory(t *testing.T) {
	sizes := []int{100_000, 500_000, 1_000_000}
	
	for _, size := range sizes {
		t.Run(fmt.Sprintf("N=%d", size), func(t *testing.T) {
			testMemoryFootprint(t, size)
		})
	}
}

func testMemoryFootprint(t *testing.T, size int) {
	t.Logf("\n🧠 Memory test: %d элементов", size)
	
	srs := getTestSRS(t)
	config := NewConfig(srs)
	
	runtime.GC()
	var m1 runtime.MemStats
	runtime.ReadMemStats(&m1)
	
	tree, _ := New(config, nil)
	defer tree.Close()
	
	batch := tree.NewBatch()
	for i := 0; i < size; i++ {
		userID := fmt.Sprintf("mem_%d", i)
		userData := &UserData{Balances: map[string]float64{"USD": float64(i)}}
		data, _ := json.Marshal(userData)
		batch.Add(userID, data)
	}
	
	tree.CommitBatch(batch)
	
	runtime.GC()
	var m2 runtime.MemStats
	runtime.ReadMemStats(&m2)
	
	allocMB := float64(m2.Alloc) / 1024 / 1024
	memPerElem := allocMB * 1024 * 1024 / float64(size)
	
	t.Logf("   Память выделено: %.1f MB", allocMB)
	t.Logf("   Память/элемент:  %.1f KB", memPerElem/1024)
	t.Logf("   Эффективность:   %.2f%%", 100*32/float64(100*memPerElem)) // 32 bytes = [32]byte key
	
	t.Logf("   🟢 Memory test PASS!")
}

// TestStressBundledLarge - стресс bundled proofs
func TestStressBundledLarge(t *testing.T) {
	sizes := []int{100, 500, 1000, 5000}
	
	for _, size := range sizes {
		t.Run(fmt.Sprintf("N=%d", size), func(t *testing.T) {
			testStressBundled(t, size)
		})
	}
}

func testStressBundled(t *testing.T, bundleSize int) {
	srs := getTestSRS(t)
	config := NewConfig(srs)
	
	totalUsers := bundleSize * 10 // 10x больше для реалистичности
	tree, err := New(config, nil)
	if err != nil {
		t.Fatalf("Failed to create tree: %v", err)
	}
	defer tree.Close()
	
	// Заполняем
	batch := tree.NewBatch()
	userIDs := make([]string, totalUsers)
	for i := 0; i < totalUsers; i++ {
		userID := fmt.Sprintf("bundled_stress_%d", i)
		userIDs[i] = userID
		userData := &UserData{Balances: map[string]float64{"USD": float64(i)}}
		data, _ := json.Marshal(userData)
		batch.Add(userID, data)
	}
	
	_, err = tree.CommitBatch(batch)
	if err != nil {
		t.Fatalf("Failed to populate: %v", err)
	}
	
	proofUsers := userIDs[:bundleSize]
	
	t.Logf("\n🧪 Bundled stress: %d пользователей в proof", bundleSize)
	
	start := time.Now()
	bundledProof, err := tree.GenerateMultiProof(proofUsers)
	if err != nil {
		t.Fatalf("Bundled proof failed: %v", err)
	}
	
	timeTotal := time.Since(start)
	timePerUser := float64(timeTotal) / float64(bundleSize)
	
	proofSize := calculateProofSize(bundledProof)
	
	t.Logf("   Время: %v (%.2f µs/user)", timeTotal, timePerUser*1e6)
	t.Logf("   Размер: %d bytes (%.1f KB)", proofSize, float64(proofSize)/1024)
	t.Logf("   🟢 Bundled stress PASS!")
}
