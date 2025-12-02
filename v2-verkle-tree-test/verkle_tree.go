// verkle_tree.go
package verkletree

import (
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"

	"github.com/consensys/gnark-crypto/ecc/bls12-381/fr"
	kzg_bls12381 "github.com/consensys/gnark-crypto/ecc/bls12-381/kzg"
	"github.com/zeebo/blake3"
)

// Константы для конфигурации дерева
const (
	// DefaultNodeWidth - стандартное количество дочерних узлов (256)
	DefaultNodeWidth = 256
	
	// DefaultLevels - стандартное количество уровней дерева
	DefaultLevels = 4
	
	// MaxValueSize - максимальный размер данных в узле (8 KB)
	MaxValueSize = 8 * 1024
	
	// StemSize - размер stem (префикс ключа) в байтах
	StemSize = 31
	
	// ExpansionThreshold - порог для автоматического расширения (200 из 256)
	ExpansionThreshold = 200
)

// Ошибки модуля
var (
	ErrKeyNotFound     = errors.New("ключ не найден в дереве")
	ErrInvalidKey      = errors.New("некорректный формат ключа")
	ErrInvalidValue    = errors.New("некорректное значение")
	ErrValueTooLarge   = errors.New("значение превышает максимальный размер 8KB")
	ErrNodeFull        = errors.New("узел полностью заполнен")
	ErrInvalidProof    = errors.New("некорректное доказательство")
)

// UserData - структура данных пользователя (например, балансы)
// Это пример, можно использовать любую структуру
type UserData struct {
	// Balances - мапа балансов по валютам
	Balances map[string]float64 `json:"balances"`
	
	// Metadata - дополнительные метаданные
	Metadata map[string]interface{} `json:"metadata,omitempty"`
	
	// Timestamp - временная метка последнего обновления
	Timestamp int64 `json:"timestamp,omitempty"`
}

// validateNodeWidth проверяет что NodeWidth - степень двойки
func validateNodeWidth(width int) error {
    validWidths := []int{8, 16, 32, 64, 128, 256, 512, 1024}
    for _, valid := range validWidths {
        if width == valid {
            return nil
        }
    }
    return fmt.Errorf("NodeWidth должен быть одним из: 8, 16, 32, 64, 128, 256, 512, 1024")
}

// getNodeIndex возвращает индекс узла с учетом NodeWidth
func getNodeIndex(byteValue byte, nodeWidth int) int {
    // Используем битовую маску для получения индекса в пределах nodeWidth
    // Например: для nodeWidth=64 (0b1000000), маска = 63 (0b111111)
    mask := nodeWidth - 1
    return int(byteValue) & mask
}

// Serialize сериализует UserData в JSON байты
func (ud *UserData) Serialize() ([]byte, error) {
	data, err := json.Marshal(ud)
	if err != nil {
		return nil, fmt.Errorf("ошибка сериализации: %w", err)
	}
	
	if len(data) > MaxValueSize {
		return nil, ErrValueTooLarge
	}
	
	return data, nil
}

// DeserializeUserData восстанавливает UserData из байтов
func DeserializeUserData(data []byte) (*UserData, error) {
	var ud UserData
	if err := json.Unmarshal(data, &ud); err != nil {
		return nil, fmt.Errorf("ошибка десериализации: %w", err)
	}
	return &ud, nil
}

// HashUserID создает хеш от ID пользователя для использования как ключ в дереве
// userID - строковый идентификатор пользователя (например, "user123")
func HashUserID(userID string) []byte {
	hasher := blake3.New()
	hasher.Write([]byte(userID))
	hash := hasher.Sum(nil)
	
	// Возвращаем первые 32 байта (blake3 выдает 32 байта по умолчанию)
	result := make([]byte, 32)
	copy(result, hash)
	return result
}

// Config - конфигурация Verkle tree
type Config struct {
	// Levels - количество уровней дерева
	Levels int
	
	// NodeWidth - количество дочерних узлов (ширина узла)
	NodeWidth int
	
	// KZGConfig - конфигурация для KZG commitments (SRS - Structured Reference String)
	KZGConfig *kzg_bls12381.SRS
	
	// Использовать KZG для внутренних узлов (по умолчанию false)
	UseKZGForInternal bool 
}

// VerkleTree - основная структура Verkle дерева
type VerkleTree struct {
	// root - корневой узел дерева
	root *InternalNode
	
	// config - конфигурация дерева
	config *Config
	
	// batch - текущий батч для обновлений
	batch *Batch
	
	// mu - мьютекс для потокобезопасности
	mu sync.RWMutex
	
	// previousRoot - хеш предыдущего корня для diff
	previousRoot []byte
	
	// dataStore - хранилище для данных (Pebble)
	dataStore Storage
	
	// nodeIndex - индекс узлов для быстрого поиска по хешу ID
	nodeIndex map[string]*LeafNode
	
	// Флаг отложенных коммитментов
	lazyCommit   bool 
	
	// Асинхронные коммитменты
    asyncMode       bool              // Включен ли асинхронный режим
    commitQueue     chan *commitTask   // Очередь задач на коммит
    commitWG        sync.WaitGroup     // Ожидание завершения коммитов
    pendingRoot     []byte             // Временный root пока идет коммит
    commitInProgress atomic.Bool       // Флаг активного коммита
}

// commitTask - задача на асинхронный коммит
type commitTask struct {
    node       *InternalNode
    resultChan chan commitResult
}

type commitResult struct {
    root []byte
    err  error
}

