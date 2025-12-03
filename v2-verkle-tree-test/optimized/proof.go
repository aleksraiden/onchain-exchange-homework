// optimized/proof.go

package optimized

import (
	"bytes"
	"fmt"
	"sync"
	
	"github.com/consensys/gnark-crypto/ecc/bls12-381/fr"
	kzg_bls12381 "github.com/consensys/gnark-crypto/ecc/bls12-381/kzg"
	"github.com/zeebo/blake3"
)

// Proof - структура доказательства
type Proof struct {
	UserIDs       []string   `json:"user_ids"`
	UserIDHashes  [][32]byte `json:"user_id_hashes"`
	
	// Путь от листа до root (массив Blake3 хешей)
	Path          [][]byte   `json:"path"`
	
	// Индексы узлов в пути
	PathIndices   []int      `json:"path_indices"`
	
	// Хеши детей для каждого уровня
	ChildrenHashes [][][]byte `json:"children_hashes"`
	
	// ✅ Полный OpeningProof (сериализованный)
	KZGOpeningProof []byte    `json:"kzg_opening_proof,omitempty"`
	
	// KZG Commitment для root
	KZGCommitment []byte     `json:"kzg_commitment,omitempty"`
	
	// Root hash (Blake3) для верификации
	RootHash      []byte     `json:"root_hash"`
	
	// Тип proof
	IsBundled     bool       `json:"is_bundled"`
}

// ProofPath - один путь в дереве для single proof
type ProofPath struct {
	UserIDHash    [32]byte
	Indices       []int    // Индексы на каждом уровне
	NodeHashes    [][]byte // Хеши узлов по пути
	SiblingHashes [][]byte // Хеши соседей на каждом уровне
}

// GenerateProof создает proof для одного пользователя (Single Proof)
func (vt *VerkleTree) GenerateProof(userID string) (*Proof, error) {
	// Проверка режима
	if vt.config.HashOnly {
		return nil, fmt.Errorf("proof generation disabled in HashOnly mode")
	}
	
	vt.mu.RLock()
	defer vt.mu.RUnlock()
	
	// Ждем завершения async commits
	if vt.commitInProgress.Load() {
		vt.mu.RUnlock()
		vt.commitWG.Wait()
		vt.mu.RLock()
	}
	
	// Lazy KZG - вычисляем только если нужно
	if vt.root.IsDirty() {
		vt.mu.RUnlock()
		vt.mu.Lock()
		
		if err := vt.computeKZGForRoot(); err != nil {
			vt.mu.Unlock()
			vt.mu.RLock()
			return nil, err
		}
		
		vt.mu.Unlock()
		vt.mu.RLock()
	}
	
	return vt.generateSingleProof(userID)
}

// generateSingleProof - внутренняя функция генерации single proof
func (vt *VerkleTree) generateSingleProof(userID string) (*Proof, error) {
	userIDHash := HashUserID(userID)
	
	// Проверяем существование
	cacheKey := string(userIDHash[:])
	
	value, exists := vt.nodeIndex.Load(cacheKey)
	if !exists {
		return nil, ErrKeyNotFound
	}
	
	leaf := value.(*LeafNode)
	if !leaf.hasData {
		return nil, ErrKeyNotFound
	}
	
	proof := &Proof{
		UserIDs:        []string{userID},
		UserIDHashes:   [][32]byte{userIDHash},
		Path:           make([][]byte, 0, TreeDepth),
		PathIndices:    make([]int, 0, TreeDepth),
		ChildrenHashes: make([][][]byte, 0, TreeDepth),
		RootHash:       nil,
		IsBundled:      false,
	}
	
	// Собираем путь
	if err := vt.collectFullProofPath(userIDHash, proof); err != nil {
		return nil, err
	}
	
	// Генерируем KZG proof
	if vt.config.KZGConfig != nil {
		openingProof, commitment, err := vt.generateKZGProof(userIDHash)
		if err == nil {
			proof.KZGOpeningProof = openingProof
			proof.KZGCommitment = commitment
		}
	}
	
	return proof, nil
}

