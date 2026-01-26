package merkletree

import (
	"fmt"
	"math/rand"
	"testing"
	"time"
)

func TestManagerStressTest(t *testing.T) {
	tests := []struct {
		name      string
		numTrees  int
		minItems  int
		maxItems  int
		updates   int
	}{
		{
			name:     "Trees_10",
			numTrees: 10,
			minItems: 100000,
			maxItems: 1000000,
			updates:  100000,
		},
		{
			name:     "Trees_100",
			numTrees: 100,
			minItems: 100000,
			maxItems: 1000000,
			updates:  100000,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			runStressTest(t, tt.numTrees, tt.minItems, tt.maxItems, tt.updates)
		})
	}
}

func runStressTest(t *testing.T, numTrees, minItems, maxItems, numUpdates int) {
	t.Logf("=== Тест с %d деревьями ===", numTrees)

	mgr := NewManager[*Account](DefaultConfig())
	rnd := rand.New(rand.NewSource(time.Now().UnixNano()))

	// Фаза 1: Создание и заполнение деревьев
	t.Log("Фаза 1: Создание и заполнение деревьев...")
	phase1Start := time.Now()

	totalItems := 0
	treeItemCounts := make(map[string]int)

	for i := 0; i < numTrees; i++ {
		treeID := fmt.Sprintf("tree_%d", i)
		tree := mgr.GetOrCreateTree(treeID)

		numItems := minItems + rnd.Intn(maxItems-minItems)
		treeItemCounts[treeID] = numItems

		items := make([]*Account, numItems)
		for j := 0; j < numItems; j++ {
			id := uint64(i*maxItems + j)
			status := AccountStatus(rnd.Intn(3))
			items[j] = NewAccount(id, status)
		}

		tree.InsertBatch(items)
		totalItems += numItems

		if (i+1)%10 == 0 || i == numTrees-1 {
			t.Logf("  Создано %d/%d деревьев, всего элементов: %d", i+1, numTrees, totalItems)
		}
	}

	phase1Duration := time.Since(phase1Start)
	t.Logf("Фаза 1 завершена за %v", phase1Duration)
	t.Logf("  Всего элементов: %d", totalItems)
	t.Logf("  Средний размер дерева: %d", totalItems/numTrees)

	// Фаза 2: Вычисление хешей всех деревьев
	t.Log("\nФаза 2: Вычисление хешей деревьев...")
	phase2Start := time.Now()

	initialHashes := make(map[string][32]byte)
	treeIDs := mgr.ListTrees()
	for _, treeID := range treeIDs {
		tree, exists := mgr.GetTree(treeID)
		if !exists {
			t.Fatalf("Tree %s not found", treeID)
		}
		hash := tree.ComputeRoot()
		initialHashes[treeID] = hash
	}

	phase2Duration := time.Since(phase2Start)
	t.Logf("Фаза 2 завершена за %v", phase2Duration)
	t.Logf("  Среднее время на дерево: %v", phase2Duration/time.Duration(numTrees))

	// Печать случайных хешей
	count := 0
	for treeID, hash := range initialHashes {
		if count < 3 {
			t.Logf("  %s: %x", treeID, hash[:16])
			count++
		}
	}

	// Фаза 3: Вычисление глобального корня
	t.Log("\nФаза 3: Вычисление глобального корня...")
	phase3Start := time.Now()

	globalRoot := mgr.ComputeGlobalRoot()

	phase3Duration := time.Since(phase3Start)
	t.Logf("Фаза 3 завершена за %v", phase3Duration)
	t.Logf("  Глобальный корень: %x", globalRoot[:16])

	// Фаза 4: Случайные обновления
	t.Log("\nФаза 4: Выполнение случайных обновлений...")
	phase4Start := time.Now()

	updateBatches := 0
	updatesPerformed := 0

	for updatesPerformed < numUpdates {
		batchSize := rnd.Intn(500) + 1
		if updatesPerformed+batchSize > numUpdates {
			batchSize = numUpdates - updatesPerformed
		}

		treeIdx := rnd.Intn(numTrees)
		treeID := fmt.Sprintf("tree_%d", treeIdx)
		tree, _ := mgr.GetTree(treeID)

		items := make([]*Account, batchSize)
		for i := 0; i < batchSize; i++ {
			id := uint64(treeIdx*maxItems + rnd.Intn(treeItemCounts[treeID]))
			status := AccountStatus(rnd.Intn(3))
			items[i] = NewAccount(id, status)
		}

		tree.InsertBatch(items)
		updateBatches++
		updatesPerformed += batchSize

		if updatesPerformed%(numUpdates/10) == 0 || updatesPerformed >= numUpdates {
			t.Logf("  Выполнено %d/%d обновлений (%d батчей)", updatesPerformed, numUpdates, updateBatches)
		}
	}

	phase4Duration := time.Since(phase4Start)
	t.Logf("Фаза 4 завершена за %v", phase4Duration)
	t.Logf("  Батчей обновлений: %d", updateBatches)
	t.Logf("  Средний размер батча: %d", numUpdates/updateBatches)
	t.Logf("  Скорость обновлений: %.0f ops/sec", float64(numUpdates)/phase4Duration.Seconds())

	// Фаза 5: Случайные удаления
	t.Log("\nФаза 5: Выполнение случайных удалений...")
	phase5Start := time.Now()

	totalDeleted := 0
	deleteBatches := 0

	for i := 0; i < numTrees; i++ {
		treeID := fmt.Sprintf("tree_%d", i)
		tree, _ := mgr.GetTree(treeID)

		// Случайное количество удалений от 100 до 10K
		numDeletes := 100 + rnd.Intn(9900)
		if numDeletes > treeItemCounts[treeID] {
			numDeletes = treeItemCounts[treeID] / 2 // Не более половины элементов
		}

		// Удаляем батчами
		deleted := 0
		for deleted < numDeletes {
			batchSize := rnd.Intn(500) + 1
			if deleted+batchSize > numDeletes {
				batchSize = numDeletes - deleted
			}

			idsToDelete := make([]uint64, batchSize)
			for j := 0; j < batchSize; j++ {
				id := uint64(i*maxItems + rnd.Intn(treeItemCounts[treeID]))
				idsToDelete[j] = id
			}

			count := tree.DeleteBatch(idsToDelete)
			deleted += count
			totalDeleted += count
			deleteBatches++
		}

		if (i+1)%10 == 0 || i == numTrees-1 {
			t.Logf("  Обработано %d/%d деревьев, удалено элементов: %d", i+1, numTrees, totalDeleted)
		}
	}

	phase5Duration := time.Since(phase5Start)
	t.Logf("Фаза 5 завершена за %v", phase5Duration)
	t.Logf("  Всего удалено: %d элементов", totalDeleted)
	t.Logf("  Батчей удалений: %d", deleteBatches)
	if phase5Duration.Seconds() > 0 {
		t.Logf("  Скорость удалений: %.0f ops/sec", float64(totalDeleted)/phase5Duration.Seconds())
	}

	// Фаза 6: Пересчет хешей после удалений
	t.Log("\nФаза 6: Пересчет хешей после удалений...")
	phase6Start := time.Now()

	changedTrees := 0
	for _, treeID := range treeIDs {
		tree, _ := mgr.GetTree(treeID)
		newHash := tree.ComputeRoot()

		if newHash != initialHashes[treeID] {
			changedTrees++
		}
	}

	phase6Duration := time.Since(phase6Start)
	t.Logf("Фаза 6 завершена за %v", phase6Duration)
	t.Logf("  Деревьев с измененным хешем: %d/%d", changedTrees, numTrees)

	// Фаза 7: Новый глобальный корень
	t.Log("\nФаза 7: Новый глобальный корень...")
	phase7Start := time.Now()

	newGlobalRoot := mgr.ComputeGlobalRoot()

	phase7Duration := time.Since(phase7Start)
	t.Logf("Фаза 7 завершена за %v", phase7Duration)
	t.Logf("  Новый глобальный корень: %x", newGlobalRoot[:16])

	if newGlobalRoot == globalRoot {
		t.Error("Глобальный корень не должен совпадать после удалений!")
	}

	// Итоговая статистика
	totalDuration := phase1Duration + phase2Duration + phase3Duration +
		phase4Duration + phase5Duration + phase6Duration + phase7Duration

	t.Log("\n=== Итоговая статистика ===")
	t.Logf("Общее время: %v", totalDuration)
	t.Logf("  Фаза 1 (заполнение): %v", phase1Duration)
	t.Logf("  Фаза 2 (хеши): %v", phase2Duration)
	t.Logf("  Фаза 3 (глобальный рут): %v", phase3Duration)
	t.Logf("  Фаза 4 (обновления): %v", phase4Duration)
	t.Logf("  Фаза 5 (удаления): %v", phase5Duration)
	t.Logf("  Фаза 6 (пересчет): %v", phase6Duration)
	t.Logf("  Фаза 7 (новый рут): %v", phase7Duration)

	stats := mgr.GetStats()
	t.Log("\nСтатистика менеджера:")
	t.Logf("  Деревьев: %d", stats.TreeCount)
	t.Logf("  Элементов: %d", stats.TotalItems)
	t.Logf("  Узлов: %d", stats.TotalNodes)
	t.Logf("  Размер кеша: %d", stats.TotalCacheSize)

	// Детальная статистика по удалениям
	t.Log("\nСтатистика удалений по деревьям:")
	for i := 0; i < min(3, numTrees); i++ {
		treeID := fmt.Sprintf("tree_%d", i)
		tree, _ := mgr.GetTree(treeID)
		treeStats := tree.GetStats()
		t.Logf("  %s:", treeID)
		t.Logf("    Элементов: %d", treeStats.TotalItems)
		t.Logf("    Удаленных узлов: %d", treeStats.DeletedNodes)
	}
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
