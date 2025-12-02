package verkletree

import (
	//"fmt"
	"strings"
	"testing"
)

// TestProofArchitectureComparison - сравнение архитектур для одиночных пруфов
func TestProofArchitectureComparison(t *testing.T) {
	configs := []struct {
		depth int
		width int
		name  string
	}{
		{6, 64, "Medium (6 levels, 64 width)"},
		{6, 128, "Large (6 levels, 128 width)"},
		{8, 64, "Deep (8 levels, 64 width)"},
		{16, 64, "Very Deep (16 levels, 64 width)"},
	}
	
	for _, cfg := range configs {
		t.Log("\n" + strings.Repeat("=", 100))
		t.Logf("КОНФИГУРАЦИЯ: %s", cfg.name)
		t.Log(strings.Repeat("=", 100))
		
		metrics := AnalyzeProofArchitecture(cfg.depth, cfg.width)
		
		t.Logf("%-20s | %-12s | %-12s | %-15s | %-15s | %s",
			"Architecture", "Proof Size", "Root Size", "Generate (μs)", "Verify (μs)", "Notes")
		t.Log(strings.Repeat("-", 100))
		
		for _, arch := range []string{"merkle", "hybrid", "full_kzg"} {
			m := metrics[arch]
			
			notes := ""
			if arch == "merkle" {
				notes = "❌ Большие пруфы"
			} else if arch == "hybrid" {
				notes = "⚡ Баланс (РЕКОМЕНДУЕТСЯ)"
			} else {
				notes = "✓ Минимальные пруфы, медленно"
			}
			
			t.Logf("%-20s | %8d B  | %8d B  | %15d | %15d | %s",
				m.Architecture, m.ProofSizeBytes, m.RootSizeBytes,
				m.GenerateTimeUS, m.VerifyTimeUS, notes)
		}
		
		// Выводим выигрыш hybrid vs merkle
		hybridSize := metrics["hybrid"].ProofSizeBytes
		merkleSize := metrics["merkle"].ProofSizeBytes
		reduction := (1.0 - float64(hybridSize)/float64(merkleSize)) * 100
		
		t.Logf("\n💡 Гибрид меньше Merkle на %.1f%%", reduction)
		t.Logf("💡 Полный KZG меньше гибрида на %.1f%%", 
			(1.0-float64(metrics["full_kzg"].ProofSizeBytes)/float64(hybridSize))*100)
	}
}

// TestMultiProofComparison - сравнение мульти-пруфов
func TestMultiProofComparison(t *testing.T) {
	depth := 6
	width := 128
	
	proofCounts := []int{1, 10, 100, 1000}
	
	t.Log("\n" + strings.Repeat("=", 120))
	t.Log("СРАВНЕНИЕ МУЛЬТИ-ПРУФОВ (depth=6, width=128)")
	t.Log(strings.Repeat("=", 120))
	
	for _, numProofs := range proofCounts {
		t.Logf("\n>>> Количество пруфов: %d", numProofs)
		t.Log(strings.Repeat("-", 120))
		
		metrics := AnalyzeMultiProof(depth, width, numProofs)
		
		t.Logf("%-20s | %-15s | %-12s | %-15s | %-15s | %s",
			"Architecture", "Total Size", "Per Proof", "Generate (μs)", "Verify (μs)", "Batching Gain")
		t.Log(strings.Repeat("-", 120))
		
		for _, arch := range []string{"merkle", "hybrid", "full_kzg"} {
			m := metrics[arch]
			perProofSize := m.TotalProofSize / numProofs
			
			t.Logf("%-20s | %11d B  | %8d B  | %15d | %15d | %.2fx",
				m.Architecture, m.TotalProofSize, perProofSize,
				m.GenerateTimeUS, m.VerifyTimeUS, m.BatchingGain)
		}
	}
	
	t.Log("\n" + strings.Repeat("=", 120))
}

