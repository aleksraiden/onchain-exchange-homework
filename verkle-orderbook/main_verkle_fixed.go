package main

import (
//	"bufio"
	"encoding/binary"
	"fmt"
	"math/rand"
	"sort"
	"sync"
	"sync/atomic"
	"time"
//	"encoding/hex"
//	"encoding/json"
	"os"
	"runtime/pprof"
	"github.com/zeebo/blake3"
)

// Константы системы
const (
	VERKLE_WIDTH    = 16                      // Ширина Verkle дерева
	VERKLE_DEPTH    = 16                      // Глубина дерева (64 бита / 4 = 16 nibbles)
	PRICE_DECIMALS  = 100                     // Точность цены (2 знака после запятой)
	MAX_TRADERS     = 10000                   // Максимальное количество трейдеров
	HASH_INTERVAL   = 0 * time.Millisecond    // Интервал хеширования (0 = выключено)
)

// Слоты для распределения ордеров
const (
	SLOT_MM_LIQUIDATION = 0  // Ликвидации маркет-мейкеров
	SLOT_VIP            = 1  // VIP-трейдеры
	SLOT_SMALL_RETAIL   = 2  // Мелкие retail ордера (<$10)
	SLOT_RETAIL_START   = 3  // Начало диапазона для retail
	SLOT_RETAIL_END     = 14 // Конец диапазона для retail
	SLOT_RESERVED       = 15 // Зарезервированный слот
)

// TreePrintMode - режим вывода дерева
type TreePrintMode int

const (
	TREE_PRINT_COMPACT TreePrintMode = iota // Топ N уровней
	TREE_PRINT_SUMMARY                      // Только статистика
	TREE_PRINT_FULL                         // Полное дерево
)

// NodeType - тип узла в дереве
type NodeType int

const (
	NODE_ROOT       NodeType = iota // Корневой узел
	NODE_BUY_SIDE                   // Узел BUY стороны
	NODE_SELL_SIDE                  // Узел SELL стороны
	NODE_INNER                      // Промежуточный узел
	NODE_LEAF                       // Листовой узел
)

func (nt NodeType) String() string {
	switch nt {
	case NODE_ROOT:
		return "ROOT"
	case NODE_BUY_SIDE:
		return "BUY_SIDE"
	case NODE_SELL_SIDE:
		return "SELL_SIDE"
	case NODE_INNER:
		return "INNER"
	case NODE_LEAF:
		return "LEAF"
	default:
		return "UNKNOWN"
	}
}

// Memory Pools
var (
	orderPool = sync.Pool{
		New: func() interface{} {
			return &Order{}
		},
	}

	priceLevelPool = sync.Pool{
		New: func() interface{} {
			return &PriceLevel{}
		},
	}

	verkleNodePool = sync.Pool{
		New: func() interface{} {
			return &VerkleNode{}
		},
	}

	hashBufferPool = sync.Pool{
		New: func() interface{} {
			return make([]byte, 0, 1024)
		},
	}
)

// SlotMetadata содержит статические метаданные слота
type SlotMetadata struct {
	Index       int
	Name        string
	Description string
	Priority    int
}

// SlotMetadataTable - статическая таблица метаданных всех слотов
var SlotMetadataTable = [VERKLE_WIDTH]SlotMetadata{
	{Index: 0, Name: "MM_LIQUIDATION", Description: "Ликвидации маркет-мейкеров", Priority: 0},
	{Index: 1, Name: "VIP", Description: "VIP-трейдеры", Priority: 1},
	{Index: 2, Name: "SMALL_RETAIL", Description: "Мелкие retail (<$10)", Priority: 2},
	{Index: 3, Name: "RETAIL_3", Description: "Retail ордера (группа 3)", Priority: 3},
	{Index: 4, Name: "RETAIL_4", Description: "Retail ордера (группа 4)", Priority: 3},
	{Index: 5, Name: "RETAIL_5", Description: "Retail ордера (группа 5)", Priority: 3},
	{Index: 6, Name: "RETAIL_6", Description: "Retail ордера (группа 6)", Priority: 3},
	{Index: 7, Name: "RETAIL_7", Description: "Retail ордера (группа 7)", Priority: 3},
	{Index: 8, Name: "RETAIL_8", Description: "Retail ордера (группа 8)", Priority: 3},
	{Index: 9, Name: "RETAIL_9", Description: "Retail ордера (группа 9)", Priority: 3},
	{Index: 10, Name: "RETAIL_10", Description: "Retail ордера (группа 10)", Priority: 3},
	{Index: 11, Name: "RETAIL_11", Description: "Retail ордера (группа 11)", Priority: 3},
	{Index: 12, Name: "RETAIL_12", Description: "Retail ордера (группа 12)", Priority: 3},
	{Index: 13, Name: "RETAIL_13", Description: "Retail ордера (группа 13)", Priority: 3},
	{Index: 14, Name: "RETAIL_14", Description: "Retail ордера (группа 14)", Priority: 3},
	{Index: 15, Name: "RESERVED", Description: "Зарезервированный слот", Priority: 99},
}

// Side - сторона ордера
type Side int

const (
	BUY Side = iota
	SELL
)

func (s Side) String() string {
	if s == BUY {
		return "BUY"
	}
	return "SELL"
}

// Trade - структура исполненной сделки
type Trade struct {
	TradeID       uint64
	TakerOrderID  uint64
	MakerOrderID  uint64
	TakerTraderID uint32
	MakerTraderID uint32
	Price         uint64
	Size          uint64
	TakerSide     Side
	TakerPartial  bool
	MakerPartial  bool
	Timestamp     int64
}

// Order - структура ордера
type Order struct {
	ID            uint64
	TraderID      uint32
	Price         uint64
	Size          uint64
	FilledSize    uint64
	Side          Side
	Slot          uint8
	IsPartialFill bool
}

func (o *Order) RemainingSize() uint64 {
	if o.FilledSize >= o.Size {
		return 0
	}
	return o.Size - o.FilledSize
}

func (o *Order) IsFilled() bool {
	return o.FilledSize >= o.Size
}

// PriceLevel - уровень цены, содержит слоты с ордерами
type PriceLevel struct {
	Price       uint64
	TotalVolume uint64
	Slots       [VERKLE_WIDTH]*Slot
}

// Slot - слот внутри ценового уровня
type Slot struct {
	Metadata *SlotMetadata
	Orders   []*Order
	Volume   uint64
}

// VerkleNode - узел Verkle дерева
type VerkleNode struct {
	Hash     [32]byte
	Children [VERKLE_WIDTH]interface{} // *VerkleNode или *PriceLevel
	IsLeaf   bool
	NodeType NodeType
	Metadata string
}

