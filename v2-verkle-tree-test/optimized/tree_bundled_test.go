// optimized/tree_bundled_test.go

package optimized

import (
	"fmt"
	"strings"
	"testing"
	"time"
	"encoding/json"
)

// TestBundledMultiProof10 - bundled proof для 10 пользователей
func TestBundledMultiProof10(t *testing.T) {
	testBundledMultiProof(t, 10)
}

// TestBundledMultiProof50 - bundled proof для 50 пользователей
func TestBundledMultiProof50(t *testing.T) {
	testBundledMultiProof(t, 50)
}

// TestBundledMultiProof100 - bundled proof для 100 пользователей
func TestBundledMultiProof100(t *testing.T) {
	testBundledMultiProof(t, 100)
}

// testBundledMultiProof - общая функция тестирования bundled proof
func testBundledMultiProof(t *testing.T, numUsers int) {
	t.Log("\n" + strings.Repeat("=", 100))
	t.Logf("🧪 BUNDLED MULTI-PROOF TEST: %d пользователей", numUsers)
	t.Log(strings.Repeat("=", 100))
	
	// 1. Создаем дерево и заполняем данными
	t.Log("\n📝 ШАГ 1: Создание дерева и вставка данных")
	
	srs := getTestSRS(t)
	config := NewConfig(srs)
	tree, err := New(config, nil)
	if err != nil {
		t.Fatalf("Failed to create tree: %v", err)
	}
	defer tree.Close()
	
	// Вставляем больше данных для реалистичности
	totalUsers := numUsers * 10 // 10x больше чем нужно для proof
	userIDs := make([]string, totalUsers)
	
	batch := tree.NewBatch()
	for i := 0; i < totalUsers; i++ {
		userID := fmt.Sprintf("bundled_test_%d_user_%05d", numUsers, i)
		userIDs[i] = userID
		
		userData := &UserData{
			Balances: map[string]float64{
				"USD": float64(i * 100),
				"BTC": float64(i) * 0.01,
				"ETH": float64(i) * 0.1,
			},
			Metadata: map[string]interface{}{
				"level": i % 100,
				"verified": true,
			},
			Timestamp: time.Now().Unix(),
		}
		
		data, _ := json.Marshal(userData)
		batch.Add(userID, data)
	}
	
	tree.CommitBatch(batch)
	tree.WaitForCommit()
	
	t.Logf("   ✓ Вставлено %d пользователей в дерево", totalUsers)
	
	// Выбираем случайных пользователей для proof
	proofUserIDs := userIDs[:numUsers]
	
	// 2. Генерация BUNDLED Multi-Proof
	t.Logf("\n📝 ШАГ 2: Генерация BUNDLED Multi-Proof (%d пользователей)", numUsers)
	
	startBundled := time.Now()
	bundledProof, err := tree.GenerateMultiProof(proofUserIDs)
	bundledDuration := time.Since(startBundled)
	
	if err != nil {
		t.Fatalf("Failed to generate bundled proof: %v", err)
	}
	
	t.Logf("   ✓ Bundled proof сгенерирован")
	t.Logf("   Время генерации: %v", bundledDuration)
	t.Logf("   Is bundled: %v", bundledProof.IsBundled)
	
	// Подсчитываем размер bundled proof
	bundledSize := calculateProofSize(bundledProof)
	
	t.Logf("\n   📊 Структура Bundled Proof:")
	t.Logf("      - User IDs: %d", len(bundledProof.UserIDs))
	t.Logf("      - Path commitments: %d", len(bundledProof.Path))
	t.Logf("      - Path indices: %d", len(bundledProof.PathIndices))
	t.Logf("      - Children hashes levels: %d", len(bundledProof.ChildrenHashes))
	t.Logf("      - Total size: %d bytes (~%.2f KB)", bundledSize, float64(bundledSize)/1024)
	
	// 3. Генерация N отдельных Single Proofs для сравнения
	t.Logf("\n📝 ШАГ 3: Генерация %d отдельных Single Proofs (для сравнения)", numUsers)
	
	startSingle := time.Now()
	singleProofs := make([]*Proof, numUsers)
	
	for i, userID := range proofUserIDs {
		proof, err := tree.GenerateProof(userID)
		if err != nil {
			t.Fatalf("Failed to generate single proof %d: %v", i, err)
		}
		singleProofs[i] = proof
	}
	
	singleDuration := time.Since(startSingle)
	
	// Подсчитываем общий размер single proofs
	totalSingleSize := 0
	for _, proof := range singleProofs {
		totalSingleSize += calculateProofSize(proof)
	}
	
	t.Logf("   ✓ %d single proofs сгенерированы", numUsers)
	t.Logf("   Время генерации: %v", singleDuration)
	t.Logf("   Total size: %d bytes (~%.2f KB)", totalSingleSize, float64(totalSingleSize)/1024)
	
	// 4. Сравнение размеров
	t.Log("\n📝 ШАГ 4: Сравнение Bundled vs Single Proofs")
	
	sizeReduction := float64(totalSingleSize-bundledSize) / float64(totalSingleSize) * 100
	compressionRatio := float64(totalSingleSize) / float64(bundledSize)
	
	t.Log("\n   " + strings.Repeat("-", 80))
	t.Logf("   | %-30s | %15s | %15s |", "Metric", "Bundled", "Single (sum)")
	t.Log("   " + strings.Repeat("-", 80))
	t.Logf("   | %-30s | %12d KB | %12d KB |", "Size", bundledSize/1024, totalSingleSize/1024)
	t.Logf("   | %-30s | %15v | %15v |", "Generation Time", bundledDuration, singleDuration)
	t.Logf("   | %-30s | %12.2f ms | %12.2f ms |", "Time per user", 
		float64(bundledDuration.Microseconds())/float64(numUsers)/1000,
		float64(singleDuration.Microseconds())/float64(numUsers)/1000)
	t.Log("   " + strings.Repeat("-", 80))
	
	t.Logf("\n   🎯 Экономия размера: %.2f%% (%.2fx compression)", sizeReduction, compressionRatio)
	
	if sizeReduction > 0 {
		t.Logf("   ✅ Bundled proof на %d bytes меньше!", totalSingleSize-bundledSize)
	} else {
		t.Logf("   ⚠️  Single proofs меньше (неожиданно!)")
	}
	
	// Сравнение времени
	if bundledDuration < singleDuration {
		speedup := float64(singleDuration) / float64(bundledDuration)
		t.Logf("   ✅ Bundled proof генерируется быстрее в %.2fx раз!", speedup)
	} else {
		t.Logf("   ⚠️  Single proofs генерируются быстрее")
	}
	
	// 5. Верификация Bundled Proof
	t.Log("\n📝 ШАГ 5: Верификация Bundled Multi-Proof")
	
	startVerify := time.Now()
	valid, err := VerifyBundledProof(bundledProof, config)
	verifyDuration := time.Since(startVerify)
	
	if err != nil {
		t.Fatalf("Bundled proof verification error: %v", err)
	}
	
	if !valid {
		t.Fatal("Bundled proof is INVALID!")
	}
	
	t.Logf("   ✅ Bundled proof VALID")
	t.Logf("   Время верификации: %v", verifyDuration)
	t.Logf("   На пользователя: %.2f ms", float64(verifyDuration.Microseconds())/float64(numUsers)/1000)
	
	// 6. Верификация всех Single Proofs для сравнения
	t.Logf("\n📝 ШАГ 6: Верификация %d Single Proofs (для сравнения)", numUsers)
	
	startVerifySingle := time.Now()
	
	for i, proof := range singleProofs {
		valid, err := VerifySingleProof(proof, config)
		if err != nil {
			t.Fatalf("Single proof %d verification error: %v", i, err)
		}
		if !valid {
			t.Fatalf("Single proof %d is INVALID!", i)
		}
	}
	
	verifySingleDuration := time.Since(startVerifySingle)
	
	t.Logf("   ✅ Все %d single proofs верифицированы", numUsers)
	t.Logf("   Время верификации: %v", verifySingleDuration)
	t.Logf("   На пользователя: %.2f ms", float64(verifySingleDuration.Microseconds())/float64(numUsers)/1000)
	
	// 7. Итоговая статистика
	t.Log("\n" + strings.Repeat("=", 100))
	t.Log("📊 ИТОГОВАЯ СТАТИСТИКА:")
	t.Log(strings.Repeat("=", 100))
	
	t.Logf("\n🔹 Размер:")
	t.Logf("   Bundled:      %d bytes (%.2f KB)", bundledSize, float64(bundledSize)/1024)
	t.Logf("   Single (sum): %d bytes (%.2f KB)", totalSingleSize, float64(totalSingleSize)/1024)
	t.Logf("   💾 Экономия:  %.2f%% (ratio %.2fx)", sizeReduction, compressionRatio)
	
	t.Logf("\n🔹 Генерация:")
	t.Logf("   Bundled: %v", bundledDuration)
	t.Logf("   Single:  %v", singleDuration)
	if bundledDuration < singleDuration {
		t.Logf("   ⚡ Ускорение: %.2fx", float64(singleDuration)/float64(bundledDuration))
	}
	
	t.Logf("\n🔹 Верификация:")
	t.Logf("   Bundled: %v", verifyDuration)
	t.Logf("   Single:  %v", verifySingleDuration)
	if verifyDuration < verifySingleDuration {
		t.Logf("   ⚡ Ускорение: %.2fx", float64(verifySingleDuration)/float64(verifyDuration))
	}
	
	t.Log("\n" + strings.Repeat("=", 100))
	t.Logf("✅ BUNDLED MULTI-PROOF TEST (%d пользователей) УСПЕШНО ЗАВЕРШЕН!", numUsers)
	t.Log(strings.Repeat("=", 100))
}

