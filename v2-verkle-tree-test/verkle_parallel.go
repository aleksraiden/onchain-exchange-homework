package verkletree

import (
	"runtime"
	"sync"
)

// ParallelConfig - конфигурация параллелизма
type ParallelConfig struct {
	// Workers - количество горутин для параллельной обработки
	Workers int
	
	// MinNodesForParallel - минимум узлов для запуска параллелизма
	MinNodesForParallel int
	
	// Enabled - включен ли параллелизм
	Enabled bool
}

// NewParallelConfig создает конфигурацию с параметрами по умолчанию
func NewParallelConfig() *ParallelConfig {
	return &ParallelConfig{
		Workers:             runtime.NumCPU(),
		MinNodesForParallel: 4,
		Enabled:             true,
	}
}

// Добавьте в VerkleTree:
// parallelConfig *ParallelConfig

// recomputeCommitmentsParallel - параллельный пересчет коммитментов
func (vt *VerkleTree) recomputeCommitmentsParallel(node *InternalNode) error {
	if node == nil {
		return nil
	}
	
	// Проверяем включен ли параллелизм
	if vt.parallelConfig == nil || !vt.parallelConfig.Enabled {
		return vt.recomputeCommitments(node)
	}
	
	// Рекурсивно обрабатываем дочерние узлы параллельно
	return vt.parallelCommitLevel(node)
}

// parallelCommitLevel обрабатывает один уровень дерева параллельно
func (vt *VerkleTree) parallelCommitLevel(node *InternalNode) error {
	if node == nil {
		return nil
	}
	
	// Собираем все внутренние и листовые узлы
	var internalNodes []*InternalNode
	var leafNodes []*LeafNode
	
	for _, child := range node.children {
		if child == nil {
			continue
		}
		
		if internalChild, ok := child.(*InternalNode); ok {
			internalNodes = append(internalNodes, internalChild)
		} else if leafChild, ok := child.(*LeafNode); ok {
			if leafChild.dirty {
				leafNodes = append(leafNodes, leafChild)
			}
		}
	}
	
	// Сначала рекурсивно обрабатываем внутренние узлы
	if len(internalNodes) > 0 {
		if err := vt.parallelProcessInternalNodes(internalNodes); err != nil {
			return err
		}
	}
	
	// Затем параллельно коммитим листья на этом уровне
	if len(leafNodes) >= vt.parallelConfig.MinNodesForParallel {
		if err := vt.parallelCommitLeaves(leafNodes); err != nil {
			return err
		}
	} else {
		// Для малого количества листьев - последовательно
		for _, leaf := range leafNodes {
			if _, err := leaf.Commit(vt.config); err != nil {
				return err
			}
		}
	}
	
	// Коммитим текущий узел
	if node.dirty {
		if _, err := node.Commit(vt.config); err != nil {
			return err
		}
	}
	
	return nil
}

// parallelProcessInternalNodes обрабатывает внутренние узлы параллельно
func (vt *VerkleTree) parallelProcessInternalNodes(nodes []*InternalNode) error {
	if len(nodes) < vt.parallelConfig.MinNodesForParallel {
		// Мало узлов - обрабатываем последовательно
		for _, node := range nodes {
			if err := vt.parallelCommitLevel(node); err != nil {
				return err
			}
		}
		return nil
	}
	
	// Параллельная обработка
	errChan := make(chan error, len(nodes))
	var wg sync.WaitGroup
	
	workers := vt.parallelConfig.Workers
	if workers > len(nodes) {
		workers = len(nodes)
	}
	
	// Создаем очередь задач
	nodeChan := make(chan *InternalNode, len(nodes))
	for _, node := range nodes {
		nodeChan <- node
	}
	close(nodeChan)
	
	// Запускаем воркеры
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for node := range nodeChan {
				if err := vt.parallelCommitLevel(node); err != nil {
					errChan <- err
					return
				}
			}
		}()
	}
	
	wg.Wait()
	close(errChan)
	
	// Проверяем ошибки
	for err := range errChan {
		if err != nil {
			return err
		}
	}
	
	return nil
}

// parallelCommitLeaves коммитит листья параллельно
func (vt *VerkleTree) parallelCommitLeaves(leaves []*LeafNode) error {
	if len(leaves) == 0 {
		return nil
	}
	
	errChan := make(chan error, len(leaves))
	var wg sync.WaitGroup
	
	workers := vt.parallelConfig.Workers
	if workers > len(leaves) {
		workers = len(leaves)
	}
	
	// Делим листья на чанки для воркеров
	chunkSize := (len(leaves) + workers - 1) / workers
	
	for i := 0; i < workers; i++ {
		start := i * chunkSize
		if start >= len(leaves) {
			break
		}
		
		end := start + chunkSize
		if end > len(leaves) {
			end = len(leaves)
		}
		
		wg.Add(1)
		go func(leafChunk []*LeafNode) {
			defer wg.Done()
			for _, leaf := range leafChunk {
				if _, err := leaf.Commit(vt.config); err != nil {
					errChan <- err
					return
				}
			}
		}(leaves[start:end])
	}
	
	wg.Wait()
	close(errChan)
	
	// Проверяем ошибки
	for err := range errChan {
		if err != nil {
			return err
		}
	}
	
	return nil
}

// EnableParallelCommits включает параллельные коммитменты
func (vt *VerkleTree) EnableParallelCommits(workers int) {
	vt.mu.Lock()
	defer vt.mu.Unlock()
	
	if workers <= 0 {
		workers = runtime.NumCPU()
	}
	
	vt.parallelConfig = &ParallelConfig{
		Workers:             workers,
		MinNodesForParallel: 4,
		Enabled:             true,
	}
}

// DisableParallelCommits выключает параллельные коммитменты
func (vt *VerkleTree) DisableParallelCommits() {
	vt.mu.Lock()
	defer vt.mu.Unlock()
	
	if vt.parallelConfig != nil {
		vt.parallelConfig.Enabled = false
	}
}