// OrderBook - основной класс ордербука (НОВАЯ АРХИТЕКТУРА - только Verkle Tree!)
type OrderBook struct {
	Symbol      string
	nextOrderID uint64
	nextTradeID uint64

	// ✅ ВСЁ ХРАНИТСЯ ТОЛЬКО В VERKLE TREE!
	Root *VerkleNode // Корень дерева

	// Кэш для O(1) доступа к лучшим ценам
	bestBidCache *PriceLevel // Указатель на лучший BUY уровень
	bestAskCache *PriceLevel // Указатель на лучший SELL уровень

	OrderIndex   map[uint64]*Order // Индекс всех ордеров по ID
	Trades       []*Trade          // История всех трейдов
	LastRootHash [32]byte

	mu         sync.RWMutex
	hashTicker *time.Ticker
	stopChan   chan struct{}
	stats      Stats
}

// Stats - статистика ордербука
type Stats struct {
	TotalOperations   uint64
	TotalOrders       uint64
	TotalMatches      uint64
	TotalCancels      uint64
	TotalModifies     uint64
	TotalMarketOrders uint64
	HashCount         uint64
	LastHashTime      time.Time
}

// TreeStats - статистика дерева
type TreeStats struct {
	TotalNodes   int
	PriceLevels  int
	TotalOrders  int
	TotalVolume  uint64
	BuyLevels    int
	SellLevels   int
	MaxPrice     uint64
	MinPrice     uint64
}

// Вспомогательные функции для работы с пулами
func getOrderFromPool() *Order {
	o := orderPool.Get().(*Order)
	*o = Order{} // Очищаем
	return o
}

func putOrderToPool(o *Order) {
	*o = Order{}
	orderPool.Put(o)
}

func getPriceLevelFromPool() *PriceLevel {
	pl := priceLevelPool.Get().(*PriceLevel)
	// Инициализируем ВСЕ 16 слотов
	for i := 0; i < VERKLE_WIDTH; i++ {
		if pl.Slots[i] == nil {
			pl.Slots[i] = &Slot{
				Metadata: &SlotMetadataTable[i],
				Orders:   make([]*Order, 0, 64),
				Volume:   0,
			}
		} else {
			pl.Slots[i].Orders = pl.Slots[i].Orders[:0]
			pl.Slots[i].Volume = 0
		}
	}
	pl.Price = 0
	pl.TotalVolume = 0
	return pl
}

func putPriceLevelToPool(pl *PriceLevel) {
	for i := 0; i < VERKLE_WIDTH; i++ {
		if pl.Slots[i] != nil {
			pl.Slots[i].Orders = pl.Slots[i].Orders[:0]
			pl.Slots[i].Volume = 0
		}
	}
	pl.Price = 0
	pl.TotalVolume = 0
	priceLevelPool.Put(pl)
}

func getVerkleNodeFromPool() *VerkleNode {
	node := verkleNodePool.Get().(*VerkleNode)
	*node = VerkleNode{} // Очищаем
	return node
}

func putVerkleNodeToPool(node *VerkleNode) {
	*node = VerkleNode{}
	verkleNodePool.Put(node)
}

// safeSubtract безопасно вычитает с защитой от underflow
func safeSubtract(from, value uint64) uint64 {
	if value > from {
		return 0
	}
	return from - value
}

// safeAdd безопасно складывает с защитой от overflow
func safeAdd(a, b uint64) uint64 {
	if a > ^uint64(0)-b {
		return ^uint64(0)
	}
	return a + b
}

// NewOrderBook создает новый ордербук
func NewOrderBook(symbol string) *OrderBook {
	ob := &OrderBook{
		Symbol:      symbol,
		nextOrderID: 0,
		nextTradeID: 0,
		OrderIndex:  make(map[uint64]*Order),
		Trades:      make([]*Trade, 0, 100000),
		stopChan:    make(chan struct{}),
	}

	// Создаем корневой узел с BUY и SELL поддеревьями
	ob.Root = getVerkleNodeFromPool()
	ob.Root.NodeType = NODE_ROOT
	ob.Root.Metadata = "OrderBook Root"

	// Children[0] = BUY сторона
	buyNode := getVerkleNodeFromPool()
	buyNode.NodeType = NODE_BUY_SIDE
	buyNode.Metadata = "BUY"
	ob.Root.Children[0] = buyNode

	// Children[1] = SELL сторона
	sellNode := getVerkleNodeFromPool()
	sellNode.NodeType = NODE_SELL_SIDE
	sellNode.Metadata = "SELL"
	ob.Root.Children[1] = sellNode

	// Инициализация кэша
	ob.bestBidCache = nil
	ob.bestAskCache = nil

	// Условный запуск хеширования
	if HASH_INTERVAL > 0 {
		ob.hashTicker = time.NewTicker(HASH_INTERVAL)
		go ob.periodicHasher()
	}

	return ob
}

// Stop останавливает ордербук и фоновые горутины
func (ob *OrderBook) Stop() {
	close(ob.stopChan)
	if ob.hashTicker != nil {
		ob.hashTicker.Stop()
	}
}

// periodicHasher - горутина для периодического пересчета хеша
func (ob *OrderBook) periodicHasher() {
	for {
		select {
		case <-ob.hashTicker.C:
			ob.mu.RLock()
			ob.computeRootHash()
			atomic.AddUint64(&ob.stats.HashCount, 1)
			ob.stats.LastHashTime = time.Now()
			ob.mu.RUnlock()
		case <-ob.stopChan:
			return
		}
	}
}

// encodePriceToBigEndian кодирует цену в BigEndian байты
func encodePriceToBigEndian(price uint64) []byte {
	buf := make([]byte, 8)
	binary.BigEndian.PutUint64(buf, price)
	return buf
}

// getPriceLevel ищет или создает ценовой уровень в Verkle Tree
func (ob *OrderBook) getPriceLevel(price uint64, side Side, create bool) *PriceLevel {
	// Выбираем поддерево
	var sideNode *VerkleNode
	if side == BUY {
		sideNode = ob.Root.Children[0].(*VerkleNode)
	} else {
		sideNode = ob.Root.Children[1].(*VerkleNode)
	}

	// Кодируем цену в BigEndian для сортировки в дереве
	priceBytes := encodePriceToBigEndian(price)

	// Обход дерева по nibbles (4 бита = 16 значений)
	currentNode := sideNode

	for depth := 0; depth < VERKLE_DEPTH; depth++ {
		// Получаем 4-битный индекс (0-15)
		byteIdx := depth / 2
		nibbleIdx := depth % 2

		var index uint8
		if nibbleIdx == 0 {
			index = priceBytes[byteIdx] >> 4 // Старшие 4 бита
		} else {
			index = priceBytes[byteIdx] & 0x0F // Младшие 4 бита
		}

		// Последний уровень - храним PriceLevel
		if depth == VERKLE_DEPTH-1 {
			if currentNode.Children[index] != nil {
				// Нашли существующий уровень
				if level, ok := currentNode.Children[index].(*PriceLevel); ok {
					return level
				}
			}

			if !create {
				return nil
			}

			// Создаем новый PriceLevel
			level := getPriceLevelFromPool()
			level.Price = price
			level.TotalVolume = 0
			currentNode.Children[index] = level

			return level
		}

		// Промежуточный узел
		if currentNode.Children[index] == nil {
			if !create {
				return nil
			}
			newNode := getVerkleNodeFromPool()
			newNode.NodeType = NODE_INNER
			currentNode.Children[index] = newNode
		}

		currentNode = currentNode.Children[index].(*VerkleNode)
	}

	return nil
}

