package merkletree

import (
	"fmt"
	"sync"
	"github.com/zeebo/blake3"
)

// TreeManager управляет несколькими Merkle-деревьями
// и вычисляет общий корневой хеш из всех деревьев
type TreeManager[T Hashable] struct {
	trees  map[string]*Tree[T]
	config *Config
	mu     sync.RWMutex
}

// NewManager создает новый менеджер деревьев
func NewManager[T Hashable](cfg *Config) *TreeManager[T] {
	if cfg == nil {
		cfg = DefaultConfig()
	}

	return &TreeManager[T]{
		trees:  make(map[string]*Tree[T]),
		config: cfg,
	}
}

// CreateTree создает новое дерево с указанным именем
func (m *TreeManager[T]) CreateTree(name string) (*Tree[T], error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if _, exists := m.trees[name]; exists {
		return nil, fmt.Errorf("дерево '%s' уже существует", name)
	}

	tree := New[T](m.config)
	m.trees[name] = tree
	return tree, nil
}

// GetTree возвращает дерево по имени
func (m *TreeManager[T]) GetTree(name string) (*Tree[T], bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	tree, exists := m.trees[name]
	return tree, exists
}

// GetOrCreateTree получает существующее дерево или создает новое
func (m *TreeManager[T]) GetOrCreateTree(name string) *Tree[T] {
	m.mu.Lock()
	defer m.mu.Unlock()

	if tree, exists := m.trees[name]; exists {
		return tree
	}

	tree := New[T](m.config)
	m.trees[name] = tree
	return tree
}

// RemoveTree удаляет дерево
func (m *TreeManager[T]) RemoveTree(name string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()

	if _, exists := m.trees[name]; exists {
		delete(m.trees, name)
		return true
	}
	return false
}

// ListTrees возвращает список имен всех деревьев
func (m *TreeManager[T]) ListTrees() []string {
	m.mu.RLock()
	defer m.mu.RUnlock()

	names := make([]string, 0, len(m.trees))
	for name := range m.trees {
		names = append(names, name)
	}
	return names
}

// ComputeGlobalRoot вычисляет общий корневой хеш из всех деревьев
// Деревья хешируются в лексикографическом порядке их имен для детерминизма
func (m *TreeManager[T]) ComputeGlobalRoot() [32]byte {
	m.mu.RLock()
	defer m.mu.RUnlock()

	if len(m.trees) == 0 {
		return [32]byte{}
	}

	// Получаем отсортированные имена деревьев
	names := make([]string, 0, len(m.trees))
	for name := range m.trees {
		names = append(names, name)
	}

	// Сортируем имена для детерминизма
	for i := 0; i < len(names)-1; i++ {
		for j := i + 1; j < len(names); j++ {
			if names[i] > names[j] {
				names[i], names[j] = names[j], names[i]
			}
		}
	}

	// Хешируем корни всех деревьев
	hasher := blake3.New()
	for _, name := range names {
		tree := m.trees[name]
		treeRoot := tree.ComputeRoot()

		// Включаем имя дерева в хеш для уникальности
		hasher.Write([]byte(name))
		hasher.Write(treeRoot[:])
	}

	var result [32]byte
	copy(result[:], hasher.Sum(nil))
	return result
}

// ComputeAllRoots вычисляет корневые хеши всех деревьев параллельно
// Возвращает map[имя_дерева]корневой_хеш
func (m *TreeManager[T]) ComputeAllRoots() map[string][32]byte {
	m.mu.RLock()
	defer m.mu.RUnlock()

	results := make(map[string][32]byte, len(m.trees))
	var wg sync.WaitGroup
	var resultMu sync.Mutex

	for name, tree := range m.trees {
		wg.Add(1)
		go func(n string, t *Tree[T]) {
			defer wg.Done()
			root := t.ComputeRoot()

			resultMu.Lock()
			results[n] = root
			resultMu.Unlock()
		}(name, tree)
	}

	wg.Wait()
	return results
}

