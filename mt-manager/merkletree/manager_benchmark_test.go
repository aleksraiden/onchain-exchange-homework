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


// BenchmarkManagerRangeQuery тестирует range-запросы с разными размерами
func BenchmarkManagerRangeQuery(b *testing.B) {
	sizes := []int{10, 25, 50, 100, 1000}
	
	for _, size := range sizes {
		b.Run(fmt.Sprintf("Size_%d", size), func(b *testing.B) {
			benchmarkManagerRangeQuerySize(b, size)
		})
	}
}

func benchmarkManagerRangeQuerySize(b *testing.B, rangeSize int) {
	mgr := NewManager[*Account](LargeConfig())
	
	// Создаем 10 деревьев по 100K элементов в каждом
	numTrees := 10
	itemsPerTree := 100000
	
	for treeIdx := 0; treeIdx < numTrees; treeIdx++ {
		treeName := fmt.Sprintf("tree_%d", treeIdx)
		tree, err := mgr.CreateTree(treeName)
		if err != nil {
			b.Fatalf("Failed to create tree: %v", err)
		}
		
		// Вставляем элементы
		accounts := make([]*Account, itemsPerTree)
		for i := 0; i < itemsPerTree; i++ {
			// Уникальный ID = treeIdx * 1M + i
			id := uint64(treeIdx*1000000 + i)
			accounts[i] = NewAccount(id, StatusUser)
		}
		tree.InsertBatch(accounts)
	}
	
	b.ResetTimer()
	b.ReportAllocs()
	
	// Выполняем range-запросы из случайных деревьев
	for i := 0; i < b.N; i++ {
		treeIdx := i % numTrees
		treeName := fmt.Sprintf("tree_%d", treeIdx)
		tree, _ := mgr.GetTree(treeName)
		
		// Случайная начальная позиция
		startID := uint64(treeIdx*1000000 + (i*13)%itemsPerTree)
		endID := startID + uint64(rangeSize)
		
		result := tree.RangeQueryByID(startID, endID, true, false)
		if len(result) == 0 {
			b.Logf("Empty result for tree %s, range [%d, %d)", treeName, startID, endID)
		}
	}
}

// BenchmarkManagerRangeQueryParallel параллельные range-запросы
func BenchmarkManagerRangeQueryParallel(b *testing.B) {
	sizes := []int{10, 25, 50, 100, 1000}
	
	for _, size := range sizes {
		b.Run(fmt.Sprintf("Size_%d", size), func(b *testing.B) {
			benchmarkManagerRangeQueryParallelSize(b, size)
		})
	}
}

func benchmarkManagerRangeQueryParallelSize(b *testing.B, rangeSize int) {
	mgr := NewManager[*Account](LargeConfig())
	
	numTrees := 10
	itemsPerTree := 100000
	
	for treeIdx := 0; treeIdx < numTrees; treeIdx++ {
		treeName := fmt.Sprintf("tree_%d", treeIdx)
		tree, err := mgr.CreateTree(treeName)
		if err != nil {
			b.Fatalf("Failed to create tree: %v", err)
		}
		
		accounts := make([]*Account, itemsPerTree)
		for i := 0; i < itemsPerTree; i++ {
			id := uint64(treeIdx*1000000 + i)
			accounts[i] = NewAccount(id, StatusUser)
		}
		tree.InsertBatch(accounts)
	}
	
	b.ResetTimer()
	b.ReportAllocs()
	
	// Параллельные запросы из разных деревьев
	b.RunParallel(func(pb *testing.PB) {
		counter := 0
		for pb.Next() {
			treeIdx := counter % numTrees
			treeName := fmt.Sprintf("tree_%d", treeIdx)
			tree, _ := mgr.GetTree(treeName)
			
			startID := uint64(treeIdx*1000000 + (counter*13)%itemsPerTree)
			endID := startID + uint64(rangeSize)
			
			result := tree.RangeQueryByID(startID, endID, true, false)
			_ = result
			counter++
		}
	})
}