// findBestBid ищет максимальную BUY цену в дереве (обход справа налево)
func (ob *OrderBook) findBestBid() *PriceLevel {
	buyNode := ob.Root.Children[0].(*VerkleNode)
	return ob.findMaxPriceLevel(buyNode)
}

// findBestAsk ищет минимальную SELL цену в дереве (обход слева направо)
func (ob *OrderBook) findBestAsk() *PriceLevel {
	sellNode := ob.Root.Children[1].(*VerkleNode)
	return ob.findMinPriceLevel(sellNode)
}

// findMaxPriceLevel ищет максимальный PriceLevel в поддереве (для BUY)
func (ob *OrderBook) findMaxPriceLevel(node *VerkleNode) *PriceLevel {
	if node == nil {
		return nil
	}

	// Идем по самым правым непустым узлам (максимальные значения)
	for i := VERKLE_WIDTH - 1; i >= 0; i-- {
		if node.Children[i] == nil {
			continue
		}

		// Если это PriceLevel - проверяем volume
		if level, ok := node.Children[i].(*PriceLevel); ok {
			if level.TotalVolume > 0 {
				return level
			}
			continue
		}

		// Если это узел - рекурсивно ищем в нём
		if childNode, ok := node.Children[i].(*VerkleNode); ok {
			result := ob.findMaxPriceLevel(childNode)
			if result != nil {
				return result
			}
		}
	}

	return nil
}

// findMinPriceLevel ищет минимальный PriceLevel в поддереве (для SELL)
func (ob *OrderBook) findMinPriceLevel(node *VerkleNode) *PriceLevel {
	if node == nil {
		return nil
	}

	// Идем по самым левым непустым узлам (минимальные значения)
	for i := 0; i < VERKLE_WIDTH; i++ {
		if node.Children[i] == nil {
			continue
		}

		// Если это PriceLevel - проверяем volume
		if level, ok := node.Children[i].(*PriceLevel); ok {
			if level.TotalVolume > 0 {
				return level
			}
			continue
		}

		// Если это узел - рекурсивно ищем в нём
		if childNode, ok := node.Children[i].(*VerkleNode); ok {
			result := ob.findMinPriceLevel(childNode)
			if result != nil {
				return result
			}
		}
	}

	return nil
}

// updateBestPricesCache обновляет кэш BestBid/BestAsk (КРИТИЧНО: вызывать после КАЖДОГО изменения!)
func (ob *OrderBook) updateBestPricesCache() {
	ob.bestBidCache = ob.findBestBid()
	ob.bestAskCache = ob.findBestAsk()
}

// GetBestBid возвращает лучшую BUY цену (O(1) через кэш)
func (ob *OrderBook) GetBestBid() uint64 {
	if ob.bestBidCache != nil && ob.bestBidCache.TotalVolume > 0 {
		return ob.bestBidCache.Price
	}
	return 0
}

// GetBestAsk возвращает лучшую SELL цену (O(1) через кэш)
func (ob *OrderBook) GetBestAsk() uint64 {
	if ob.bestAskCache != nil && ob.bestAskCache.TotalVolume > 0 {
		return ob.bestAskCache.Price
	}
	return 0
}

// invalidateBestPricesCache проверяет нужно ли обновить кэш
func (ob *OrderBook) invalidateBestPricesCache(side Side, newPrice uint64) {
	if side == BUY {
		// Новый BUY уровень - проверяем больше ли он текущего BestBid
		if ob.bestBidCache == nil || newPrice > ob.bestBidCache.Price {
			ob.bestBidCache = ob.getPriceLevel(newPrice, BUY, false)
		}
	} else {
		// Новый SELL уровень - проверяем меньше ли он текущего BestAsk
		if ob.bestAskCache == nil || newPrice < ob.bestAskCache.Price {
			ob.bestAskCache = ob.getPriceLevel(newPrice, SELL, false)
		}
	}
}

// determineSlot определяет слот для ордера
func (ob *OrderBook) determineSlot(order *Order) uint8 {
	if order.TraderID < 100 {
		return SLOT_VIP
	}
	if order.Size < 1000 {
		return SLOT_SMALL_RETAIL
	}
	slotRange := SLOT_RETAIL_END - SLOT_RETAIL_START + 1
	slot := SLOT_RETAIL_START + uint8(order.TraderID%uint32(slotRange))
	return slot
}

// AddLimitOrder добавляет лимитный ордер в ордербук
func (ob *OrderBook) AddLimitOrder(traderID uint32, price uint64, size uint64, side Side) *Order {
	order := getOrderFromPool()
	order.ID = atomic.AddUint64(&ob.nextOrderID, 1)
	order.TraderID = traderID
	order.Price = price
	order.Size = size
	order.FilledSize = 0
	order.IsPartialFill = false
	order.Side = side
	order.Slot = ob.determineSlot(order)

	ob.mu.Lock()
	defer ob.mu.Unlock()

	// Матчинг
	ob.tryMatchUnsafe(order)

	// Если ордер не исполнен полностью - добавляем в дерево
	if !order.IsFilled() {
		level := ob.getPriceLevel(price, side, true)

		remainingSize := order.RemainingSize()
		slot := level.Slots[order.Slot]
		slot.Orders = append(slot.Orders, order)
		slot.Volume = safeAdd(slot.Volume, remainingSize)
		level.TotalVolume = safeAdd(level.TotalVolume, remainingSize)

		ob.OrderIndex[order.ID] = order

		// Обновляем кэш BestBid/BestAsk
		ob.invalidateBestPricesCache(side, price)
	}

	atomic.AddUint64(&ob.stats.TotalOrders, 1)
	atomic.AddUint64(&ob.stats.TotalOperations, 1)

	return order
}

