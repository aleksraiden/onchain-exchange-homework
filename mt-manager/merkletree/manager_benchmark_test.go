package merkletree

import (
	"fmt"
	"math/rand"
	"testing"
	"time"
)

// TestManagerStressTest - комплексный стресс-тест менеджера
func TestManagerStressTest(t *testing.T) {
	if testing.Short() {
		t.Skip("Пропуск стресс-теста в short режиме")
	}

	// Разное количество деревьев для тестирования
	treeCounts := []int{10, 25, 50, 100}

	for _, numTrees := range treeCounts {
		t.Run(fmt.Sprintf("Trees_%d", numTrees), func(t *testing.T) {
			testManagerWithTreeCount(t, numTrees)
		})
	}
}

func testManagerWithTreeCount(t *testing.T, numTrees int) {
	rng := rand.New(rand.NewSource(time.Now().UnixNano()))

	cfg := DefaultConfig()
	cfg.CacheSize = 50000
	cfg.MaxDepth = 3

	manager := NewManager[*Account](cfg)

	t.Logf("=== Тест с %d деревьями ===", numTrees)

	// Фаза 1: Создание и заполнение деревьев
	t.Logf("Фаза 1: Создание и заполнение %d деревьев...", numTrees)
	startPhase1 := time.Now()

	treeNames := make([]string, numTrees)
	treeSizes := make([]int, numTrees)
	totalItems := 0

	for i := 0; i < numTrees; i++ {
		treeName := fmt.Sprintf("tree_%d", i)
		treeNames[i] = treeName

		// Случайное количество элементов от 10K до 1M
		minSize := 10_000
		maxSize := 1_000_000
		treeSize := minSize + rng.Intn(maxSize-minSize+1)
		treeSizes[i] = treeSize
		totalItems += treeSize

		tree, err := manager.CreateTree(treeName)
		if err != nil {
			t.Fatalf("Не удалось создать дерево %s: %v", treeName, err)
		}

		// Заполняем дерево
		baseUID := uint64(i * 1_000_000) // Разделяем UID между деревьями
		for j := 0; j < treeSize; j++ {
			uid := baseUID + uint64(j)
			status := AccountStatus(rng.Intn(5))
			tree.Insert(NewAccount(uid, status))
		}

		if (i+1)%10 == 0 || i == numTrees-1 {
			t.Logf("  Создано %d/%d деревьев, всего элементов: %d",
				i+1, numTrees, totalItems)
		}
	}

	phase1Duration := time.Since(startPhase1)
	t.Logf("Фаза 1 завершена за %v", phase1Duration)
	t.Logf("  Всего элементов: %d", totalItems)
	t.Logf("  Средний размер дерева: %d", totalItems/numTrees)

	// Фаза 2: Вычисление индивидуальных хешей всех деревьев
	t.Logf("\nФаза 2: Вычисление хешей %d деревьев...", numTrees)
	startPhase2 := time.Now()

	individualRoots := manager.ComputeAllRoots()

	phase2Duration := time.Since(startPhase2)
	t.Logf("Фаза 2 завершена за %v", phase2Duration)
	t.Logf("  Среднее время на дерево: %v", phase2Duration/time.Duration(numTrees))

	// Показываем несколько примеров хешей
	count := 0
	for name, root := range individualRoots {
		if count < 3 {
			t.Logf("  %s: %x", name, root[:16])
			count++
		}
	}

	// Фаза 3: Вычисление глобального рута
	t.Logf("\nФаза 3: Вычисление глобального корня...")
	startPhase3 := time.Now()

	globalRoot1 := manager.ComputeGlobalRoot()

	phase3Duration := time.Since(startPhase3)
	t.Logf("Фаза 3 завершена за %v", phase3Duration)
	t.Logf("  Глобальный корень: %x", globalRoot1[:16])

	// Фаза 4: Случайные обновления
	t.Logf("\nФаза 4: Выполнение 100K случайных обновлений...")
	startPhase4 := time.Now()

	totalUpdates := 100_000
	updatesPerformed := 0
	updateBatches := 0

	for updatesPerformed < totalUpdates {
		// Выбираем случайное дерево
		treeIdx := rng.Intn(numTrees)
		treeName := treeNames[treeIdx]
		tree, _ := manager.GetTree(treeName)

		// Случайное количество обновлений от 1 до 1000
		batchSize := 1 + rng.Intn(1000)
		if updatesPerformed+batchSize > totalUpdates {
			batchSize = totalUpdates - updatesPerformed
		}

		// Выполняем обновления
		baseUID := uint64(treeIdx * 1_000_000)
		maxUID := uint64(treeSizes[treeIdx])

		for i := 0; i < batchSize; i++ {
			// Обновляем случайный существующий элемент
			uid := baseUID + uint64(rng.Int63n(int64(maxUID)))
			status := AccountStatus(rng.Intn(5))
			tree.Insert(NewAccount(uid, status))
		}

		updatesPerformed += batchSize
		updateBatches++

		if updatesPerformed%10000 == 0 {
			t.Logf("  Выполнено %d/%d обновлений (%d батчей)",
				updatesPerformed, totalUpdates, updateBatches)
		}
	}

	phase4Duration := time.Since(startPhase4)
	t.Logf("Фаза 4 завершена за %v", phase4Duration)
	t.Logf("  Батчей обновлений: %d", updateBatches)
	t.Logf("  Средний размер батча: %d", totalUpdates/updateBatches)
	t.Logf("  Скорость обновлений: %.0f ops/sec",
		float64(totalUpdates)/phase4Duration.Seconds())

	// Фаза 5: Пересчет всех хешей после обновлений
	t.Logf("\nФаза 5: Пересчет хешей после обновлений...")
	startPhase5 := time.Now()

	individualRoots2 := manager.ComputeAllRoots()

	phase5Duration := time.Since(startPhase5)
	t.Logf("Фаза 5 завершена за %v", phase5Duration)

	// Проверяем, что хеши изменились
	changedTrees := 0
	for name, root := range individualRoots2 {
		if root != individualRoots[name] {
			changedTrees++
		}
	}
	t.Logf("  Деревьев с измененным хешем: %d/%d", changedTrees, numTrees)

	// Фаза 6: Новый глобальный рут
	t.Logf("\nФаза 6: Новый глобальный корень...")
	startPhase6 := time.Now()

	globalRoot2 := manager.ComputeGlobalRoot()

	phase6Duration := time.Since(startPhase6)
	t.Logf("Фаза 6 завершена за %v", phase6Duration)
	t.Logf("  Новый глобальный корень: %x", globalRoot2[:16])

	// Проверка изменения глобального корня
	if globalRoot1 == globalRoot2 {
		t.Error("Глобальный корень не должен совпадать после обновлений")
	}

	// Итоговая статистика
	totalDuration := time.Since(startPhase1)
	stats := manager.GetTotalStats()

	t.Logf("\n=== Итоговая статистика ===")
	t.Logf("Общее время: %v", totalDuration)
	t.Logf("  Фаза 1 (заполнение): %v", phase1Duration)
	t.Logf("  Фаза 2 (хеши): %v", phase2Duration)
	t.Logf("  Фаза 3 (глобальный рут): %v", phase3Duration)
	t.Logf("  Фаза 4 (обновления): %v", phase4Duration)
	t.Logf("  Фаза 5 (пересчет): %v", phase5Duration)
	t.Logf("  Фаза 6 (новый рут): %v", phase6Duration)
	t.Logf("\nСтатистика менеджера:")
	t.Logf("  Деревьев: %d", stats.TreeCount)
	t.Logf("  Элементов: %d", stats.TotalItems)
	t.Logf("  Узлов: %d", stats.TotalNodes)
	t.Logf("  Размер кеша: %d", stats.TotalCacheSize)
}