// GetTotalStats возвращает суммарную статистику по всем деревьям
func (m *TreeManager[T]) GetTotalStats() ManagerStats {
	m.mu.RLock()
	defer m.mu.RUnlock()

	stats := ManagerStats{
		TreeCount: len(m.trees),
		TreeStats: make(map[string]Stats),
	}

	for name, tree := range m.trees {
		treeStats := tree.GetStats()
		stats.TreeStats[name] = treeStats
		stats.TotalItems += treeStats.TotalItems
		stats.TotalNodes += treeStats.AllocatedNodes
		stats.TotalCacheSize += treeStats.CacheSize
	}

	return stats
}

// ManagerStats содержит статистику менеджера
type ManagerStats struct {
	TreeCount      int              // Количество деревьев
	TotalItems     int              // Всего элементов во всех деревьях
	TotalNodes     int              // Всего узлов
	TotalCacheSize int              // Общий размер кешей
	TreeStats      map[string]Stats // Статистика по каждому дереву
}

// InsertToTree вставляет элемент в указанное дерево (helper метод)
func (m *TreeManager[T]) InsertToTree(treeName string, item T) error {
	tree, exists := m.GetTree(treeName)
	if !exists {
		return fmt.Errorf("дерево '%s' не найдено", treeName)
	}

	tree.Insert(item)
	return nil
}

// BatchInsertToTree вставляет пакет элементов в указанное дерево
func (m *TreeManager[T]) BatchInsertToTree(treeName string, items []T) error {
	tree, exists := m.GetTree(treeName)
	if !exists {
		return fmt.Errorf("дерево '%s' не найдено", treeName)
	}

	tree.InsertBatch(items)
	return nil
}

// ClearAll очищает все деревья
func (m *TreeManager[T]) ClearAll() {
	m.mu.Lock()
	defer m.mu.Unlock()

	for _, tree := range m.trees {
		tree.Clear()
	}
}

// RemoveAll удаляет все деревья
func (m *TreeManager[T]) RemoveAll() {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.trees = make(map[string]*Tree[T])
}

// GetMetaTree возвращает мета-дерево из корней всех деревьев
func (m *TreeManager[T]) GetMetaTree() *MetaTree {
	return m.BuildMetaTree()
}

// GetTreeProof возвращает Merkle proof для конкретного дерева
func (m *TreeManager[T]) GetTreeProof(treeName string) (*MerkleProof, error) {
	metaTree := m.BuildMetaTree()
	return metaTree.GetProof(treeName)
}

// VerifyTreeInclusion проверяет, что дерево включено в глобальный корень
func (m *TreeManager[T]) VerifyTreeInclusion(treeName string) (bool, error) {
	proof, err := m.GetTreeProof(treeName)
	if err != nil {
		return false, err
	}

	return proof.Verify(), nil
}

// RenameTree переименовывает дерево
func (m *TreeManager[T]) RenameTree(oldName, newName string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	tree, exists := m.trees[oldName]
	if !exists {
		return fmt.Errorf("дерево '%s' не найдено", oldName)
	}

	if _, exists := m.trees[newName]; exists {
		return fmt.Errorf("дерево '%s' уже существует", newName)
	}

	m.trees[newName] = tree
	delete(m.trees, oldName)

	return nil
}

// TreeExists проверяет существование дерева
func (m *TreeManager[T]) TreeExists(name string) bool {
	m.mu.RLock()
	defer m.mu.RUnlock()

	_, exists := m.trees[name]
	return exists
}

// GetTreeSize возвращает размер дерева по имени
func (m *TreeManager[T]) GetTreeSize(name string) (int, error) {
	tree, exists := m.GetTree(name)
	if !exists {
		return 0, fmt.Errorf("дерево '%s' не найдено", name)
	}

	return tree.Size(), nil
}

// GetTreeStats возвращает статистику конкретного дерева
func (m *TreeManager[T]) GetTreeStats(name string) (Stats, error) {
	tree, exists := m.GetTree(name)
	if !exists {
		return Stats{}, fmt.Errorf("дерево '%s' не найдено", name)
	}

	return tree.GetStats(), nil
}

// GetTreeRoot возвращает корень конкретного дерева
func (m *TreeManager[T]) GetTreeRoot(name string) ([32]byte, error) {
	tree, exists := m.GetTree(name)
	if !exists {
		return [32]byte{}, fmt.Errorf("дерево '%s' не найдено", name)
	}

	return tree.ComputeRoot(), nil
}