// BenchmarkManagerRangeQueryCrossTree range-запросы из разных деревьев последовательно
func BenchmarkManagerRangeQueryCrossTree(b *testing.B) {
	mgr := NewManager[*Account](LargeConfig())
	
	numTrees := 10
	itemsPerTree := 100000
	
	for treeIdx := 0; treeIdx < numTrees; treeIdx++ {
		treeName := fmt.Sprintf("tree_%d", treeIdx)
		tree, err := mgr.CreateTree(treeName)
		if err != nil {
			b.Fatalf("Failed to create tree: %v", err)
		}
		
		accounts := make([]*Account, itemsPerTree)
		for i := 0; i < itemsPerTree; i++ {
			id := uint64(treeIdx*1000000 + i)
			accounts[i] = NewAccount(id, StatusUser)
		}
		tree.InsertBatch(accounts)
	}
	
	rangeSize := 100
	b.ResetTimer()
	b.ReportAllocs()
	
	// Каждая итерация читает из ВСЕХ деревьев
	for i := 0; i < b.N; i++ {
		for treeIdx := 0; treeIdx < numTrees; treeIdx++ {
			treeName := fmt.Sprintf("tree_%d", treeIdx)
			tree, _ := mgr.GetTree(treeName)
			
			startID := uint64(treeIdx*1000000 + (i*13)%itemsPerTree)
			endID := startID + uint64(rangeSize)
			
			result := tree.RangeQueryByID(startID, endID, true, false)
			_ = result
		}
	}
}

// BenchmarkManagerMixedOps смешанные операции: вставки + range-запросы
func BenchmarkManagerMixedOps(b *testing.B) {
	mgr := NewManager[*Account](LargeConfig())
	
	numTrees := 10
	itemsPerTree := 50000 // Меньше, т.к. будем добавлять
	
	for treeIdx := 0; treeIdx < numTrees; treeIdx++ {
		treeName := fmt.Sprintf("tree_%d", treeIdx)
		tree, err := mgr.CreateTree(treeName)
		if err != nil {
			b.Fatalf("Failed to create tree: %v", err)
		}
		
		accounts := make([]*Account, itemsPerTree)
		for i := 0; i < itemsPerTree; i++ {
			id := uint64(treeIdx*1000000 + i)
			accounts[i] = NewAccount(id, StatusUser)
		}
		tree.InsertBatch(accounts)
	}
	
	b.ResetTimer()
	b.ReportAllocs()
	
	// 80% range-запросов, 20% вставок
	for i := 0; i < b.N; i++ {
		treeIdx := i % numTrees
		treeName := fmt.Sprintf("tree_%d", treeIdx)
		tree, _ := mgr.GetTree(treeName)
		
		if i%5 == 0 {
			// 20% - вставка нового элемента
			newID := uint64(treeIdx*1000000 + itemsPerTree + i)
			acc := NewAccount(newID, StatusUser)
			tree.Insert(acc)
		} else {
			// 80% - range-запрос
			startID := uint64(treeIdx*1000000 + (i*13)%itemsPerTree)
			endID := startID + 50
			
			result := tree.RangeQueryByID(startID, endID, true, false)
			_ = result
		}
	}
}

// BenchmarkManagerRangeQueryDifferentDepths range-запросы на разных глубинах дерева
func BenchmarkManagerRangeQueryDifferentDepths(b *testing.B) {
	configs := []struct {
		name   string
		config *Config
	}{
		{"Depth3_Small", SmallConfig()},
		{"Depth3_Medium", MediumConfig()},
		{"Depth4_Large", LargeConfig()},
		{"Depth5_Huge", HugeConfig()},
	}
	
	for _, cfg := range configs {
		b.Run(cfg.name, func(b *testing.B) {
			mgr := NewManager[*Account](cfg.config)
			
			tree, err := mgr.CreateTree("test")
			if err != nil {
				b.Fatalf("Failed to create tree: %v", err)
			}
			
			// Вставляем 100K элементов
			accounts := make([]*Account, 100000)
			for i := 0; i < 100000; i++ {
				accounts[i] = NewAccount(uint64(i), StatusUser)
			}
			tree.InsertBatch(accounts)
			
			rangeSize := 100
			b.ResetTimer()
			b.ReportAllocs()
			
			for i := 0; i < b.N; i++ {
				startID := uint64((i * 13) % 90000)
				endID := startID + uint64(rangeSize)
				
				result := tree.RangeQueryByID(startID, endID, true, false)
				if len(result) == 0 {
					b.Logf("Empty result for range [%d, %d)", startID, endID)
				}
			}
		})
	}
}