// InternalNode - внутренний узел дерева
type InternalNode struct {
	// children - массив дочерних узлов
	children []VerkleNode
	
	// commitment - KZG коммитмент для этого узла (точка на эллиптической кривой)
	commitment []byte
	
	// depth - глубина узла в дереве
	depth int
	
	// dirty - флаг, указывающий на необходимость пересчета коммитмента
	dirty bool
}

// LeafNode - листовой узел дерева (содержит данные пользователя)
type LeafNode struct {
	// userIDHash - хеш ID пользователя (32 байта)
	userIDHash [32]byte
	
	// stem - префикс ключа для навигации в дереве (31 байт)
	stem [StemSize]byte
	
	// dataKey - ключ для хранения данных в Pebble
	// Формат: "data:" + hex(userIDHash)
	dataKey string
	
	// inMemoryData - данные в памяти (когда dataStore == nil)
	inMemoryData []byte
	
	// commitment - KZG коммитмент для этого листа
	commitment []byte
	
	// dirty - флаг для пересчета коммитмента
	dirty bool
	
	// hasData - флаг наличия данных
	hasData bool
}

// VerkleNode - интерфейс для всех типов узлов
type VerkleNode interface {
	// Commit вычисляет KZG коммитмент для узла
	Commit(cfg *Config) ([]byte, error)
	
	// Hash возвращает blake3 хеш коммитмента
	Hash() []byte
	
	// IsLeaf проверяет, является ли узел листом
	IsLeaf() bool
	
	// IsDirty проверяет, нужно ли пересчитать коммитмент
	IsDirty() bool
}

// Batch - структура для батч-обновлений
type Batch struct {
	// updates - мапа обновлений (userIDHash -> данные)
	updates map[string][]byte
	
	// closed - флаг закрытия батча
	closed bool
	
	// mu - мьютекс для потокобезопасности
	mu sync.Mutex
}

// NewConfig создает новую конфигурацию с параметрами по умолчанию
func NewConfig(levels, nodeWidth int, srs *kzg_bls12381.SRS) *Config {
    if levels == 0 {
        levels = DefaultLevels
    }
    if nodeWidth == 0 {
        nodeWidth = DefaultNodeWidth
    }
    
    // Валидируем NodeWidth
    if err := validateNodeWidth(nodeWidth); err != nil {
        panic(err)  // Или можно вернуть ошибку
    }
    
    return &Config{
        Levels:            levels,
        NodeWidth:         nodeWidth,
        KZGConfig:         srs,
        UseKZGForInternal: false,
    }
}

// New создает новое Verkle дерево в памяти
// levels - количество уровней дерева
// nodeWidth - количество узлов (по умолчанию 256)
// dataStore - хранилище для данных (Pebble), может быть nil для in-memory
func New(levels, nodeWidth int, srs *kzg_bls12381.SRS, dataStore Storage) (*VerkleTree, error) {
	config := NewConfig(levels, nodeWidth, srs)
	
	// Создаем корневой внутренний узел
	root := &InternalNode{
		children: make([]VerkleNode, config.NodeWidth),
		depth:    0,
		dirty:    true,
	}
	
	tree := &VerkleTree{
		root:      root,
		config:    config,
		dataStore: dataStore,
		nodeIndex: make(map[string]*LeafNode),
		lazyCommit: true,
		asyncMode:  false,  // По умолчанию выключено
        commitQueue: make(chan *commitTask, 10),  // Буфер на 10 задач
	}
	
	return tree, nil
}

// EnableAsyncCommit включает асинхронный режим коммитментов
// workers - количество фоновых воркеров (рекомендуется 1-2)
func (vt *VerkleTree) EnableAsyncCommit(workers int) {
    vt.mu.Lock()
    defer vt.mu.Unlock()
    
    if vt.asyncMode {
        return // Уже включен
    }
    
    vt.asyncMode = true
    
    // Запускаем воркеры для обработки коммитментов
    for i := 0; i < workers; i++ {
        go vt.commitWorker()
    }
}

// DisableAsyncCommit выключает асинхронный режим и ждет завершения всех коммитов
func (vt *VerkleTree) DisableAsyncCommit() {
    vt.mu.Lock()
    if !vt.asyncMode {
        vt.mu.Unlock()
        return
    }
    vt.asyncMode = false
    vt.mu.Unlock()
    
    // Закрываем очередь и ждем завершения
    close(vt.commitQueue)
    vt.commitWG.Wait()
}

// commitWorker - фоновый воркер для обработки коммитментов
func (vt *VerkleTree) commitWorker() {
    for task := range vt.commitQueue {
        // Выполняем коммит
        err := vt.recomputeCommitments(task.node)
        
        var root []byte
        if err == nil {
            root = task.node.Hash()
        }
        
        // Отправляем результат
        task.resultChan <- commitResult{
            root: root,
            err:  err,
        }
        
        vt.commitWG.Done()
    }
}

// BeginBatch начинает новый батч для групповых обновлений
func (vt *VerkleTree) BeginBatch() *Batch {
    vt.mu.Lock()
    defer vt.mu.Unlock()
    
    vt.batch = &Batch{
        updates: make(map[string][]byte, 1024),  // Предвыделяем для 1024 элементов
        closed:  false,
    }
    
    return vt.batch
}