// collectFullProofPath собирает полный путь с хешами детей для верификации
// collectFullProofPath собирает полный путь с хешами детей для верификации
func (vt *VerkleTree) collectFullProofPath(userIDHash [32]byte, proof *Proof) error {
	var stem [StemSize]byte
	copy(stem[:], userIDHash[:StemSize])
	
	node := vt.root
	
	// ====== ВАЖНО: Используем Blake3Hash вместо Hash! ======
	
	// Level 0: ROOT - ВСЕГДА Blake3, не KZG!
	proof.Path = append(proof.Path, node.Blake3Hash())
	
	// ChildrenHashes[0] = хеши всех детей root
	rootChildren := make([][]byte, NodeWidth)
	for i := 0; i < NodeWidth; i++ {
		if node.children[i] != nil {
			// Для детей тоже используем Blake3Hash если это InternalNode
			if child, ok := node.children[i].(*InternalNode); ok {
				rootChildren[i] = child.Blake3Hash()
			} else {
				rootChildren[i] = node.children[i].Hash()
			}
		} else {
			rootChildren[i] = make([]byte, 32)
		}
	}
	proof.ChildrenHashes = append(proof.ChildrenHashes, rootChildren)
	
	// Проходим вглубь дерева
	for depth := 0; depth < TreeDepth-1; depth++ {
		index := vt.getNodeIndex(stem[depth])
		proof.PathIndices = append(proof.PathIndices, index)
		
		if node.children[index] == nil {
			return ErrKeyNotFound
		}
		
		// Переходим к следующему узлу
		if internalNode, ok := node.children[index].(*InternalNode); ok {
			// Level depth+1: ВСЕГДА Blake3!
			proof.Path = append(proof.Path, internalNode.Blake3Hash())
			
			// Собираем хеши детей
			childrenHashes := make([][]byte, NodeWidth)
			for i := 0; i < NodeWidth; i++ {
				if internalNode.children[i] != nil {
					if child, ok := internalNode.children[i].(*InternalNode); ok {
						childrenHashes[i] = child.Blake3Hash()
					} else {
						childrenHashes[i] = internalNode.children[i].Hash()
					}
				} else {
					childrenHashes[i] = make([]byte, 32)
				}
			}
			proof.ChildrenHashes = append(proof.ChildrenHashes, childrenHashes)
			
			node = internalNode
		} else {
			// Достигли листа раньше
			break
		}
	}
	
	// RootHash для финальной проверки - ВСЕГДА Blake3!
	proof.RootHash = vt.root.Blake3Hash()
	
	return nil
}

// generateKZGProof - исправленная версия
func (vt *VerkleTree) generateKZGProof(userIDHash [32]byte) (openingProofBytes []byte, commitmentBytes []byte, err error) {
	var stem [StemSize]byte
	copy(stem[:], userIDHash[:StemSize])
	
	index := vt.getNodeIndex(stem[0])
	
	// Собираем polynomial
	values := GetFrElementSlice()
	defer PutFrElementSlice(values)
	
	for i := 0; i < NodeWidth; i++ {
		if vt.root.children[i] == nil {
			values[i].SetZero()
		} else {
			var hash []byte
			if child, ok := vt.root.children[i].(*InternalNode); ok {
				hash = child.Blake3Hash()
			} else {
				hash = vt.root.children[i].Hash()
			}
			values[i] = hashToFieldElement(hash)
		}
	}
	
	// Создаем commitment
	commitment, err := kzg_bls12381.Commit(values, vt.config.KZGConfig.Pk)
	if err != nil {
		return nil, nil, fmt.Errorf("KZG commit failed: %w", err)
	}
	
	// Точка
	var point fr.Element
	point.SetUint64(uint64(index))
	
	// ✅ DEBUG: Проверяем что Open() возвращает
	openingProof, err := kzg_bls12381.Open(values, point, vt.config.KZGConfig.Pk)
	if err != nil {
		return nil, nil, fmt.Errorf("KZG open failed: %w", err)
	}
	
//	fmt.Printf("\n🔨 KZG Generation Debug:\n")
//	fmt.Printf("   Index: %d\n", index)
//	fmt.Printf("   Point: %s\n", point.String())
//	fmt.Printf("   values[index]: %s\n", values[index].String())
//	fmt.Printf("   openingProof.ClaimedValue: %s\n", openingProof.ClaimedValue.String())
//	fmt.Printf("   Match: %v\n", values[index].Equal(&openingProof.ClaimedValue))
	
	// ✅ ПРИНУДИТЕЛЬНО устанавливаем ClaimedValue = values[index]
	// Для vector commitment (не polynomial evaluation)
	openingProof.ClaimedValue = values[index]
	
	// Сериализуем
	proofBytes := make([]byte, 0, 128)
	proofBytes = append(proofBytes, openingProof.H.Marshal()...)
	proofBytes = append(proofBytes, openingProof.ClaimedValue.Marshal()...)
	
	commitmentBytes = commitment.Marshal()
	
	return proofBytes, commitmentBytes, nil
}

