// optimized/tree_basic_test.go

package optimized

import (
	"encoding/json"
	"fmt"
	"math/rand"
	"math/big"
//	"runtime"
	"testing"
	"time"
	"strings"
	"sync" 
	
	kzg_bls12381 "github.com/consensys/gnark-crypto/ecc/bls12-381/kzg"
)

// Глобальный SRS для переиспользования
var (
	testSRS     *kzg_bls12381.SRS
	testSRSOnce sync.Once
)

// getTestSRS возвращает переиспользуемый SRS
func getTestSRS(t *testing.T) *kzg_bls12381.SRS {
	testSRSOnce.Do(func() {
		var err error
		
		t.Log("📊 Инициализация KZG SRS (256)...")
		start := time.Now()
		
		testSRS, err = kzg_bls12381.NewSRS(256, big.NewInt(12345))
		if err != nil {
			t.Fatalf("Failed to initialize SRS: %v", err)
		}
		
		t.Logf("✅ SRS инициализирован за %v", time.Since(start))
		t.Logf("   Pk size: %d", len(testSRS.Pk.G1)) // ✅ Исправлено
	})
	return testSRS
}

// TestBasicTreeOperations - базовый тест дерева
func TestBasicTreeOperations(t *testing.T) {
	t.Log("\n" + strings.Repeat("=", 100)) // ✅ Исправили
	t.Log("🧪 БАЗОВЫЙ ТЕСТ: Создание дерева, заполнение 1000 элементов, генерация и верификация 10 proofs")
	t.Log(strings.Repeat("=", 100)) // ✅ Исправили
	
	// 1. Создаем дерево
	t.Log("\n📝 ШАГ 1: Создание оптимизированного Verkle дерева")
	srs := getTestSRS(t)
	config := NewConfig(srs)
	
	t.Logf("   Конфигурация:")
	t.Logf("   - Глубина: %d", TreeDepth)
	t.Logf("   - Ширина узла: %d", NodeWidth)
	t.Logf("   - Workers: %d", config.Workers)
	t.Logf("   - Cache size: %d", config.CacheSize)
	t.Logf("   - Lazy commit: %v", config.LazyCommit)
	t.Logf("   - Async mode: %v", config.AsyncMode)
	
	tree, err := New(config, nil) // nil = in-memory (без Pebble)
	if err != nil {
		t.Fatalf("Failed to create tree: %v", err)
	}
	defer tree.Close()
	
	// 2. Заполняем дерево 1000 элементов (БЕЗ batch)
	t.Log("\n📝 ШАГ 2: Последовательная вставка 1000 элементов (без batch)")
	
	numUsers := 1000
	userIDs := make([]string, numUsers)
	
	startInsert := time.Now()
	
	for i := 0; i < numUsers; i++ {
		userID := fmt.Sprintf("user_%06d", i)
		userIDs[i] = userID
		
		// Создаем данные пользователя
		userData := &UserData{
			Balances: map[string]float64{
				"USD": float64(i * 100),
				"BTC": float64(i) * 0.001,
				"ETH": float64(i) * 0.01,
			},
			Metadata: map[string]interface{}{
				"level":    i % 10,
				"verified": i%2 == 0,
			},
			Timestamp: time.Now().Unix(),
		}
		
		// Сериализуем в JSON
		data, err := json.Marshal(userData)
		if err != nil {
			t.Fatalf("Failed to marshal user data: %v", err)
		}
		
		// Вставляем в дерево
		if err := tree.Insert(userID, data); err != nil {
			t.Fatalf("Failed to insert user %s: %v", userID, err)
		}
		
		// Прогресс каждые 100 элементов
		if (i+1)%100 == 0 {
			t.Logf("   Вставлено: %d/%d элементов", i+1, numUsers)
		}
	}
	
	insertDuration := time.Since(startInsert)
	insertPerOp := insertDuration / time.Duration(numUsers)
	
	t.Logf("\n✅ Вставка завершена:")
	t.Logf("   Всего времени: %v", insertDuration)
	t.Logf("   На одну операцию: %v", insertPerOp)
	t.Logf("   Throughput: %.0f ops/sec", float64(numUsers)/insertDuration.Seconds())
	
	// Ждем завершения async commits
	tree.WaitForCommit()
	
	// 3. Статистика дерева
	t.Log("\n📝 ШАГ 3: Статистика дерева")
	stats := tree.Stats()
	t.Logf("   Узлов в индексе: %v", stats["node_count"])
	t.Logf("   Cache hits: %v", stats["cache_hits"])
	t.Logf("   Cache misses: %v", stats["cache_misses"])
	t.Logf("   Cache hit rate: %.2f%%", stats["cache_hit_rate"])
	
	// 4. Проверяем чтение
	t.Log("\n📝 ШАГ 4: Тест чтения (10 случайных элементов)")
	
	rand.Seed(time.Now().UnixNano())
	readTestUsers := make([]string, 10)
	for i := 0; i < 10; i++ {
		readTestUsers[i] = userIDs[rand.Intn(numUsers)]
	}
	
	startRead := time.Now()
	
	for _, userID := range readTestUsers {
		data, err := tree.Get(userID)
		if err != nil {
			t.Fatalf("Failed to get user %s: %v", userID, err)
		}
		
		// Проверяем что данные корректны
		var userData UserData
		if err := json.Unmarshal(data, &userData); err != nil {
			t.Fatalf("Failed to unmarshal data for %s: %v", userID, err)
		}
		
		t.Logf("   ✓ %s: Balances=%v", userID, userData.Balances)
	}
	
	readDuration := time.Since(startRead)
	t.Logf("\n✅ Чтение завершено:")
	t.Logf("   Всего времени: %v", readDuration)
	t.Logf("   На одну операцию: %v", readDuration/10)
	
	// 5. Генерация proofs для 10 случайных пользователей
	t.Log("\n📝 ШАГ 5: Генерация Single Proofs (10 пользователей)")
	
	proofUsers := make([]string, 10)
	for i := 0; i < 10; i++ {
		proofUsers[i] = userIDs[rand.Intn(numUsers)]
	}
	
	proofs := make([]*Proof, 10)
	startProofGen := time.Now()
	
	for i, userID := range proofUsers {
		proof, err := tree.GenerateProof(userID)
		if err != nil {
			t.Fatalf("Failed to generate proof for %s: %v", userID, err)
		}
		proofs[i] = proof
		
		t.Logf("   ✓ Proof #%d: %s", i+1, userID)
		t.Logf("      - Path length: %d", len(proof.Path))
		t.Logf("      - Children hashes: %d levels", len(proof.ChildrenHashes))
		t.Logf("      - KZG proof: %v", len(proof.KZGOpeningProof) > 0) // ✅ Исправлено
	}
	
	proofGenDuration := time.Since(startProofGen)
	t.Logf("\n✅ Генерация proofs завершена:")
	t.Logf("   Всего времени: %v", proofGenDuration)
	t.Logf("   На один proof: %v", proofGenDuration/10)
	
	// 6. Верификация proofs
	t.Log("\n📝 ШАГ 6: Верификация Single Proofs")
	
	startVerify := time.Now()
	
	for i, proof := range proofs {
		valid, err := VerifySingleProof(proof, config)
		if err != nil {
			t.Fatalf("Proof verification error for proof #%d: %v", i+1, err)
		}
		
		if !valid {
			t.Fatalf("Proof #%d is INVALID!", i+1)
		}
		
		t.Logf("   ✓ Proof #%d: VALID", i+1)
	}
	
	verifyDuration := time.Since(startVerify)
	t.Logf("\n✅ Верификация завершена:")
	t.Logf("   Всего времени: %v", verifyDuration)
	t.Logf("   На один proof: %v", verifyDuration/10)
	
	// 7. Итоговая статистика
	t.Log("\n" + strings.Repeat("=", 100))
	t.Log("📊 ИТОГОВАЯ СТАТИСТИКА:")
	t.Log(strings.Repeat("=", 100))
	
	totalTime := time.Since(startInsert)
	
	t.Logf("✅ Вставка %d элементов:      %v  (%.2f ms/op)", numUsers, insertDuration, float64(insertPerOp.Microseconds())/1000)
	t.Logf("✅ Чтение 10 элементов:       %v  (%.2f ms/op)", readDuration, float64(readDuration.Microseconds())/10000)
	t.Logf("✅ Генерация 10 proofs:       %v  (%.2f ms/op)", proofGenDuration, float64(proofGenDuration.Microseconds())/10000)
	t.Logf("✅ Верификация 10 proofs:     %v  (%.2f ms/op)", verifyDuration, float64(verifyDuration.Microseconds())/10000)
	t.Logf("\n📈 Общее время теста:         %v", totalTime)
	
	// Финальная статистика кэша
	finalStats := tree.Stats()
	t.Logf("\n💾 Cache статистика:")
	t.Logf("   Hit rate: %.2f%%", finalStats["cache_hit_rate"])
	t.Logf("   Hits: %v", finalStats["cache_hits"])
	t.Logf("   Misses: %v", finalStats["cache_misses"])
	
	t.Log("\n" + strings.Repeat("=", 100))
	t.Log("✅ ТЕСТ УСПЕШНО ЗАВЕРШЕН!")
	t.Log(strings.Repeat("=", 100))
}