// tryMatchUnsafe пытается совместить ордер (вызывается под lock)
func (ob *OrderBook) tryMatchUnsafe(takerOrder *Order) {
	for !takerOrder.IsFilled() {
		// Получаем лучшую цену противоположной стороны
		var bestLevel *PriceLevel
		var canMatch bool

		if takerOrder.Side == BUY {
			bestLevel = ob.bestAskCache
			if bestLevel == nil || bestLevel.TotalVolume == 0 {
				ob.bestAskCache = ob.findBestAsk()
				bestLevel = ob.bestAskCache
			}
			canMatch = bestLevel != nil && bestLevel.TotalVolume > 0 && bestLevel.Price <= takerOrder.Price
		} else {
			bestLevel = ob.bestBidCache
			if bestLevel == nil || bestLevel.TotalVolume == 0 {
				ob.bestBidCache = ob.findBestBid()
				bestLevel = ob.bestBidCache
			}
			canMatch = bestLevel != nil && bestLevel.TotalVolume > 0 && bestLevel.Price >= takerOrder.Price
		}

		if !canMatch || bestLevel == nil {
			break
		}

		bestPrice := bestLevel.Price

		// Исполняем ордер по приоритету слотов (0 -> 15)
		levelMatched := false
		for slotIdx := 0; slotIdx < VERKLE_WIDTH; slotIdx++ {
			if takerOrder.IsFilled() {
				break
			}

			slot := bestLevel.Slots[slotIdx]
			if len(slot.Orders) == 0 {
				continue
			}

			// Обрабатываем ордера в слоте (FIFO)
			i := 0
			for i < len(slot.Orders) {
				if takerOrder.IsFilled() {
					break
				}

				makerOrder := slot.Orders[i]
				takerRemaining := takerOrder.RemainingSize()
				makerRemaining := makerOrder.RemainingSize()
				executeSize := takerRemaining
				if makerRemaining < executeSize {
					executeSize = makerRemaining
				}

				// Создаем трейд
				trade := &Trade{
					TradeID:       atomic.AddUint64(&ob.nextTradeID, 1),
					TakerOrderID:  takerOrder.ID,
					MakerOrderID:  makerOrder.ID,
					TakerTraderID: takerOrder.TraderID,
					MakerTraderID: makerOrder.TraderID,
					Price:         bestPrice,
					Size:          executeSize,
					TakerSide:     takerOrder.Side,
					TakerPartial:  false,
					MakerPartial:  false,
					Timestamp:     time.Now().UnixNano(),
				}

				// Обновляем заполнение
				takerOrder.FilledSize += executeSize
				makerOrder.FilledSize += executeSize

				if !takerOrder.IsFilled() {
					takerOrder.IsPartialFill = true
					trade.TakerPartial = true
				}

				if !makerOrder.IsFilled() {
					makerOrder.IsPartialFill = true
					trade.MakerPartial = true
				}

				// Обновляем объемы
				slot.Volume = safeSubtract(slot.Volume, executeSize)
				bestLevel.TotalVolume = safeSubtract(bestLevel.TotalVolume, executeSize)

				ob.Trades = append(ob.Trades, trade)
				atomic.AddUint64(&ob.stats.TotalMatches, 1)

				// Если maker ордер исполнен полностью - удаляем
				if makerOrder.IsFilled() {
					slot.Orders = append(slot.Orders[:i], slot.Orders[i+1:]...)
					delete(ob.OrderIndex, makerOrder.ID)
					putOrderToPool(makerOrder)
				} else {
					i++
				}

				levelMatched = true
			}

			if len(slot.Orders) == 0 {
				slot.Volume = 0
			}
		}

		// КРИТИЧНО: Если уровень стал пустым - ОБЯЗАТЕЛЬНО обновляем кэш!
		if bestLevel.TotalVolume == 0 {
			ob.updateBestPricesCache()
		}

		if !levelMatched {
			break
		}
	}

	if takerOrder.IsFilled() {
		delete(ob.OrderIndex, takerOrder.ID)
	}
}

// ExecuteMarketOrder исполняет рыночный ордер
func (ob *OrderBook) ExecuteMarketOrder(traderID uint32, size uint64, side Side) bool {
	order := getOrderFromPool()
	order.ID = atomic.AddUint64(&ob.nextOrderID, 1)
	order.TraderID = traderID

	if side == BUY {
		order.Price = uint64(^uint64(0)) // Max - купим по любой цене
	} else {
		order.Price = 0 // Min - продадим по любой цене
	}

	order.Size = size
	order.FilledSize = 0
	order.Side = side
	order.Slot = ob.determineSlot(order)

	ob.mu.Lock()
	ob.tryMatchUnsafe(order)
	ob.mu.Unlock()

	putOrderToPool(order)

	atomic.AddUint64(&ob.stats.TotalMarketOrders, 1)
	atomic.AddUint64(&ob.stats.TotalOperations, 1)

	return true
}

// CancelOrder отменяет ордер по ID
func (ob *OrderBook) CancelOrder(orderID uint64) bool {
	ob.mu.Lock()
	defer ob.mu.Unlock()

	order, exists := ob.OrderIndex[orderID]
	if !exists {
		return false
	}

	level := ob.getPriceLevel(order.Price, order.Side, false)
	if level == nil {
		delete(ob.OrderIndex, orderID)
		putOrderToPool(order)
		atomic.AddUint64(&ob.stats.TotalCancels, 1)
		return true
	}

	slot := level.Slots[order.Slot]

	// Удаляем ордер из слота
	for i, o := range slot.Orders {
		if o.ID == orderID {
			slot.Orders = append(slot.Orders[:i], slot.Orders[i+1:]...)
			remainingSize := order.RemainingSize()
			slot.Volume = safeSubtract(slot.Volume, remainingSize)
			level.TotalVolume = safeSubtract(level.TotalVolume, remainingSize)
			break
		}
	}

	delete(ob.OrderIndex, orderID)
	putOrderToPool(order)

	if len(slot.Orders) == 0 {
		slot.Volume = 0
	}

	// КРИТИЧНО: Если уровень стал пустым, обновляем кэш
	if level.TotalVolume == 0 {
		// Проверяем был ли это BestBid/BestAsk
		if order.Side == BUY && ob.bestBidCache != nil && ob.bestBidCache.Price == level.Price {
			ob.updateBestPricesCache()
		} else if order.Side == SELL && ob.bestAskCache != nil && ob.bestAskCache.Price == level.Price {
			ob.updateBestPricesCache()
		}
	}

	atomic.AddUint64(&ob.stats.TotalOperations, 1)
	atomic.AddUint64(&ob.stats.TotalCancels, 1)
	return true
}