// BenchmarkManagerRangeQuerySequentialVsRandom последовательные vs случайные диапазоны
func BenchmarkManagerRangeQuerySequentialVsRandom(b *testing.B) {
	b.Run("Sequential", func(b *testing.B) {
		mgr := NewManager[*Account](LargeConfig())
		
		tree, _ := mgr.CreateTree("test")
		
		accounts := make([]*Account, 100000)
		for i := 0; i < 100000; i++ {
			accounts[i] = NewAccount(uint64(i), StatusUser)
		}
		tree.InsertBatch(accounts)
		
		rangeSize := 100
		b.ResetTimer()
		
		for i := 0; i < b.N; i++ {
			startID := uint64((i * rangeSize) % 90000)
			endID := startID + uint64(rangeSize)
			result := tree.RangeQueryByID(startID, endID, true, false)
			_ = result
		}
	})
	
	b.Run("Random", func(b *testing.B) {
		mgr := NewManager[*Account](LargeConfig())
		
		tree, _ := mgr.CreateTree("test")
		
		accounts := make([]*Account, 100000)
		for i := 0; i < 100000; i++ {
			accounts[i] = NewAccount(uint64(i), StatusUser)
		}
		tree.InsertBatch(accounts)
		
		rangeSize := 100
		b.ResetTimer()
		
		for i := 0; i < b.N; i++ {
			// Случайная позиция с помощью простого PRNG
			startID := uint64((i*13 + i*i*7) % 90000)
			endID := startID + uint64(rangeSize)
			result := tree.RangeQueryByID(startID, endID, true, false)
			_ = result
		}
	})
}


// ========== БЫСТРЫЕ БЕНЧМАРКИ ==========

// BenchmarkManagerRangeQueryQuick быстрые range-запросы (малые деревья)
func BenchmarkManagerRangeQueryQuick(b *testing.B) {
	sizes := []int{10, 25, 50, 100}
	
	for _, size := range sizes {
		b.Run(fmt.Sprintf("Size_%d", size), func(b *testing.B) {
			benchmarkManagerRangeQueryQuickSize(b, size)
		})
	}
}

func benchmarkManagerRangeQueryQuickSize(b *testing.B, rangeSize int) {
	mgr := NewManager[*Account](SmallConfig())
	
	// Только 3 дерева по 10K элементов
	numTrees := 3
	itemsPerTree := 10000
	
	for treeIdx := 0; treeIdx < numTrees; treeIdx++ {
		treeName := fmt.Sprintf("tree_%d", treeIdx)
		tree, err := mgr.CreateTree(treeName)
		if err != nil {
			b.Fatalf("Failed to create tree: %v", err)
		}
		
		accounts := make([]*Account, itemsPerTree)
		for i := 0; i < itemsPerTree; i++ {
			id := uint64(treeIdx*100000 + i)
			accounts[i] = NewAccount(id, StatusUser)
		}
		tree.InsertBatch(accounts)
	}
	
	b.ResetTimer()
	b.ReportAllocs()
	
	for i := 0; i < b.N; i++ {
		treeIdx := i % numTrees
		treeName := fmt.Sprintf("tree_%d", treeIdx)
		tree, _ := mgr.GetTree(treeName)
		
		startID := uint64(treeIdx*100000 + (i*13)%itemsPerTree)
		endID := startID + uint64(rangeSize)
		
		result := tree.RangeQueryByID(startID, endID, true, false)
		_ = result
	}
}

// BenchmarkManagerRangeQueryParallelQuick быстрый параллельный бенчмарк
func BenchmarkManagerRangeQueryParallelQuick(b *testing.B) {
	mgr := NewManager[*Account](SmallConfig())
	
	numTrees := 3
	itemsPerTree := 10000
	
	for treeIdx := 0; treeIdx < numTrees; treeIdx++ {
		treeName := fmt.Sprintf("tree_%d", treeIdx)
		tree, _ := mgr.CreateTree(treeName)
		
		accounts := make([]*Account, itemsPerTree)
		for i := 0; i < itemsPerTree; i++ {
			id := uint64(treeIdx*100000 + i)
			accounts[i] = NewAccount(id, StatusUser)
		}
		tree.InsertBatch(accounts)
	}
	
	rangeSize := 50
	b.ResetTimer()
	b.ReportAllocs()
	
	b.RunParallel(func(pb *testing.PB) {
		counter := 0
		for pb.Next() {
			treeIdx := counter % numTrees
			treeName := fmt.Sprintf("tree_%d", treeIdx)
			tree, _ := mgr.GetTree(treeName)
			
			startID := uint64(treeIdx*100000 + (counter*13)%itemsPerTree)
			endID := startID + uint64(rangeSize)
			
			result := tree.RangeQueryByID(startID, endID, true, false)
			_ = result
			counter++
		}
	})
}