// TestBasicProofVerification - отдельный тест верификации
func TestBasicProofVerification(t *testing.T) {
	t.Log("\n🧪 ТЕСТ: Базовая верификация proof")
	
	srs := getTestSRS(t)
	config := NewConfig(srs)
	tree, err := New(config, nil)
	if err != nil {
		t.Fatalf("Failed to create tree: %v", err)
	}
	defer tree.Close()
	
	// Вставляем один элемент
	userID := "test_user_001"
	userData := &UserData{
		Balances: map[string]float64{"USD": 1000},
	}
	data, _ := json.Marshal(userData)
	
	if err := tree.Insert(userID, data); err != nil {
		t.Fatalf("Insert failed: %v", err)
	}
	
	tree.WaitForCommit()
	
	// Генерируем proof
	proof, err := tree.GenerateProof(userID)
	if err != nil {
		t.Fatalf("GenerateProof failed: %v", err)
	}
	
	t.Logf("✓ Proof generated:")
	t.Logf("  - User IDs: %v", proof.UserIDs)
	t.Logf("  - Path length: %d", len(proof.Path))
	t.Logf("  - Is bundled: %v", proof.IsBundled)
	
	// Верифицируем
	valid, err := VerifySingleProof(proof, config)
	if err != nil {
		t.Fatalf("VerifySingleProof failed: %v", err)
	}
	
	if !valid {
		t.Fatal("Proof is INVALID!")
	}
	
	t.Log("✅ Proof is VALID!")
	
	// Негативный тест: модифицируем proof
	t.Log("\n🔍 Негативный тест: модификация proof")
	
	// Меняем root hash
	corruptedProof := *proof
	corruptedProof.RootHash = make([]byte, 32)
	for i := range corruptedProof.RootHash {
		corruptedProof.RootHash[i] = 0xFF
	}
	
	valid, err = VerifySingleProof(&corruptedProof, config)
	if valid {
		t.Fatal("Corrupted proof should be INVALID!")
	}
	
	t.Log("✅ Corrupted proof correctly rejected!")
}

