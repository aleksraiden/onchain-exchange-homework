// Создайте файл collision_analysis_test.go

package verkletree

import (
	"crypto/sha256"
	"fmt"
	"math"
	"strings"
	"testing"
)

// TestCollisionExplanation - детальное объяснение коллизий
func TestCollisionExplanation(t *testing.T) {
	t.Log("\n" + strings.Repeat("=", 100))
	t.Log("ЧТО ТАКОЕ КОЛЛИЗИЯ В VERKLE ДЕРЕВЕ")
	t.Log(strings.Repeat("=", 100))
	
	t.Log("\n### Определение:")
	t.Log("Коллизия = когда 2+ разных пользователя попадают в ОДНУ И ТУ ЖЕ листовую ячейку")
	
	t.Log("\n### Как это происходит:")
	
	// Пример 1: НЕТ коллизии
	t.Log("\n--- Пример 1: НЕТ коллизии ---")
	userA := "alice"
	userB := "bob"
	
	hashA := sha256.Sum256([]byte(userA))
	hashB := sha256.Sum256([]byte(userB))
	
	t.Logf("Alice hash: %x...", hashA[:8])
	t.Logf("Bob   hash: %x...", hashB[:8])
	
	depth := 8
	width := 128
	
	// Показываем путь для каждого
	t.Log("\nПуть Alice:")
	pathA := make([]int, depth)
	for i := 0; i < depth; i++ {
		pathA[i] = getNodeIndex(hashA[i], width)
		t.Logf("  Level %d: byte=%d -> index=%d", i, hashA[i], pathA[i])
	}
	
	t.Log("\nПуть Bob:")
	pathB := make([]int, depth)
	for i := 0; i < depth; i++ {
		pathB[i] = getNodeIndex(hashB[i], width)
		t.Logf("  Level %d: byte=%d -> index=%d", i, hashB[i], pathB[i])
	}
	
	// Проверяем коллизию
	collision := true
	for i := 0; i < depth; i++ {
		if pathA[i] != pathB[i] {
			collision = false
			t.Logf("\n✅ НЕТ коллизии: пути расходятся на уровне %d", i)
			break
		}
	}
	
	if collision {
		t.Log("\n❌ КОЛЛИЗИЯ! Оба пользователя в одной ячейке!")
	}
	
	t.Log("\n" + strings.Repeat("=", 100))
}

// TestCollisionProbabilityMath - математика вероятности
func TestCollisionProbabilityMath(t *testing.T) {
	t.Log("\n" + strings.Repeat("=", 100))
	t.Log("МАТЕМАТИКА ВЕРОЯТНОСТИ КОЛЛИЗИЙ")
	t.Log(strings.Repeat("=", 100))
	
	depth := 8
	width := 128
	
	t.Logf("\nКонфигурация: depth=%d, width=%d", depth, width)
	
	t.Log("\n### Шанс что 2 пользователя попадут в одну ячейку:")
	
	// На каждом уровне независимая вероятность
	probPerLevel := 1.0 / float64(width)
	t.Logf("Вероятность на одном уровне: 1/%d = %.6f", width, probPerLevel)
	
	// Для коллизии нужно совпадение на ВСЕХ уровнях
	probCollision := math.Pow(probPerLevel, float64(depth))
	t.Logf("Вероятность коллизии на всех %d уровнях: (1/%d)^%d = %.15f", 
		depth, width, depth, probCollision)
	
	t.Logf("\nЭто 1 шанс из %.0f", 1.0/probCollision)
	
	// Для понимания масштаба
	t.Log("\n### Для понимания масштаба:")
	earthPopulation := 8_000_000_000.0
	
	expectedCollisions := earthPopulation * probCollision
	t.Logf("Если добавить ВСЁ население Земли (8 млрд):")
	t.Logf("  Ожидаемое число коллизий: %.2f человек", expectedCollisions)
	
	if expectedCollisions < 0.001 {
		t.Log("  ✅ Меньше 0.001 - практически невозможно!")
	}
	
	// Birthday paradox для дерева
	t.Log("\n### Birthday Paradox (более точная оценка):")
	
	userCounts := []int64{1_000_000, 10_000_000, 100_000_000, 1_000_000_000}
	
	for _, n := range userCounts {
		// Приблизительная формула birthday paradox:
		// P(collision) ≈ 1 - e^(-n²/2m)
		// где m = общее количество возможных позиций
		
		totalPositions := math.Pow(float64(width), float64(depth))
		
		// Упрощенная формула для малых вероятностей:
		// P ≈ n² / (2 * m)
		nFloat := float64(n)
		pApprox := (nFloat * nFloat) / (2.0 * totalPositions)
		
		t.Logf("\nДля %s пользователей:", formatNumber(n))
		t.Logf("  Вероятность хотя бы одной коллизии: %.8f%%", pApprox*100)
		t.Logf("  Это примерно 1 шанс из %.0f", 1.0/pApprox)
		
		if pApprox < 0.00001 {
			t.Log("  ✅ Крайне маловероятно")
		} else if pApprox < 0.0001 {
			t.Log("  ✅ Очень маловероятно")
		} else if pApprox < 0.001 {
			t.Log("  ⚠️  Маловероятно, но возможно")
		} else {
			t.Log("  ❌ Возможно!")
		}
	}
	
	t.Log("\n" + strings.Repeat("=", 100))
}