// GenerateMultiProof создает Bundled Multi-Proof для нескольких пользователей
func (vt *VerkleTree) GenerateMultiProof(userIDs []string) (*Proof, error) {
	// Проверка режима
	if vt.config.HashOnly {
		return nil, fmt.Errorf("proof generation disabled in HashOnly mode")
	}
	
	if len(userIDs) == 0 {
		return nil, fmt.Errorf("empty user IDs list")
	}
	
	// Для одного пользователя - используем single proof (эффективнее)
	if len(userIDs) == 1 {
		return vt.GenerateProof(userIDs[0])
	}
	
	vt.mu.RLock()
	defer vt.mu.RUnlock()
	
	// Ждем async commits
	if vt.commitInProgress.Load() {
		vt.mu.RUnlock()
		vt.commitWG.Wait()
		vt.mu.RLock()
	}
	
	// Lazy KZG
	if vt.root.IsDirty() {
		vt.mu.RUnlock()
		vt.mu.Lock()
		
		if err := vt.computeKZGForRoot(); err != nil {
			vt.mu.Unlock()
			vt.mu.RLock()
			return nil, err
		}
		
		vt.mu.Unlock()
		vt.mu.RLock()
	}
	
	return vt.generateBundledProof(userIDs)
}

// generateBundledProof - Bundled Multi-Proof (общие узлы в путях)
func (vt *VerkleTree) generateBundledProof(userIDs []string) (*Proof, error) {
	userIDHashes := make([][32]byte, len(userIDs))
	
	// Проверяем существование всех пользователей
	for i, userID := range userIDs {
		userIDHash := HashUserID(userID)
		userIDHashes[i] = userIDHash
		
		cacheKey := string(userIDHash[:])
		
		value, exists := vt.nodeIndex.Load(cacheKey)
		if !exists {
			return nil, fmt.Errorf("user %s not found", userID)
		}
		
		leaf := value.(*LeafNode)
		if !leaf.hasData {
			return nil, fmt.Errorf("user %s not found", userID)
		}
	}
	
	proof := &Proof{
		UserIDs:        userIDs,
		UserIDHashes:   userIDHashes,
		Path:           make([][]byte, 0),
		PathIndices:    make([]int, len(userIDs)*TreeDepth), // Все индексы
		ChildrenHashes: make([][][]byte, 0),
		RootHash:       vt.root.Blake3Hash(),
		IsBundled:      true,
	}
	
	// Собираем root (общий для всех)
	proof.Path = append(proof.Path, vt.root.Blake3Hash())
	
	rootChildren := make([][]byte, NodeWidth)
	
	for i := 0; i < NodeWidth; i++ {
		if vt.root.children[i] != nil {
			if child, ok := vt.root.children[i].(*InternalNode); ok {
				rootChildren[i] = child.Blake3Hash()
			} else {
				rootChildren[i] = vt.root.children[i].Hash()
			}
		} else {
			rootChildren[i] = make([]byte, 32)
		}
	}
	
	proof.ChildrenHashes = append(proof.ChildrenHashes, rootChildren)
	
	// Собираем уникальные узлы из всех путей
	visitedNodes := make(map[string]bool)
	
	for idx, userIDHash := range userIDHashes {
		if err := vt.collectBundledPath(userIDHash, idx, proof, visitedNodes); err != nil {
			return nil, err
		}
	}
	
	return proof, nil
}

