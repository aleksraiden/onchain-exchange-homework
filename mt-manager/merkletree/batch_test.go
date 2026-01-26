package merkletree

import (
	"testing"
)

func TestTreeBatch(t *testing.T) {
	tree := New[*Account](DefaultConfig())

	// Начинаем батч
	batch := tree.BeginBatch()

	if !batch.IsActive() {
		t.Error("Батч должен быть активен")
	}

	// Добавляем элементы
	for i := uint64(0); i < 1000; i++ {
		batch.Insert(NewAccount(i, StatusUser))
	}

	if batch.Size() != 1000 {
		t.Errorf("Размер батча должен быть 1000, получено %d", batch.Size())
	}

	// Коммитим
	err := batch.Commit()
	if err != nil {
		t.Fatalf("Ошибка коммита: %v", err)
	}

	if tree.Size() != 1000 {
		t.Errorf("Размер дерева должен быть 1000, получено %d", tree.Size())
	}

	if batch.IsActive() {
		t.Error("Батч не должен быть активен после коммита")
	}
}

func TestTreeBatchRollback(t *testing.T) {
	tree := New[*Account](DefaultConfig())

	batch := tree.BeginBatch()

	for i := uint64(0); i < 500; i++ {
		batch.Insert(NewAccount(i, StatusUser))
	}

	// Rollback
	batch.Rollback()

	if tree.Size() != 0 {
		t.Errorf("Дерево должно быть пустым после rollback, получено %d", tree.Size())
	}
}

func TestManagerBatch(t *testing.T) {
	manager := NewManager[*Account](DefaultConfig())

	manager.CreateTree("tree1")
	manager.CreateTree("tree2")

	batch := manager.BeginBatch()

	// Добавляем в разные деревья
	for i := uint64(0); i < 100; i++ {
		batch.InsertToTree("tree1", NewAccount(i, StatusUser))
		batch.InsertToTree("tree2", NewAccount(i+1000, StatusMM))
	}

	stats := batch.GetStats()
	if stats.TotalItems != 200 {
		t.Errorf("Ожидалось 200 элементов, получено %d", stats.TotalItems)
	}

	if stats.AffectedTrees != 2 {
		t.Errorf("Ожидалось 2 дерева, получено %d", stats.AffectedTrees)
	}

	// Коммит
	err := batch.Commit()
	if err != nil {
		t.Fatalf("Ошибка коммита: %v", err)
	}

	size1, _ := manager.GetTreeSize("tree1")
	size2, _ := manager.GetTreeSize("tree2")

	if size1 != 100 {
		t.Errorf("tree1 должно содержать 100, получено %d", size1)
	}

	if size2 != 100 {
		t.Errorf("tree2 должно содержать 100, получено %d", size2)
	}
}

func TestManagerBatchWithRoots(t *testing.T) {
	manager := NewManager[*Account](DefaultConfig())

	manager.CreateTree("test1")
	manager.CreateTree("test2")

	batch := manager.BeginBatch()

	for i := uint64(0); i < 50; i++ {
		batch.InsertToTree("test1", NewAccountDeterministic(i, StatusUser))
		batch.InsertToTree("test2", NewAccountDeterministic(i+100, StatusMM))
	}

	roots, err := batch.CommitWithRoots()
	if err != nil {
		t.Fatalf("Ошибка: %v", err)
	}

	if len(roots) != 2 {
		t.Errorf("Ожидалось 2 корня, получено %d", len(roots))
	}

	_, ok1 := roots["test1"]
	_, ok2 := roots["test2"]

	if !ok1 || !ok2 {
		t.Error("Должны быть корни для обоих деревьев")
	}
}

func BenchmarkTreeBatchVsRegular(b *testing.B) {
	b.Run("Regular", func(b *testing.B) {
		tree := New[*Account](DefaultConfig())

		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			tree.Insert(NewAccount(uint64(i), StatusUser))
		}
	})

	b.Run("Batch", func(b *testing.B) {
		tree := New[*Account](DefaultConfig())
		batch := tree.BeginBatch()

		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			batch.Insert(NewAccount(uint64(i), StatusUser))
		}
		batch.Commit()
	})
}

func BenchmarkManagerBatch(b *testing.B) {
	manager := NewManager[*Account](DefaultConfig())
	manager.CreateTree("bench")

	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		batch := manager.BeginBatch()

		for j := 0; j < 1000; j++ {
			batch.InsertToTree("bench", NewAccount(uint64(i*1000+j), StatusUser))
		}

		batch.Commit()
	}
}
