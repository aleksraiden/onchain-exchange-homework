package merkletree

import (
	"fmt"
	"sort"

	"github.com/zeebo/blake3"
)

// MetaNode представляет узел в мета-дереве
type MetaNode struct {
	Hash     [32]byte  // Хеш узла
	TreeName string    // Имя дерева (для листьев)
	TreeRoot [32]byte  // Корень дерева (для листьев)
	Left     *MetaNode // Левый потомок
	Right    *MetaNode // Правый потомок
	IsLeaf   bool      // Флаг листа
}

// MetaTree представляет мета-дерево из корней других деревьев
type MetaTree struct {
	Root    *MetaNode      // Корень мета-дерева
	Leaves  []*MetaNode    // Листья (корни деревьев)
	TreeMap map[string]int // Карта имя -> индекс листа
}

// MerkleProof представляет доказательство включения дерева
type MerkleProof struct {
	TreeName   string     // Имя дерева
	TreeRoot   [32]byte   // Корень дерева
	ProofPath  [][32]byte // Путь хешей для верификации
	IsLeft     []bool     // Флаги позиции (true = текущий узел слева)
	GlobalRoot [32]byte   // Глобальный корень для верификации
}

// BuildMetaTree строит мета-дерево из корней всех деревьев менеджера
func (m *TreeManager[T]) BuildMetaTree() *MetaTree {
	m.mu.RLock()
	defer m.mu.RUnlock()

	if len(m.trees) == 0 {
		return &MetaTree{
			Root:    nil,
			Leaves:  []*MetaNode{},
			TreeMap: make(map[string]int),
		}
	}

	// Получаем отсортированные имена деревьев (для детерминизма)
	names := make([]string, 0, len(m.trees))
	for name := range m.trees {
		names = append(names, name)
	}
	sort.Strings(names)

	// Создаем листья для каждого дерева
	leaves := make([]*MetaNode, len(names))
	treeMap := make(map[string]int)

	for i, name := range names {
		treeRoot := m.trees[name].ComputeRoot()
		leaf := &MetaNode{
			Hash:     treeRoot,
			TreeName: name,
			TreeRoot: treeRoot,
			IsLeaf:   true,
		}
		leaves[i] = leaf
		treeMap[name] = i
	}

	// Строим бинарное дерево снизу вверх
	root := buildMetaTreeRecursive(leaves)

	return &MetaTree{
		Root:    root,
		Leaves:  leaves,
		TreeMap: treeMap,
	}
}

// buildMetaTreeRecursive рекурсивно строит мета-дерево
func buildMetaTreeRecursive(nodes []*MetaNode) *MetaNode {
	if len(nodes) == 0 {
		return nil
	}

	if len(nodes) == 1 {
		return nodes[0]
	}

	// Создаем родительские узлы для пар детей
	var parents []*MetaNode

	for i := 0; i < len(nodes); i += 2 {
		left := nodes[i]
		var right *MetaNode

		if i+1 < len(nodes) {
			right = nodes[i+1]
		} else {
			// Нечетное количество - дублируем последний
			right = nodes[i]
		}

		// Вычисляем хеш родителя
		hasher := blake3.New()
		hasher.Write(left.Hash[:])
		hasher.Write(right.Hash[:])

		var parentHash [32]byte
		copy(parentHash[:], hasher.Sum(nil))

		parent := &MetaNode{
			Hash:   parentHash,
			Left:   left,
			Right:  right,
			IsLeaf: false,
		}

		parents = append(parents, parent)
	}

	// Рекурсивно строим следующий уровень
	return buildMetaTreeRecursive(parents)
}

// GetGlobalRoot возвращает корень мета-дерева
func (mt *MetaTree) GetGlobalRoot() [32]byte {
	if mt.Root == nil {
		return [32]byte{}
	}
	return mt.Root.Hash
}

// GetProof возвращает Merkle proof для конкретного дерева
func (mt *MetaTree) GetProof(treeName string) (*MerkleProof, error) {
	leafIndex, exists := mt.TreeMap[treeName]
	if !exists {
		return nil, fmt.Errorf("дерево '%s' не найдено", treeName)
	}

	leaf := mt.Leaves[leafIndex]

	// Собираем proof path
	proofPath := [][32]byte{}
	isLeft := []bool{}

	collectProofPath(mt.Root, leaf, &proofPath, &isLeft)

	return &MerkleProof{
		TreeName:   treeName,
		TreeRoot:   leaf.TreeRoot,
		ProofPath:  proofPath,
		IsLeft:     isLeft,
		GlobalRoot: mt.Root.Hash,
	}, nil
}