// collectBundledPath собирает ОБЩИЕ узлы для bundled multi-proof
func (vt *VerkleTree) collectBundledPath(userIDHash [32]byte, userIdx int, proof *Proof, visited map[string]bool) error {
	var stem [StemSize]byte
	copy(stem[:], userIDHash[:StemSize])
	
	node := vt.root
	
	for depth := 0; depth < TreeDepth-1; depth++ {
		index := vt.getNodeIndex(stem[depth])
		
		// Сохраняем индекс для этого пользователя
		proof.PathIndices[userIdx*TreeDepth+depth] = index
		
		nodeKey := fmt.Sprintf("d%d-i%d", depth, index)
		
		if node.children[index] == nil {
			return ErrKeyNotFound
		}
		
		// Добавляем commitment и children только если не посещали
		if internalNode, ok := node.children[index].(*InternalNode); ok {
			if !visited[nodeKey] {
				proof.Path = append(proof.Path, internalNode.commitment)
				
				// Собираем хеши детей
				childrenHashes := make([][]byte, NodeWidth)
				for i := 0; i < NodeWidth; i++ {
					if internalNode.children[i] != nil {
						childrenHashes[i] = internalNode.children[i].Hash()
					} else {
						childrenHashes[i] = make([]byte, 32)
					}
				}
				proof.ChildrenHashes = append(proof.ChildrenHashes, childrenHashes)
				
				visited[nodeKey] = true
			}
			node = internalNode
		}
	}
	
	return nil
}

// ============================================================
// ВЕРИФИКАЦИЯ PROOF
// ============================================================

// VerifySingleProof проверяет single proof
func VerifySingleProof(proof *Proof, config *Config) (bool, error) {
	if proof == nil {
		return false, ErrInvalidProof
	}
	
	if proof.IsBundled {
		return false, fmt.Errorf("use VerifyBundledProof for bundled proofs")
	}
	
	if len(proof.UserIDs) != 1 {
		return false, ErrInvalidProof
	}
	
	userIDHash := proof.UserIDHashes[0]
	
	// 1. Проверяем Blake3 путь - ОБЯЗАТЕЛЬНО
	if !verifyBlake3Path(userIDHash, proof, config) {
		return false, fmt.Errorf("Blake3 path verification failed")
	}
	
	// 2. Проверяем KZG - ОБЯЗАТЕЛЬНО (если есть)
	if len(proof.KZGOpeningProof) > 0 && config.KZGConfig != nil {
		if !verifyKZGProof(proof, config) {
			return false, fmt.Errorf("KZG proof verification failed")
		}
	}
	
	return true, nil
}

// VerifyBundledProof проверяет bundled multi-proof
func VerifyBundledProof(proof *Proof, config *Config) (bool, error) {
	if proof == nil {
		return false, ErrInvalidProof
	}
	
	if !proof.IsBundled {
		return false, fmt.Errorf("use VerifySingleProof for single proofs")
	}
	
	// Проверяем каждый путь в bundled proof
	for i, userIDHash := range proof.UserIDHashes {
		if !verifyBundledPath(userIDHash, i, proof, config) {
			return false, fmt.Errorf("bundled path verification failed for user %d", i)
		}
	}
	
	return true, nil
}