// ModifyOrder изменяет цену и/или размер существующего ордера
func (ob *OrderBook) ModifyOrder(orderID uint64, newPrice *uint64, newSize *uint64) bool {
	ob.mu.Lock()
	defer ob.mu.Unlock()

	order, exists := ob.OrderIndex[orderID]
	if !exists {
		return false
	}

	priceChanged := newPrice != nil && *newPrice != order.Price
	sizeChanged := newSize != nil && *newSize != order.Size

	if !priceChanged && !sizeChanged {
		return true
	}

	oldLevel := ob.getPriceLevel(order.Price, order.Side, false)
	if oldLevel == nil {
		return false
	}

	oldSlot := oldLevel.Slots[order.Slot]
	oldLevelWasBest := false

	// Проверяем был ли старый уровень BestBid/BestAsk
	if order.Side == BUY && ob.bestBidCache != nil && ob.bestBidCache.Price == oldLevel.Price {
		oldLevelWasBest = true
	} else if order.Side == SELL && ob.bestAskCache != nil && ob.bestAskCache.Price == oldLevel.Price {
		oldLevelWasBest = true
	}

	// СЛУЧАЙ 1: Меняется только размер
	if !priceChanged && sizeChanged {
		newSizeVal := *newSize
		oldRemainingSize := order.RemainingSize()

		oldSlot.Volume = safeSubtract(oldSlot.Volume, oldRemainingSize)
		oldLevel.TotalVolume = safeSubtract(oldLevel.TotalVolume, oldRemainingSize)

		order.Size = newSizeVal
		newRemainingSize := order.RemainingSize()

		if newRemainingSize == 0 {
			for i, o := range oldSlot.Orders {
				if o.ID == orderID {
					oldSlot.Orders = append(oldSlot.Orders[:i], oldSlot.Orders[i+1:]...)
					break
				}
			}

			if len(oldSlot.Orders) == 0 {
				oldSlot.Volume = 0
			}

			if oldLevel.TotalVolume == 0 && oldLevelWasBest {
				ob.updateBestPricesCache()
			}

			delete(ob.OrderIndex, orderID)
			putOrderToPool(order)
			atomic.AddUint64(&ob.stats.TotalModifies, 1)
			atomic.AddUint64(&ob.stats.TotalOperations, 1)
			return true
		}

		newSlot := ob.determineSlot(order)
		if newSlot != order.Slot {
			for i, o := range oldSlot.Orders {
				if o.ID == orderID {
					oldSlot.Orders = append(oldSlot.Orders[:i], oldSlot.Orders[i+1:]...)
					break
				}
			}

			if len(oldSlot.Orders) == 0 {
				oldSlot.Volume = 0
			}

			order.Slot = newSlot
			targetSlot := oldLevel.Slots[newSlot]
			targetSlot.Orders = append(targetSlot.Orders, order)
			targetSlot.Volume = safeAdd(targetSlot.Volume, newRemainingSize)
		} else {
			oldSlot.Volume = safeAdd(oldSlot.Volume, newRemainingSize)
		}

		oldLevel.TotalVolume = safeAdd(oldLevel.TotalVolume, newRemainingSize)
		atomic.AddUint64(&ob.stats.TotalModifies, 1)
		atomic.AddUint64(&ob.stats.TotalOperations, 1)
		return true
	}

	// СЛУЧАЙ 2: Меняется цена
	if priceChanged {
		newPriceVal := *newPrice
		newSizeVal := order.Size
		if sizeChanged {
			newSizeVal = *newSize
		}

		oldRemainingSize := order.RemainingSize()

		// Удаляем из старого уровня
		orderFound := false
		for i, o := range oldSlot.Orders {
			if o.ID == orderID {
				oldSlot.Orders = append(oldSlot.Orders[:i], oldSlot.Orders[i+1:]...)
				oldSlot.Volume = safeSubtract(oldSlot.Volume, oldRemainingSize)
				oldLevel.TotalVolume = safeSubtract(oldLevel.TotalVolume, oldRemainingSize)
				orderFound = true
				break
			}
		}

		if !orderFound {
			return false
		}

		if len(oldSlot.Orders) == 0 {
			oldSlot.Volume = 0
		}

		if oldLevel.TotalVolume == 0 && oldLevelWasBest {
			ob.updateBestPricesCache()
		}

		// Обновляем параметры ордера
		order.Price = newPriceVal
		order.Size = newSizeVal
		order.Slot = ob.determineSlot(order)

		newRemainingSize := order.RemainingSize()

		if newRemainingSize == 0 {
			delete(ob.OrderIndex, orderID)
			putOrderToPool(order)
			atomic.AddUint64(&ob.stats.TotalModifies, 1)
			atomic.AddUint64(&ob.stats.TotalOperations, 1)
			return true
		}

		// Добавляем в новый уровень цены
		newLevel := ob.getPriceLevel(newPriceVal, order.Side, true)

		newSlot := newLevel.Slots[order.Slot]
		newSlot.Orders = append(newSlot.Orders, order)
		newSlot.Volume = safeAdd(newSlot.Volume, newRemainingSize)
		newLevel.TotalVolume = safeAdd(newLevel.TotalVolume, newRemainingSize)

		// Обновляем кэш
		ob.invalidateBestPricesCache(order.Side, newPriceVal)

		atomic.AddUint64(&ob.stats.TotalModifies, 1)
		atomic.AddUint64(&ob.stats.TotalOperations, 1)

		// Проверяем возможность матчинга с новой ценой
		ob.tryMatchUnsafe(order)
		return true
	}

	return false
}

// collectAllPriceLevels собирает все ценовые уровни из дерева
func (ob *OrderBook) collectAllPriceLevels(node interface{}) []*PriceLevel {
	levels := make([]*PriceLevel, 0)

	switch n := node.(type) {
	case *VerkleNode:
		for i := 0; i < VERKLE_WIDTH; i++ {
			if n.Children[i] != nil {
				childLevels := ob.collectAllPriceLevels(n.Children[i])
				levels = append(levels, childLevels...)
			}
		}
	case *PriceLevel:
		if n.TotalVolume > 0 {
			levels = append(levels, n)
		}
	}

	return levels
}

// collectTreeStats собирает статистику по дереву
func (ob *OrderBook) collectTreeStats(node interface{}) TreeStats {
	stats := TreeStats{
		MinPrice: ^uint64(0), // Max uint64
		MaxPrice: 0,
	}

	switch n := node.(type) {
	case *VerkleNode:
		stats.TotalNodes++
		for i := 0; i < VERKLE_WIDTH; i++ {
			if n.Children[i] != nil {
				childStats := ob.collectTreeStats(n.Children[i])
				stats.TotalNodes += childStats.TotalNodes
				stats.PriceLevels += childStats.PriceLevels
				stats.TotalOrders += childStats.TotalOrders
				stats.TotalVolume += childStats.TotalVolume

				if childStats.MaxPrice > stats.MaxPrice {
					stats.MaxPrice = childStats.MaxPrice
				}
				if childStats.MinPrice < stats.MinPrice {
					stats.MinPrice = childStats.MinPrice
				}
			}
		}
	case *PriceLevel:
		stats.TotalNodes++
		stats.PriceLevels++
		stats.TotalVolume += n.TotalVolume
		stats.MaxPrice = n.Price
		stats.MinPrice = n.Price

		for _, slot := range n.Slots {
			stats.TotalOrders += len(slot.Orders)
		}
	}

	return stats
}

