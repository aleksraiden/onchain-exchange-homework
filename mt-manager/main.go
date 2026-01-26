package main

import (
	"fmt"
	"mt-manager/merkletree"
	"runtime"
	"time"
)

func main() {
	runtime.GOMAXPROCS(runtime.NumCPU())
	
	fmt.Println("=== Batch/Commit интерфейс ===\n")
	
	cfg := merkletree.DefaultConfig()
	manager := merkletree.NewManager[*merkletree.Account](cfg)
	
	// Создаем деревья
	manager.CreateTree("users")
	manager.CreateTree("orders")
	manager.CreateTree("transactions")
	
	// === ПРИМЕР 1: Батч на уровне дерева ===
	fmt.Println("1. Батч на уровне дерева:")
	
	userTree, _ := manager.GetTree("users")
	
	// Начинаем батч
	treeBatch := userTree.BeginBatch()
	
	// Добавляем много элементов без пересчета хешей
	fmt.Println("   Добавление 100K элементов в батч...")
	start := time.Now()
	
	for i := uint64(0); i < 100_000; i++ {
		treeBatch.Insert(merkletree.NewAccount(i, merkletree.StatusUser))
	}
	
	fmt.Printf("   Буферизация: %v (размер батча: %d)\n", 
		time.Since(start), treeBatch.Size())
	
	// Применяем батч (здесь происходит вставка и пересчет)
	fmt.Println("   Коммит батча...")
	commitStart := time.Now()
	treeBatch.Commit()
	fmt.Printf("   Коммит: %v\n", time.Since(commitStart))
	fmt.Printf("   Размер дерева: %d\n", userTree.Size())
	
	// === ПРИМЕР 2: Батч на уровне менеджера ===
	fmt.Println("\n2. Батч на уровне менеджера:")
	
	// Начинаем батч менеджера
	managerBatch := manager.BeginBatch()
	
	fmt.Println("   Добавление в несколько деревьев...")
	start = time.Now()
	
	// Добавляем в разные деревья
	for i := uint64(100_000); i < 150_000; i++ {
		managerBatch.InsertToTree("users", 
			merkletree.NewAccount(i, merkletree.StatusUser))
	}
	
	for i := uint64(0); i < 10_000; i++ {
		managerBatch.InsertToTree("orders", 
			merkletree.NewAccount(i, merkletree.StatusMM))
	}
	
	for i := uint64(0); i < 5_000; i++ {
		managerBatch.InsertToTree("transactions", 
			merkletree.NewAccount(i, merkletree.StatusAlgo))
	}
	
	fmt.Printf("   Буферизация: %v\n", time.Since(start))
	
	// Статистика батча
	stats := managerBatch.GetStats()
	fmt.Printf("   Всего в батче: %d элементов\n", stats.TotalItems)
	fmt.Printf("   Затронуто деревьев: %d\n", stats.AffectedTrees)
	for tree, size := range stats.TreeSizes {
		fmt.Printf("     - %s: %d элементов\n", tree, size)
	}
	
	// Коммит с получением корней
	fmt.Println("\n   Коммит батча менеджера...")
	commitStart = time.Now()
	roots, err := managerBatch.CommitWithRoots()
	if err != nil {
		fmt.Printf("   Ошибка: %v\n", err)
	} else {
		fmt.Printf("   Коммит: %v\n", time.Since(commitStart))
		fmt.Println("   Новые корни деревьев:")
		for name, root := range roots {
			fmt.Printf("     %s: %x\n", name, root[:12])
		}
	}
	
	// === ПРИМЕР 3: Батч с глобальным корнем ===
	fmt.Println("\n3. Батч с вычислением глобального корня:")
	
	batch2 := manager.BeginBatch()
	
	// Добавляем обновления
	for i := uint64(200_000); i < 250_000; i++ {
		batch2.InsertToTree("users", 
			merkletree.NewAccount(i, merkletree.StatusUser))
	}
	
	fmt.Printf("   Размер батча: %d элементов\n", batch2.GetTotalBatchSize())
	
	// Коммит с глобальным корнем
	start = time.Now()
	globalRoot, err := batch2.CommitWithGlobalRoot()
	if err != nil {
		fmt.Printf("   Ошибка: %v\n", err)
	} else {
		fmt.Printf("   Время коммита + глобальный корень: %v\n", time.Since(start))
		fmt.Printf("   Глобальный корень: %x\n", globalRoot[:16])
	}
	
	// === ПРИМЕР 4: Rollback ===
	fmt.Println("\n4. Rollback батча:")
	
	batch3 := manager.BeginBatch()
	
	// Добавляем данные
	for i := uint64(0); i < 10_000; i++ {
		batch3.InsertToTree("users", 
			merkletree.NewAccount(uint64(1_000_000+i), merkletree.StatusBlocked))
	}
	
	sizeBefore, _ := manager.GetTreeSize("users")
	fmt.Printf("   Размер 'users' до rollback: %d\n", sizeBefore)
	fmt.Printf("   Элементов в батче: %d\n", batch3.GetTotalBatchSize())
	
	// Отменяем изменения
	batch3.Rollback()
	fmt.Println("   Rollback выполнен")
	
	sizeAfter, _ := manager.GetTreeSize("users")
	fmt.Printf("   Размер 'users' после rollback: %d\n", sizeAfter)
	
	// === ПРИМЕР 5: InsertMany ===
	fmt.Println("\n5. Batch InsertMany:")
	
	batch4 := manager.BeginBatch()
	
	// Подготовим большой пакет
	accounts := make([]*merkletree.Account, 50_000)
	for i := range accounts {
		accounts[i] = merkletree.NewAccount(uint64(2_000_000+i), merkletree.StatusUser)
	}
	
	start = time.Now()
	batch4.InsertManyToTree("users", accounts)
	fmt.Printf("   InsertMany 50K: %v\n", time.Since(start))
	
	start = time.Now()
	batch4.Commit()
	fmt.Printf("   Commit: %v\n", time.Since(start))
	
	// Итоговая статистика
	fmt.Println("\n6. Итоговая статистика:")
	totalStats := manager.GetTotalStats()
	fmt.Printf("   Всего элементов: %d\n", totalStats.TotalItems)
	
	for _, info := range manager.GetAllTreesInfo() {
		fmt.Printf("   %s: %d элементов\n", info.Name, info.Size)
	}
	
	var m runtime.MemStats
	runtime.ReadMemStats(&m)
	fmt.Printf("\n   Heap: %d MB\n", m.HeapAlloc/1024/1024)
}