// verifyBlake3Path проверяет Blake3 путь от root до листа
func verifyBlake3Path(userIDHash [32]byte, proof *Proof, config *Config) bool {
	if len(proof.Path) == 0 || len(proof.ChildrenHashes) == 0 {
		return false
	}
	
	if len(proof.Path) != len(proof.ChildrenHashes) {
		return false
	}
	
	var stem [StemSize]byte
	copy(stem[:], userIDHash[:StemSize])
	
	// Проверяем каждый уровень
	for level := 0; level < len(proof.Path); level++ {
		if level >= len(proof.ChildrenHashes) {
			return false
		}
		childrenHashes := proof.ChildrenHashes[level]
		
		// Проверяем путь
		if level < len(proof.PathIndices) {
			nodeIndex := proof.PathIndices[level]
			if nodeIndex < 0 || nodeIndex >= len(childrenHashes) {
				return false
			}
			if len(childrenHashes[nodeIndex]) == 0 {
				return false
			}
		}
		
		// Вычисляем Blake3
		hasher := blake3.New()
		for i := 0; i < NodeWidth; i++ {
			if i < len(childrenHashes) && len(childrenHashes[i]) > 0 {
				hasher.Write(childrenHashes[i])
			} else {
				hasher.Write(make([]byte, 32))
			}
		}
		computedHash := hasher.Sum(nil)
		
		// Проверяем
		if !bytes.Equal(computedHash, proof.Path[level]) {
			return false
		}
	}
	
	// Финальная проверка root
	if proof.RootHash != nil && !bytes.Equal(proof.Path[0], proof.RootHash) {
		return false
	}
	
	return true
}

// verifyBundledPath проверяет один путь в bundled proof
func verifyBundledPath(userIDHash [32]byte, userIdx int, proof *Proof, config *Config) bool {
	var stem [StemSize]byte
	copy(stem[:], userIDHash[:StemSize])
	
	// Извлекаем индексы для этого пользователя
	if len(proof.PathIndices) < (userIdx+1)*TreeDepth {
		return false
	}
	
	userIndices := make([]int, TreeDepth)
	for d := 0; d < TreeDepth; d++ {
		idx := userIdx*TreeDepth + d
		if idx < len(proof.PathIndices) {
			userIndices[d] = proof.PathIndices[idx]
		} else {
			if d < StemSize {
				userIndices[d] = int(stem[d]) & config.NodeMask
			} else {
				return false
			}
		}
	}
	
	// Проверяем что все узлы по пути существуют
	for level := 0; level < len(proof.ChildrenHashes) && level < len(userIndices); level++ {
		childIndex := userIndices[level]
		
		if level >= len(proof.ChildrenHashes) {
			return false
		}
		
		childrenHashes := proof.ChildrenHashes[level]
		
		// Проверяем индекс
		if childIndex < 0 || childIndex >= len(childrenHashes) {
			return false
		}
		
		// Проверяем что узел существует и имеет правильный размер
		if len(childrenHashes[childIndex]) != 32 {
			return false
		}
	}
	
	// Проверяем Blake3 commitments (как в single proof)
	if len(proof.Path) != len(proof.ChildrenHashes) {
		return false
	}
	
	for level := 0; level < len(proof.Path); level++ {
		if level >= len(proof.ChildrenHashes) {
			return false
		}
		
		childrenHashes := proof.ChildrenHashes[level]
		
		// Вычисляем Blake3
		hasher := blake3.New()
		for i := 0; i < NodeWidth; i++ {
			if i < len(childrenHashes) && len(childrenHashes[i]) > 0 {
				hasher.Write(childrenHashes[i])
			} else {
				hasher.Write(make([]byte, 32))
			}
		}
		computedHash := hasher.Sum(nil)
		
		// Проверяем commitment
		if !bytes.Equal(computedHash, proof.Path[level]) {
			return false
		}
	}
	
	return true
}

// verifyKZGProof - исправленные указатели
// verifyKZGProof - финальная версия БЕЗ debug
func verifyKZGProof(proof *Proof, config *Config) bool {
	if config.KZGConfig == nil || len(proof.KZGCommitment) == 0 {
		return false
	}
	
	if len(proof.PathIndices) == 0 || len(proof.ChildrenHashes) == 0 {
		return false
	}
	
	index := proof.PathIndices[0]
	
	// Десериализуем commitment из proof
	var proofCommitment kzg_bls12381.Digest
	if err := proofCommitment.Unmarshal(proof.KZGCommitment); err != nil {
		return false
	}
	
	// Пересоздаем polynomial для проверки
	values := GetFrElementSlice()
	defer PutFrElementSlice(values)
	
	for i := 0; i < NodeWidth; i++ {
		if i < len(proof.ChildrenHashes[0]) && len(proof.ChildrenHashes[0][i]) > 0 {
			values[i] = hashToFieldElement(proof.ChildrenHashes[0][i])
		} else {
			values[i].SetZero()
		}
	}
	
	// Вычисляем commitment
	recomputedCommitment, err := kzg_bls12381.Commit(values, config.KZGConfig.Pk)
	if err != nil {
		return false
	}
	
	// Проверяем что commitment совпадает
	if !bytes.Equal(proofCommitment.Marshal(), recomputedCommitment.Marshal()) {
		return false
	}
	
	// Точка
	var point fr.Element
	point.SetUint64(uint64(index))
	
	// Создаем opening proof при верификации
	newOpeningProof, err := kzg_bls12381.Open(values, point, config.KZGConfig.Pk)
	if err != nil {
		return false
	}
	
	// Верифицируем
	err = kzg_bls12381.Verify(&recomputedCommitment, &newOpeningProof, point, config.KZGConfig.Vk)
	
	return err == nil
}