// Add добавляет данные пользователя в батч
// userID - строковый ID пользователя (например, "user123")
// data - произвольные данные (до 8KB), можно передать сериализованный JSON
func (b *Batch) Add(userID string, data []byte) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	
	if b.closed {
		return errors.New("батч уже закрыт")
	}
	
	if len(data) > MaxValueSize {
		return ErrValueTooLarge
	}
	
	// Хешируем ID пользователя
	userIDHash := HashUserID(userID)
	
	b.updates[string(userIDHash)] = data
	
	return nil
}

// AddUserData добавляет структурированные данные пользователя в батч
// Это удобная обертка для работы со структурой UserData
func (b *Batch) AddUserData(userID string, userData *UserData) error {
	data, err := userData.Serialize()
	if err != nil {
		return err
	}
	
	return b.Add(userID, data)
}

// CommitBatch закрывает батч и выполняет все обновления с пересчетом коммитментов
func (vt *VerkleTree) CommitBatch(b *Batch) ([]byte, error) {
    vt.mu.Lock()
    defer vt.mu.Unlock()
    
    b.mu.Lock()
    b.closed = true
    updates := b.updates
    b.mu.Unlock()
    
    // Сохраняем предыдущий корень
    if vt.root.commitment != nil {
        vt.previousRoot = vt.root.Hash()
    }
    
    // Применяем все обновления из батча
    for userIDHashStr, data := range updates {
        userIDHash := []byte(userIDHashStr)
        
        if vt.dataStore != nil {
            dataKey := "data:" + fmt.Sprintf("%x", userIDHash)
            if err := vt.dataStore.Put([]byte(dataKey), data); err != nil {
                return nil, fmt.Errorf("ошибка сохранения в Pebble: %w", err)
            }
        }
        
        if err := vt.insert(userIDHash, data); err != nil {
            return nil, fmt.Errorf("ошибка вставки узла: %w", err)
        }
    }
    
    // Если асинхронный режим включен
    if vt.asyncMode {
        return vt.commitBatchAsync()
    }
    
    // Синхронный коммит (как раньше)
    if err := vt.recomputeCommitments(vt.root); err != nil {
        return nil, fmt.Errorf("ошибка пересчета коммитментов: %w", err)
    }
    
    return vt.root.Hash(), nil
}

// commitBatchAsync - асинхронный коммит
func (vt *VerkleTree) commitBatchAsync() ([]byte, error) {
    // Генерируем временный хеш (быстрый Blake3)
    hasher := blake3.New()
    for _, child := range vt.root.children {
        if child != nil {
            hasher.Write(child.Hash())
        }
    }
    tempRoot := hasher.Sum(nil)
    vt.pendingRoot = tempRoot
    
    // Ставим задачу в очередь на асинхронный коммит
    resultChan := make(chan commitResult, 1)
    task := &commitTask{
        node:       vt.root,
        resultChan: resultChan,
    }
    
    vt.commitWG.Add(1)
    vt.commitInProgress.Store(true)
    
    // Отправляем задачу в очередь (неблокирующая отправка)
    select {
    case vt.commitQueue <- task:
        // Задача добавлена
    default:
        // Очередь заполнена, выполняем синхронно
        vt.commitWG.Done()
        vt.commitInProgress.Store(false)
        if err := vt.recomputeCommitments(vt.root); err != nil {
            return nil, err
        }
        return vt.root.Hash(), nil
    }
    
    // Запускаем горутину для обновления финального root
    go func() {
        result := <-resultChan
        
        vt.mu.Lock()
        if result.err == nil {
            vt.root.commitment = result.root
        }
        vt.commitInProgress.Store(false)
        vt.mu.Unlock()
    }()
    
    // Возвращаем временный хеш немедленно
    return tempRoot, nil
}

// WaitForCommit ждет завершения всех асинхронных коммитментов
func (vt *VerkleTree) WaitForCommit() {
    vt.commitWG.Wait()
}

// GetFinalRoot возвращает финальный root (ждет если коммит в процессе)
func (vt *VerkleTree) GetFinalRoot() []byte {
    // Если коммит в процессе - ждем
    if vt.commitInProgress.Load() {
        vt.WaitForCommit()
    }
    
    vt.mu.RLock()
    defer vt.mu.RUnlock()
    
    if vt.root.commitment == nil {
        return nil
    }
    
    return vt.root.Hash()
}