// BenchmarkManagerScaling - бенчмарк масштабирования менеджера
func BenchmarkManagerScaling(b *testing.B) {
	treeCounts := []int{10, 50, 100}

	for _, numTrees := range treeCounts {
		b.Run(fmt.Sprintf("Trees_%d", numTrees), func(b *testing.B) {
			benchmarkManagerWithTreeCount(b, numTrees)
		})
	}
}

func benchmarkManagerWithTreeCount(b *testing.B, numTrees int) {
	rng := rand.New(rand.NewSource(42)) // Фиксированный seed для воспроизводимости

	cfg := DefaultConfig()
	cfg.CacheSize = 10000
	manager := NewManager[*Account](cfg)

	// Подготовка: создаем деревья с данными
	for i := 0; i < numTrees; i++ {
		treeName := fmt.Sprintf("tree_%d", i)
		tree, _ := manager.CreateTree(treeName)

		treeSize := 10000 // Фиксированный размер для бенчмарка
		baseUID := uint64(i * 1_000_000)

		for j := 0; j < treeSize; j++ {
			uid := baseUID + uint64(j)
			tree.Insert(NewAccount(uid, StatusUser))
		}
	}

	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		// Обновление
		treeIdx := rng.Intn(numTrees)
		treeName := fmt.Sprintf("tree_%d", treeIdx)
		tree, _ := manager.GetTree(treeName)

		baseUID := uint64(treeIdx * 1_000_000)
		uid := baseUID + uint64(rng.Intn(10000))
		tree.Insert(NewAccount(uid, StatusUser))

		// Вычисление глобального корня каждые 100 итераций
		if i%100 == 0 {
			manager.ComputeGlobalRoot()
		}
	}
}

// BenchmarkGlobalRootComputation - бенчмарк вычисления глобального корня
func BenchmarkGlobalRootComputation(b *testing.B) {
	treeCounts := []int{10, 50, 100}

	for _, numTrees := range treeCounts {
		b.Run(fmt.Sprintf("Trees_%d", numTrees), func(b *testing.B) {
			manager := setupManagerForBench(numTrees, 10000)

			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				manager.ComputeGlobalRoot()
			}
		})
	}
}

// BenchmarkParallelRootComputation - бенчмарк параллельного вычисления корней
func BenchmarkParallelRootComputation(b *testing.B) {
	treeCounts := []int{10, 50, 100}

	for _, numTrees := range treeCounts {
		b.Run(fmt.Sprintf("Trees_%d", numTrees), func(b *testing.B) {
			manager := setupManagerForBench(numTrees, 10000)

			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				manager.ComputeAllRoots()
			}
		})
	}
}

// setupManagerForBench подготавливает менеджер для бенчмарков
func setupManagerForBench(numTrees, itemsPerTree int) *TreeManager[*Account] {
	cfg := DefaultConfig()
	cfg.CacheSize = 10000
	manager := NewManager[*Account](cfg)

	for i := 0; i < numTrees; i++ {
		treeName := fmt.Sprintf("tree_%d", i)
		tree, _ := manager.CreateTree(treeName)

		baseUID := uint64(i * 1_000_000)
		for j := 0; j < itemsPerTree; j++ {
			uid := baseUID + uint64(j)
			tree.Insert(NewAccountDeterministic(uid, StatusUser))
		}
	}

	return manager
}