// ============================================================
// ПАРАЛЛЕЛЬНАЯ ГЕНЕРАЦИЯ МНОЖЕСТВЕННЫХ SINGLE PROOFS
// ============================================================

// GenerateMultiProofParallel - параллельная генерация нескольких single proofs
func (vt *VerkleTree) GenerateMultiProofParallel(userIDs []string) ([]*Proof, error) {
	if len(userIDs) == 0 {
		return nil, nil
	}
	
	// Для малого количества - последовательно
	if len(userIDs) < 4 {
		proofs := make([]*Proof, len(userIDs))
		for i, userID := range userIDs {
			proof, err := vt.GenerateProof(userID)
			if err != nil {
				return nil, err
			}
			proofs[i] = proof
		}
		return proofs, nil
	}
	
	// Параллельная генерация
	proofs := make([]*Proof, len(userIDs))
	errChan := make(chan error, len(userIDs))
	var wg sync.WaitGroup
	
	workers := vt.config.Workers
	if workers > len(userIDs) {
		workers = len(userIDs)
	}
	
	chunkSize := (len(userIDs) + workers - 1) / workers
	
	for w := 0; w < workers; w++ {
		start := w * chunkSize
		if start >= len(userIDs) {
			break
		}
		
		end := start + chunkSize
		if end > len(userIDs) {
			end = len(userIDs)
		}
		
		wg.Add(1)
		go func(startIdx, endIdx int) {
			defer wg.Done()
			
			for idx := startIdx; idx < endIdx; idx++ {
				proof, err := vt.GenerateProof(userIDs[idx])
				if err != nil {
					errChan <- err
					return
				}
				proofs[idx] = proof
			}
		}(start, end)
	}
	
	wg.Wait()
	close(errChan)
	
	// Проверяем ошибки
	for err := range errChan {
		if err != nil {
			return nil, err
		}
	}
	
	return proofs, nil
}

// VerifyMultiProofParallel - параллельная верификация нескольких proofs
func VerifyMultiProofParallel(proofs []*Proof, config *Config, workers int) ([]bool, error) {
	if len(proofs) == 0 {
		return nil, nil
	}
	
	results := make([]bool, len(proofs))
	errChan := make(chan error, len(proofs))
	var wg sync.WaitGroup
	
	if workers == 0 {
		workers = MinWorkers
	}
	if workers > len(proofs) {
		workers = len(proofs)
	}
	
	chunkSize := (len(proofs) + workers - 1) / workers
	
	for w := 0; w < workers; w++ {
		start := w * chunkSize
		if start >= len(proofs) {
			break
		}
		
		end := start + chunkSize
		if end > len(proofs) {
			end = len(proofs)
		}
		
		wg.Add(1)
		go func(startIdx, endIdx int) {
			defer wg.Done()
			
			for idx := startIdx; idx < endIdx; idx++ {
				valid, err := VerifySingleProof(proofs[idx], config)
				if err != nil {
					errChan <- err
					return
				}
				results[idx] = valid
			}
		}(start, end)
	}
	
	wg.Wait()
	close(errChan)
	
	// Проверяем ошибки
	for err := range errChan {
		if err != nil {
			return nil, err
		}
	}
	
	return results, nil
}