// printTreeCompact выводит компактное представление (топ уровни)
func (ob *OrderBook) printTreeCompact() {
	// Собираем все уровни из BUY и SELL сторон
	buyNode := ob.Root.Children[0].(*VerkleNode)
	sellNode := ob.Root.Children[1].(*VerkleNode)

	buyLevels := ob.collectAllPriceLevels(buyNode)
	sellLevels := ob.collectAllPriceLevels(sellNode)

	// Сортируем BUY уровни по убыванию (максимальные сверху)
	sort.Slice(buyLevels, func(i, j int) bool {
		return buyLevels[i].Price > buyLevels[j].Price
	})

	// Сортируем SELL уровни по возрастанию (минимальные сверху)
	sort.Slice(sellLevels, func(i, j int) bool {
		return sellLevels[i].Price < sellLevels[j].Price
	})

	fmt.Printf("├─ [ROOT] %s (hash: %x...)\n", ob.Root.Metadata, ob.LastRootHash[:4])
	fmt.Println()

	// BUY сторона
	fmt.Printf(" ├─ [BUY_SIDE] (%d уровней)\n", len(buyLevels))
	limit := 10
	if len(buyLevels) < limit {
		limit = len(buyLevels)
	}
	for i := 0; i < limit; i++ {
		level := buyLevels[i]
		ordersCount := 0
		for _, slot := range level.Slots {
			ordersCount += len(slot.Orders)
		}
		prefix := " │  ├─"
		if i == limit-1 && len(buyLevels) <= 10 {
			prefix = " │  └─"
		}
		fmt.Printf("%s [PRICE] %.2f (volume: %.2f, orders: %d)\n",
			prefix,
			float64(level.Price)/PRICE_DECIMALS,
			float64(level.TotalVolume)/PRICE_DECIMALS,
			ordersCount)
	}
	if len(buyLevels) > 10 {
		fmt.Printf(" │  ... еще %d уровней\n", len(buyLevels)-10)
	}
	fmt.Println()

	// SELL сторона
	fmt.Printf(" └─ [SELL_SIDE] (%d уровней)\n", len(sellLevels))
	if len(sellLevels) < limit {
		limit = len(sellLevels)
	}
	for i := 0; i < limit; i++ {
		level := sellLevels[i]
		ordersCount := 0
		for _, slot := range level.Slots {
			ordersCount += len(slot.Orders)
		}
		prefix := "    ├─"
		if i == limit-1 && len(sellLevels) <= 10 {
			prefix = "    └─"
		}
		fmt.Printf("%s [PRICE] %.2f (volume: %.2f, orders: %d)\n",
			prefix,
			float64(level.Price)/PRICE_DECIMALS,
			float64(level.TotalVolume)/PRICE_DECIMALS,
			ordersCount)
	}
	if len(sellLevels) > 10 {
		fmt.Printf("    ... еще %d уровней\n", len(sellLevels)-10)
	}
}

// printTreeSummary выводит только статистику
func (ob *OrderBook) printTreeSummary() {
	stats := ob.collectTreeStats(ob.Root)

	buyStats := ob.collectTreeStats(ob.Root.Children[0])
	sellStats := ob.collectTreeStats(ob.Root.Children[1])

	fmt.Printf("├─ [ROOT] %s\n", ob.Root.Metadata)
	fmt.Printf("│ • Root hash: %x...\n", ob.LastRootHash[:8])
	fmt.Printf("│\n")

	fmt.Printf("├─ [BUY_SIDE]\n")
	fmt.Printf("│ • Уровней: %d\n", buyStats.PriceLevels)
	fmt.Printf("│ • Ордеров: %d\n", buyStats.TotalOrders)
	fmt.Printf("│ • Объем: %.2f\n", float64(buyStats.TotalVolume)/PRICE_DECIMALS)
	if buyStats.MaxPrice > 0 {
		fmt.Printf("│ • Max цена: %.2f\n", float64(buyStats.MaxPrice)/PRICE_DECIMALS)
	}
	fmt.Printf("│\n")

	fmt.Printf("├─ [SELL_SIDE]\n")
	fmt.Printf("│ • Уровней: %d\n", sellStats.PriceLevels)
	fmt.Printf("│ • Ордеров: %d\n", sellStats.TotalOrders)
	fmt.Printf("│ • Объем: %.2f\n", float64(sellStats.TotalVolume)/PRICE_DECIMALS)
	if sellStats.MinPrice < ^uint64(0) {
		fmt.Printf("│ • Min цена: %.2f\n", float64(sellStats.MinPrice)/PRICE_DECIMALS)
	}
	fmt.Printf("│\n")

	fmt.Printf("├─ ИТОГО:\n")
	fmt.Printf(" • Всего узлов: %d\n", stats.TotalNodes)
	fmt.Printf(" • Всего уровней: %d\n", stats.PriceLevels)
	fmt.Printf(" • Всего ордеров: %d\n", stats.TotalOrders)
	fmt.Printf(" • Общий объем: %.2f\n", float64(stats.TotalVolume)/PRICE_DECIMALS)
}

// computeRootHash вычисляет Blake3 хеш корня дерева
func (ob *OrderBook) computeRootHash() {
	ob.LastRootHash = ob.hashNode(ob.Root)
}

// hashNode рекурсивно вычисляет хеш узла
func (ob *OrderBook) hashNode(node *VerkleNode) [32]byte {
	hasher := blake3.New()
	hasher.Write([]byte{byte(node.NodeType)})

	for i := 0; i < VERKLE_WIDTH; i++ {
		var childHash [32]byte
		switch child := node.Children[i].(type) {
		case *VerkleNode:
			childHash = ob.hashNode(child)
		case *PriceLevel:
			childHash = ob.hashPriceLevel(child)
		default:
			childHash = [32]byte{}
		}
		hasher.Write(childHash[:])
	}

	var result [32]byte
	hasher.Sum(result[:0])
	node.Hash = result
	return result
}