// GetFromTree получает элемент из конкретного дерева
func (m *TreeManager[T]) GetFromTree(treeName string, id uint64) (T, error) {
	tree, exists := m.GetTree(treeName)
	if !exists {
		var zero T
		return zero, fmt.Errorf("дерево '%s' не найдено", treeName)
	}

	item, ok := tree.Get(id)
	if !ok {
		var zero T
		return zero, fmt.Errorf("элемент %d не найден в дереве '%s'", id, treeName)
	}

	return item, nil
}

// ClearTree очищает конкретное дерево
func (m *TreeManager[T]) ClearTree(name string) error {
	tree, exists := m.GetTree(name)
	if !exists {
		return fmt.Errorf("дерево '%s' не найдено", name)
	}

	tree.Clear()
	return nil
}

// CloneTree создает копию дерева с новым именем
func (m *TreeManager[T]) CloneTree(sourceName, newName string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	sourceTree, exists := m.trees[sourceName]
	if !exists {
		return fmt.Errorf("дерево '%s' не найдено", sourceName)
	}

	if _, exists := m.trees[newName]; exists {
		return fmt.Errorf("дерево '%s' уже существует", newName)
	}

	// Создаем новое дерево
	newTree := New[T](m.config)

	// Копируем все элементы
	items := sourceTree.GetAllItems()
	newTree.InsertBatch(items)

	m.trees[newName] = newTree

	return nil
}

// GetTreeInfo возвращает детальную информацию о дереве
func (m *TreeManager[T]) GetTreeInfo(name string) (TreeInfo, error) {
	tree, exists := m.GetTree(name)
	if !exists {
		return TreeInfo{}, fmt.Errorf("дерево '%s' не найдено", name)
	}

	stats := tree.GetStats()
	root := tree.ComputeRoot()

	return TreeInfo{
		Name:      name,
		Size:      stats.TotalItems,
		Nodes:     stats.AllocatedNodes,
		CacheSize: stats.CacheSize,
		Root:      root,
	}, nil
}

// TreeInfo информация о дереве
type TreeInfo struct {
	Name      string
	Size      int
	Nodes     int
	CacheSize int
	Root      [32]byte
}

// String форматированный вывод информации о дереве
func (ti TreeInfo) String() string {
	return fmt.Sprintf("Дерево '%s':\n  Элементов: %d\n  Узлов: %d\n  Кеш: %d\n  Корень: %x",
		ti.Name, ti.Size, ti.Nodes, ti.CacheSize, ti.Root[:16])
}

// GetAllTreesInfo возвращает информацию обо всех деревьях
func (m *TreeManager[T]) GetAllTreesInfo() []TreeInfo {
	names := m.ListTrees()
	infos := make([]TreeInfo, 0, len(names))

	for _, name := range names {
		if info, err := m.GetTreeInfo(name); err == nil {
			infos = append(infos, info)
		}
	}

	return infos
}

// CreateTreeWithConfig создает дерево с кастомной конфигурацией
func (m *TreeManager[T]) CreateTreeWithConfig(name string, cfg *Config) (*Tree[T], error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if _, exists := m.trees[name]; exists {
		return nil, fmt.Errorf("дерево '%s' уже существует", name)
	}

	tree := New[T](cfg)
	m.trees[name] = tree
	return tree, nil
}

// SetDefaultConfig устанавливает конфигурацию по умолчанию для новых деревьев
func (m *TreeManager[T]) SetDefaultConfig(cfg *Config) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.config = cfg
}

// GetStats возвращает общую статистику менеджера
func (tm *TreeManager[T]) GetStats() ManagerStats {
	tm.mu.RLock()
	defer tm.mu.RUnlock()

	stats := ManagerStats{
		TreeCount: len(tm.trees),
	}

	for _, tree := range tm.trees {
		treeStats := tree.GetStats()
		stats.TotalItems += treeStats.TotalItems
		stats.TotalNodes += treeStats.AllocatedNodes
		stats.TotalCacheSize += treeStats.CacheSize
	}

	return stats
}