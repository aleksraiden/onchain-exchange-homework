// optimized/tree_batch_test.go

package optimized

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"
)

// TestBatchInsert8 - тест batch вставки 8 элементов
func TestBatchInsert8(t *testing.T) {
	testBatchInsert(t, 8)
}

// TestBatchInsert16 - тест batch вставки 16 элементов
func TestBatchInsert16(t *testing.T) {
	testBatchInsert(t, 16)
}

// TestBatchInsert32 - тест batch вставки 32 элементов
func TestBatchInsert32(t *testing.T) {
	testBatchInsert(t, 32)
}

// TestBatchInsert64 - тест batch вставки 64 элементов
func TestBatchInsert64(t *testing.T) {
	testBatchInsert(t, 64)
}

// testBatchInsert - общая функция тестирования batch вставки
func testBatchInsert(t *testing.T, batchSize int) {
	t.Log("\n" + strings.Repeat("=", 100))
	t.Logf("🧪 BATCH TEST: Вставка %d элементов в batch", batchSize)
	t.Log(strings.Repeat("=", 100))
	
	// 1. Создаем дерево
	t.Log("\n📝 ШАГ 1: Создание дерева")
	srs := getTestSRS(t)
	config := NewConfig(srs)
	
	tree, err := New(config, nil)
	if err != nil {
		t.Fatalf("Failed to create tree: %v", err)
	}
	defer tree.Close()
	
	// 2. Создаем batch
	t.Logf("\n📝 ШАГ 2: Создание batch на %d элементов", batchSize)
	
	batch := tree.NewBatch()
	userIDs := make([]string, batchSize)
	
	for i := 0; i < batchSize; i++ {
		userID := fmt.Sprintf("batch_%d_user_%04d", batchSize, i)
		userIDs[i] = userID
		
		userData := &UserData{
			Balances: map[string]float64{
				"USD": float64(i * 1000),
				"BTC": float64(i) * 0.1,
				"ETH": float64(i) * 1.5,
			},
			Metadata: map[string]interface{}{
				"batch_size": batchSize,
				"index":      i,
				"verified":   true,
			},
			Timestamp: time.Now().Unix(),
		}
		
		data, err := json.Marshal(userData)
		if err != nil {
			t.Fatalf("Failed to marshal data: %v", err)
		}
		
		if err := batch.Add(userID, data); err != nil {
			t.Fatalf("Failed to add to batch: %v", err)
		}
	}
	
	t.Logf("   ✓ Batch создан: %d элементов", batchSize)
	
	// 3. Commit batch
	t.Log("\n📝 ШАГ 3: Commit batch")
	
	startCommit := time.Now()
	root, err := tree.CommitBatch(batch)
	commitDuration := time.Since(startCommit)
	
	if err != nil {
		t.Fatalf("Failed to commit batch: %v", err)
	}
	
	t.Logf("   ✓ Batch committed")
	t.Logf("   Root: %x", root[:16])
	t.Logf("   Время commit: %v", commitDuration)
	t.Logf("   На элемент: %v", commitDuration/time.Duration(batchSize))
	
	// Ждем async commits
	tree.WaitForCommit()
	
	// 4. Проверяем чтение всех элементов
	t.Log("\n📝 ШАГ 4: Верификация вставленных данных")
	
	startRead := time.Now()
	
	for i, userID := range userIDs {
		data, err := tree.Get(userID)
		if err != nil {
			t.Fatalf("Failed to get user %s: %v", userID, err)
		}
		
		var userData UserData
		if err := json.Unmarshal(data, &userData); err != nil {
			t.Fatalf("Failed to unmarshal data: %v", err)
		}
		
		// Проверяем корректность данных
		expectedUSD := float64(i * 1000)
		if userData.Balances["USD"] != expectedUSD {
			t.Fatalf("Data mismatch for %s: expected USD=%f, got %f", 
				userID, expectedUSD, userData.Balances["USD"])
		}
		
		if i%10 == 0 {
			t.Logf("   ✓ Verified %d/%d", i+1, batchSize)
		}
	}
	
	readDuration := time.Since(startRead)
	
	t.Logf("\n   ✓ Все элементы верифицированы")
	t.Logf("   Время чтения: %v", readDuration)
	t.Logf("   На элемент: %v", readDuration/time.Duration(batchSize))
	
	// 5. Статистика дерева
	t.Log("\n📝 ШАГ 5: Статистика дерева")
	stats := tree.Stats()
	
	t.Logf("   Узлов в дереве: %v", stats["node_count"])
	t.Logf("   Cache hits: %v", stats["cache_hits"])
	t.Logf("   Cache misses: %v", stats["cache_misses"])
	t.Logf("   Cache hit rate: %.2f%%", stats["cache_hit_rate"])
	
	// 6. Генерация proofs для всех элементов
	t.Logf("\n📝 ШАГ 6: Генерация %d single proofs", batchSize)
	
	startProofGen := time.Now()
	proofs := make([]*Proof, batchSize)
	
	for i, userID := range userIDs {
		proof, err := tree.GenerateProof(userID)
		if err != nil {
			t.Fatalf("Failed to generate proof for %s: %v", userID, err)
		}
		proofs[i] = proof
		
		if (i+1)%10 == 0 {
			t.Logf("   Generated %d/%d proofs", i+1, batchSize)
		}
	}
	
	proofGenDuration := time.Since(startProofGen)
	
	t.Logf("\n   ✓ Все proofs сгенерированы")
	t.Logf("   Время генерации: %v", proofGenDuration)
	t.Logf("   На proof: %v", proofGenDuration/time.Duration(batchSize))
	
	// 7. Верификация всех proofs
	t.Logf("\n📝 ШАГ 7: Верификация %d proofs", batchSize)
	
	startVerify := time.Now()
	
	for i, proof := range proofs {
		valid, err := VerifySingleProof(proof, config)
		if err != nil {
			t.Fatalf("Verification error for proof %d: %v", i, err)
		}
		
		if !valid {
			t.Fatalf("Proof %d is INVALID!", i)
		}
		
		if (i+1)%10 == 0 {
			t.Logf("   Verified %d/%d proofs", i+1, batchSize)
		}
	}
	
	verifyDuration := time.Since(startVerify)
	
	t.Logf("\n   ✓ Все proofs верифицированы")
	t.Logf("   Время верификации: %v", verifyDuration)
	t.Logf("   На proof: %v", verifyDuration/time.Duration(batchSize))
	
	// 8. Итоговая статистика
	t.Log("\n" + strings.Repeat("=", 100))
	t.Log("📊 ИТОГОВАЯ СТАТИСТИКА:")
	t.Log(strings.Repeat("=", 100))
	
	totalTime := time.Since(startCommit)
	
	t.Logf("Размер batch:                %d элементов", batchSize)
	t.Logf("✅ Commit batch:             %v  (%.2f µs/элемент)", 
		commitDuration, float64(commitDuration.Microseconds())/float64(batchSize))
	t.Logf("✅ Чтение %d элементов:      %v  (%.2f µs/элемент)", 
		batchSize, readDuration, float64(readDuration.Microseconds())/float64(batchSize))
	t.Logf("✅ Генерация %d proofs:      %v  (%.2f ms/proof)", 
		batchSize, proofGenDuration, float64(proofGenDuration.Milliseconds())/float64(batchSize))
	t.Logf("✅ Верификация %d proofs:    %v  (%.2f ms/proof)", 
		batchSize, verifyDuration, float64(verifyDuration.Milliseconds())/float64(batchSize))
	t.Logf("\n📈 Общее время теста:        %v", totalTime)
	
	// Cache статистика
	finalStats := tree.Stats()
	t.Logf("\n💾 Cache статистика:")
	t.Logf("   Hit rate: %.2f%%", finalStats["cache_hit_rate"])
	
	t.Log("\n" + strings.Repeat("=", 100))
	t.Logf("✅ BATCH TEST (%d элементов) УСПЕШНО ЗАВЕРШЕН!", batchSize)
	t.Log(strings.Repeat("=", 100))
}