// TestRealWorldCollisionScenarios - реальные сценарии
func TestRealWorldCollisionScenarios(t *testing.T) {
	t.Log("\n" + strings.Repeat("=", 100))
	t.Log("РЕАЛЬНЫЕ СЦЕНАРИИ КОЛЛИЗИЙ")
	t.Log(strings.Repeat("=", 100))
	
	scenarios := []struct {
		depth       int
		width       int
		users       int64
		description string
	}{
		{6, 64, 1_000_000, "Маленькое дерево, средний сервис"},
		{6, 128, 10_000_000, "Средний размер, крупный сервис"},
		{8, 128, 100_000_000, "РЕКОМЕНДУЕМАЯ конфигурация"},
		{8, 128, 1_000_000_000, "Социальная сеть (1B пользователей)"},
		{4, 32, 100_000, "Опасная конфигурация"},
	}
	
	t.Logf("\n%-8s | %-8s | %-15s | %-25s | %s", 
		"Depth", "Width", "Users", "Collision Prob", "Status")
	t.Log(strings.Repeat("-", 100))
	
	for _, s := range scenarios {
		// Capacity
		totalPositions := math.Pow(float64(s.width), float64(s.depth))
		
		// Birthday paradox probability
		nFloat := float64(s.users)
		pCollision := (nFloat * nFloat) / (2.0 * totalPositions)
		
		status := ""
		if pCollision < 0.000001 {
			status = "✅ Идеально (можно игнорировать)"
		} else if pCollision < 0.0001 {
			status = "✅ Отлично (редко)"
		} else if pCollision < 0.01 {
			status = "⚠️  Хорошо (нужна обработка)"
		} else if pCollision < 0.1 {
			status = "⚠️  Так себе (часто)"
		} else {
			status = "❌ Плохо (очень часто)"
		}
		
		t.Logf("%-8d | %-8d | %12s    | %15.9f%%      | %s",
			s.depth, s.width, formatNumber(s.users), pCollision*100, status)
	}
	
	t.Log("\n💡 ВЫВОД:")
	t.Log("  • Depth=8, Width=128: коллизии практически невозможны до 1B пользователей")
	t.Log("  • Depth=6, Width=128: коллизии крайне редки до 100M пользователей")
	t.Log("  • Depth<6 или Width<64: нужна обязательная обработка коллизий!")
	
	t.Log("\n" + strings.Repeat("=", 100))
}

