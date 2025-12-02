package verkletree

// CommitmentCache - кэш коммитментов для избежания повторных вычислений
type CommitmentCache struct {
    cache map[string][]byte
}

func newCommitmentCache() *CommitmentCache {
    return &CommitmentCache{
        cache: make(map[string][]byte),
    }
}

// OptimizedCommit - оптимизированный коммит только для измененных путей
func (vt *VerkleTree) optimizedRecomputeCommitments(node *InternalNode, changedIndices map[int]bool) error {
    if node == nil || !node.dirty {
        return nil
    }
    
    // Пересчитываем только измененные дочерние узлы
    for _, child := range node.children {  // УБРАЛ idx
        if child == nil {
            continue
        }
        
        // Пропускаем неизмененные узлы
        if !child.IsDirty() {
            continue
        }
        
        if internalChild, ok := child.(*InternalNode); ok {
            if err := vt.optimizedRecomputeCommitments(internalChild, changedIndices); err != nil {
                return err
            }
        } else if leafChild, ok := child.(*LeafNode); ok {
            if _, err := leafChild.Commit(vt.config); err != nil {
                return err
            }
        }
    }
    
    // Теперь коммитим текущий узел
    if _, err := node.Commit(vt.config); err != nil {
        return err
    }
    
    return nil
}