// TestBatchComparison - сравнительный тест разных размеров batch
func TestBatchComparison(t *testing.T) {
	t.Log("\n" + strings.Repeat("=", 100))
	t.Log("🧪 СРАВНИТЕЛЬНЫЙ ТЕСТ: Разные размеры batch")
	t.Log(strings.Repeat("=", 100))
	
	batchSizes := []int{8, 16, 32, 64}
	results := make(map[int]map[string]time.Duration)
	
	srs := getTestSRS(t)
	config := NewConfig(srs)
	
	for _, batchSize := range batchSizes {
		t.Logf("\n📊 Тестирование batch size = %d", batchSize)
		
		tree, err := New(config, nil)
		if err != nil {
			t.Fatalf("Failed to create tree: %v", err)
		}
		
		// Создаем batch
		batch := tree.NewBatch()
		userIDs := make([]string, batchSize)
		
		for i := 0; i < batchSize; i++ {
			userID := fmt.Sprintf("comp_%d_user_%04d", batchSize, i)
			userIDs[i] = userID
			
			userData := &UserData{
				Balances: map[string]float64{"USD": float64(i * 100)},
			}
			data, _ := json.Marshal(userData)
			batch.Add(userID, data)
		}
		
		// Измеряем commit
		startCommit := time.Now()
		tree.CommitBatch(batch)
		commitTime := time.Since(startCommit)
		tree.WaitForCommit()
		
		// Измеряем генерацию proofs
		startProof := time.Now()
		for _, userID := range userIDs {
			tree.GenerateProof(userID)
		}
		proofTime := time.Since(startProof)
		
		results[batchSize] = map[string]time.Duration{
			"commit": commitTime,
			"proof":  proofTime,
		}
		
		tree.Close()
		
		t.Logf("   Commit: %v (%.2f µs/elem)", 
			commitTime, float64(commitTime.Microseconds())/float64(batchSize))
		t.Logf("   Proof gen: %v (%.2f ms/proof)", 
			proofTime, float64(proofTime.Milliseconds())/float64(batchSize))
	}
	
	// Выводим сравнительную таблицу
	t.Log("\n" + strings.Repeat("=", 100))
	t.Log("📊 СРАВНИТЕЛЬНАЯ ТАБЛИЦА:")
	t.Log(strings.Repeat("=", 100))
	t.Log("\n| Batch Size | Commit Time | µs/elem | Proof Gen Time | ms/proof |")
	t.Log("|------------|-------------|---------|----------------|----------|")
	
	for _, size := range batchSizes {
		commitTime := results[size]["commit"]
		proofTime := results[size]["proof"]
		
		t.Logf("| %10d | %11v | %7.2f | %14v | %8.2f |",
			size,
			commitTime,
			float64(commitTime.Microseconds())/float64(size),
			proofTime,
			float64(proofTime.Milliseconds())/float64(size),
		)
	}
	
	t.Log("\n" + strings.Repeat("=", 100))
	t.Log("✅ СРАВНИТЕЛЬНЫЙ ТЕСТ ЗАВЕРШЕН!")
	t.Log(strings.Repeat("=", 100))
}