// collectProofPath собирает путь хешей для proof
func collectProofPath(node *MetaNode, target *MetaNode, path *[][32]byte, isLeft *[]bool) bool {
	if node == nil {
		return false
	}

	if node == target {
		return true
	}

	if node.IsLeaf {
		return false
	}

	// Ищем в левом поддереве
	if collectProofPath(node.Left, target, path, isLeft) {
		// Нашли в левом - добавляем правый хеш
		*path = append(*path, node.Right.Hash)
		*isLeft = append(*isLeft, true) // target слева
		return true
	}

	// Ищем в правом поддереве
	if collectProofPath(node.Right, target, path, isLeft) {
		// Нашли в правом - добавляем левый хеш
		*path = append(*path, node.Left.Hash)
		*isLeft = append(*isLeft, false) // target справа
		return true
	}

	return false
}

// Verify проверяет корректность Merkle proof
func (proof *MerkleProof) Verify() bool {
	currentHash := proof.TreeRoot

	// Идем снизу вверх по дереву
	for i := 0; i < len(proof.ProofPath); i++ {
		hasher := blake3.New()

		if proof.IsLeft[i] {
			// Текущий хеш слева
			hasher.Write(currentHash[:])
			hasher.Write(proof.ProofPath[i][:])
		} else {
			// Текущий хеш справа
			hasher.Write(proof.ProofPath[i][:])
			hasher.Write(currentHash[:])
		}

		copy(currentHash[:], hasher.Sum(nil))
	}

	return currentHash == proof.GlobalRoot
}

// String возвращает строковое представление proof
func (proof *MerkleProof) String() string {
	s := fmt.Sprintf("MerkleProof для '%s'\n", proof.TreeName)
	s += fmt.Sprintf("  Корень дерева: %x\n", proof.TreeRoot[:16])
	s += fmt.Sprintf("  Глобальный корень: %x\n", proof.GlobalRoot[:16])
	s += fmt.Sprintf("  Длина пути: %d\n", len(proof.ProofPath))
	s += "  Путь:\n"

	for i, hash := range proof.ProofPath {
		side := "right"
		if proof.IsLeft[i] {
			side = "left"
		}
		s += fmt.Sprintf("    [%d] %s: %x\n", i, side, hash[:16])
	}

	return s
}

// GetTreeStructure возвращает текстовое представление структуры мета-дерева
func (mt *MetaTree) GetTreeStructure() string {
	if mt.Root == nil {
		return "Пустое мета-дерево"
	}

	return fmt.Sprintf("Мета-дерево:\n  Корень: %x\n  Листьев: %d\n%s",
		mt.Root.Hash[:16],
		len(mt.Leaves),
		formatMetaNode(mt.Root, 0))
}

// formatMetaNode рекурсивно форматирует узел
func formatMetaNode(node *MetaNode, depth int) string {
	if node == nil {
		return ""
	}

	indent := ""
	for i := 0; i < depth; i++ {
		indent += "  "
	}

	if node.IsLeaf {
		return fmt.Sprintf("%s└─ [Лист] %s: %x\n",
			indent, node.TreeName, node.Hash[:8])
	}

	s := fmt.Sprintf("%s└─ [Узел] %x\n", indent, node.Hash[:8])

	if node.Left != nil {
		s += formatMetaNode(node.Left, depth+1)
	}
	if node.Right != nil && node.Right != node.Left {
		s += formatMetaNode(node.Right, depth+1)
	}

	return s
}

// GetStats возвращает статистику мета-дерева
func (mt *MetaTree) GetStats() MetaTreeStats {
	if mt.Root == nil {
		return MetaTreeStats{}
	}

	totalNodes := countNodes(mt.Root)
	height := getHeight(mt.Root)

	return MetaTreeStats{
		TotalNodes: totalNodes,
		LeafNodes:  len(mt.Leaves),
		Height:     height,
	}
}

// MetaTreeStats статистика мета-дерева
type MetaTreeStats struct {
	TotalNodes int // Всего узлов
	LeafNodes  int // Листовых узлов
	Height     int // Высота дерева
}

// countNodes подсчитывает общее количество узлов
func countNodes(node *MetaNode) int {
	if node == nil {
		return 0
	}

	if node.IsLeaf {
		return 1
	}

	leftCount := countNodes(node.Left)
	rightCount := 0
	if node.Right != node.Left {
		rightCount = countNodes(node.Right)
	}

	return 1 + leftCount + rightCount
}

// getHeight возвращает высоту дерева
func getHeight(node *MetaNode) int {
	if node == nil {
		return 0
	}

	if node.IsLeaf {
		return 1
	}

	leftHeight := getHeight(node.Left)
	rightHeight := getHeight(node.Right)

	if leftHeight > rightHeight {
		return leftHeight + 1
	}
	return rightHeight + 1
}