// hashPriceLevel вычисляет хеш ценового уровня
func (ob *OrderBook) hashPriceLevel(level *PriceLevel) [32]byte {
	hasher := blake3.New()
	buf := hashBufferPool.Get().([]byte)
	buf = buf[:0]
	defer func() {
		hashBufferPool.Put(buf)
	}()

	if cap(buf) < 8 {
		buf = make([]byte, 8)
	}

	buf = buf[:8]
	binary.BigEndian.PutUint64(buf, level.Price)
	hasher.Write(buf)

	binary.BigEndian.PutUint64(buf, level.TotalVolume)
	hasher.Write(buf)

	for i := 0; i < VERKLE_WIDTH; i++ {
		slot := level.Slots[i]
		binary.BigEndian.PutUint64(buf, slot.Volume)
		hasher.Write(buf)
	}

	var result [32]byte
	hasher.Sum(result[:0])
	return result
}

// PrintStats выводит статистику ордербука
func (ob *OrderBook) PrintStats() {
	ob.mu.Lock()
	ob.computeRootHash()
	atomic.AddUint64(&ob.stats.HashCount, 1)

	bestBid := ob.GetBestBid()
	bestAsk := ob.GetBestAsk()

	totalOperations := atomic.LoadUint64(&ob.stats.TotalOperations)
	totalOrders := atomic.LoadUint64(&ob.stats.TotalOrders)
	totalMatches := atomic.LoadUint64(&ob.stats.TotalMatches)
	totalCancels := atomic.LoadUint64(&ob.stats.TotalCancels)
	totalModifies := atomic.LoadUint64(&ob.stats.TotalModifies)
	totalMarketOrders := atomic.LoadUint64(&ob.stats.TotalMarketOrders)
	hashCount := atomic.LoadUint64(&ob.stats.HashCount)
	rootHash := ob.LastRootHash
	activeOrders := len(ob.OrderIndex)
	tradesCount := len(ob.Trades)

	ob.mu.Unlock()

	fmt.Printf("\n═══════════════════════════════════════════\n")
	fmt.Printf("Статистика %s:\n", ob.Symbol)
	fmt.Printf(" • Активных ордеров: %d\n", activeOrders)
	fmt.Printf(" • Всего добавлено: %d\n", totalOrders)
	fmt.Printf(" • Маркет-ордеров: %d\n", totalMarketOrders)
	fmt.Printf(" • Трейдов: %d\n", tradesCount)
	fmt.Printf(" • Матчей: %d\n", totalMatches)
	fmt.Printf(" • Отмен: %d\n", totalCancels)
	fmt.Printf(" • Изменений: %d\n", totalModifies)
	fmt.Printf(" • Всего операций (Tx): %d\n", totalOperations)
	fmt.Printf(" • Хешей посчитано: %d\n", hashCount)
	fmt.Printf("─────────────────────────────────────────\n")
	fmt.Printf(" • Root hash: %x...\n", rootHash[:16])
	fmt.Println("─────────────────────────────────────────")
	fmt.Printf(" • Best Bid: %.2f\n", float64(bestBid)/PRICE_DECIMALS)
	fmt.Printf(" • Best Ask: %.2f\n", float64(bestAsk)/PRICE_DECIMALS)

	if bestAsk > 0 && bestBid > 0 {
		if bestAsk > bestBid {
			spread := float64(bestAsk-bestBid) / PRICE_DECIMALS
			fmt.Printf(" • Spread: %.2f ✅\n", spread)
		} else {
			spread := float64(bestBid-bestAsk) / PRICE_DECIMALS
			fmt.Printf(" • Spread: -%.2f (⚠️ CROSSED MARKET: Bid >= Ask!)\n", spread)
		}
	}
	fmt.Printf("═══════════════════════════════════════════\n\n")
}

// PrintTreeStructure выводит структуру дерева в консоль
func (ob *OrderBook) PrintTreeStructure(mode TreePrintMode) {
	ob.mu.RLock()
	defer ob.mu.RUnlock()

	ob.computeRootHash()

	fmt.Println("\n🌳 VERKLE TREE STRUCTURE (Verkle-only architecture)")
	fmt.Println("═══════════════════════════════════════════")

	switch mode {
	case TREE_PRINT_COMPACT:
		fmt.Println("Режим: КОМПАКТНЫЙ (Топ-10 с каждой стороны)")
		fmt.Println()
		ob.printTreeCompact()
	case TREE_PRINT_SUMMARY:
		fmt.Println("Режим: СТАТИСТИКА")
		fmt.Println()
		ob.printTreeSummary()
	case TREE_PRINT_FULL:
		fmt.Println("Режим: ПОЛНОЕ ДЕРЕВО (не реализовано для больших объемов)")
		fmt.Println()
		ob.printTreeSummary()
	}

	fmt.Println("═══════════════════════════════════════════\n")
}

// TraderType - тип трейдера
type TraderType int

const (
	TRADER_RETAIL       TraderType = iota
	TRADER_MARKET_MAKER
	TRADER_WHALE
	TRADER_BOT
)

// TraderProfile - профиль трейдера
type TraderProfile struct {
	ID          uint32
	Type        TraderType
	PriceSpread int
	OrderSize   int
	CancelRate  float32
}

// generateTraderProfiles создает профили трейдеров
func generateTraderProfiles(numTraders int) []TraderProfile {
	profiles := make([]TraderProfile, numTraders)
	for i := 0; i < numTraders; i++ {
		traderID := uint32(i + 1)
		if i < numTraders*5/100 {
			profiles[i] = TraderProfile{
				ID:          traderID,
				Type:        TRADER_MARKET_MAKER,
				PriceSpread: 50,
				OrderSize:   5000,
				CancelRate:  0.3,
			}
		} else if i < numTraders*15/100 {
			profiles[i] = TraderProfile{
				ID:          traderID,
				Type:        TRADER_WHALE,
				PriceSpread: 200,
				OrderSize:   20000,
				CancelRate:  0.1,
			}
		} else if i < numTraders*45/100 {
			profiles[i] = TraderProfile{
				ID:          traderID,
				Type:        TRADER_BOT,
				PriceSpread: 100,
				OrderSize:   3000,
				CancelRate:  0.5,
			}
		} else {
			profiles[i] = TraderProfile{
				ID:          traderID,
				Type:        TRADER_RETAIL,
				PriceSpread: 500,
				OrderSize:   1000,
				CancelRate:  0.2,
			}
		}
	}
	return profiles
}

// generateSize генерирует размер ордера с учетом профиля
func generateSize(profile TraderProfile) uint64 {
	variation := profile.OrderSize / 2
	size := profile.OrderSize - variation + rand.Intn(variation*2)
	if size < 100 {
		size = 100
	}
	return uint64(size)
}

// generatePrice генерирует цену для трейдера с учетом профиля
func generatePrice(basePrice uint64, profile TraderProfile, side Side) uint64 {
	spread := profile.PriceSpread
	if side == BUY {
		offset := rand.Intn(spread) + 1
		price := int64(basePrice) - int64(offset)
		if price < 100 {
			price = 100
		}
		return uint64(price)
	} else {
		offset := rand.Intn(spread) + 1
		price := int64(basePrice) + int64(offset)
		return uint64(price)
	}
}