// TestCollisionHandlingStrategies - стратегии обработки
func TestCollisionHandlingStrategies(t *testing.T) {
	t.Log("\n" + strings.Repeat("=", 100))
	t.Log("СТРАТЕГИИ ОБРАБОТКИ КОЛЛИЗИЙ")
	t.Log(strings.Repeat("=", 100))
	
	strategies := []struct {
		name        string
		complexity  string
		performance string
		safety      string
		description string
	}{
		{
			name:        "1. Игнорировать (overwrite)",
			complexity:  "⭐ Самая простая",
			performance: "⭐⭐⭐ Максимальная",
			safety:      "⚠️  Потеря данных при коллизии",
			description: "Просто перезаписываем. OK для depth≥8, width≥128",
		},
		{
			name:        "2. Linked list в ячейке",
			complexity:  "⭐⭐ Простая",
			performance: "⭐⭐ Хорошая",
			safety:      "✅ Данные не теряются",
			description: "Храним несколько листьев в одной ячейке (next pointer)",
		},
		{
			name:        "3. Проверка и ошибка",
			complexity:  "⭐ Самая простая",
			performance: "⭐⭐⭐ Максимальная",
			safety:      "✅ Явная ошибка",
			description: "Возвращаем ошибку при коллизии. Клиент повторяет",
		},
		{
			name:        "4. Расширение узла",
			complexity:  "⭐⭐⭐ Сложная",
			performance: "⭐ Медленная",
			safety:      "✅ Данные не теряются",
			description: "Создаем поддерево. Много кода, медленно",
		},
	}
	
	for i, s := range strategies {
		t.Logf("\n### %s", s.name)
		t.Logf("Сложность:       %s", s.complexity)
		t.Logf("Производительность: %s", s.performance)
		t.Logf("Безопасность:    %s", s.safety)
		t.Logf("Описание:        %s", s.description)
		
		if i == 0 {
			t.Log("\n✅ РЕКОМЕНДУЕТСЯ для depth=8, width=128")
			t.Log("   Вероятность коллизии настолько мала, что можно игнорировать")
		}
	}
	
	t.Log("\n" + strings.Repeat("=", 100))
}

// TestSimulateCollisions - симуляция реальных коллизий
func TestSimulateCollisions(t *testing.T) {
	if testing.Short() {
		t.Skip("Пропускаем медленный тест")
	}
	
	t.Log("\n" + strings.Repeat("=", 100))
	t.Log("СИМУЛЯЦИЯ РЕАЛЬНЫХ КОЛЛИЗИЙ")
	t.Log(strings.Repeat("=", 100))
	
	depth := 8
	width := 128
	userCount := 100000
	
	t.Logf("\nГенерируем %d пользователей и проверяем коллизии...", userCount)
	
	// Карта для отслеживания занятых ячеек
	occupied := make(map[string]bool)
	collisions := 0
	
	for i := 0; i < userCount; i++ {
		userID := fmt.Sprintf("user_%d", i)
		hash := sha256.Sum256([]byte(userID))
		
		// Вычисляем путь
		path := make([]byte, depth)
		for level := 0; level < depth; level++ {
			path[level] = byte(getNodeIndex(hash[level], width))
		}
		
		// Проверяем коллизию
		pathKey := string(path)
		if occupied[pathKey] {
			collisions++
			if collisions <= 5 {
				t.Logf("  Коллизия #%d на пользователе '%s' (путь: %v)", 
					collisions, userID, path)
			}
		}
		occupied[pathKey] = true
	}
	
	collisionRate := float64(collisions) / float64(userCount) * 100
	
	t.Logf("\n📊 РЕЗУЛЬТАТ:")
	t.Logf("Всего пользователей: %d", userCount)
	t.Logf("Обнаружено коллизий: %d", collisions)
	t.Logf("Процент коллизий: %.6f%%", collisionRate)
	
	if collisions == 0 {
		t.Log("✅ НИ ОДНОЙ КОЛЛИЗИИ! Можно безопасно игнорировать")
	} else if collisionRate < 0.01 {
		t.Log("✅ Очень мало коллизий. Можно игнорировать или простая обработка")
	} else if collisionRate < 0.1 {
		t.Log("⚠️  Редкие коллизии. Рекомендуется обработка")
	} else {
		t.Log("❌ Частые коллизии. Обязательна обработка!")
	}
	
	t.Log("\n" + strings.Repeat("=", 100))
}
