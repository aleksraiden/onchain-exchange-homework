package verkletree

import (
	"fmt"
	"math/big"
	"strings"
	"testing"
)

// TestCapacityAnalysis - анализ емкости разных конфигураций
func TestCapacityAnalysis(t *testing.T) {
	configs := []struct {
		depth int
		width int
	}{
		{4, 64}, {4, 128}, {4, 256},
		{6, 64}, {6, 128}, {6, 256},
		{8, 64}, {8, 128}, {8, 256},
		{12, 64}, {12, 128},
		{16, 64}, {16, 128},
	}
	
	t.Log("\n" + strings.Repeat("=", 90))
	t.Log("АНАЛИЗ ЕМКОСТИ VERKLE TREE")
	t.Log(strings.Repeat("=", 90))
	t.Logf("%-8s | %-10s | %-40s | %s", "Depth", "Width", "Capacity", "Use Cases")
	t.Log(strings.Repeat("-", 90))
	
	for _, cfg := range configs {
		capacity := CalculateCapacity(cfg.depth, cfg.width)
		useCase := getUseCase(capacity.MaxItems)
		
		t.Logf("%-8d | %-10d | %-40s | %s", 
			cfg.depth, cfg.width, capacity.FormatCapacity(), useCase)
	}
	t.Log(strings.Repeat("=", 90))
}

func getUseCase(capacity *big.Int) string {
	million := big.NewInt(1e6)
	billion := big.NewInt(1e9)
	trillion := big.NewInt(1e12)
	
	if capacity.Cmp(trillion) >= 0 {
		return "Глобальная база данных всех людей на Земле"
	} else if capacity.Cmp(billion) >= 0 {
		return "Крупная корпоративная система, соцсеть"
	} else if capacity.Cmp(million) >= 0 {
		return "Средний сервис, приложение"
	}
	return "Малое приложение"
}

// TestPerformanceAnalysis - анализ производительности
func TestPerformanceAnalysis(t *testing.T) {
	configs := []struct {
		depth  int
		width  int
		useKZG bool
	}{
		{6, 64, false},
		{6, 128, false},
		{8, 64, false},
		{8, 128, false},
		{16, 64, false},
		{16, 128, false},
		// С KZG
		{6, 128, true},
		{8, 128, true},
	}
	
	t.Log("\n" + strings.Repeat("=", 100))
	t.Log("АНАЛИЗ ПРОИЗВОДИТЕЛЬНОСТИ")
	t.Log(strings.Repeat("=", 100))
	t.Logf("%-8s | %-8s | %-8s | %-12s | %-12s | %-15s | %s", 
		"Depth", "Width", "KZG", "Insert(μs)", "Read(μs)", "Mem/Node(KB)", "Capacity")
	t.Log(strings.Repeat("-", 100))
	
	for _, cfg := range configs {
		perf := EstimatePerformance(cfg.depth, cfg.width, cfg.useKZG)
		capacity := CalculateCapacity(cfg.depth, cfg.width)
		
		kzgStr := "No"
		if cfg.useKZG {
			kzgStr = "Yes"
		}
		
		t.Logf("%-8d | %-8d | %-8s | %-12d | %-12d | %-15.2f | %s",
			cfg.depth, cfg.width, kzgStr,
			perf.EstimatedInsertUS, perf.EstimatedReadUS,
			float64(perf.MemoryPerNode)/1024.0,
			capacity.FormatCapacity())
	}
	t.Log(strings.Repeat("=", 100))
}

// TestRealWorldScenarios - реальные сценарии
func TestRealWorldScenarios(t *testing.T) {
	scenarios := []struct {
		name      string
		users     int64
		depth     int
		width     int
	}{
		{"Стартап (10K пользователей)", 10_000, 4, 64},
		{"Средний SaaS (1M пользователей)", 1_000_000, 6, 64},
		{"Крупный сервис (100M)", 100_000_000, 6, 128},
		{"Соцсеть (1B пользователей)", 1_000_000_000, 8, 128},
		{"Глобальная система (10B)", 10_000_000_000, 8, 256},
	}
	
	t.Log("\n" + strings.Repeat("=", 100))
	t.Log("РЕКОМЕНДАЦИИ ДЛЯ РЕАЛЬНЫХ СЦЕНАРИЕВ")
	t.Log(strings.Repeat("=", 100))
	t.Logf("%-35s | %-8s | %-8s | %-20s | %s", 
		"Scenario", "Depth", "Width", "Utilization", "Status")
	t.Log(strings.Repeat("-", 100))
	
	for _, scenario := range scenarios {
		capacity := CalculateCapacity(scenario.depth, scenario.width)
		
		// Процент использования
		users := big.NewInt(scenario.users)
		utilization := new(big.Float).Quo(
			new(big.Float).SetInt(users),
			new(big.Float).SetInt(capacity.MaxItems),
		)
		util, _ := utilization.Float64()
		utilPercent := util * 100
		
		status := "✓ Оптимально"
		if utilPercent > 80 {
			status = "⚠ Близко к лимиту"
		} else if utilPercent > 95 {
			status = "✗ Недостаточно"
		} else if utilPercent < 0.01 {
			status = "→ Избыточно (можно меньше depth)"
		}
		
		t.Logf("%-35s | %-8d | %-8d | %-20s | %s",
			scenario.name, scenario.depth, scenario.width,
			fmt.Sprintf("%.6f%%", utilPercent), status)
	}
	t.Log(strings.Repeat("=", 100))
}

// TestPresetConfigs - тест предустановленных конфигураций
func TestPresetConfigs(t *testing.T) {
	presets := []string{"small", "medium", "large", "global", "massive"}
	
	t.Log("\n" + strings.Repeat("=", 80))
	t.Log("ТЕСТ ПРЕДУСТАНОВЛЕННЫХ КОНФИГУРАЦИЙ")
	t.Log(strings.Repeat("=", 80))
	
	for _, preset := range presets {
		tree, err := NewWithPreset(preset, nil, nil)
		if err != nil {
			t.Fatalf("Ошибка создания preset '%s': %v", preset, err)
		}
		
		capacity := CalculateCapacity(tree.config.Levels, tree.config.NodeWidth)
		
		t.Logf("Preset '%s': depth=%d, width=%d, capacity=%s", 
			preset, tree.config.Levels, tree.config.NodeWidth, 
			capacity.FormatCapacity())
		
		// Проверяем что автоматический рост отключен
		if tree.config.AutoGrowDepth {
			t.Errorf("AutoGrowDepth должен быть false для preset '%s'", preset)
		}
		
		// Проверяем что можем добавлять данные
		batch := tree.BeginBatch()
		userData := &UserData{
			Balances: map[string]float64{"USD": 100.0},
		}
		if err := batch.AddUserData("test_user", userData); err != nil {
			t.Errorf("Ошибка добавления данных для preset '%s': %v", preset, err)
		}
		
		if _, err := tree.CommitBatch(batch); err != nil {
			t.Errorf("Ошибка коммита для preset '%s': %v", preset, err)
		}
	}
	
	t.Log(strings.Repeat("=", 80))
}
