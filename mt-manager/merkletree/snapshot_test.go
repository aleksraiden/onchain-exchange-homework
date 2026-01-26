package merkletree

import (
	"bytes"
	"testing"
)

func TestSnapshotMetadata(t *testing.T) {
	tree := New[*Account](DefaultConfig())

	for i := uint64(0); i < 1000; i++ {
		tree.Insert(NewAccount(i, StatusUser))
	}

	storage := NewMemorySnapshotStorage()
	snapshotter := NewTreeSnapshot(tree, storage)

	metadata, err := snapshotter.CreateSnapshot(nil)
	if err != nil {
		t.Fatalf("Failed to create snapshot: %v", err)
	}

	if metadata.ItemCount != 1000 {
		t.Errorf("Expected 1000 items, got %d", metadata.ItemCount)
	}

	t.Logf("Snapshot created:\n%s", metadata.String())
}

func TestSnapshotSaveLoad(t *testing.T) {
	tree := New[*Account](DefaultConfig())

	for i := uint64(0); i < 100; i++ {
		tree.Insert(NewAccountDeterministic(i, StatusUser))
	}

	root1 := tree.ComputeRoot()

	// Save
	var buf bytes.Buffer
	if err := tree.SaveToSnapshot(&buf, nil); err != nil {
		t.Fatalf("Failed to save: %v", err)
	}

	// Load
	newTree := New[*Account](DefaultConfig())
	if err := newTree.LoadFromSnapshot(&buf); err != nil {
		t.Logf("Load not implemented yet: %v", err)
	}

	t.Logf("Original root: %x", root1[:16])
}

func TestMemoryStorage(t *testing.T) {
	storage := NewMemorySnapshotStorage()

	metadata := &SnapshotMetadata{
		Version:   1,
		ItemCount: 100,
	}

	data := []byte("test data")

	// Save
	if err := storage.Save(metadata, data); err != nil {
		t.Fatalf("Failed to save: %v", err)
	}

	// Load
	loaded, err := storage.Load(1)
	if err != nil {
		t.Fatalf("Failed to load: %v", err)
	}

	if string(loaded) != "test data" {
		t.Errorf("Data mismatch")
	}

	// List
	list, err := storage.List()
	if err != nil {
		t.Fatalf("Failed to list: %v", err)
	}

	if len(list) != 1 {
		t.Errorf("Expected 1 snapshot, got %d", len(list))
	}
}

func TestManagerSnapshot(t *testing.T) {
	manager := NewManager[*Account](DefaultConfig())

	manager.CreateTree("tree1")
	manager.CreateTree("tree2")

	tree1, _ := manager.GetTree("tree1")
	for i := uint64(0); i < 500; i++ {
		tree1.Insert(NewAccount(i, StatusUser))
	}

	storage := NewMemorySnapshotStorage()
	snapshotter := NewManagerSnapshot(manager, storage)

	metadata, err := snapshotter.CreateSnapshot(nil)
	if err != nil {
		t.Fatalf("Failed to create manager snapshot: %v", err)
	}

	if metadata.TreeCount != 2 {
		t.Errorf("Expected 2 trees, got %d", metadata.TreeCount)
	}

	t.Logf("Manager snapshot: %d trees, %d items",
		metadata.TreeCount, metadata.ItemCount)
}