// generatePriceWithMagnetism генерирует цену с "притяжением" к круглым числам
func generatePriceWithMagnetism(basePrice uint64, profile TraderProfile, side Side) uint64 {
	if rand.Float32() < 0.4 {
		roundBase := (basePrice / 5000) * 5000
		if side == BUY {
			offset := rand.Intn(100)
			price := int64(roundBase) - int64(offset)
			if price < 100 {
				price = 100
			}
			return uint64(price)
		} else {
			offset := rand.Intn(100)
			price := int64(roundBase) + int64(offset)
			return uint64(price)
		}
	}
	return generatePrice(basePrice, profile, side)
}

func main() {
	fmt.Println("🌳 Verkle Tree Orderbook Simulation")
	fmt.Println("━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━")
	fmt.Println("✓ Verkle Tree ONLY architecture (no map duplication)")
	fmt.Println("✓ BigEndian encoding for sorted tree traversal")
	fmt.Println("✓ O(1) BestBid/BestAsk via cache")
	fmt.Println("✓ O(log n) insert/delete/search")
	fmt.Println("━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━\n")

	rand.Seed(time.Now().UnixNano())

	// ПРОФИЛИРОВАНИЕ
	cpuFile, _ := os.Create("cpu.prof")
	pprof.StartCPUProfile(cpuFile)
	defer func() {
		pprof.StopCPUProfile()
		cpuFile.Close()
	}()

	ob := NewOrderBook("BTC")
	defer ob.Stop()

	basePrice := uint64(6500000) // $65000
	numOperations := 1_000_000

	// Создаем профили трейдеров
	fmt.Println("👥 Генерация профилей трейдеров...")
	traderProfiles := generateTraderProfiles(MAX_TRADERS)
	mmCount := 0
	whaleCount := 0
	botCount := 0
	retailCount := 0
	for _, p := range traderProfiles {
		switch p.Type {
		case TRADER_MARKET_MAKER:
			mmCount++
		case TRADER_WHALE:
			whaleCount++
		case TRADER_BOT:
			botCount++
		case TRADER_RETAIL:
			retailCount++
		}
	}
	fmt.Printf(" • Маркет-мейкеры: %d\n", mmCount)
	fmt.Printf(" • Киты: %d\n", whaleCount)
	fmt.Printf(" • Боты: %d\n", botCount)
	fmt.Printf(" • Retail: %d\n", retailCount)
	fmt.Println()

	// Инициализация: создаем начальную ликвидность от MM
	fmt.Println("💧 Создание начальной ликвидности...")
	addedOrders := make([]uint64, 0, numOperations)
	for i := 0; i < mmCount; i++ {
		profile := traderProfiles[i]
		numOrders := rand.Intn(6) + 5
		for j := 0; j < numOrders; j++ {
			// BUY ордер
			price := generatePrice(basePrice, profile, BUY)
			size := generateSize(profile)
			order := ob.AddLimitOrder(profile.ID, price, size, BUY)
			addedOrders = append(addedOrders, order.ID)

			// SELL ордер
			price = generatePrice(basePrice, profile, SELL)
			size = generateSize(profile)
			order = ob.AddLimitOrder(profile.ID, price, size, SELL)
			addedOrders = append(addedOrders, order.ID)
		}
	}
	fmt.Printf("✓ Создано %d начальных ордеров\n\n", len(addedOrders))
	fmt.Printf("✓ Base price: %.2f\n", float64(basePrice)/PRICE_DECIMALS)

	bestBid := ob.GetBestBid()
	bestAsk := ob.GetBestAsk()
	fmt.Printf(" • Initial BestBid: %.2f\n", float64(bestBid)/PRICE_DECIMALS)
	fmt.Printf(" • Initial BestAsk: %.2f\n", float64(bestAsk)/PRICE_DECIMALS)

	startTime := time.Now()
	for i := 0; i < numOperations; i++ {
		profile := traderProfiles[rand.Intn(len(traderProfiles))]
		r := rand.Float32()

		if r < 0.25 {
			// 25% - маркет ордера
			size := generateSize(profile)
			side := BUY
			if rand.Float32() < 0.5 {
				side = SELL
			}
			ob.ExecuteMarketOrder(profile.ID, size, side)
		} else if r < 0.60 {
			// 35% - лимитные ордера
			size := generateSize(profile)
			side := BUY
			if rand.Float32() < 0.5 {
				side = SELL
			}
			price := generatePriceWithMagnetism(basePrice, profile, side)
			order := ob.AddLimitOrder(profile.ID, price, size, side)
			addedOrders = append(addedOrders, order.ID)
		} else if r < 0.80 {
			// 20% - отмена ордеров
			if len(addedOrders) == 0 {
				continue
			}
			idx := rand.Intn(len(addedOrders))
			orderID := addedOrders[idx]
			if ob.CancelOrder(orderID) {
				addedOrders = append(addedOrders[:idx], addedOrders[idx+1:]...)
			}
		} else {
			// 20% - модификация ордеров
			if len(addedOrders) == 0 {
				continue
			}
			orderID := addedOrders[rand.Intn(len(addedOrders))]

			ob.mu.RLock()
			existingOrder, exists := ob.OrderIndex[orderID]
			ob.mu.RUnlock()

			if !exists {
				continue
			}

			modType := rand.Intn(3)
			switch modType {
			case 0:
				// Изменение размера
				newSize := generateSize(profile)
				ob.ModifyOrder(orderID, nil, &newSize)
			case 1:
				// Изменение цены
				newPrice := generatePriceWithMagnetism(basePrice, profile, existingOrder.Side)
				ob.ModifyOrder(orderID, &newPrice, nil)
			case 2:
				// Изменение цены и размера
				newPrice := generatePriceWithMagnetism(basePrice, profile, existingOrder.Side)
				newSize := generateSize(profile)
				ob.ModifyOrder(orderID, &newPrice, &newSize)
			}
		}
	}

	elapsed := time.Since(startTime)

	// Финальная статистика
	fmt.Println("\n🏁 ФИНАЛЬНАЯ СТАТИСТИКА")
	ob.PrintStats()
	ob.PrintTreeStructure(TREE_PRINT_COMPACT)

	tps := float64(numOperations) / elapsed.Seconds()
	fmt.Printf("⚡ Производительность: %.0f операций/сек\n", tps)
	fmt.Printf("⏱ Общее время: %v\n", elapsed)

	if HASH_INTERVAL > 0 {
		time.Sleep(HASH_INTERVAL + 100*time.Millisecond)
	}
	fmt.Println("\n✅ Симуляция завершена")
}