// TestCachePerformance - тест производительности кэша
func TestCachePerformance(t *testing.T) {
	t.Log("\n🧪 ТЕСТ: Производительность LRU Cache")
	
	srs := getTestSRS(t)
	config := NewConfig(srs)
	tree, err := New(config, nil)
	if err != nil {
		t.Fatalf("Failed to create tree: %v", err)
	}
	defer tree.Close()
	
	// Вставляем 100 элементов
	t.Log("📝 Вставка 100 элементов...")
	for i := 0; i < 100; i++ {
		userID := fmt.Sprintf("cache_user_%03d", i)
		userData := &UserData{
			Balances: map[string]float64{"USD": float64(i * 100)},
		}
		data, _ := json.Marshal(userData)
		tree.Insert(userID, data)
	}
	
	tree.WaitForCommit()
	
	// Читаем 10 "горячих" пользователей много раз
	t.Log("\n📝 Тест cache hit rate (10 горячих пользователей, 100 чтений каждый)")
	
	hotUsers := []string{
		"cache_user_001", "cache_user_002", "cache_user_003", "cache_user_004", "cache_user_005",
		"cache_user_006", "cache_user_007", "cache_user_008", "cache_user_009", "cache_user_010",
	}
	
	start := time.Now()
	
	for i := 0; i < 100; i++ {
		for _, userID := range hotUsers {
			_, err := tree.Get(userID)
			if err != nil {
				t.Fatalf("Get failed: %v", err)
			}
		}
	}
	
	duration := time.Since(start)
	totalReads := 100 * len(hotUsers)
	
	stats := tree.Stats()
	
	t.Logf("\n✅ Результаты:")
	t.Logf("   Всего чтений: %d", totalReads)
	t.Logf("   Время: %v", duration)
	t.Logf("   На одно чтение: %v", duration/time.Duration(totalReads))
	t.Logf("   Cache hit rate: %.2f%%", stats["cache_hit_rate"])
	t.Logf("   Cache hits: %v", stats["cache_hits"])
	t.Logf("   Cache misses: %v", stats["cache_misses"])
	
	hitRate := stats["cache_hit_rate"].(float64)
	if hitRate < 80.0 {
		t.Errorf("Cache hit rate too low: %.2f%% (expected > 80%%)", hitRate)
	}
}

// Helper function для strings.Repeat
func repeat(s string, count int) string {
	result := ""
	for i := 0; i < count; i++ {
		result += s
	}
	return result
}