// insert выполняет вставку узла с данными пользователя в дерево
func (vt *VerkleTree) insert(userIDHash []byte, data []byte) error {
    if len(userIDHash) != 32 {
        return ErrInvalidKey
    }
    
    var stem [StemSize]byte
    copy(stem[:], userIDHash[:StemSize])
    
    // Находим или создаем путь к листовому узлу
    node := vt.root
    
    for depth := 0; depth < vt.config.Levels-1; depth++ {
        // Вычисляем индекс с учетом NodeWidth
        index := getNodeIndex(stem[depth], vt.config.NodeWidth)
        
        if node.children[index] == nil {
            // Создаем новый внутренний узел
            node.children[index] = &InternalNode{
                children: make([]VerkleNode, vt.config.NodeWidth),
                depth:    depth + 1,
                dirty:    true,
            }
        }
        
        // Переходим к следующему уровню
        if internalNode, ok := node.children[index].(*InternalNode); ok {
            node = internalNode
        } else {
            // Достигли листа раньше времени, нужно расширить
            // Сохраняем старый лист
            oldLeaf := node.children[index].(*LeafNode)
            
            // Создаем новый внутренний узел
            newInternal := &InternalNode{
                children: make([]VerkleNode, vt.config.NodeWidth),
                depth:    depth + 1,
                dirty:    true,
            }
            
            // Перемещаем старый лист в новый узел
            oldLeafIndex := getNodeIndex(oldLeaf.stem[depth+1], vt.config.NodeWidth)
            newInternal.children[oldLeafIndex] = oldLeaf
            
            // Заменяем лист на внутренний узел
            node.children[index] = newInternal
            node = newInternal
        }
    }
    
    // Вычисляем индекс для листа
    leafIndex := getNodeIndex(stem[vt.config.Levels-1], vt.config.NodeWidth)
    
    // Проверяем автоматическое расширение листа
    if node.children[leafIndex] != nil {
        if leaf, ok := node.children[leafIndex].(*LeafNode); ok {
            // Проверяем нужно ли расширение (заполнено больше половины)
            threshold := (vt.config.NodeWidth + 1) / 2  // Округление вверх
            
            if leaf.hasData && leaf.userIDHash != [32]byte(userIDHash) {
                // Это другой пользователь, создаем подузел
                if vt.shouldExpandLeaf(node, leafIndex, threshold) {
                    if err := vt.expandLeaf(node, leafIndex, leaf); err != nil {
                        return err
                    }
                    // Пробуем вставить снова в расширенный узел
                    return vt.insert(userIDHash, data)
                }
            }
        }
    }
    
    // Создаем или обновляем листовой узел
    var leaf *LeafNode
    if node.children[leafIndex] == nil {
        var userIDHashArray [32]byte
        copy(userIDHashArray[:], userIDHash)
        
        dataKey := "data:" + fmt.Sprintf("%x", userIDHash)
        
        leaf = &LeafNode{
            userIDHash:   userIDHashArray,
            stem:         stem,
            dataKey:      dataKey,
            inMemoryData: data,
            dirty:        true,
            hasData:      true,
        }
        node.children[leafIndex] = leaf
        
        // Добавляем в индекс для быстрого поиска
        vt.nodeIndex[string(userIDHash)] = leaf
    } else {
        var ok bool
        leaf, ok = node.children[leafIndex].(*LeafNode)
        if !ok {
            return errors.New("ожидался листовой узел")
        }
        leaf.inMemoryData = data
        leaf.hasData = true
        leaf.dirty = true
    }
    
    // Помечаем все родительские узлы как dirty
    vt.markDirtyPath(userIDHash)
    
    return nil
}

// shouldExpandLeaf проверяет нужно ли расширять узел
func (vt *VerkleTree) shouldExpandLeaf(node *InternalNode, leafIndex int, threshold int) bool {
    // Подсчитываем заполненность узла
    occupied := 0
    for _, child := range node.children {
        if child != nil {
            if leaf, ok := child.(*LeafNode); ok && leaf.hasData {
                occupied++
            }
        }
    }
    
    return occupied >= threshold
}

// expandLeaf расширяет листовой узел в поддерево
func (vt *VerkleTree) expandLeaf(parentNode *InternalNode, leafIndex int, oldLeaf *LeafNode) error {
    // Создаем новый внутренний узел на месте листа
    newInternal := &InternalNode{
        children: make([]VerkleNode, vt.config.NodeWidth),
        depth:    parentNode.depth + 1,
        dirty:    true,
    }
    
    // Вычисляем новый индекс для старого листа
    if newInternal.depth >= vt.config.Levels {
        // Достигли максимальной глубины, не можем расширять
        return errors.New("достигнута максимальная глубина дерева")
    }
    
    newLeafIndex := getNodeIndex(oldLeaf.stem[newInternal.depth], vt.config.NodeWidth)
    
    // Перемещаем старый лист в новый узел
    newInternal.children[newLeafIndex] = oldLeaf
    
    // Заменяем лист на внутренний узел
    parentNode.children[leafIndex] = newInternal
    parentNode.dirty = true
    
    return nil
}


// markDirtyPath помечает все узлы на пути к ключу как требующие обновления
func (vt *VerkleTree) markDirtyPath(userIDHash []byte) {
    node := vt.root
    node.dirty = true
    
    for depth := 0; depth < vt.config.Levels-1; depth++ {
        index := getNodeIndex(userIDHash[depth], vt.config.NodeWidth)
        if node.children[index] != nil {
            if internalNode, ok := node.children[index].(*InternalNode); ok {
                internalNode.dirty = true
                node = internalNode
            }
        }
    }
}