// BenchmarkManagerRangeQuerySingleTree быстрый бенчмарк для одного дерева
func BenchmarkManagerRangeQuerySingleTree(b *testing.B) {
	sizes := []int{10, 50, 100, 500}
	
	for _, size := range sizes {
		b.Run(fmt.Sprintf("Size_%d", size), func(b *testing.B) {
			mgr := NewManager[*Account](SmallConfig())
			tree, _ := mgr.CreateTree("test")
			
			// Только 10K элементов
			accounts := make([]*Account, 10000)
			for i := 0; i < 10000; i++ {
				accounts[i] = NewAccount(uint64(i), StatusUser)
			}
			tree.InsertBatch(accounts)
			
			b.ResetTimer()
			b.ReportAllocs()
			
			for i := 0; i < b.N; i++ {
				startID := uint64((i * 13) % 9000)
				endID := startID + uint64(size)
				result := tree.RangeQueryByID(startID, endID, true, false)
				_ = result
			}
		})
	}
}

// BenchmarkManagerRangeQueryTiny совсем маленький бенчмарк для быстрой проверки
func BenchmarkManagerRangeQueryTiny(b *testing.B) {
	mgr := NewManager[*Account](SmallConfig())
	tree, _ := mgr.CreateTree("tiny")
	
	// Только 1000 элементов!
	accounts := make([]*Account, 1000)
	for i := 0; i < 1000; i++ {
		accounts[i] = NewAccount(uint64(i), StatusUser)
	}
	tree.InsertBatch(accounts)
	
	b.ResetTimer()
	b.ReportAllocs()
	
	for i := 0; i < b.N; i++ {
		startID := uint64((i * 13) % 900)
		endID := startID + 50
		result := tree.RangeQueryByID(startID, endID, true, false)
		_ = result
	}
}

// BenchmarkManagerRangeQueryCompare сравнение последовательного и параллельного
func BenchmarkManagerRangeQueryCompare(b *testing.B) {
	mgr := NewManager[*Account](MediumConfig())
	tree, _ := mgr.CreateTree("compare")
	
	// 50K элементов - средний размер
	accounts := make([]*Account, 50000)
	for i := 0; i < 50000; i++ {
		accounts[i] = NewAccount(uint64(i), StatusUser)
	}
	tree.InsertBatch(accounts)
	
	b.Run("Sequential_Small", func(b *testing.B) {
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			startID := uint64((i * 13) % 40000)
			result := tree.RangeQueryByID(startID, startID+50, true, false)
			_ = result
		}
	})
	
	b.Run("Sequential_Large", func(b *testing.B) {
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			startID := uint64((i * 13) % 30000)
			result := tree.RangeQueryByID(startID, startID+1000, true, false)
			_ = result
		}
	})
	
	b.Run("Parallel_Small", func(b *testing.B) {
		b.ResetTimer()
		b.RunParallel(func(pb *testing.PB) {
			counter := 0
			for pb.Next() {
				startID := uint64((counter * 13) % 40000)
				result := tree.RangeQueryByID(startID, startID+50, true, false)
				_ = result
				counter++
			}
		})
	})
	
	b.Run("Parallel_Large", func(b *testing.B) {
		b.ResetTimer()
		b.RunParallel(func(pb *testing.PB) {
			counter := 0
			for pb.Next() {
				startID := uint64((counter * 13) % 30000)
				result := tree.RangeQueryByID(startID, startID+1000, true, false)
				_ = result
				counter++
			}
		})
	})
}

// BenchmarkManagerRangeQueryCacheBehavior проверка поведения кеша
func BenchmarkManagerRangeQueryCacheBehavior(b *testing.B) {
	b.Run("HotRange", func(b *testing.B) {
		// Один и тот же диапазон - должен быть в кеше
		mgr := NewManager[*Account](MediumConfig())
		tree, _ := mgr.CreateTree("hot")
		
		accounts := make([]*Account, 10000)
		for i := 0; i < 10000; i++ {
			accounts[i] = NewAccount(uint64(i), StatusUser)
		}
		tree.InsertBatch(accounts)
		
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			// Всегда один и тот же диапазон
			result := tree.RangeQueryByID(1000, 1100, true, false)
			_ = result
		}
	})
	
	b.Run("ColdRange", func(b *testing.B) {
		// Каждый раз новый диапазон - кеш не помогает
		mgr := NewManager[*Account](MediumConfig())
		tree, _ := mgr.CreateTree("cold")
		
		accounts := make([]*Account, 10000)
		for i := 0; i < 10000; i++ {
			accounts[i] = NewAccount(uint64(i), StatusUser)
		}
		tree.InsertBatch(accounts)
		
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			// Каждый раз новый диапазон
			startID := uint64((i * 100) % 9000)
			result := tree.RangeQueryByID(startID, startID+100, true, false)
			_ = result
		}
	})
}


