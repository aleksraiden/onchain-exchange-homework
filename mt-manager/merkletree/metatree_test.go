package merkletree

import (
	"fmt"
	"testing"
)

func TestMetaTreeBasic(t *testing.T) {
	manager := NewManager[*Account](DefaultConfig())

	// Создаем несколько деревьев
	for i := 0; i < 5; i++ {
		treeName := fmt.Sprintf("tree_%d", i)
		tree, _ := manager.CreateTree(treeName)

		// Заполняем
		for j := uint64(0); j < 100; j++ {
			tree.Insert(NewAccountDeterministic(uint64(i*1000+int(j)), StatusUser))
		}
	}

	// Строим мета-дерево
	metaTree := manager.GetMetaTree()

	if metaTree.Root == nil {
		t.Fatal("Корень мета-дерева не должен быть nil")
	}

	if len(metaTree.Leaves) != 5 {
		t.Errorf("Ожидалось 5 листьев, получено %d", len(metaTree.Leaves))
	}

	// Проверяем глобальный корень
	globalRoot := metaTree.GetGlobalRoot()
	var zero [32]byte
	if globalRoot == zero {
		t.Error("Глобальный корень не должен быть нулевым")
	}

	t.Logf("Глобальный корень: %x", globalRoot[:16])
}

func TestMetaTreeProof(t *testing.T) {
	manager := NewManager[*Account](DefaultConfig())

	// Создаем деревья
	treeNames := []string{"users", "admins", "guests"}
	for _, name := range treeNames {
		tree, _ := manager.CreateTree(name)
		for j := uint64(0); j < 50; j++ {
			tree.Insert(NewAccountDeterministic(j, StatusUser))
		}
	}

	// Получаем proof для каждого дерева
	for _, name := range treeNames {
		proof, err := manager.GetTreeProof(name)
		if err != nil {
			t.Fatalf("Не удалось получить proof для %s: %v", name, err)
		}

		t.Logf("\n%s", proof.String())

		// Верифицируем proof
		if !proof.Verify() {
			t.Errorf("Proof для %s не прошел верификацию", name)
		}
	}
}

func TestMetaTreeVerification(t *testing.T) {
	manager := NewManager[*Account](DefaultConfig())

	// Создаем деревья
	for i := 0; i < 10; i++ {
		treeName := fmt.Sprintf("tree_%d", i)
		tree, _ := manager.CreateTree(treeName)

		for j := uint64(0); j < 100; j++ {
			tree.Insert(NewAccountDeterministic(uint64(i*1000+int(j)), StatusUser))
		}
	}

	// Проверяем включение каждого дерева
	for i := 0; i < 10; i++ {
		treeName := fmt.Sprintf("tree_%d", i)

		verified, err := manager.VerifyTreeInclusion(treeName)
		if err != nil {
			t.Fatalf("Ошибка верификации %s: %v", treeName, err)
		}

		if !verified {
			t.Errorf("Дерево %s не прошло верификацию", treeName)
		}
	}

	// Проверяем несуществующее дерево
	_, err := manager.VerifyTreeInclusion("nonexistent")
	if err == nil {
		t.Error("Должна быть ошибка для несуществующего дерева")
	}
}

func TestMetaTreeStructure(t *testing.T) {
	manager := NewManager[*Account](DefaultConfig())

	// Создаем небольшое количество деревьев для визуализации
	for i := 0; i < 4; i++ {
		treeName := fmt.Sprintf("tree_%d", i)
		tree, _ := manager.CreateTree(treeName)

		for j := uint64(0); j < 10; j++ {
			tree.Insert(NewAccountDeterministic(uint64(i*100+int(j)), StatusUser))
		}
	}

	metaTree := manager.GetMetaTree()
	structure := metaTree.GetTreeStructure()

	t.Logf("\n%s", structure)

	stats := metaTree.GetStats()
	t.Logf("\nСтатистика мета-дерева:")
	t.Logf("  Всего узлов: %d", stats.TotalNodes)
	t.Logf("  Листьев: %d", stats.LeafNodes)
	t.Logf("  Высота: %d", stats.Height)
}

func TestMetaTreeAfterUpdates(t *testing.T) {
	manager := NewManager[*Account](DefaultConfig())

	// Создаем деревья
	for i := 0; i < 3; i++ {
		treeName := fmt.Sprintf("tree_%d", i)
		tree, _ := manager.CreateTree(treeName)

		for j := uint64(0); j < 100; j++ {
			tree.Insert(NewAccountDeterministic(uint64(i*1000+int(j)), StatusUser))
		}
	}

	// Первое мета-дерево
	metaTree1 := manager.GetMetaTree()
	root1 := metaTree1.GetGlobalRoot()

	// Обновляем одно из деревьев
	tree, _ := manager.GetTree("tree_1")
	for j := uint64(0); j < 50; j++ {
		tree.Insert(NewAccountDeterministic(uint64(2000+int(j)), StatusMM))
	}

	// Второе мета-дерево после обновлений
	metaTree2 := manager.GetMetaTree()
	root2 := metaTree2.GetGlobalRoot()

	// Корни должны отличаться
	if root1 == root2 {
		t.Error("Глобальный корень должен измениться после обновлений")
	}

	t.Logf("Корень до обновлений:  %x", root1[:16])
	t.Logf("Корень после обновлений: %x", root2[:16])
}
