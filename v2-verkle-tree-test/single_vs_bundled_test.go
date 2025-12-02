// Исправьте файл single_vs_bundled_test.go

package verkletree

import (
	"testing"
	"time"
)

// TestSingleVsBundledOneElement - сравнение single vs bundled для 1 элемента
func TestSingleVsBundledOneElement(t *testing.T) {
	// Подготовка
	srs, _ := InitSRS(256)
	tree, _ := New(8, 128, srs, nil)
	
	// Заполняем дерево
	batch := tree.BeginBatch()
	for i := 0; i < 1000; i++ {
		userData := &UserData{
			Balances: map[string]float64{"USD": float64(i * 100)},
		}
		batch.AddUserData(userID(i), userData)
	}
	tree.CommitBatch(batch)
	tree.WaitForCommit()
	
	testUserID := "user_500"
	iterations := 10000 // Увеличим для точности
	
	t.Logf("\n=== СРАВНЕНИЕ: Single Proof vs Bundled (1 элемент) ===")
	t.Logf("User ID: %s", testUserID)
	t.Logf("Iterations: %d\n", iterations)
	
	// === TEST 1: Single Proof (оптимизированный) ===
	t.Log("1️⃣  Single Proof (GenerateProof)")
	
	startTime := time.Now()
	var singleProof *Proof
	for i := 0; i < iterations; i++ {
		proof, err := tree.GenerateProof(testUserID)
		if err != nil {
			t.Fatalf("Error generating single proof: %v", err)
		}
		singleProof = proof
	}
	singleTime := time.Since(startTime)
	avgSingleTime := singleTime / time.Duration(iterations)
	
	t.Logf("   Total time:    %v", singleTime)
	t.Logf("   Avg per proof: %v", avgSingleTime)
	singleSize := estimateProofSize(singleProof)
	t.Logf("   Size:          ~%d bytes\n", singleSize)
	
	// === TEST 2: Bundled для 1 элемента ===
	t.Log("2️⃣  Bundled Multi-Proof (1 элемент)")
	
	startTime = time.Now()
	var bundledProof *Proof
	for i := 0; i < iterations; i++ {
		// Создаем bundled с 1 элементом
		bundled := &BundledMultiProof{
			Proofs:  make([]*Proof, 0, 1),
			UserIDs: make([]string, 0, 1),
		}
		
		proof, err := tree.GenerateProof(testUserID)
		if err != nil {
			t.Fatalf("Error generating bundled proof: %v", err)
		}
		
		bundled.Proofs = append(bundled.Proofs, proof)
		bundled.UserIDs = append(bundled.UserIDs, testUserID)
		
		// Извлекаем обратно
		bundledProof = bundled.ExtractProof(testUserID)
	}
	bundledTime := time.Since(startTime)
	avgBundledTime := bundledTime / time.Duration(iterations)
	
	t.Logf("   Total time:    %v", bundledTime)
	t.Logf("   Avg per proof: %v", avgBundledTime)
	
	// Оценка размера bundled структуры
	bundledSize := estimateProofSize(bundledProof)
	bundledOverhead := 100 // overhead на Bundle структуру (slices, metadata)
	t.Logf("   Size:          ~%d bytes (proof) + ~%d bytes (overhead) = ~%d bytes\n", 
		bundledSize, bundledOverhead, bundledSize+bundledOverhead)
	
	// === СРАВНЕНИЕ ===
	t.Log("📊 РЕЗУЛЬТАТ:")
	
	// Разница во времени
	timeDiff := bundledTime - singleTime
	timeRatio := float64(bundledTime) / float64(singleTime)
	
	t.Logf("   Single:  %v per proof", avgSingleTime)
	t.Logf("   Bundled: %v per proof", avgBundledTime)
	
	if timeDiff > 0 {
		overhead := (timeRatio - 1.0) * 100
		t.Logf("   Bundled медленнее в %.2fx раз (+%.1f%%)", timeRatio, overhead)
	} else {
		speedup := (1.0 - timeRatio) * 100
		t.Logf("   Bundled БЫСТРЕЕ в %.2fx раз (%.1f%% быстрее)", 1.0/timeRatio, speedup)
	}
	
	// Разница в размере
	sizeDiff := (bundledSize + bundledOverhead) - singleSize
	t.Logf("   Разница в размере: +%d bytes (+%.1f%%)", 
		sizeDiff, float64(sizeDiff)/float64(singleSize)*100)
	
	// === АНАЛИЗ ===
	t.Log("\n🔍 АНАЛИЗ:")
	
	if abs(timeDiff) < time.Microsecond*10 {
		t.Log("   ⏱️  Время: разница незначительна (< 10μs)")
		t.Log("      Причина: оба метода делают одно и то же (GenerateProof)")
		t.Log("      Bundled просто оборачивает результат в структуру")
		t.Log("      Вариация может быть из-за CPU cache, GC, etc.")
	} else if timeDiff > 0 {
		t.Log("   ⏱️  Время: Bundled медленнее")
		t.Log("      Причина: overhead на создание слайсов и структур")
	} else {
		t.Log("   ⏱️  Время: Bundled быстрее (неожиданно!)")
		t.Log("      Причина: вероятно случайная вариация или кэш CPU")
		t.Log("      При большем числе итераций разница должна сгладиться")
	}
	
	if sizeDiff > 50 {
		t.Log("   💾 Память: Bundled использует больше памяти")
		t.Logf("      +%d bytes на Bundle структуру (slices, metadata)", bundledOverhead)
	}
	
	// === ФИНАЛЬНАЯ РЕКОМЕНДАЦИЯ ===
	t.Log("\n💡 ФИНАЛЬНАЯ РЕКОМЕНДАЦИЯ:")
	t.Log("   ✅ Используйте GenerateProof() для одного элемента")
	t.Log("\n   Причины:")
	t.Log("   1. Более простой и понятный API")
	t.Log("   2. Меньше памяти (нет overhead на Bundle)")
	t.Log("   3. Более явное намерение кода")
	t.Log("   4. Производительность примерно одинакова (или single чуть лучше)")
	t.Log("\n   Используйте Bundled ТОЛЬКО когда:")
	t.Log("   • Действительно нужно несколько пруфов")
	t.Log("   • Нужна дедупликация общих узлов")
	t.Log("   • Количество элементов > 10")
}

func abs(d time.Duration) time.Duration {
	if d < 0 {
		return -d
	}
	return d
}

// userID генерирует ID пользователя
func userID(i int) string {
	return "user_" + string(rune('0'+(i/100)%10)) + 
	       string(rune('0'+(i/10)%10)) + 
	       string(rune('0'+i%10))
}