// TestBatchVsSingle - сравнение batch vs последовательная вставка
func TestBatchVsSingle(t *testing.T) {
	t.Log("\n" + strings.Repeat("=", 100))
	t.Log("🧪 ТЕСТ: Batch вставка vs Последовательная вставка (100 элементов)")
	t.Log(strings.Repeat("=", 100))
	
	numElements := 100
	srs := getTestSRS(t)
	config := NewConfig(srs)
	
	// 1. Последовательная вставка
	t.Log("\n📝 Тест 1: Последовательная вставка")
	
	tree1, _ := New(config, nil)
	defer tree1.Close()
	
	startSingle := time.Now()
	
	for i := 0; i < numElements; i++ {
		userID := fmt.Sprintf("single_user_%04d", i)
		userData := &UserData{
			Balances: map[string]float64{"USD": float64(i * 100)},
		}
		data, _ := json.Marshal(userData)
		tree1.Insert(userID, data)
	}
	
	tree1.WaitForCommit()
	singleDuration := time.Since(startSingle)
	
	t.Logf("   Время: %v", singleDuration)
	t.Logf("   На элемент: %v", singleDuration/time.Duration(numElements))
	
	// 2. Batch вставка
	t.Log("\n📝 Тест 2: Batch вставка")
	
	tree2, _ := New(config, nil)
	defer tree2.Close()
	
	batch := tree2.NewBatch()
	
	for i := 0; i < numElements; i++ {
		userID := fmt.Sprintf("batch_user_%04d", i)
		userData := &UserData{
			Balances: map[string]float64{"USD": float64(i * 100)},
		}
		data, _ := json.Marshal(userData)
		batch.Add(userID, data)
	}
	
	startBatch := time.Now()
	tree2.CommitBatch(batch)
	tree2.WaitForCommit()
	batchDuration := time.Since(startBatch)
	
	t.Logf("   Время: %v", batchDuration)
	t.Logf("   На элемент: %v", batchDuration/time.Duration(numElements))
	
	// 3. Сравнение
	t.Log("\n" + strings.Repeat("=", 100))
	t.Log("📊 СРАВНЕНИЕ:")
	t.Log(strings.Repeat("=", 100))
	
	speedup := float64(singleDuration) / float64(batchDuration)
	
	t.Logf("Последовательная вставка:  %v", singleDuration)
	t.Logf("Batch вставка:             %v", batchDuration)
	t.Logf("\n🚀 Ускорение (batch):      %.2fx", speedup)
	
	if speedup > 1.0 {
		t.Logf("   ✅ Batch быстрее на %.1f%%", (speedup-1)*100)
	} else {
		t.Logf("   ⚠️  Последовательная вставка быстрее")
	}
	
	t.Log("\n" + strings.Repeat("=", 100))
	t.Log("✅ ТЕСТ ЗАВЕРШЕН!")
	t.Log(strings.Repeat("=", 100))
}