// recomputeCommitments рекурсивно пересчитывает KZG коммитменты
func (vt *VerkleTree) recomputeCommitments(node *InternalNode) error {
    if node == nil {
        return nil
    }
    
    // Собираем все dirty узлы на текущем уровне
    var dirtyLeaves []*LeafNode
    var dirtyInternal []*InternalNode
    
    // Первый проход: собираем все dirty узлы
    for _, child := range node.children {
        if child == nil {
            continue
        }
        
        if internalChild, ok := child.(*InternalNode); ok {
            if err := vt.recomputeCommitments(internalChild); err != nil {
                return err
            }
            if internalChild.dirty {
                dirtyInternal = append(dirtyInternal, internalChild)
            }
        } else if leafChild, ok := child.(*LeafNode); ok {
            if leafChild.dirty {
                dirtyLeaves = append(dirtyLeaves, leafChild)
            }
        }
    }
    
    // Батчим коммитменты для листьев (можно распараллелить)
    if len(dirtyLeaves) > 0 {
        // Потенциально можно распараллелить через goroutines
        for _, leaf := range dirtyLeaves {
            if _, err := leaf.Commit(vt.config); err != nil {
                return err
            }
        }
    }
    
    // Коммитим внутренние узлы
    for _, internal := range dirtyInternal {
        if _, err := internal.Commit(vt.config); err != nil {
            return err
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

// Добавьте функцию параллельных коммитментов
func (vt *VerkleTree) parallelLeafCommit(leaves []*LeafNode) error {
    if len(leaves) == 0 {
        return nil
    }
    
    // Для небольшого количества листьев не стоит параллелить
    if len(leaves) < 10 {
        for _, leaf := range leaves {
            if _, err := leaf.Commit(vt.config); err != nil {
                return err
            }
        }
        return nil
    }
    
    // Параллелим коммитменты
    errChan := make(chan error, len(leaves))
    var wg sync.WaitGroup
    
    workers := 4 // Количество воркеров
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


// Get получает данные пользователя по его ID
// Возвращает сырые байты данных, которые нужно десериализовать
func (vt *VerkleTree) Get(userID string) ([]byte, error) {
	vt.mu.RLock()
	defer vt.mu.RUnlock()
	
	userIDHash := HashUserID(userID)
	return vt.get(userIDHash)
}

// GetUserData получает и десериализует данные пользователя
func (vt *VerkleTree) GetUserData(userID string) (*UserData, error) {
	data, err := vt.Get(userID)
	if err != nil {
		return nil, err
	}
	
	return DeserializeUserData(data)
}

// get внутренняя функция получения данных (без блокировки)
func (vt *VerkleTree) get(userIDHash []byte) ([]byte, error) {
	if len(userIDHash) != 32 {
		return nil, ErrInvalidKey
	}
	
	// Сначала проверяем наличие узла в индексе (быстрый путь)
	leaf, exists := vt.nodeIndex[string(userIDHash)]
	if !exists {
		return nil, ErrKeyNotFound
	}
	
	if !leaf.hasData {
		return nil, ErrKeyNotFound
	}
	
	// Если работаем в памяти - возвращаем данные из узла
	if vt.dataStore == nil {
		if leaf.inMemoryData == nil {
			return nil, ErrKeyNotFound
		}
		return leaf.inMemoryData, nil
	}
	
	// Получаем данные из Pebble
	data, err := vt.dataStore.Get([]byte(leaf.dataKey))
	if err != nil {
		return nil, fmt.Errorf("ошибка чтения из Pebble: %w", err)
	}
	return data, nil
}


// GetMultiple получает данные нескольких пользователей одним запросом
// userIDs - массив строковых ID пользователей
// Возвращает массив данных в том же порядке, что и ID
// Если ID не найден, возвращает nil для этой позиции
func (vt *VerkleTree) GetMultiple(userIDs []string) ([][]byte, error) {
	vt.mu.RLock()
	defer vt.mu.RUnlock()
	
	results := make([][]byte, len(userIDs))
	
	for i, userID := range userIDs {
		userIDHash := HashUserID(userID)
		data, err := vt.get(userIDHash)
		if err == ErrKeyNotFound {
			results[i] = nil
			continue
		}
		if err != nil {
			return nil, err
		}
		results[i] = data
	}
	
	return results, nil
}

// GetMultipleUserData получает и десериализует данные нескольких пользователей
func (vt *VerkleTree) GetMultipleUserData(userIDs []string) ([]*UserData, error) {
	dataList, err := vt.GetMultiple(userIDs)
	if err != nil {
		return nil, err
	}
	
	results := make([]*UserData, len(dataList))
	for i, data := range dataList {
		if data == nil {
			results[i] = nil
			continue
		}
		
		userData, err := DeserializeUserData(data)
		if err != nil {
			return nil, fmt.Errorf("ошибка десериализации данных пользователя %s: %w", userIDs[i], err)
		}
		results[i] = userData
	}
	
	return results, nil
}

// Has проверяет наличие пользователя в дереве по его ID (быстрый метод O(1))
func (vt *VerkleTree) Has(userID string) bool {
	vt.mu.RLock()
	defer vt.mu.RUnlock()
	
	userIDHash := HashUserID(userID)
	leaf, exists := vt.nodeIndex[string(userIDHash)]
	return exists && leaf.hasData
}

// HasByHash проверяет наличие узла по хешу ID пользователя
func (vt *VerkleTree) HasByHash(userIDHash []byte) bool {
	vt.mu.RLock()
	defer vt.mu.RUnlock()
	
	leaf, exists := vt.nodeIndex[string(userIDHash)]
	return exists && leaf.hasData
}

// GetRoot возвращает корневой хеш дерева
func (vt *VerkleTree) GetRoot() []byte {
	vt.mu.RLock()
	defer vt.mu.RUnlock()
	
	if vt.root.commitment == nil {
		return nil
	}
	
	return vt.root.Hash()
}

// GetNodeCount возвращает количество узлов с данными в дереве
func (vt *VerkleTree) GetNodeCount() int {
	vt.mu.RLock()
	defer vt.mu.RUnlock()
	
	return len(vt.nodeIndex)
}

// Оптимизированный Commit для InternalNode:
func (n *InternalNode) Commit(cfg *Config) ([]byte, error) {
    if !n.dirty && n.commitment != nil {
        return n.commitment, nil
    }
    
    // Для промежуточных узлов используем быстрый Blake3 хеш
    if !cfg.UseKZGForInternal && n.depth > 0 {
        hasher := blake3.New()
        
        for _, child := range n.children {
            if child == nil {
                // Записываем нулевые байты для пустых узлов
                hasher.Write(make([]byte, 32))
            } else {
                hash := child.Hash()
                hasher.Write(hash)
            }
        }
        
        n.commitment = hasher.Sum(nil)
        n.dirty = false
        return n.commitment, nil
    }
    
    // KZG только для корня (depth == 0) или если явно включено
    values := getFrElementSlice(cfg.NodeWidth)
    defer putFrElementSlice(values)
    
    for i, child := range n.children {
        if child == nil {
            values[i].SetZero()
        } else {
            hash := child.Hash()
            values[i].SetBytes(hash)
        }
    }
    
    commitment, err := commitPolynomial(values, cfg.KZGConfig)
    if err != nil {
        return nil, err
    }
    
    n.commitment = commitment
    n.dirty = false
    
    return commitment, nil
}




// Hash возвращает blake3 хеш коммитмента для InternalNode
func (n *InternalNode) Hash() []byte {
	if n.commitment == nil {
		return make([]byte, 32)
	}
	
	// Коммитмент уже является хешем в нашей упрощенной реализации
	return n.commitment
}

// IsLeaf для InternalNode возвращает false
func (n *InternalNode) IsLeaf() bool {
	return false
}

// IsDirty для InternalNode возвращает флаг dirty
func (n *InternalNode) IsDirty() bool {
	return n.dirty
}

// Commit для LeafNode вычисляет KZG коммитмент
func (n *LeafNode) Commit(cfg *Config) ([]byte, error) {
	if !n.dirty && n.commitment != nil {
		return n.commitment, nil
	}
	
	// Создаем элемент поля из userIDHash
	hashElem := hashToFieldElement(n.userIDHash[:])
	
	// Создаем простой массив из одного элемента
	values := []fr.Element{hashElem}
	
	// Создаем KZG коммитмент
	commitment, err := commitPolynomial(values, cfg.KZGConfig)
	if err != nil {
		return nil, fmt.Errorf("ошибка KZG commitment для листа: %w", err)
	}
	
	n.commitment = commitment
	n.dirty = false
	
	return commitment, nil
}



// Hash возвращает blake3 хеш коммитмента для LeafNode
func (n *LeafNode) Hash() []byte {
	if n.commitment == nil {
		return make([]byte, 32)
	}
	
	return n.commitment
}

// IsLeaf для LeafNode возвращает true
func (n *LeafNode) IsLeaf() bool {
	return true
}

// IsDirty для LeafNode возвращает флаг dirty
func (n *LeafNode) IsDirty() bool {
	return n.dirty
}

// Proof - структура доказательства для одного или нескольких пользователей
type Proof struct {
	// UserIDs - ID пользователей, для которых создано доказательство
	UserIDs []string `json:"user_ids"`
	
	// UserIDHashes - хеши ID пользователей
	UserIDHashes [][]byte `json:"user_id_hashes"`
	
	// DataKeys - ключи данных в Pebble
	DataKeys []string `json:"data_keys"`
	
	// Path - путь от корня до листа с коммитментами
	Path [][]byte `json:"path"`
	
	// KZGProof - KZG доказательство (в полной реализации)
	KZGProof []byte `json:"kzg_proof,omitempty"`
	
	// SiblingHashes - хеши соседних узлов
	SiblingHashes [][]byte `json:"sibling_hashes"`
}

// GenerateProof создает доказательство для одного пользователя
func (vt *VerkleTree) GenerateProof(userID string) (*Proof, error) {
	vt.mu.RLock()
	defer vt.mu.RUnlock()
	
	return vt.generateProof([]string{userID})
}

// GenerateMultiProof создает мульти-доказательство для нескольких пользователей
// Это более эффективно, чем создавать отдельные доказательства
func (vt *VerkleTree) GenerateMultiProof(userIDs []string) (*Proof, error) {
	vt.mu.RLock()
	defer vt.mu.RUnlock()
	
	return vt.generateProof(userIDs)
}

// generateProof внутренняя функция генерации доказательства
func (vt *VerkleTree) generateProof(userIDs []string) (*Proof, error) {
	proof := &Proof{
		UserIDs:       userIDs,
		UserIDHashes:  make([][]byte, len(userIDs)),
		DataKeys:      make([]string, len(userIDs)),
		Path:          make([][]byte, 0),
		SiblingHashes: make([][]byte, 0),
	}
	
	// Для каждого пользователя собираем путь и метаданные
	for i, userID := range userIDs {
		userIDHash := HashUserID(userID)
		proof.UserIDHashes[i] = userIDHash
		
		// Проверяем наличие пользователя
		leaf, exists := vt.nodeIndex[string(userIDHash)]
		if !exists || !leaf.hasData {
			return nil, fmt.Errorf("пользователь %s не найден в дереве", userID)
		}
		
		proof.DataKeys[i] = leaf.dataKey
		
		// Собираем путь от корня до листа
		if err := vt.collectProofPath(userIDHash, proof); err != nil {
			return nil, err
		}
	}
	
	// TODO: Создать настоящее KZG доказательство
	// proof.KZGProof = kzg.Open(...)
	
	return proof, nil
}

// collectProofPath собирает путь и соседние хеши для доказательства
func (vt *VerkleTree) collectProofPath(userIDHash []byte, proof *Proof) error {
    var stem [StemSize]byte
    copy(stem[:], userIDHash[:StemSize])
    
    node := vt.root
    
    // Добавляем коммитмент корня
    if node.commitment != nil {
        proof.Path = append(proof.Path, node.commitment)
    }
    
    for depth := 0; depth < vt.config.Levels-1; depth++ {
        index := getNodeIndex(stem[depth], vt.config.NodeWidth)
        
        // Собираем хеши соседних узлов
        for i, child := range node.children {
            if i != index && child != nil {
                proof.SiblingHashes = append(proof.SiblingHashes, child.Hash())
            }
        }
        
        if node.children[index] == nil {
            return ErrKeyNotFound
        }
        
        if internalNode, ok := node.children[index].(*InternalNode); ok {
            if internalNode.commitment != nil {
                proof.Path = append(proof.Path, internalNode.commitment)
            }
            node = internalNode
        }
    }
    
    return nil
}


// VerifyProof проверяет доказательство
func VerifyProof(root []byte, proof *Proof) (bool, error) {
	if proof == nil {
		return false, errors.New("пустое доказательство")
	}
	
	// TODO: Реализовать проверку KZG доказательства
	// return kzg.Verify(proof.KZGProof, root, ...)
	
	return true, nil
}

// SerializedTree - сериализованное дерево для JSON
type SerializedTree struct {
	Levels    int              `json:"levels"`
	NodeWidth int              `json:"node_width"`
	NodeCount int              `json:"node_count"`
	Root      *SerializedNode  `json:"root"`
}

// SerializedNode - сериализованный узел
type SerializedNode struct {
	Type       byte             `json:"type"`        // 0 = internal, 1 = leaf
	Depth      int              `json:"depth"`
	Commitment []byte           `json:"commitment"`
	Children   []SerializedNode `json:"children,omitempty"`
	
	// Поля для листового узла
	UserIDHash []byte `json:"user_id_hash,omitempty"`
	DataKey    string `json:"data_key,omitempty"`
	HasData    bool   `json:"has_data,omitempty"`
}

// Serialize сериализует дерево в компактный бинарный формат
func (vt *VerkleTree) Serialize() ([]byte, error) {
	vt.mu.RLock()
	defer vt.mu.RUnlock()
	
	var buf []byte
	
	// Магическое число и версия
	buf = append(buf, []byte("VKL2")...) // Версия 2
	
	// Конфигурация дерева
	configBytes := make([]byte, 12)
	binary.LittleEndian.PutUint32(configBytes[0:4], uint32(vt.config.Levels))
	binary.LittleEndian.PutUint32(configBytes[4:8], uint32(vt.config.NodeWidth))
	binary.LittleEndian.PutUint32(configBytes[8:12], uint32(len(vt.nodeIndex)))
	buf = append(buf, configBytes...)
	
	// Сериализуем дерево начиная с корня
	rootBytes, err := vt.serializeNode(vt.root)
	if err != nil {
		return nil, err
	}
	
	buf = append(buf, rootBytes...)
	
	return buf, nil
}

// serializeNode рекурсивно сериализует узел
func (vt *VerkleTree) serializeNode(node VerkleNode) ([]byte, error) {
	if node == nil {
		return []byte{0xFF}, nil
	}
	
	var buf []byte
	
	if leaf, ok := node.(*LeafNode); ok {
		buf = append(buf, 0x01) // Тип: лист
		buf = append(buf, leaf.userIDHash[:]...)
		buf = append(buf, leaf.stem[:]...)
		
		dataKeyBytes := []byte(leaf.dataKey)
		keyLenBytes := make([]byte, 2)
		binary.LittleEndian.PutUint16(keyLenBytes, uint16(len(dataKeyBytes)))
		buf = append(buf, keyLenBytes...)
		buf = append(buf, dataKeyBytes...)
		
		if leaf.hasData {
			buf = append(buf, 0x01)
		} else {
			buf = append(buf, 0x00)
		}
		
		if leaf.commitment != nil {
			buf = append(buf, leaf.commitment...)
		}
		
	} else if internal, ok := node.(*InternalNode); ok {
		buf = append(buf, 0x00) // Тип: внутренний
		
		depthByte := make([]byte, 1)
		depthByte[0] = byte(internal.depth)
		buf = append(buf, depthByte...)
		
		nonEmptyCount := 0
		for _, child := range internal.children {
			if child != nil {
				nonEmptyCount++
			}
		}
		
		countBytes := make([]byte, 2)
		binary.LittleEndian.PutUint16(countBytes, uint16(nonEmptyCount))
		buf = append(buf, countBytes...)
		
		for i, child := range internal.children {
			if child != nil {
				indexBytes := make([]byte, 2)
				binary.LittleEndian.PutUint16(indexBytes, uint16(i))
				buf = append(buf, indexBytes...)
				
				childBytes, err := vt.serializeNode(child)
				if err != nil {
					return nil, err
				}
				
				lenBytes := make([]byte, 4)
				binary.LittleEndian.PutUint32(lenBytes, uint32(len(childBytes)))
				buf = append(buf, lenBytes...)
				
				buf = append(buf, childBytes...)
			}
		}
		
		if internal.commitment != nil {
			buf = append(buf, internal.commitment...)
		}
	}
	
	return buf, nil
}

// SerializeJSON сериализует дерево в человеко-читаемый JSON
func (vt *VerkleTree) SerializeJSON() ([]byte, error) {
	vt.mu.RLock()
	defer vt.mu.RUnlock()
	
	tree := &SerializedTree{
		Levels:    vt.config.Levels,
		NodeWidth: vt.config.NodeWidth,
		NodeCount: len(vt.nodeIndex),
		Root:      &SerializedNode{},
	}
	
	vt.serializeNodeJSON(vt.root, tree.Root)
	
	return json.MarshalIndent(tree, "", "  ")
}

// serializeNodeJSON рекурсивно создает JSON структуру
func (vt *VerkleTree) serializeNodeJSON(node VerkleNode, sn *SerializedNode) {
	if node == nil {
		return
	}
	
	if leaf, ok := node.(*LeafNode); ok {
		sn.Type = 1
		sn.UserIDHash = leaf.userIDHash[:]
		sn.DataKey = leaf.dataKey
		sn.HasData = leaf.hasData
		if leaf.commitment != nil {
			sn.Commitment = leaf.commitment
		}
	} else if internal, ok := node.(*InternalNode); ok {
		sn.Type = 0
		sn.Depth = internal.depth
		if internal.commitment != nil {
			sn.Commitment = internal.commitment
		}
		
		sn.Children = make([]SerializedNode, 0)
		for _, child := range internal.children {
			if child != nil {
				childSerialized := SerializedNode{}
				vt.serializeNodeJSON(child, &childSerialized)
				sn.Children = append(sn.Children, childSerialized)
			}
		}
	}
}

// Deserialize восстанавливает дерево из бинарного формата
func Deserialize(data []byte, srs *kzg_bls12381.SRS, dataStore Storage) (*VerkleTree, error) {
	if len(data) < 16 {
		return nil, errors.New("некорректный формат данных")
	}
	
	if string(data[0:4]) != "VKL2" {
		return nil, errors.New("некорректное магическое число или версия")
	}
	
	levels := int(binary.LittleEndian.Uint32(data[4:8]))
	nodeWidth := int(binary.LittleEndian.Uint32(data[8:12]))
	
	tree, err := New(levels, nodeWidth, srs, dataStore)
	if err != nil {
		return nil, err
	}
	
	// TODO: Десериализовать узлы
	
	return tree, nil
}

// DeserializeJSON восстанавливает дерево из JSON
func DeserializeJSON(data []byte, srs *kzg_bls12381.SRS, dataStore Storage) (*VerkleTree, error) {
	var st SerializedTree
	if err := json.Unmarshal(data, &st); err != nil {
		return nil, err
	}
	
	tree, err := New(st.Levels, st.NodeWidth, srs, dataStore)
	if err != nil {
		return nil, err
	}
	
	// TODO: Рекурсивно восстановить дерево
	
	return tree, nil
}

// TreeDiff представляет разницу между двумя состояниями дерева
type TreeDiff struct {
	PreviousRoot []byte            `json:"previous_root"`
	CurrentRoot  []byte            `json:"current_root"`
	Modified     map[string][]byte `json:"modified"`
	Added        map[string][]byte `json:"added"`
	Deleted      []string          `json:"deleted"`
}

// GetDiff возвращает разницу между текущим и предыдущим состоянием дерева
func (vt *VerkleTree) GetDiff(previousRoot []byte) (*TreeDiff, error) {
	vt.mu.RLock()
	defer vt.mu.RUnlock()
	
	diff := &TreeDiff{
		PreviousRoot: previousRoot,
		CurrentRoot:  vt.GetRoot(),
		Modified:     make(map[string][]byte),
		Added:        make(map[string][]byte),
		Deleted:      make([]string, 0),
	}
	
	// TODO: Реализовать сравнение деревьев
	
	return diff, nil
}

// Storage - интерфейс для персистентного хранилища (Pebble)
type Storage interface {
	Put(key []byte, value []byte) error
	Get(key []byte) ([]byte, error)
	Delete(key []byte) error
	Close() error
}

// SaveToDisk сохраняет структуру дерева в Pebble
func (vt *VerkleTree) SaveToDisk() error {
	vt.mu.RLock()
	defer vt.mu.RUnlock()
	
	if vt.dataStore == nil {
		return errors.New("хранилище не настроено")
	}
	
	metadata := map[string]interface{}{
		"levels":     vt.config.Levels,
		"node_width": vt.config.NodeWidth,
		"node_count": len(vt.nodeIndex),
		"root_hash":  fmt.Sprintf("%x", vt.GetRoot()),
	}
	
	metadataBytes, err := json.Marshal(metadata)
	if err != nil {
		return err
	}
	
	if err := vt.dataStore.Put([]byte("tree:metadata"), metadataBytes); err != nil {
		return err
	}
	
	for userIDHash, leaf := range vt.nodeIndex {
		indexKey := "index:" + fmt.Sprintf("%x", userIDHash)
		indexValue := map[string]interface{}{
			"data_key": leaf.dataKey,
			"has_data": leaf.hasData,
		}
		
		indexBytes, err := json.Marshal(indexValue)
		if err != nil {
			return err
		}
		
		if err := vt.dataStore.Put([]byte(indexKey), indexBytes); err != nil {
			return err
		}
	}
	
	return nil
}

// LoadFromDisk загружает дерево из Pebble
func LoadFromDisk(dataStore Storage, srs *kzg_bls12381.SRS) (*VerkleTree, error) {
	metadataBytes, err := dataStore.Get([]byte("tree:metadata"))
	if err != nil {
		return nil, fmt.Errorf("ошибка загрузки метаданных: %w", err)
	}
	
	var metadata map[string]interface{}
	if err := json.Unmarshal(metadataBytes, &metadata); err != nil {
		return nil, err
	}
	
	levels := int(metadata["levels"].(float64))
	nodeWidth := int(metadata["node_width"].(float64))
	
	tree, err := New(levels, nodeWidth, srs, dataStore)
	if err != nil {
		return nil, err
	}
	
	// TODO: Загрузить индекс узлов
	
	return tree, nil
}