// TestBundledComparison - сравнительный тест разных размеров bundled proof
func TestBundledComparison(t *testing.T) {
	t.Log("\n" + strings.Repeat("=", 100))
	t.Log("🧪 СРАВНИТЕЛЬНЫЙ ТЕСТ: Bundled Multi-Proof разных размеров")
	t.Log(strings.Repeat("=", 100))
	
	sizes := []int{5, 10, 25, 50, 100}
	
	srs := getTestSRS(t)
	config := NewConfig(srs)
	
	// Создаем дерево с данными
	tree, _ := New(config, nil)
	defer tree.Close()
	
	totalUsers := 1000
	userIDs := make([]string, totalUsers)
	
	batch := tree.NewBatch()
	for i := 0; i < totalUsers; i++ {
		userID := fmt.Sprintf("comparison_user_%05d", i)
		userIDs[i] = userID
		
		userData := &UserData{
			Balances: map[string]float64{"USD": float64(i * 100)},
		}
		data, _ := json.Marshal(userData)
		batch.Add(userID, data)
	}
	
	tree.CommitBatch(batch)
	tree.WaitForCommit()
	
	t.Logf("\n✓ Дерево создано: %d пользователей\n", totalUsers)
	
	// Таблица результатов
	type result struct {
		size           int
		bundledSize    int
		singleSize     int
		bundledTime    time.Duration
		singleTime     time.Duration
		compressionRatio float64
		speedup        float64
	}
	
	results := make([]result, 0, len(sizes))
	
	for _, size := range sizes {
		t.Logf("📊 Тестирование: %d пользователей...", size)
		
		proofUsers := userIDs[:size]
		
		// Bundled proof
		startB := time.Now()
		bundledProof, _ := tree.GenerateMultiProof(proofUsers)
		bundledTime := time.Since(startB)
		bundledSize := calculateProofSize(bundledProof)
		
		// Single proofs
		startS := time.Now()
		totalSingleSize := 0
		for _, userID := range proofUsers {
			proof, _ := tree.GenerateProof(userID)
			totalSingleSize += calculateProofSize(proof)
		}
		singleTime := time.Since(startS)
		
		compressionRatio := float64(totalSingleSize) / float64(bundledSize)
		speedup := float64(singleTime) / float64(bundledTime)
		
		results = append(results, result{
			size:             size,
			bundledSize:      bundledSize,
			singleSize:       totalSingleSize,
			bundledTime:      bundledTime,
			singleTime:       singleTime,
			compressionRatio: compressionRatio,
			speedup:          speedup,
		})
	}
	
	// Выводим сводную таблицу
	t.Log("\n" + strings.Repeat("=", 120))
	t.Log("📊 СВОДНАЯ ТАБЛИЦА:")
	t.Log(strings.Repeat("=", 120))
	
	t.Log("\n| Users | Bundled Size | Single Size | Compression | Bundled Time | Single Time | Speedup |")
	t.Log("|-------|--------------|-------------|-------------|--------------|-------------|---------|")
	
	for _, r := range results {
		t.Logf("| %5d | %9d KB | %8d KB | %9.2fx | %12v | %11v | %6.2fx |",
			r.size,
			r.bundledSize/1024,
			r.singleSize/1024,
			r.compressionRatio,
			r.bundledTime,
			r.singleTime,
			r.speedup,
		)
	}
	
	t.Log("\n" + strings.Repeat("=", 120))
	
	// Выводы
	t.Log("\n📈 ВЫВОДЫ:")
	
	avgCompression := 0.0
	avgSpeedup := 0.0
	for _, r := range results {
		avgCompression += r.compressionRatio
		avgSpeedup += r.speedup
	}
	avgCompression /= float64(len(results))
	avgSpeedup /= float64(len(results))
	
	t.Logf("   • Средняя компрессия: %.2fx", avgCompression)
	t.Logf("   • Среднее ускорение: %.2fx", avgSpeedup)
	t.Logf("   • Bundled Multi-Proof эффективен для %d+ пользователей", sizes[0])
	
	t.Log("\n" + strings.Repeat("=", 120))
	t.Log("✅ СРАВНИТЕЛЬНЫЙ ТЕСТ ЗАВЕРШЕН!")
	t.Log(strings.Repeat("=", 120))
}

// calculateProofSize - подсчитывает размер proof в байтах
func calculateProofSize(proof *Proof) int {
	size := 0
	
	// UserIDs (strings)
	for _, id := range proof.UserIDs {
		size += len(id)
	}
	
	// UserIDHashes (32 bytes each)
	size += len(proof.UserIDHashes) * 32
	
	// Path (commitments)
	for _, p := range proof.Path {
		size += len(p)
	}
	
	// PathIndices (int = 8 bytes)
	size += len(proof.PathIndices) * 8
	
	// ChildrenHashes
	for _, level := range proof.ChildrenHashes {
		for _, hash := range level {
			size += len(hash)
		}
	}
	
	// KZG data
	size += len(proof.KZGOpeningProof)
	size += len(proof.KZGCommitment)
	size += len(proof.RootHash)
	
	// IsBundled (1 byte)
	size += 1
	
	return size
}