// TestProofScalability - тест масштабируемости пруфов
func TestProofScalability(t *testing.T) {
	width := 128
	depths := []int{4, 6, 8, 10, 12, 16}
	
	t.Log("\n" + strings.Repeat("=", 110))
	t.Log("МАСШТАБИРУЕМОСТЬ ПРУФОВ (width=128)")
	t.Log(strings.Repeat("=", 110))
	t.Logf("%-8s | %-20s | %-20s | %-20s | %s", 
		"Depth", "Merkle", "Hybrid", "Full KZG", "Winner")
	t.Log(strings.Repeat("-", 110))
	
	for _, depth := range depths {
		metrics := AnalyzeProofArchitecture(depth, width)
		
		merkleSize := metrics["merkle"].ProofSizeBytes
		hybridSize := metrics["hybrid"].ProofSizeBytes
		kzgSize := metrics["full_kzg"].ProofSizeBytes
		
		winner := "KZG"
		if hybridSize < kzgSize && hybridSize < merkleSize*2 {
			winner = "Hybrid ⚡"
		}
		
		t.Logf("%-8d | %15d B  | %15d B  | %15d B  | %s",
			depth, merkleSize, hybridSize, kzgSize, winner)
	}
	t.Log(strings.Repeat("=", 110))
	
	t.Log("\n💡 ВЫВОДЫ:")
	t.Log("  • Для depth ≤ 8: Гибрид оптимален (баланс размер/скорость)")
	t.Log("  • Для depth > 10: Full KZG лучше (пруфы не растут с глубиной)")
	t.Log("  • Merkle: только если нельзя использовать KZG")
}

// TestRealWorldProofAnalysis - реальные сценарии
func TestRealWorldProofAnalysis(t *testing.T) {
	scenarios := []struct {
		name       string
		depth      int
		width      int
		numProofs  int
		frequency  string // как часто генерируются пруфы
	}{
		{"API queries (редкие пруфы)", 6, 128, 1, "редко"},
		{"Light client (частые пруфы)", 6, 128, 100, "часто"},
		{"Batch verification", 6, 128, 1000, "батчами"},
		{"Deep tree (много данных)", 12, 64, 10, "иногда"},
	}
	
	t.Log("\n" + strings.Repeat("=", 100))
	t.Log("АНАЛИЗ РЕАЛЬНЫХ СЦЕНАРИЕВ")
	t.Log(strings.Repeat("=", 100))
	
	for _, scenario := range scenarios {
		t.Logf("\n📌 Сценарий: %s", scenario.name)
		t.Logf("   Конфигурация: depth=%d, width=%d, proofs=%d",
			scenario.depth, scenario.width, scenario.numProofs)
		
		if scenario.numProofs == 1 {
			// Одиночный пруф
			metrics := AnalyzeProofArchitecture(scenario.depth, scenario.width)
			
			t.Log("\n   Рекомендация:")
			if scenario.frequency == "редко" {
				t.Log("   ✓ Используйте ГИБРИД (Blake3 + KZG root)")
				t.Logf("     - Размер пруфа: %d байт", metrics["hybrid"].ProofSizeBytes)
				t.Logf("     - Генерация: %d μs", metrics["hybrid"].GenerateTimeUS)
				t.Logf("     - Верификация: %d μs", metrics["hybrid"].VerifyTimeUS)
			} else {
				t.Log("   ✓ Используйте Full KZG (минимальные пруфы)")
				t.Logf("     - Размер пруфа: %d байт (константа!)", metrics["full_kzg"].ProofSizeBytes)
				t.Logf("     - Генерация: %d μs", metrics["full_kzg"].GenerateTimeUS)
			}
		} else {
			// Мульти-пруфы
			metrics := AnalyzeMultiProof(scenario.depth, scenario.width, scenario.numProofs)
			
			t.Log("\n   Рекомендация для мульти-пруфов:")
			if scenario.numProofs < 100 {
				t.Log("   ✓ Используйте ГИБРИД с дедупликацией")
				t.Logf("     - Общий размер: %d байт", metrics["hybrid"].TotalProofSize)
				t.Logf("     - На пруф: %d байт", metrics["hybrid"].TotalProofSize/scenario.numProofs)
				t.Logf("     - Батчинг выигрыш: %.2fx", metrics["hybrid"].BatchingGain)
			} else {
				t.Log("   ✓ Используйте Full KZG (aggregated proofs)")
				t.Logf("     - Общий размер: %d байт", metrics["full_kzg"].TotalProofSize)
				t.Logf("     - На пруф: %d байт (!)", metrics["full_kzg"].TotalProofSize/scenario.numProofs)
				t.Logf("     - Батчинг выигрыш: %.2fx", metrics["full_kzg"].BatchingGain)
			}
		}
	}
	
	t.Log("\n" + strings.Repeat("=", 100))
}
