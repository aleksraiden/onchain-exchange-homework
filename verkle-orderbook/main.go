package main

import (
	"bufio"
	"encoding/binary"
	"fmt"
	"math/rand"
	"sort"
	"sync"
	"sync/atomic"
	"time"
	
	"encoding/hex"
	"encoding/json"
	"os"
	
	"runtime/pprof"

	"github.com/zeebo/blake3"
)

// Константы системы
const (
	VERKLE_WIDTH      = 16      // Ширина Verkle дерева
	PRICE_DECIMALS    = 100     // Точность цены (2 знака после запятой)
	MAX_TRADERS       = 1000   // Максимальное количество трейдеров
	HASH_INTERVAL     = 0 //500 * time.Millisecond // Интервал хеширования
	
	// Слоты для распределения ордеров с описанием
	SLOT_MM_LIQUIDATION = 0     // Ликвидации маркет-мейкеров
	SLOT_VIP            = 1     // VIP-трейдеры
	SLOT_SMALL_RETAIL   = 2     // Мелкие retail ордера (<$10)
	SLOT_RETAIL_START   = 3     // Начало диапазона для retail
	SLOT_RETAIL_END     = 14    // Конец диапазона для retail
	SLOT_RESERVED       = 15    // Зарезервированный слот
)

// TreePrintMode - режим вывода дерева
type TreePrintMode int

const (
	TREE_PRINT_COMPACT  TreePrintMode = iota // Топ N уровней с каждой стороны
	TREE_PRINT_SUMMARY                       // Только статистика по узлам
	TREE_PRINT_FULL                          // Полное дерево (может быть огромным!)
)

// NodeType - тип узла в дереве
type NodeType int

const (
	NODE_ROOT        NodeType = iota // Корневой узел
	NODE_BUY_SIDE                    // Узел BUY стороны
	NODE_SELL_SIDE                   // Узел SELL стороны
	NODE_PRICE_GROUP                 // Группа ценовых уровней
	NODE_LEAF                        // Листовой узел
)

func (nt NodeType) String() string {
	switch nt {
	case NODE_ROOT:
		return "ROOT"
	case NODE_BUY_SIDE:
		return "BUY_SIDE"
	case NODE_SELL_SIDE:
		return "SELL_SIDE"
	case NODE_PRICE_GROUP:
		return "PRICE_GROUP"
	case NODE_LEAF:
		return "LEAF"
	default:
		return "UNKNOWN"
	}
}

// Memory Pools для минимизации аллокаций
var (
	orderPool = sync.Pool{
		New: func() interface{} {
			return &Order{}
		},
	}
	
	slotPool = sync.Pool{
		New: func() interface{} {
			return &Slot{
				Orders: make([]*Order, 0, 64), // Предаллокация
			}
		},
	}
	
	priceLevelPool = sync.Pool{
		New: func() interface{} {
			return &PriceLevel{}
		},
	}
	
	hashBufferPool = sync.Pool{
		New: func() interface{} {
			return make([]byte, 0, 1024) // Буфер для хеширования
		},
	}
)

// SlotMetadata содержит статические метаданные слота
type SlotMetadata struct {
	Index       int
	Name        string
	Description string
	Priority    int // Приоритет исполнения (0 = высший)
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

// Добавьте метод для быстрого чтения BestBid/Ask БЕЗ LOCK:
func (ob *OrderBook) GetSpreadUnsafe() (bestBid, bestAsk uint64) {
    return atomic.LoadUint64(&ob.BestBid), atomic.LoadUint64(&ob.BestAsk)
}

// getPriceMagnet возвращает "магнитную" цену (round numbers)
func getPriceMagnet(basePrice uint64) []uint64 {
	magnets := make([]uint64, 0, 20)
	
	// Круглые числа: $65000, $64950, $65050 и т.д.
	roundBase := (basePrice / 5000) * 5000 // Округляем до $50
	
	for i := -5; i <= 5; i++ {
		magnets = append(magnets, roundBase+uint64(i*5000))
	}
	
	return magnets
}

// generatePrice генерирует цену для трейдера с учетом профиля
func generatePrice(basePrice uint64, profile TraderProfile, side Side) uint64 {
	spread := profile.PriceSpread
	
	if side == BUY {
		// BUY ордера ВСЕГДА НИЖЕ базовой цены
		var offset int
		
		if profile.Type == TRADER_MARKET_MAKER {
			offset = rand.Intn(50) + 1 // MM очень близко
		} else {
			offset = rand.Intn(spread) + 1
		}
		
		price := int64(basePrice) - int64(offset)
		if price < 100 {
			price = 100
		}
		
		return uint64(price)
		
	} else { // SELL
		// SELL ордера ВСЕГДА ВЫШЕ базовой цены
		var offset int
		
		if profile.Type == TRADER_MARKET_MAKER {
			offset = rand.Intn(50) + 1 // MM очень близко
		} else {
			offset = rand.Intn(spread) + 1
		}
		
		price := int64(basePrice) + int64(offset)
		
		return uint64(price)
	}
}

// generatePriceWithMagnetism генерирует цену с "притяжением" к круглым числам
func generatePriceWithMagnetism(basePrice uint64, profile TraderProfile, side Side) uint64 {
	// 40% шанс использовать "магнитную" цену
	if rand.Float32() < 0.4 {
		magnets := getPriceMagnet(basePrice)
		
		if side == BUY {
			// Для BUY выбираем магниты НИЖЕ basePrice
			lowerMagnets := make([]uint64, 0)
			for _, m := range magnets {
				if m < basePrice {
					lowerMagnets = append(lowerMagnets, m)
				}
			}
			
			if len(lowerMagnets) == 0 {
				// Fallback если нет подходящих магнитов
				return generatePrice(basePrice, profile, BUY)
			}
			
			magnetPrice := lowerMagnets[rand.Intn(len(lowerMagnets))]
			
			// Небольшой offset вниз
			offset := rand.Intn(100)
			price := int64(magnetPrice) - int64(offset)
			if price < 100 {
				price = 100
			}
			return uint64(price)
			
		} else { // SELL
			// Для SELL выбираем магниты ВЫШЕ basePrice
			higherMagnets := make([]uint64, 0)
			for _, m := range magnets {
				if m > basePrice {
					higherMagnets = append(higherMagnets, m)
				}
			}
			
			if len(higherMagnets) == 0 {
				// Fallback если нет подходящих магнитов
				return generatePrice(basePrice, profile, SELL)
			}
			
			magnetPrice := higherMagnets[rand.Intn(len(higherMagnets))]
			
			// Небольшой offset вверх
			offset := rand.Intn(100)
			price := int64(magnetPrice) + int64(offset)
			return uint64(price)
		}
	}
	
	// Иначе используем обычную генерацию
	return generatePrice(basePrice, profile, side)
}

// Вспомогательные функции для работы с пулами
func getOrderFromPool() *Order {
	return orderPool.Get().(*Order)
}

func putOrderToPool(o *Order) {
	// Очищаем данные перед возвратом в пул
	*o = Order{}
	orderPool.Put(o)
}

func getPriceLevelFromPool() *PriceLevel {
	pl := priceLevelPool.Get().(*PriceLevel)
	
	// Инициализируем ВСЕ 16 слотов со статическими метаданными
	for i := 0; i < VERKLE_WIDTH; i++ {
		if pl.Slots[i] == nil {
			pl.Slots[i] = &Slot{
				Metadata: &SlotMetadataTable[i], // Ссылка на статическую метадату
				Orders:   make([]*Order, 0, 64),
				Volume:   0,
			}
		} else {
			// Слот уже существует, просто очищаем
			pl.Slots[i].Orders = pl.Slots[i].Orders[:0]
			pl.Slots[i].Volume = 0
			// Метаданные не трогаем - они статические
		}
	}
	
	pl.Price = 0
	pl.TotalVolume = 0
	
	return pl
}

// putPriceLevelToPool возвращает PriceLevel в пул (слоты НЕ удаляем)
func putPriceLevelToPool(pl *PriceLevel) {
	// Очищаем все слоты, но НЕ устанавливаем в nil
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

// getSlotFromPool больше не нужна для PriceLevel, но оставим для совместимости
func getSlotFromPool() *Slot {
	s := slotPool.Get().(*Slot)
	s.Orders = s.Orders[:0]
	s.Volume = 0
	return s
}

// putSlotToPool больше не используется для слотов в PriceLevel
func putSlotToPool(s *Slot) {
	s.Orders = s.Orders[:0]
	s.Volume = 0
	slotPool.Put(s)
}

// Side - сторона ордера
type Side int

const (
	BUY Side = iota
	SELL
)

// Trade - структура исполненной сделки
type Trade struct {
	TradeID       uint64  // Уникальный ID трейда
	TakerOrderID  uint64  // ID ордера инициатора (taker)
	MakerOrderID  uint64  // ID ордера из книги (maker)
	TakerTraderID uint32  // ID трейдера-инициатора
	MakerTraderID uint32  // ID трейдера из книги
	Price         uint64  // Цена исполнения
	Size          uint64  // Объем исполнения
	TakerSide     Side    // Сторона taker (BUY/SELL)
	TakerPartial  bool    // Частичное заполнение taker ордера
	MakerPartial  bool    // Частичное заполнение maker ордера
	Timestamp     int64   // Unix timestamp в наносекундах
}

// TradeJSON - JSON представление трейда
type TradeJSON struct {
	TradeID       uint64  `json:"trade_id"`
	TakerOrderID  uint64  `json:"taker_order_id"`
	MakerOrderID  uint64  `json:"maker_order_id"`
	TakerTraderID uint32  `json:"taker_trader_id"`
	MakerTraderID uint32  `json:"maker_trader_id"`
	Price         float64 `json:"price"`
	Size          float64 `json:"size"`
	TakerSide     string  `json:"taker_side"`
	MakerSide     string  `json:"maker_side"`
	TakerPartial  bool    `json:"taker_partial"`
	MakerPartial  bool    `json:"maker_partial"`
	Timestamp     int64   `json:"timestamp"`
}

func (s Side) String() string {
	if s == BUY {
		return "BUY"
	}
	return "SELL"
}

// Order - структура ордера
type Order struct {
	ID            uint64  // Уникальный последовательный ID
	TraderID      uint32  // ID трейдера
	Price         uint64  // Цена в целых числах (умножена на 100)
	Size          uint64  // Объем ордера
	FilledSize    uint64  // Уже исполненный объем
	Side          Side    // Сторона (BUY/SELL)
	Slot          uint8   // Слот в Verkle дереве
	IsPartialFill bool    // Флаг частичного заполнения
}

// RemainingSize возвращает неисполненный объем
func (o *Order) RemainingSize() uint64 {
	if o.FilledSize >= o.Size {
		return 0
	}
	return o.Size - o.FilledSize
}

// IsFilled проверяет полностью ли исполнен ордер
func (o *Order) IsFilled() bool {
	return o.FilledSize >= o.Size
}

// safeSubtract безопасно вычитает с защитой от underflow
func safeSubtract(from, value uint64) uint64 {
	if value > from {
		fmt.Printf("⚠️  UNDERFLOW PREVENTED: попытка вычесть %d из %d\n", value, from)
		return 0
	}
	return from - value
}

// safeAdd безопасно складывает с защитой от overflow
func safeAdd(a, b uint64) uint64 {
	if a > ^uint64(0)-b { // Проверка переполнения
		fmt.Printf("⚠️  OVERFLOW PREVENTED: попытка сложить %d + %d\n", a, b)
		return ^uint64(0) // Возвращаем максимальное значение
	}
	return a + b
}

// PriceLevel - уровень цены, содержит слоты с ордерами
type PriceLevel struct {
	Price       uint64              // Цена этого уровня
	TotalVolume uint64              // Суммарный объем всех ордеров на уровне
	Slots       [VERKLE_WIDTH]*Slot // 16 слотов для распределения ордеров
}

// Slot - слот внутри ценового уровня
type Slot struct {
	Metadata *SlotMetadata // Указатель на статические метаданные
	Orders []*Order // Список ордеров в слоте (FIFO)
	Volume uint64   // Суммарный объем ордеров в слоте
}

// VerkleNode - узел Verkle дерева
type VerkleNode struct {
	Hash     [32]byte              // Blake3 хеш узла
	Children [VERKLE_WIDTH]interface{} // Дочерние узлы или price levels
	IsLeaf   bool                  // Является ли узел листом
	NodeType NodeType                  // Тип узла
	Metadata string                    // Дополнительные метаданные (например, "BUY", "SELL")
}

// OrderBook - основной класс ордербука
type OrderBook struct {
	Symbol       string                    // Символ инструмента (BTC)
	nextOrderID  uint64                    // Атомарный счетчик для ID ордеров
	nextTradeID  uint64                    // Атомарный счетчик для ID трейдов
	BuyLevels    map[uint64]*PriceLevel   // Bid уровни
	SellLevels   map[uint64]*PriceLevel   // Ask уровни
	OrderIndex   map[uint64]*Order        // Индекс всех ордеров по ID
	Trades       []*Trade                 // История всех трейдов
	Root         *VerkleNode              // Корень Verkle дерева
	LastRootHash [32]byte                 // Последний вычисленный root hash
	
	BestBid      uint64                   // Лучшая цена покупки
	BestAsk      uint64                   // Лучшая цена продажи
	
	bestBidAtomic  atomic.Uint64  // atomic доступ без lock
    bestAskAtomic  atomic.Uint64  // atomic доступ без lock
	
	// Кэш отсортированных цен
    buyPricesSorted  []uint64
    sellPricesSorted []uint64
    pricesCacheDirty atomic.Bool
	
	mu           sync.RWMutex             // Mutex для защиты
	hashTicker   *time.Ticker             // Ticker для хеширования
	stopChan     chan struct{}            // Канал для остановки
	stats        Stats                    // Статистика
	hashRequest  chan struct{}            // Канал для запроса хеша
}

// Stats - статистика ордербука
type Stats struct {
	TotalOperations  uint64
	TotalOrders      uint64
	TotalMatches     uint64
	TotalCancels     uint64
	TotalModifies    uint64
	TotalMarketOrders uint64
	LastHashTime     time.Time
	HashCount        uint64
	
	LockWaitTime      int64  // Наносекунды ожидания lock
    LockAcquisitions  uint64 // Количество захватов
}

//=== JSON 
// Структуры для JSON экспорта
type OrderJSON struct {
	ID       uint64  `json:"id"`
	TraderID uint32  `json:"trader_id"`
	Price    float64 `json:"price"`
	Size     float64 `json:"size"`
	Side     string  `json:"side"`
}

type SlotJSON struct {
	SlotIndex   int          `json:"slot_index"`
	SlotName    string       `json:"slot_name"`
	Description string       `json:"description"`
	Priority    int          `json:"priority"`
	Volume      float64      `json:"volume"`
	OrdersCount int          `json:"orders_count"`
	Orders      []OrderJSON  `json:"orders,omitempty"` // omitempty для компактности
}

type PriceLevelJSON struct {
	Price       float64    `json:"price"`
	TotalVolume float64    `json:"total_volume"`
	Slots       []SlotJSON `json:"slots"`
	Hash        string     `json:"hash"`
}

type VerkleNodeJSON struct {
	Hash     string              `json:"hash"`
	NodeType string              `json:"node_type"`
	Metadata string              `json:"metadata,omitempty"`
	Children []interface{}       `json:"children,omitempty"`
	Stats    *NodeStatsJSON      `json:"stats,omitempty"` // Статистика узла
}

type NodeStatsJSON struct {
	ChildrenCount int     `json:"children_count"`
	TotalOrders   int     `json:"total_orders"`
	TotalVolume   float64 `json:"total_volume"`
}

type OrderBookStateJSON struct {
	Symbol          string             `json:"symbol"`
	RootHash        string             `json:"root_hash"`
	ActiveOrders    int                `json:"active_orders"`
	BuyLevelsCount  int                `json:"buy_levels_count"`
	SellLevelsCount int                `json:"sell_levels_count"`
	BestBid         float64            `json:"best_bid"`
	BestAsk         float64            `json:"best_ask"`
	Spread          float64            `json:"spread"`
	Stats           StatsJSON          `json:"stats"`
	RecentTrades    []TradeJSON        `json:"recent_trades"` // Последние трейды
	Tree            VerkleNodeJSON     `json:"tree"`
	BuyLevels       []PriceLevelJSON   `json:"buy_levels"`
	SellLevels      []PriceLevelJSON   `json:"sell_levels"`
}

type StatsJSON struct {
	TotalOperations  uint64 `json:"total_operations"`
	TotalOrders      uint64 `json:"total_orders"`
	TotalMatches     uint64 `json:"total_matches"`
	TotalCancels     uint64 `json:"total_cancels"`
	TotalModifies    uint64 `json:"total_modifies"`
	TotalMarketOrders uint64 `json:"total_market_orders"`
	HashCount        uint64 `json:"hash_count"`
}

// ExportToJSON - обновленная версия с трейдами
func (ob *OrderBook) ExportToJSON(filename string) error {
	ob.mu.RLock()
	defer ob.mu.RUnlock()
	
	ob.rebuildTree()
	ob.computeRootHash()
	
	bestBid := ob.bestBidAtomic.Load()
	bestAsk := ob.bestAskAtomic.Load()
	
	spread := 0.0
	if bestBid > 0 && bestAsk > 0 {
		spread = float64(bestAsk - bestBid) / PRICE_DECIMALS
	}
	
	// Сериализуем последние 100 трейдов
	recentTrades := make([]TradeJSON, 0)
	tradesLimit := 100
	if tradesLimit > len(ob.Trades) {
		tradesLimit = len(ob.Trades)
	}
	startIdx := len(ob.Trades) - tradesLimit
	
	for i := startIdx; i < len(ob.Trades); i++ {
		trade := ob.Trades[i]
		makerSide := "SELL"
		if trade.TakerSide == SELL {
			makerSide = "BUY"
		}
		
		recentTrades = append(recentTrades, TradeJSON{
			TradeID:       trade.TradeID,
			TakerOrderID:  trade.TakerOrderID,
			MakerOrderID:  trade.MakerOrderID,
			TakerTraderID: trade.TakerTraderID,
			MakerTraderID: trade.MakerTraderID,
			Price:         float64(trade.Price) / PRICE_DECIMALS,
			Size:          float64(trade.Size) / PRICE_DECIMALS,
			TakerSide:     trade.TakerSide.String(),
			MakerSide:     makerSide,
			TakerPartial:  trade.TakerPartial,
			MakerPartial:  trade.MakerPartial,
			Timestamp:     trade.Timestamp,
		})
	}
	
	state := OrderBookStateJSON{
		Symbol:          ob.Symbol,
		RootHash:        hex.EncodeToString(ob.LastRootHash[:]),
		ActiveOrders:    len(ob.OrderIndex),
		BuyLevelsCount:  len(ob.BuyLevels),
		SellLevelsCount: len(ob.SellLevels),
		BestBid:         float64(bestBid) / PRICE_DECIMALS,
		BestAsk:         float64(bestAsk) / PRICE_DECIMALS,
		Spread:          spread,
		Stats:           StatsJSON{
			TotalOperations:   ob.stats.TotalOperations,
			TotalOrders:       ob.stats.TotalOrders,
			TotalMatches:      ob.stats.TotalMatches,
			TotalCancels:      ob.stats.TotalCancels,
			TotalModifies:     ob.stats.TotalModifies,
			TotalMarketOrders: ob.stats.TotalMarketOrders,
			HashCount:         ob.stats.HashCount,
		},
		RecentTrades:    recentTrades,
		Tree:            ob.serializeVerkleNode(ob.Root),
		BuyLevels:       ob.serializeLevels(ob.BuyLevels),
		SellLevels:      ob.serializeLevels(ob.SellLevels),
	}
	
	jsonData, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return fmt.Errorf("ошибка сериализации JSON: %w", err)
	}
	
	err = os.WriteFile(filename, jsonData, 0644)
	if err != nil {
		return fmt.Errorf("ошибка записи файла: %w", err)
	}
	
	fmt.Printf("✓ Состояние экспортировано в %s (%.2f KB, %d трейдов)\n", 
		filename, float64(len(jsonData))/1024, len(recentTrades))
	
	return nil
}

// GetSideHashes возвращает хеши BUY и SELL сторон отдельно
func (ob *OrderBook) GetSideHashes() (buyHash [32]byte, sellHash [32]byte) {
	ob.mu.RLock()
	defer ob.mu.RUnlock()
	
	ob.rebuildTree()
	
	if buyNode, ok := ob.Root.Children[0].(*VerkleNode); ok {
		buyHash = ob.hashNode(buyNode)
	}
	
	if sellNode, ok := ob.Root.Children[1].(*VerkleNode); ok {
		sellHash = ob.hashNode(sellNode)
	}
	
	return
}

// PrintTreeStructure выводит структуру дерева в консоль
// PrintTreeStructure выводит структуру дерева в консоль
func (ob *OrderBook) PrintTreeStructure(mode TreePrintMode) {
	ob.mu.Lock()
	
	// Валидация и исправление
	fmt.Println("🔍 Проверка консистентности...")
	validationCount := 0
	for _, level := range ob.BuyLevels {
		ob.validateAndFixLevelConsistency(level)
		validationCount++
	}
	for _, level := range ob.SellLevels {
		ob.validateAndFixLevelConsistency(level)
		validationCount++
	}
	fmt.Printf("✓ Проверено %d ценовых уровней\n", validationCount)
	
	ob.mu.Unlock()
	
	ob.mu.RLock()
	defer ob.mu.RUnlock()
	
	ob.rebuildTree()
	ob.computeRootHash()
	
	fmt.Println("\n🌳 СТРУКТУРА VERKLE ДЕРЕВА")
	fmt.Println("═══════════════════════════════════════════")
	
	switch mode {
	case TREE_PRINT_COMPACT:
		ob.printTreeCompact()
	case TREE_PRINT_SUMMARY:
		ob.printTreeSummary()
	case TREE_PRINT_FULL:
		ob.printTreeFull()
	}
	
	fmt.Println("═══════════════════════════════════════════\n")
}

// printTreeFull выводит полное дерево
func (ob *OrderBook) printTreeFull() {
	fmt.Println("Режим: ПОЛНОЕ ДЕРЕВО")
	fmt.Println()
	
	stats := &TreeStats{
		TotalNodes:  0,
		PriceLevels: 0,
		TotalOrders: 0,
	}
	
	ob.printNodeRecursiveFull(ob.Root, 0, stats)
	
	fmt.Println()
	fmt.Printf("📊 Статистика вывода:\n")
	fmt.Printf("  • Узлов: %d\n", stats.TotalNodes)
	fmt.Printf("  • Ценовых уровней: %d\n", stats.PriceLevels)
	fmt.Printf("  • Ордеров: %d\n", stats.TotalOrders)
}

// printNodeRecursiveFull выводит полное дерево с подсчетом статистики
func (ob *OrderBook) printNodeRecursiveFull(node interface{}, depth int, stats *TreeStats) {
	indent := ""
	for i := 0; i < depth; i++ {
		indent += "  "
	}
	
	switch n := node.(type) {
	case *VerkleNode:
		stats.TotalNodes++
		
		childCount := 0
		for i := 0; i < VERKLE_WIDTH; i++ {
			if n.Children[i] != nil {
				childCount++
			}
		}
		
		fmt.Printf("%s├─ [%s] %s (hash: %x..., children: %d)\n",
			indent, n.NodeType.String(), n.Metadata, n.Hash[:4], childCount)
		
		// Рекурсивно выводим всех детей
		for i := 0; i < VERKLE_WIDTH; i++ {
			if n.Children[i] != nil {
				ob.printNodeRecursiveFull(n.Children[i], depth+1, stats)
			}
		}
		
	case *PriceLevel:
		stats.TotalNodes++
		stats.PriceLevels++  // <- ИСПРАВЛЕНО
		
		ordersCount := 0
		for _, slot := range n.Slots {
			ordersCount += len(slot.Orders)
		}
		stats.TotalOrders += ordersCount
		
		// Пропускаем полностью пустые уровни
		if ordersCount == 0 && n.TotalVolume == 0 {
			return
		}
		
		fmt.Printf("%s├─ [PRICE] %.2f (volume: %.2f, orders: %d)\n",
			indent, 
			float64(n.Price)/PRICE_DECIMALS,
			float64(n.TotalVolume)/PRICE_DECIMALS,
			ordersCount)
	}
}

// collectTreeStats собирает статистику по дереву
func (ob *OrderBook) collectTreeStats(node interface{}) TreeStats {
	stats := TreeStats{}
	
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
			}
		}
		
	case *PriceLevel:
		stats.TotalNodes++
		stats.PriceLevels++
		stats.TotalVolume += n.TotalVolume
		
		for _, slot := range n.Slots {
			stats.TotalOrders += len(slot.Orders)
		}
	}
	
	return stats
}

// printTreeCompact выводит компактное представление (топ уровни)
func (ob *OrderBook) printTreeCompact() {
	fmt.Println("Режим: КОМПАКТНЫЙ (Топ-10 с каждой стороны)")
	fmt.Println()
	
	// Корневой узел
	fmt.Printf("├─ [ROOT] %s (hash: %x...)\n", ob.Root.Metadata, ob.Root.Hash[:4])
	
	// BUY сторона
	if buyNode, ok := ob.Root.Children[0].(*VerkleNode); ok {
		fmt.Printf("  ├─ [BUY_SIDE] (hash: %x..., levels: %d)\n", 
			buyNode.Hash[:4], len(ob.BuyLevels))
		ob.printTopLevels(ob.BuyLevels, true, 10)
	}
	
	// SELL сторона
	if sellNode, ok := ob.Root.Children[1].(*VerkleNode); ok {
		fmt.Printf("  ├─ [SELL_SIDE] (hash: %x..., levels: %d)\n", 
			sellNode.Hash[:4], len(ob.SellLevels))
		ob.printTopLevels(ob.SellLevels, false, 10)
	}
}

// printTopLevels выводит топ N ценовых уровней
func (ob *OrderBook) printTopLevels(levels map[uint64]*PriceLevel, isBuy bool, limit int) {
	// Собираем и сортируем цены
	prices := make([]uint64, 0, len(levels))
	for price, level := range levels {
		if level.TotalVolume > 0 { // Только непустые
			prices = append(prices, price)
		}
	}
	
	// Сортируем
	if isBuy {
		sort.Slice(prices, func(i, j int) bool { return prices[i] > prices[j] })
	} else {
		sort.Slice(prices, func(i, j int) bool { return prices[i] < prices[j] })
	}
	
	// Ограничиваем количество
	if len(prices) > limit {
		prices = prices[:limit]
	}
	
	// Выводим уровни
	for idx, price := range prices {
		level := levels[price]
		ordersCount := 0
		for _, slot := range level.Slots {
			ordersCount += len(slot.Orders)
		}
		
		prefix := "    ├─"
		if idx == len(prices)-1 {
			prefix = "    └─"
		}
		
		fmt.Printf("%s [PRICE] %.2f (volume: %.2f, orders: %d)\n",
			prefix,
			float64(level.Price)/PRICE_DECIMALS,
			float64(level.TotalVolume)/PRICE_DECIMALS,
			ordersCount)
	}
	
	if len(prices) < len(levels) {
		fmt.Printf("    ... еще %d уровней (используйте TREE_PRINT_FULL)\n", 
			len(levels)-len(prices))
	}
}

// TreeStats - статистика дерева
type TreeStats struct {
	TotalNodes  int
	PriceLevels int
	TotalOrders int
	TotalVolume uint64
}

// printTreeSummary выводит только статистику
func (ob *OrderBook) printTreeSummary() {
	fmt.Println("Режим: СТАТИСТИКА")
	fmt.Println()
	
	stats := ob.collectTreeStats(ob.Root)
	
	fmt.Printf("├─ [ROOT] %s\n", ob.Root.Metadata)
	fmt.Printf("│  • Root hash: %x...\n", ob.Root.Hash[:8])
	fmt.Printf("│\n")
	
	if buyNode, ok := ob.Root.Children[0].(*VerkleNode); ok {
		buyStats := ob.collectTreeStats(buyNode)
		fmt.Printf("├─ [BUY_SIDE]\n")
		fmt.Printf("│  • Hash: %x...\n", buyNode.Hash[:8])
		fmt.Printf("│  • Уровней: %d\n", buyStats.PriceLevels)
		fmt.Printf("│  • Ордеров: %d\n", buyStats.TotalOrders)
		fmt.Printf("│  • Объем: %.2f\n", float64(buyStats.TotalVolume)/PRICE_DECIMALS)
		fmt.Printf("│  • Узлов: %d\n", buyStats.TotalNodes)
		fmt.Printf("│\n")
	}
	
	if sellNode, ok := ob.Root.Children[1].(*VerkleNode); ok {
		sellStats := ob.collectTreeStats(sellNode)
		fmt.Printf("├─ [SELL_SIDE]\n")
		fmt.Printf("│  • Hash: %x...\n", sellNode.Hash[:8])
		fmt.Printf("│  • Уровней: %d\n", sellStats.PriceLevels)
		fmt.Printf("│  • Ордеров: %d\n", sellStats.TotalOrders)
		fmt.Printf("│  • Объем: %.2f\n", float64(sellStats.TotalVolume)/PRICE_DECIMALS)
		fmt.Printf("│  • Узлов: %d\n", sellStats.TotalNodes)
		fmt.Printf("│\n")
	}
	
	fmt.Printf("├─ ИТОГО:\n")
	fmt.Printf("   • Всего узлов: %d\n", stats.TotalNodes)
	fmt.Printf("   • Всего уровней: %d\n", stats.PriceLevels)
	fmt.Printf("   • Всего ордеров: %d\n", stats.TotalOrders)
	fmt.Printf("   • Общий объем: %.2f\n", float64(stats.TotalVolume)/PRICE_DECIMALS)
}

// printNodeRecursive рекурсивно печатает структуру узла
func (ob *OrderBook) printNodeRecursive(node interface{}, depth int) {
	indent := ""
	for i := 0; i < depth; i++ {
		indent += "  "
	}
	
	switch n := node.(type) {
	case *VerkleNode:
		childCount := 0
		for i := 0; i < VERKLE_WIDTH; i++ {
			if n.Children[i] != nil {
				childCount++
			}
		}
		
		fmt.Printf("%s├─ [%s] %s (hash: %x..., children: %d)\n",
			indent, n.NodeType.String(), n.Metadata, n.Hash[:4], childCount)
		
		for i := 0; i < VERKLE_WIDTH; i++ {
			if n.Children[i] != nil {
				ob.printNodeRecursive(n.Children[i], depth+1)
			}
		}
		
	case *PriceLevel:
		ordersCount := 0
		for _, slot := range n.Slots {
			ordersCount += len(slot.Orders)
		}
		
		// Отображаем статус уровня
		status := ""
		if ordersCount == 0 && n.TotalVolume == 0 {
			status = " [EMPTY - кэшировано]"
		} else if ordersCount == 0 && n.TotalVolume > 0 {
			status = " [⚠️  НЕКОРРЕКТНО: volume без ордеров]"
		}
		
		// Пропускаем полностью пустые уровни в выводе
		if ordersCount == 0 && n.TotalVolume == 0 {
			return // Не показываем пустые кэшированные уровни
		}
		
		fmt.Printf("%s├─ [PRICE] %.2f (volume: %.2f, orders: %d)%s\n",
			indent, 
			float64(n.Price)/PRICE_DECIMALS,
			float64(n.TotalVolume)/PRICE_DECIMALS,
			ordersCount,
			status)
	}
}

// CleanupEmptyLevels удаляет пустые уровни из памяти (опционально)
func (ob *OrderBook) CleanupEmptyLevels() int {
	ob.mu.Lock()
	defer ob.mu.Unlock()
	
	removed := 0
	
	// Очистка BUY уровней
	for price, level := range ob.BuyLevels {
		if level.TotalVolume == 0 {
			delete(ob.BuyLevels, price)
			putPriceLevelToPool(level)
			removed++
		}
	}
	
	// Очистка SELL уровней
	for price, level := range ob.SellLevels {
		if level.TotalVolume == 0 {
			delete(ob.SellLevels, price)
			putPriceLevelToPool(level)
			removed++
		}
	}
	
	// Обновляем BestBid/BestAsk после очистки
	if removed > 0 {
		ob.updateBestPrices()
		fmt.Printf("🧹 Очищено %d пустых ценовых уровней\n", removed)
	}
	
	return removed
}

// serializeVerkleNode рекурсивно сериализует узел Verkle дерева
func (ob *OrderBook) serializeVerkleNode(node *VerkleNode) VerkleNodeJSON {
	result := VerkleNodeJSON{
		Hash:     hex.EncodeToString(node.Hash[:]),
		NodeType: node.NodeType.String(),
		Metadata: node.Metadata,
		Children: make([]interface{}, 0),
	}
	
	// Собираем статистику узла
	stats := &NodeStatsJSON{}
	
	for i := 0; i < VERKLE_WIDTH; i++ {
		switch child := node.Children[i].(type) {
		case *VerkleNode:
			result.Children = append(result.Children, ob.serializeVerkleNode(child))
			stats.ChildrenCount++
			
		case *PriceLevel:
			levelJSON := ob.serializePriceLevel(child)
			result.Children = append(result.Children, levelJSON)
			stats.ChildrenCount++
			
			// Считаем ордера и объем
			for _, slot := range child.Slots {
				stats.TotalOrders += len(slot.Orders)
			}
			stats.TotalVolume += float64(child.TotalVolume) / PRICE_DECIMALS
		}
	}
	
	if stats.ChildrenCount > 0 {
		result.Stats = stats
	}
	
	return result
}

// serializePriceLevel сериализует ценовой уровень
func (ob *OrderBook) serializePriceLevel(level *PriceLevel) PriceLevelJSON {
	hash := ob.hashPriceLevel(level)
	
	result := PriceLevelJSON{
		Price:       float64(level.Price) / PRICE_DECIMALS,
		TotalVolume: float64(level.TotalVolume) / PRICE_DECIMALS,
		Hash:        hex.EncodeToString(hash[:]),
		Slots:       make([]SlotJSON, 0, VERKLE_WIDTH),
	}
	
	// Сериализуем ВСЕ слоты (даже пустые для наглядности)
	for i := 0; i < VERKLE_WIDTH; i++ {
		slot := level.Slots[i]
		
		slotJSON := SlotJSON{
			SlotIndex:   i,
			SlotName:    slot.Metadata.Name,
			Description: slot.Metadata.Description,
			Priority:    slot.Metadata.Priority,
			Volume:      float64(slot.Volume) / PRICE_DECIMALS,
			OrdersCount: len(slot.Orders),
			Orders:      make([]OrderJSON, 0),
		}
		
		// Добавляем ордера только если они есть (для компактности)
		if len(slot.Orders) > 0 {
			maxOrders := 5 // Ограничиваем для читаемости
			for idx, order := range slot.Orders {
				if idx >= maxOrders {
					break
				}
				slotJSON.Orders = append(slotJSON.Orders, OrderJSON{
					ID:       order.ID,
					TraderID: order.TraderID,
					Price:    float64(order.Price) / PRICE_DECIMALS,
					Size:     float64(order.Size) / PRICE_DECIMALS,
					Side:     order.Side.String(),
				})
			}
		}
		
		// Добавляем слот в результат только если в нем есть объем
		if slot.Volume > 0 {
			result.Slots = append(result.Slots, slotJSON)
		}
	}
	
	return result
}

// serializeLevels сериализует все ценовые уровни
func (ob *OrderBook) serializeLevels(levels map[uint64]*PriceLevel) []PriceLevelJSON {
	result := make([]PriceLevelJSON, 0, len(levels))
	
	// Ограничиваем количество уровней для читаемости (топ-20)
	maxLevels := 20
	count := 0
	
	for _, level := range levels {
		if count >= maxLevels {
			break
		}
		result = append(result, ob.serializePriceLevel(level))
		count++
	}
	
	return result
}

// ExportToJSONCompact экспортирует компактную версию (без деталей ордеров)
func (ob *OrderBook) ExportToJSONCompact(filename string) error {
	ob.mu.RLock()
	defer ob.mu.RUnlock()
	
	ob.rebuildTree()
	ob.computeRootHash()
	
	bestBid := ob.bestBidAtomic.Load()
	bestAsk := ob.bestAskAtomic.Load()
	
	// Упрощенная структура только со статистикой и топ уровнями
	type CompactLevel struct {
		Price       float64 `json:"price"`
		TotalVolume float64 `json:"volume"`
		OrdersCount int     `json:"orders_count"`
	}
	
	type CompactState struct {
		Symbol          string         `json:"symbol"`
		RootHash        string         `json:"root_hash"`
		ActiveOrders    int            `json:"active_orders"`
		BuyLevelsCount  int            `json:"buy_levels"`
		SellLevelsCount int            `json:"sell_levels"`
		BestBid         float64        `json:"best_bid"`
		BestAsk         float64        `json:"best_ask"`
		Spread          float64        `json:"spread"`
		Stats           StatsJSON      `json:"stats"`
		TopBuyLevels    []CompactLevel `json:"top_buy_levels"`
		TopSellLevels   []CompactLevel `json:"top_sell_levels"`
	}
	
	spread := 0.0
	if bestAsk > 0 && bestBid > 0 {
		spread = float64(bestAsk - bestBid) / PRICE_DECIMALS
	}
	
	state := CompactState{
		Symbol:          ob.Symbol,
		RootHash:        hex.EncodeToString(ob.LastRootHash[:]),
		ActiveOrders:    len(ob.OrderIndex),
		BuyLevelsCount:  len(ob.BuyLevels),
		SellLevelsCount: len(ob.SellLevels),
		BestBid:         float64(bestBid) / PRICE_DECIMALS,
		BestAsk:         float64(bestAsk) / PRICE_DECIMALS,
		Spread:          spread,
		Stats: StatsJSON{
			TotalOperations:   ob.stats.TotalOperations,
			TotalOrders:       ob.stats.TotalOrders,
			TotalMatches:      ob.stats.TotalMatches,
			TotalCancels:      ob.stats.TotalCancels,
			TotalModifies:     ob.stats.TotalModifies,
			TotalMarketOrders: ob.stats.TotalMarketOrders,
			HashCount:         ob.stats.HashCount,
		},
		TopBuyLevels:  make([]CompactLevel, 0),
		TopSellLevels: make([]CompactLevel, 0),
	}
	
	// Топ-10 buy уровней
	buyPrices := make([]uint64, 0, len(ob.BuyLevels))
	for price := range ob.BuyLevels {
		buyPrices = append(buyPrices, price)
	}
	sort.Slice(buyPrices, func(i, j int) bool { return buyPrices[i] > buyPrices[j] })
	
	for i := 0; i < len(buyPrices) && i < 10; i++ {
		level := ob.BuyLevels[buyPrices[i]]
		ordersCount := 0
		for _, slot := range level.Slots {
			ordersCount += len(slot.Orders)
		}
		state.TopBuyLevels = append(state.TopBuyLevels, CompactLevel{
			Price:       float64(level.Price) / PRICE_DECIMALS,
			TotalVolume: float64(level.TotalVolume) / PRICE_DECIMALS,
			OrdersCount: ordersCount,
		})
	}
	
	// Топ-10 sell уровней
	sellPrices := make([]uint64, 0, len(ob.SellLevels))
	for price := range ob.SellLevels {
		sellPrices = append(sellPrices, price)
	}
	sort.Slice(sellPrices, func(i, j int) bool { return sellPrices[i] < sellPrices[j] })
	
	for i := 0; i < len(sellPrices) && i < 10; i++ {
		level := ob.SellLevels[sellPrices[i]]
		ordersCount := 0
		for _, slot := range level.Slots {
			ordersCount += len(slot.Orders)
		}
		state.TopSellLevels = append(state.TopSellLevels, CompactLevel{
			Price:       float64(level.Price) / PRICE_DECIMALS,
			TotalVolume: float64(level.TotalVolume) / PRICE_DECIMALS,
			OrdersCount: ordersCount,
		})
	}
	
	jsonData, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return fmt.Errorf("ошибка сериализации JSON: %w", err)
	}
	
	err = os.WriteFile(filename, jsonData, 0644)
	if err != nil {
		return fmt.Errorf("ошибка записи файла: %w", err)
	}
	
	fmt.Printf("✓ Компактное состояние экспортировано в %s (%.2f KB)\n", 
		filename, float64(len(jsonData))/1024)
	
	return nil
}
//========

// NewOrderBook создает новый ордербук
func NewOrderBook(symbol string) *OrderBook {
	ob := &OrderBook{
		Symbol:      symbol,
		nextOrderID: 0,
		nextTradeID: 0,
		BuyLevels:   make(map[uint64]*PriceLevel),
		SellLevels:  make(map[uint64]*PriceLevel),
		OrderIndex:  make(map[uint64]*Order),
		Trades:      make([]*Trade, 0, 100_000), // Предаллокация для трейдов
		Root:        &VerkleNode{IsLeaf: false},
				
		//hashTicker:  time.NewTicker(HASH_INTERVAL),
		stopChan:    make(chan struct{}),
		hashRequest: make(chan struct{}, 1),
	}
	
	// Инициализация atomic полей
	ob.bestBidAtomic.Store(0)
	ob.bestAskAtomic.Store(0)
	
	// Условный запуск хеширования
    if HASH_INTERVAL > 0 {
        ob.hashTicker = time.NewTicker(HASH_INTERVAL)
        go ob.periodicHasher()
        go ob.hashWorker()
    }
	
	//go ob.periodicHasher()
	//go ob.hashWorker()
	
	return ob
}

// Stop останавливает ордербук и фоновые горутины
func (ob *OrderBook) Stop() {
	close(ob.stopChan)
	
	if HASH_INTERVAL > 0 {
		ob.hashTicker.Stop()
	}
}

// periodicHasher - горутина для периодического пересчета хеша
func (ob *OrderBook) periodicHasher() {
	for {
		select {
		case <-ob.hashTicker.C:
			// Неблокирующая отправка запроса
			select {
			case ob.hashRequest <- struct{}{}:
			default:
				// Канал занят - пропускаем
			}
		case <-ob.stopChan:
			return
		}
	}
}

func (ob *OrderBook) hashWorker() {
	for {
		select {
		case <-ob.hashRequest:
			ob.mu.RLock()
			ob.rebuildTree()
			ob.computeRootHash()
			atomic.AddUint64(&ob.stats.HashCount, 1)
			ob.stats.LastHashTime = time.Now()
			//rootHash := ob.LastRootHash
			ob.mu.RUnlock()
			
			//fmt.Printf("⏱  Периодический хеш [%s]: %x...\n", time.Now().Format("15:04:05.000"), rootHash[:8])
		case <-ob.stopChan:
			return
		}
	}
}

// determineSlot определяет слот для ордера на основе размера и типа трейдера
func (ob *OrderBook) determineSlot(order *Order) uint8 {
	// VIP-трейдеры (ID < 100)
	if order.TraderID < 100 {
		return SLOT_VIP
	}
	
	// Мелкие retail ордера (объем < $10 = 1000 центов)
	if order.Size < 1000 {
		return SLOT_SMALL_RETAIL
	}
	
	// Распределяем остальные retail ордера равномерно по слотам 3-14
	slotRange := SLOT_RETAIL_END - SLOT_RETAIL_START + 1
	slot := SLOT_RETAIL_START + uint8(order.TraderID%uint32(slotRange))
	return slot
}

// AddLimitOrderBatch добавляет несколько ордеров за один lock
func (ob *OrderBook) AddLimitOrderBatch(orders []struct{
    TraderID uint32
    Price    uint64
    Size     uint64
    Side     Side
}) []*Order {
    ob.mu.Lock()
    defer ob.mu.Unlock()
    
    results := make([]*Order, 0, len(orders))
    
    for _, req := range orders {
        order := getOrderFromPool()
        order.ID = atomic.AddUint64(&ob.nextOrderID, 1)
        order.TraderID = req.TraderID
        order.Price = req.Price
        order.Size = req.Size
        order.FilledSize = 0
        order.IsPartialFill = false
        order.Side = req.Side
        order.Slot = ob.determineSlot(order)
        
        // Матчинг и добавление (без unlock)
        ob.tryMatchUnsafe(order)
        
        if !order.IsFilled() {
            levels := ob.BuyLevels
            if req.Side == SELL {
                levels = ob.SellLevels
            }
            
            level, exists := levels[req.Price]
            if !exists {
                level = getPriceLevelFromPool()
                level.Price = req.Price
                level.TotalVolume = 0
                levels[req.Price] = level
				
				ob.pricesCacheDirty.Store(true)
            }
            
            remainingSize := order.RemainingSize()
            slot := level.Slots[order.Slot]
            slot.Orders = append(slot.Orders, order)
            slot.Volume = safeAdd(slot.Volume, remainingSize)
            level.TotalVolume = safeAdd(level.TotalVolume, remainingSize)
            
            ob.OrderIndex[order.ID] = order
        }
        
        results = append(results, order)
        atomic.AddUint64(&ob.stats.TotalOrders, 1)
        atomic.AddUint64(&ob.stats.TotalOperations, 1)
    }
    
    ob.updateBestPrices()
    
    return results
}

// AddLimitOrder добавляет лимитный ордер в ордербук
func (ob *OrderBook) AddLimitOrder(traderID uint32, price uint64, size uint64, side Side) *Order {
	order := getOrderFromPool()
	order.ID = atomic.AddUint64(&ob.nextOrderID, 1)
	order.TraderID = traderID
	order.Price = price
	order.Size = size
	order.FilledSize = 0           // Сброс заполнения
	order.IsPartialFill = false    // Сброс флага
	order.Side = side
	order.Slot = ob.determineSlot(order)
	
	//Perf debug
	lockStart := time.Now()
    ob.mu.Lock()
		lockWait := time.Since(lockStart).Nanoseconds()
		atomic.AddInt64(&ob.stats.LockWaitTime, lockWait)
		atomic.AddUint64(&ob.stats.LockAcquisitions, 1)
    defer ob.mu.Unlock()
	
	//ob.mu.Lock()
	//defer ob.mu.Unlock()
	
	// Пытаемся сматчить ордер
	ob.tryMatchUnsafe(order)
	
	// Если ордер не исполнен полностью - добавляем в книгу
	if !order.IsFilled() {
		levels := ob.BuyLevels
		if side == SELL {
			levels = ob.SellLevels
		}
		
		level, exists := levels[price]
		if !exists {
			level = getPriceLevelFromPool()
			level.Price = price
			level.TotalVolume = 0
			levels[price] = level
			
			// Обновляем BestBid/BestAsk
			// Оптимизация: точечное обновление только для нового уровня
			if side == SELL {
				currentBestAsk := ob.bestAskAtomic.Load()
				if currentBestAsk == 0 || price < currentBestAsk {
					ob.bestAskAtomic.Store(price)
				}
			} else { // BUY
				currentBestBid := ob.bestBidAtomic.Load()
				if price > currentBestBid {
					ob.bestBidAtomic.Store(price)
				}
			}
		}
		
		// Добавляем в слот (остаток неисполненного объема)
		remainingSize := order.RemainingSize()
		slot := level.Slots[order.Slot]
		slot.Orders = append(slot.Orders, order)
		slot.Volume = safeAdd(slot.Volume, remainingSize)
		level.TotalVolume = safeAdd(level.TotalVolume, remainingSize)
		
		// Индексируем ордер
		ob.OrderIndex[order.ID] = order
	}
	
	atomic.AddUint64(&ob.stats.TotalOrders, 1)
	atomic.AddUint64(&ob.stats.TotalOperations, 1)
	
	return order
}

// Временная диагностика - добавьте счетчик
var updateBestPricesCallCount uint64

// updateBestPrices пересчитывает BestBid/BestAsk (вызывать под lock)
func (ob *OrderBook) updateBestPrices() {
    atomic.AddUint64(&updateBestPricesCallCount, 1)
	
	// Быстрый путь: если кэш валиден
    if !ob.pricesCacheDirty.Load() && len(ob.buyPricesSorted) > 0 && len(ob.sellPricesSorted) > 0 {
        ob.bestBidAtomic.Store(ob.buyPricesSorted[0])
        ob.bestAskAtomic.Store(ob.sellPricesSorted[0])
        return
    }
    
    // Медленный путь: пересчет
    ob.rebuildBestPrices()
}

func (ob *OrderBook) rebuildBestPrices() {
    // BUY: отсортировать по убыванию
    ob.buyPricesSorted = ob.buyPricesSorted[:0]
    for price := range ob.BuyLevels {
        ob.buyPricesSorted = append(ob.buyPricesSorted, price)
    }
    if len(ob.buyPricesSorted) > 0 {
        sort.Slice(ob.buyPricesSorted, func(i, j int) bool {
            return ob.buyPricesSorted[i] > ob.buyPricesSorted[j]
        })
        ob.bestBidAtomic.Store(ob.buyPricesSorted[0])
    } else {
        ob.bestBidAtomic.Store(0)
    }
    
    // SELL: отсортировать по возрастанию
    ob.sellPricesSorted = ob.sellPricesSorted[:0]
    for price := range ob.SellLevels {
        ob.sellPricesSorted = append(ob.sellPricesSorted, price)
    }
    if len(ob.sellPricesSorted) > 0 {
        sort.Slice(ob.sellPricesSorted, func(i, j int) bool {
            return ob.sellPricesSorted[i] < ob.sellPricesSorted[j]
        })
        ob.bestAskAtomic.Store(ob.sellPricesSorted[0])
    } else {
        ob.bestAskAtomic.Store(0)
    }
    
    ob.pricesCacheDirty.Store(false)
}

// tryMatchUnsafe пытается совместить ордер (вызывается под lock)
func (ob *OrderBook) tryMatchUnsafe(takerOrder *Order) {
	for !takerOrder.IsFilled() {
		// Получаем текущие best prices
		bestBid := ob.bestBidAtomic.Load()
		bestAsk := ob.bestAskAtomic.Load()
		
		var bestPrice uint64
		var canMatch bool
		
		if takerOrder.Side == BUY {
			bestPrice = bestAsk
			canMatch = bestAsk > 0 && bestPrice <= takerOrder.Price
		} else {
			bestPrice = bestBid
			canMatch = bestBid > 0 && bestPrice >= takerOrder.Price
		}
		
		if !canMatch {
			break // Больше нет подходящей ликвидности
		}
		
		// Получаем противоположную сторону книги
		oppositeLevels := ob.SellLevels
		if takerOrder.Side == SELL {
			oppositeLevels = ob.BuyLevels
		}
		
		level := oppositeLevels[bestPrice]
		if level == nil {
			break // Уровень исчез (race condition защита)
		}
		
		// Исполняем ордер по приоритету слотов (0 -> 15)
		levelMatched := false
		for slotIdx := 0; slotIdx < VERKLE_WIDTH; slotIdx++ {
			if takerOrder.IsFilled() {
				break
			}
			
			slot := level.Slots[slotIdx]
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
				
				// Вычисляем объем для исполнения
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
				
				// Устанавливаем флаги частичного заполнения
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
				level.TotalVolume = safeSubtract(level.TotalVolume, executeSize)
				
				// Сохраняем трейд
				ob.Trades = append(ob.Trades, trade)
				atomic.AddUint64(&ob.stats.TotalMatches, 1)
				
				// Если maker ордер исполнен полностью - удаляем
				if makerOrder.IsFilled() {
					slot.Orders = append(slot.Orders[:i], slot.Orders[i+1:]...)
					delete(ob.OrderIndex, makerOrder.ID)
					putOrderToPool(makerOrder)
					// i не увеличиваем, т.к. удалили элемент
				} else {
					i++
				}
				
				levelMatched = true
			}
			
			// Если слот пуст, обнуляем volume
			if len(slot.Orders) == 0 {
				slot.Volume = 0
			}
		}
		
		// Если уровень стал пустым, удаляем
		if level.TotalVolume == 0 {
			delete(oppositeLevels, bestPrice)
			putPriceLevelToPool(level)
			ob.updateBestPrices()
		}
		
		// Если на этом уровне ничего не исполнилось - выходим
		// (защита от бесконечного цикла)
		if !levelMatched {
			break
		}
	}
	
	// Если taker ордер полностью исполнен - удаляем из индекса
	if takerOrder.IsFilled() {
		delete(ob.OrderIndex, takerOrder.ID)
		// НЕ возвращаем в пул - он еще используется в вызывающем коде
	}
}

// validateAndFixLevelConsistency проверяет и ИСПРАВЛЯЕТ консистентность
func (ob *OrderBook) validateAndFixLevelConsistency(level *PriceLevel) {
	calculatedTotal := uint64(0)
	hasAnyOrders := false
	
	for i := 0; i < VERKLE_WIDTH; i++ {
		slot := level.Slots[i]
		
		// Пересчитываем volume на основе реальных ордеров
		realVolume := uint64(0)
		validOrders := make([]*Order, 0, len(slot.Orders))
		
		for _, order := range slot.Orders {
			// Проверка цены
			if order.Price != level.Price {
				//fmt.Printf("❌ Ордер #%d с ценой %.2f удален из уровня %.2f\n",
				//	order.ID, 
				//	float64(order.Price)/PRICE_DECIMALS,
				//	float64(level.Price)/PRICE_DECIMALS)
				continue
			}
			
			// Проверка на переполнение
			if order.Size > 1000000*PRICE_DECIMALS {
				//fmt.Printf("❌ Ордер #%d с подозрительным размером %.2f удален\n",
				//	order.ID, float64(order.Size)/PRICE_DECIMALS)
				continue
			}
			
			realVolume += order.Size
			validOrders = append(validOrders, order)
			hasAnyOrders = true
		}
		
		// Исправляем список ордеров
		if len(validOrders) != len(slot.Orders) {
			/*fmt.Printf("⚠️  Слот %d уровня %.2f: удалено %d некорректных ордеров\n",
				i, float64(level.Price)/PRICE_DECIMALS, len(slot.Orders)-len(validOrders))*/
			slot.Orders = validOrders
		}
		
		// Исправляем volume слота
		if slot.Volume != realVolume {
			/*if slot.Volume > 0 && realVolume == 0 {
				fmt.Printf("⚠️  Слот %d уровня %.2f: volume %.2f обнулен (нет ордеров)\n",
					i, float64(level.Price)/PRICE_DECIMALS, float64(slot.Volume)/PRICE_DECIMALS)
			}*/
			slot.Volume = realVolume
		}
		
		calculatedTotal += realVolume
	}
	
	// КРИТИЧНО: Если нет ордеров вообще - обнуляем total volume
	if !hasAnyOrders && level.TotalVolume != 0 {
		/**
		fmt.Printf("⚠️  Уровень %.2f: НЕТ ОРДЕРОВ, volume %.2f → 0.00 (уровень сохранен для оптимизации)\n",
			float64(level.Price)/PRICE_DECIMALS,
			float64(level.TotalVolume)/PRICE_DECIMALS) */
		level.TotalVolume = 0
		calculatedTotal = 0
	}
	
	// Исправляем total volume уровня
	if level.TotalVolume != calculatedTotal {
		if level.TotalVolume > 1000000*PRICE_DECIMALS {
			/**fmt.Printf("⚠️  КРИТИЧНО: Уровень %.2f - total volume %.2f → %.2f\n",
				float64(level.Price)/PRICE_DECIMALS,
				float64(level.TotalVolume)/PRICE_DECIMALS,
				float64(calculatedTotal)/PRICE_DECIMALS) **/
		}
		level.TotalVolume = calculatedTotal
	}
}

// GetTradesByOrderID возвращает все трейды связанные с ордером
func (ob *OrderBook) GetTradesByOrderID(orderID uint64) []*Trade {
	ob.mu.RLock()
	defer ob.mu.RUnlock()
	
	trades := make([]*Trade, 0)
	for _, trade := range ob.Trades {
		if trade.TakerOrderID == orderID || trade.MakerOrderID == orderID {
			trades = append(trades, trade)
		}
	}
	return trades
}

// GetTradesByTraderID возвращает все трейды трейдера
func (ob *OrderBook) GetTradesByTraderID(traderID uint32) []*Trade {
	ob.mu.RLock()
	defer ob.mu.RUnlock()
	
	trades := make([]*Trade, 0)
	for _, trade := range ob.Trades {
		if trade.TakerTraderID == traderID || trade.MakerTraderID == traderID {
			trades = append(trades, trade)
		}
	}
	return trades
}

// TraderType - тип трейдера
type TraderType int

const (
	TRADER_RETAIL       TraderType = iota // Обычный трейдер
	TRADER_MARKET_MAKER                   // Маркет-мейкер (создает ликвидность)
	TRADER_WHALE                          // Крупный трейдер
	TRADER_BOT                            // Бот (частые операции)
)

// TraderProfile - профиль трейдера
type TraderProfile struct {
	ID           uint32
	Type         TraderType
	PriceSpread  int // Разброс цены для ордеров (в единицах)
	OrderSize    int // Типичный размер ордера
	CancelRate   float32 // Вероятность отмены (0-1)
}

// generateTraderProfiles создает профили трейдеров
func generateTraderProfiles(numTraders int) []TraderProfile {
	profiles := make([]TraderProfile, numTraders)
	
	for i := 0; i < numTraders; i++ {
		traderID := uint32(i + 1)
		
		// 5% маркет-мейкеры
		if i < numTraders*5/100 {
			profiles[i] = TraderProfile{
				ID:          traderID,
				Type:        TRADER_MARKET_MAKER,
				PriceSpread: 50,   // Узкий спред ±$0.50
				OrderSize:   5000, // Крупные ордера
				CancelRate:  0.3,  // Часто обновляют
			}
		// 10% киты
		} else if i < numTraders*15/100 {
			profiles[i] = TraderProfile{
				ID:          traderID,
				Type:        TRADER_WHALE,
				PriceSpread: 200,  // ±$2
				OrderSize:   20000,
				CancelRate:  0.1,  // Редко отменяют
			}
		// 30% боты
		} else if i < numTraders*45/100 {
			profiles[i] = TraderProfile{
				ID:          traderID,
				Type:        TRADER_BOT,
				PriceSpread: 100,  // ±$1
				OrderSize:   3000,
				CancelRate:  0.5,  // Очень часто обновляют
			}
		// 55% retail
		} else {
			profiles[i] = TraderProfile{
				ID:          traderID,
				Type:        TRADER_RETAIL,
				PriceSpread: 500,  // ±$5
				OrderSize:   1000,
				CancelRate:  0.2,
			}
		}
	}
	
	return profiles
}

// generateSize генерирует размер ордера с учетом профиля
func generateSize(profile TraderProfile) uint64 {
	// Базовый размер из профиля с вариацией ±50%
	variation := profile.OrderSize / 2
	size := profile.OrderSize - variation + rand.Intn(variation*2)
	
	if size < 100 {
		size = 100
	}
	
	return uint64(size)
}

// GetRecentTrades возвращает последние N трейдов
func (ob *OrderBook) GetRecentTrades(limit int) []*Trade {
	ob.mu.RLock()
	defer ob.mu.RUnlock()
	
	if limit <= 0 || limit > len(ob.Trades) {
		limit = len(ob.Trades)
	}
	
	start := len(ob.Trades) - limit
	return ob.Trades[start:]
}

// ClearOldTrades очищает трейды старше заданного времени (для экономии памяти)
func (ob *OrderBook) ClearOldTrades(olderThan time.Duration) int {
	ob.mu.Lock()
	defer ob.mu.Unlock()
	
	cutoff := time.Now().Add(-olderThan).UnixNano()
	
	// Находим индекс первого "свежего" трейда
	firstValidIdx := 0
	for i, trade := range ob.Trades {
		if trade.Timestamp >= cutoff {
			firstValidIdx = i
			break
		}
	}
	
	if firstValidIdx == 0 {
		return 0
	}
	
	removed := firstValidIdx
	ob.Trades = ob.Trades[firstValidIdx:]
	return removed
}

// CancelOrder отменяет ордер по ID
// CancelOrder удаляет ордер по ID (возвращает true если успешно)
func (ob *OrderBook) CancelOrder(orderID uint64) bool {
	ob.mu.Lock()
	defer ob.mu.Unlock()
	
	order, exists := ob.OrderIndex[orderID]
	if !exists {
		return false
	}
	
	levels := ob.BuyLevels
	if order.Side == SELL {
		levels = ob.SellLevels
	}
	
	level, levelExists := levels[order.Price]
	if !levelExists {
		// Уровень не существует, но ордер в индексе - удаляем из индекса
		delete(ob.OrderIndex, orderID)
		putOrderToPool(order)
		atomic.AddUint64(&ob.stats.TotalCancels, 1)
		return true
	}
	
	slot := level.Slots[order.Slot]
	
	// Удаляем ордер из слота
	//found := false
	for i, o := range slot.Orders {
		if o.ID == orderID {
			slot.Orders = append(slot.Orders[:i], slot.Orders[i+1:]...)
			
			// ИСПРАВЛЕНИЕ: Вычитаем только неисполненную часть!
			remainingSize := order.RemainingSize()
			slot.Volume = safeSubtract(slot.Volume, remainingSize)
			level.TotalVolume = safeSubtract(level.TotalVolume, remainingSize)
			
			//found = true
			break
		}
	}
	
	delete(ob.OrderIndex, orderID)
	putOrderToPool(order)
	
	// Проверка консистентности
	if len(slot.Orders) == 0 {
		/*
		if slot.Volume != 0 {
			fmt.Printf("⚠️  ИСПРАВЛЕНИЕ: Слот %d опустел, но volume = %d, сброс в 0\n", 
				order.Slot, slot.Volume)
		}*/
		slot.Volume = 0
	}
	
	// Если уровень стал пустым, удаляем его
	if level.TotalVolume == 0 {
//deletedPrice := level.Price
		delete(levels, level.Price)
		putPriceLevelToPool(level)
		
		// Обновляем BestBid/BestAsk если удалили best уровень
		ob.updateBestPrices()
		/**
		if order.Side == BUY && deletedPrice == ob.BestBid {
			ob.BestBid = 0
			for price := range ob.BuyLevels {
				if price > ob.BestBid {
					ob.BestBid = price
				}
			}
		} else if order.Side == SELL && deletedPrice == ob.BestAsk {
			ob.BestAsk = 0
			for price := range ob.SellLevels {
				if ob.BestAsk == 0 || price < ob.BestAsk {
					ob.BestAsk = price
				}
			}
		}
		**/
	}
	
	atomic.AddUint64(&ob.stats.TotalOperations, 1)
	atomic.AddUint64(&ob.stats.TotalCancels, 1)
	
	return true
}

// CancelAllByTrader отменяет все ордера трейдера
func (ob *OrderBook) CancelAllByTrader(traderID uint32) int {
	ob.mu.Lock()
	
	toCancel := make([]uint64, 0)
	for orderID, order := range ob.OrderIndex {
		if order.TraderID == traderID {
			toCancel = append(toCancel, orderID)
		}
	}
	ob.mu.Unlock()
	
	count := 0
	for _, orderID := range toCancel {
		if ob.CancelOrder(orderID) {
			count++
		}
	}
	
//	fmt.Printf("✗ Отменено %d ордеров трейдера %d\n", count, traderID)
	return count
}

// ModifyOrder изменяет ордер (оптимизированная версия)
// Если меняется только объем - остается в узле, может сменить слот
// Если меняется цена - атомарно переносим в другой узел
// OrderID остается идентичным
// ModifyOrder изменяет цену и/или размер существующего ордера
// Параметры newPrice и newSize - указатели, nil означает "не изменять"
func (ob *OrderBook) ModifyOrder(orderID uint64, newPrice *uint64, newSize *uint64) bool {
	ob.mu.Lock()
	defer ob.mu.Unlock()
	
	// Проверяем существование ордера
	order, exists := ob.OrderIndex[orderID]
	if !exists {
		return false
	}
	
	// Определяем что меняется
	priceChanged := newPrice != nil && *newPrice != order.Price
	sizeChanged := newSize != nil && *newSize != order.Size
	
	// Если ничего не меняется - успешно завершаем
	if !priceChanged && !sizeChanged {
		return true
	}
	
	// Получаем нужную сторону книги
	levels := ob.BuyLevels
	if order.Side == SELL {
		levels = ob.SellLevels
	}
	
	// Получаем текущий уровень цены
	oldLevel, levelExists := levels[order.Price]
	if !levelExists {
		// Уровень не существует - некорректное состояние
		/*fmt.Printf("⚠️  ОШИБКА: Уровень %.2f для ордера #%d не найден\n",
			float64(order.Price)/PRICE_DECIMALS, orderID)*/
		return false
	}
	
	oldSlot := oldLevel.Slots[order.Slot]
	
	// ═══════════════════════════════════════════════════════════════════
	// СЛУЧАЙ 1: Меняется только размер (остаемся на том же ценовом уровне)
	// ═══════════════════════════════════════════════════════════════════
	if !priceChanged && sizeChanged {
		newSizeVal := *newSize
		
		// Получаем текущий неисполненный объем
		oldRemainingSize := order.RemainingSize()
		
		// КРИТИЧНО: Вычитаем только неисполненную часть из volumes
		oldSlot.Volume = safeSubtract(oldSlot.Volume, oldRemainingSize)
		oldLevel.TotalVolume = safeSubtract(oldLevel.TotalVolume, oldRemainingSize)
		
		// Обновляем размер ордера (FilledSize не трогаем!)
		order.Size = newSizeVal
		
		// Вычисляем новый неисполненный объем
		newRemainingSize := order.RemainingSize()
		
		// Если ордер уже полностью исполнен после уменьшения - удаляем
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
			
			// Удаляем пустой уровень если нужно
			if oldLevel.TotalVolume == 0 {
				delete(levels, oldLevel.Price)
				putPriceLevelToPool(oldLevel)
			}
			
			delete(ob.OrderIndex, orderID)
			putOrderToPool(order)
			
			atomic.AddUint64(&ob.stats.TotalModifies, 1)
			atomic.AddUint64(&ob.stats.TotalOperations, 1)
			return true
		}
		
		// Проверяем нужно ли сменить слот (из-за изменения размера)
		newSlot := ob.determineSlot(order)
		
		if newSlot != order.Slot {
			// Удаляем из старого слота
			for i, o := range oldSlot.Orders {
				if o.ID == orderID {
					oldSlot.Orders = append(oldSlot.Orders[:i], oldSlot.Orders[i+1:]...)
					break
				}
			}
			
			// Проверка консистентности старого слота
			if len(oldSlot.Orders) == 0 {
				oldSlot.Volume = 0
			}
			
			// Добавляем в новый слот
			order.Slot = newSlot
			targetSlot := oldLevel.Slots[newSlot]
			targetSlot.Orders = append(targetSlot.Orders, order)
			targetSlot.Volume = safeAdd(targetSlot.Volume, newRemainingSize)
		} else {
			// Остаемся в том же слоте - просто обновляем volume
			oldSlot.Volume = safeAdd(oldSlot.Volume, newRemainingSize)
		}
		
		// Обновляем total volume уровня
		oldLevel.TotalVolume = safeAdd(oldLevel.TotalVolume, newRemainingSize)
		
		atomic.AddUint64(&ob.stats.TotalModifies, 1)
		atomic.AddUint64(&ob.stats.TotalOperations, 1)
		return true
	}
	
	// ═══════════════════════════════════════════════════════════════════
	// СЛУЧАЙ 2: Меняется цена (возможно и размер тоже)
	// ═══════════════════════════════════════════════════════════════════
	if priceChanged {
		newPriceVal := *newPrice
		newSizeVal := order.Size
		if sizeChanged {
			newSizeVal = *newSize
		}
		
		// Получаем текущий неисполненный объем
		oldRemainingSize := order.RemainingSize()
		
		// ───────────────────────────────────────────────────────────────
		// ШАГ 1: Удаляем ордер из старого уровня
		// ───────────────────────────────────────────────────────────────
		orderFound := false
		for i, o := range oldSlot.Orders {
			if o.ID == orderID {
				oldSlot.Orders = append(oldSlot.Orders[:i], oldSlot.Orders[i+1:]...)
				
				// КРИТИЧНО: Вычитаем только неисполненную часть
				oldSlot.Volume = safeSubtract(oldSlot.Volume, oldRemainingSize)
				oldLevel.TotalVolume = safeSubtract(oldLevel.TotalVolume, oldRemainingSize)
				
				orderFound = true
				break
			}
		}
		
		if !orderFound {
			/*fmt.Printf("⚠️  ОШИБКА: Ордер #%d не найден в слоте %d уровня %.2f\n",
				orderID, order.Slot, float64(order.Price)/PRICE_DECIMALS)*/
			return false
		}
		
		// Проверка консистентности старого слота
		if len(oldSlot.Orders) == 0 {
			/*if oldSlot.Volume != 0 {
				fmt.Printf("⚠️  ИСПРАВЛЕНИЕ: Старый слот %d опустел, volume %d → 0\n", 
					order.Slot, oldSlot.Volume)
			}*/
			oldSlot.Volume = 0
		}
		
		// Если старый уровень стал пустым, удаляем его
		if oldLevel.TotalVolume == 0 {
//deletedPrice := oldLevel.Price
			delete(levels, oldLevel.Price)
			putPriceLevelToPool(oldLevel)
			
			// Обновляем BestBid/BestAsk если удалили best уровень
			ob.updateBestPrices()
			/*
			if order.Side == BUY && deletedPrice == ob.BestBid {
				ob.BestBid = 0
				for price := range ob.BuyLevels {
					if price > ob.BestBid {
						ob.BestBid = price
					}
				}
			} else if order.Side == SELL && deletedPrice == ob.BestAsk {
				ob.BestAsk = 0
				for price := range ob.SellLevels {
					if ob.BestAsk == 0 || price < ob.BestAsk {
						ob.BestAsk = price
					}
				}
			}
			*/
		}
		
		// ───────────────────────────────────────────────────────────────
		// ШАГ 2: Обновляем параметры ордера
		// ───────────────────────────────────────────────────────────────
//oldPrice := order.Price
		order.Price = newPriceVal
		order.Size = newSizeVal
		// ВАЖНО: FilledSize НЕ сбрасываем! Частичное исполнение сохраняется
		order.Slot = ob.determineSlot(order)
		
		// Вычисляем новый неисполненный объем
		newRemainingSize := order.RemainingSize()
		
		// Если после изменения ордер полностью исполнен - удаляем
		if newRemainingSize == 0 {
			delete(ob.OrderIndex, orderID)
			putOrderToPool(order)
			
			atomic.AddUint64(&ob.stats.TotalModifies, 1)
			atomic.AddUint64(&ob.stats.TotalOperations, 1)
			return true
		}
		
		// ───────────────────────────────────────────────────────────────
		// ШАГ 3: Добавляем ордер в новый уровень цены
		// ───────────────────────────────────────────────────────────────
		newLevel, exists := levels[newPriceVal]
		if !exists {
			// Создаем новый уровень
			newLevel = getPriceLevelFromPool()
			newLevel.Price = newPriceVal
			newLevel.TotalVolume = 0
			levels[newPriceVal] = newLevel
			
			// Обновляем BestBid/BestAsk при создании нового уровня
			ob.updateBestPrices()
			
			/*
			if order.Side == SELL {
				if ob.BestAsk == 0 || newPriceVal < ob.BestAsk {
					ob.BestAsk = newPriceVal
				}
			} else if order.Side == BUY {
				if ob.BestBid == 0 || newPriceVal > ob.BestBid {
					ob.BestBid = newPriceVal
				}
			}*/
		}
		
		// Добавляем ордер в новый слот (только неисполненную часть!)
		newSlot := newLevel.Slots[order.Slot]
		newSlot.Orders = append(newSlot.Orders, order)
		newSlot.Volume = safeAdd(newSlot.Volume, newRemainingSize)
		newLevel.TotalVolume = safeAdd(newLevel.TotalVolume, newRemainingSize)
		
		atomic.AddUint64(&ob.stats.TotalModifies, 1)
		atomic.AddUint64(&ob.stats.TotalOperations, 1)
		
		// ───────────────────────────────────────────────────────────────
		// ШАГ 4: Проверяем возможность матчинга с новой ценой
		// ───────────────────────────────────────────────────────────────
		ob.tryMatchUnsafe(order)
		
		return true
	}
	
	// Не должны сюда попасть
	return false
}

// rebuildTree перестраивает Verkle дерево с разделением BUY/SELL
func (ob *OrderBook) rebuildTree() {
	// Создаем корневой узел
	ob.Root = &VerkleNode{
		NodeType: NODE_ROOT,
		Metadata: "OrderBook Root",
	}
	
	// Child[0] = BUY сторона
	// Child[1] = SELL сторона
	// Children[2-15] = зарезервированы для будущего использования
	
	// Строим BUY поддерево
	if len(ob.BuyLevels) > 0 {
		ob.Root.Children[0] = ob.buildSideTree(ob.BuyLevels, NODE_BUY_SIDE)
	}
	
	// Строим SELL поддерево
	if len(ob.SellLevels) > 0 {
		ob.Root.Children[1] = ob.buildSideTree(ob.SellLevels, NODE_SELL_SIDE)
	}
}

// buildSideTree строит поддерево для одной стороны (BUY или SELL)
func (ob *OrderBook) buildSideTree(levels map[uint64]*PriceLevel, sideType NodeType) *VerkleNode {
	sideNode := &VerkleNode{
		NodeType: sideType,
		Metadata: sideType.String(),
	}
	
	// Собираем ТОЛЬКО непустые уровни
	sortedLevels := make([]*PriceLevel, 0, len(levels))
	for _, level := range levels {
		// ФИЛЬТР: Пропускаем пустые уровни (без ордеров и volume)
		if level.TotalVolume > 0 {
			sortedLevels = append(sortedLevels, level)
		}
	}
	
	// Если нет непустых уровней - возвращаем пустой узел
	if len(sortedLevels) == 0 {
		return sideNode
	}
	
	// Сортируем: BUY по убыванию, SELL по возрастанию
	if sideType == NODE_BUY_SIDE {
		sort.Slice(sortedLevels, func(i, j int) bool {
			return sortedLevels[i].Price > sortedLevels[j].Price
		})
	} else {
		sort.Slice(sortedLevels, func(i, j int) bool {
			return sortedLevels[i].Price < sortedLevels[j].Price
		})
	}
	
	// Если уровней <= 16, размещаем их напрямую в children
	if len(sortedLevels) <= VERKLE_WIDTH {
		for i, level := range sortedLevels {
			sideNode.Children[i] = level
		}
		return sideNode
	}
	
	// Если уровней > 16, создаем промежуточные узлы
	groupSize := (len(sortedLevels) + VERKLE_WIDTH - 1) / VERKLE_WIDTH
	
	for groupIdx := 0; groupIdx < VERKLE_WIDTH && groupIdx*groupSize < len(sortedLevels); groupIdx++ {
		groupNode := &VerkleNode{
			NodeType: NODE_PRICE_GROUP,
			Metadata: fmt.Sprintf("Group %d", groupIdx),
		}
		
		startIdx := groupIdx * groupSize
		endIdx := startIdx + groupSize
		if endIdx > len(sortedLevels) {
			endIdx = len(sortedLevels)
		}
		
		for i := startIdx; i < endIdx && i-startIdx < VERKLE_WIDTH; i++ {
			groupNode.Children[i-startIdx] = sortedLevels[i]
		}
		
		sideNode.Children[groupIdx] = groupNode
	}
	
	return sideNode
}

// computeRootHash вычисляет Blake3 хеш корня дерева
func (ob *OrderBook) computeRootHash() {
	ob.LastRootHash = ob.hashNode(ob.Root)
}

// hashNode рекурсивно вычисляет хеш узла
func (ob *OrderBook) hashNode(node *VerkleNode) [32]byte {
	hasher := blake3.New()
	
	// Добавляем тип узла в хеш для уникальности
	hasher.Write([]byte{byte(node.NodeType)})
	
	// Хешируем всех детей
	for i := 0; i < VERKLE_WIDTH; i++ {
		var childHash [32]byte
		
		switch child := node.Children[i].(type) {
		case *VerkleNode:
			childHash = ob.hashNode(child)
		case *PriceLevel:
			childHash = ob.hashPriceLevel(child)
		default:
			// Пустой узел
			childHash = [32]byte{}
		}
		
		hasher.Write(childHash[:])
	}
	
	var result [32]byte
	hasher.Sum(result[:0])
	node.Hash = result // Сохраняем хеш в узле
	return result
}

// ExecuteMarketOrder исполняет рыночный ордер (не добавляется в книгу)
func (ob *OrderBook) ExecuteMarketOrder(traderID uint32, size uint64, side Side) bool {
	ob.mu.Lock()
	defer ob.mu.Unlock()
	
	oppositeLevels := ob.SellLevels
	if side == BUY {
		oppositeLevels = ob.SellLevels
	} else {
		oppositeLevels = ob.BuyLevels
	}
	
	if len(oppositeLevels) == 0 {
		// Нет ликвидности для исполнения
		return false
	}
	
	// Получаем отсортированные цены
	prices := make([]uint64, 0, len(oppositeLevels))
	for price := range oppositeLevels {
		prices = append(prices, price)
	}
	
	// Сортируем: для BUY берем самый дешевый SELL, для SELL - самый дорогой BUY
	if side == BUY {
		sort.Slice(prices, func(i, j int) bool { return prices[i] < prices[j] })
	} else {
		sort.Slice(prices, func(i, j int) bool { return prices[i] > prices[j] })
	}
/*	
	bestPrice := prices[0]
	level := oppositeLevels[bestPrice]
	
	// Исполняем по лучшей цене (в реальности здесь будет логика частичного заполнения)
	fmt.Printf("💥 МАРКЕТ: %s размер %.2f исполнен по цене %.2f (доступно %.2f)\n",
		side, float64(size)/PRICE_DECIMALS, float64(bestPrice)/PRICE_DECIMALS,
		float64(level.TotalVolume)/PRICE_DECIMALS) */
	
	atomic.AddUint64(&ob.stats.TotalMatches, 1)
	atomic.AddUint64(&ob.stats.TotalMarketOrders, 1)
	atomic.AddUint64(&ob.stats.TotalOperations, 1)
	return true
}


// hashPriceLevel вычисляет хеш ценового уровня
func (ob *OrderBook) hashPriceLevel(level *PriceLevel) [32]byte {
	hasher := blake3.New()
	
	// Получаем буфер из пула
	buf := hashBufferPool.Get().([]byte)
	buf = buf[:0]
	defer func() {
		hashBufferPool.Put(buf)
	}()
	
	// Хешируем цену (BigEndian)
	if cap(buf) < 8 {
		buf = make([]byte, 8)
	}
	buf = buf[:8]
	binary.BigEndian.PutUint64(buf, level.Price)
	hasher.Write(buf)
	
	// Хешируем общий объем
	binary.BigEndian.PutUint64(buf, level.TotalVolume)
	hasher.Write(buf)
	
	// Хешируем каждый слот
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
	
	ob.rebuildTree()
	ob.computeRootHash()
	atomic.AddUint64(&ob.stats.HashCount, 1)
	
	bestBid := ob.bestBidAtomic.Load()
    bestAsk := ob.bestAskAtomic.Load()
	
	// Получаем хеши сторон
	buyHash := [32]byte{}
	sellHash := [32]byte{}
	if buyNode, ok := ob.Root.Children[0].(*VerkleNode); ok {
		buyHash = buyNode.Hash
	}
	if sellNode, ok := ob.Root.Children[1].(*VerkleNode); ok {
		sellHash = sellNode.Hash
	}
	
	totalOperations := atomic.LoadUint64(&ob.stats.TotalOperations)
	totalOrders := atomic.LoadUint64(&ob.stats.TotalOrders)
	totalMatches := atomic.LoadUint64(&ob.stats.TotalMatches)
	totalCancels := atomic.LoadUint64(&ob.stats.TotalCancels)
	totalModifies := atomic.LoadUint64(&ob.stats.TotalModifies)
	totalMarketOrders := atomic.LoadUint64(&ob.stats.TotalMarketOrders)
	hashCount := atomic.LoadUint64(&ob.stats.HashCount)
	rootHash := ob.LastRootHash
	
	activeOrders := len(ob.OrderIndex)
	buyLevels := len(ob.BuyLevels)
	sellLevels := len(ob.SellLevels)
	tradesCount := len(ob.Trades)
	
	ob.mu.Unlock()
	
	fmt.Printf("\n═══════════════════════════════════════════\n")
	fmt.Printf("Статистика %s:\n", ob.Symbol)
	fmt.Printf("  • Активных ордеров: %d\n", activeOrders)
	fmt.Printf("  • Всего добавлено: %d\n", totalOrders)
	fmt.Printf("  • Маркет-ордеров: %d\n", totalMarketOrders)
	fmt.Printf("  • Трейдов: %d\n", tradesCount)
	fmt.Printf("  • Матчей: %d\n", totalMatches)
	fmt.Printf("  • Отмен: %d\n", totalCancels)
	fmt.Printf("  • Изменений: %d\n", totalModifies)
	fmt.Printf("  • BUY уровней: %d\n", buyLevels)
	fmt.Printf("  • SELL уровней: %d\n", sellLevels)
	fmt.Printf("  • Всего операций (Tx): %d\n", totalOperations)
	fmt.Printf("  • Хешей посчитано: %d\n", hashCount)
	fmt.Printf("─────────────────────────────────────────\n")
	fmt.Printf("  • Root hash:  %x...\n", rootHash[:16])
	fmt.Printf("  • BUY hash:   %x...\n", buyHash[:16])
	fmt.Printf("  • SELL hash:  %x...\n", sellHash[:16])
	
	// Best Bid/Ask
    fmt.Println("─────────────────────────────────────────")
    fmt.Printf("  • Best Bid: %.2f\n", float64(bestBid)/PRICE_DECIMALS)
    fmt.Printf("  • Best Ask: %.2f\n", float64(bestAsk)/PRICE_DECIMALS)
    
	// ИСПРАВЛЕННОЕ вычисление spread:
    if bestAsk > 0 && bestBid > 0 {
        if bestAsk > bestBid {
            spread := float64(bestAsk-bestBid) / PRICE_DECIMALS
            fmt.Printf("  • Spread: %.2f\n", spread)
        } else {
            // ДИАГНОСТИКА: Bid > Ask - некорректное состояние!
            spread := float64(bestBid-bestAsk) / PRICE_DECIMALS
            fmt.Printf("  • Spread: %.2f (⚠️ CROSSED MARKET: Bid > Ask!)\n", spread)
        }
    }
	
	fmt.Printf("═══════════════════════════════════════════\n\n")
	fmt.Printf("  • updateBestPrices calls: %d\n", atomic.LoadUint64(&updateBestPricesCallCount))
	
	lockAcq := atomic.LoadUint64(&ob.stats.LockAcquisitions)
    lockWait := atomic.LoadInt64(&ob.stats.LockWaitTime)
    if lockAcq > 0 {
        avgWaitMicros := float64(lockWait) / float64(lockAcq) / 1e3 // ← ИЗМЕНЕНО: делим на 1e3 вместо 1e6
        fmt.Printf("  • Avg lock wait time: %.3f μs\n", avgWaitMicros)  // ← ИЗМЕНЕНО: μs вместо ms
        fmt.Printf("  • Total lock acquisitions: %d\n", lockAcq)
        
        // Также можно добавить процент времени в ожидании:
        if lockWait > 0 {
            totalWaitMs := float64(lockWait) / 1e6
            fmt.Printf("  • Total lock wait time: %.2f ms\n", totalWaitMs)
        }
    }
	
	
    // ДИАГНОСТИКА ОПТИМИЗАЦИЙ:
    fmt.Println("🔧 Диагностика оптимизаций:")
    fmt.Printf("  • HASH_INTERVAL = %v\n", HASH_INTERVAL)
    fmt.Printf("  • hashTicker == nil: %v\n", ob.hashTicker == nil)
    fmt.Printf("  • updateBestPrices calls: %d\n", atomic.LoadUint64(&updateBestPricesCallCount))
    fmt.Printf("  • pricesCacheDirty loads: %v\n", ob.pricesCacheDirty.Load())
    fmt.Printf("  • buyPricesSorted len: %d\n", len(ob.buyPricesSorted))
    fmt.Printf("  • sellPricesSorted len: %d\n", len(ob.sellPricesSorted))
    
    fmt.Println("─────────────────────────────────────────")
}

// ExportTreeToTextFile экспортирует полное дерево в текстовый файл
func (ob *OrderBook) ExportTreeToTextFile(filename string) error {
	ob.mu.RLock()
	defer ob.mu.RUnlock()
	
	ob.rebuildTree()
	ob.computeRootHash()
	
	file, err := os.Create(filename)
	if err != nil {
		return err
	}
	defer file.Close()
	
	writer := bufio.NewWriter(file)
	defer writer.Flush()
	
	writer.WriteString("═══════════════════════════════════════════\n")
	writer.WriteString("VERKLE TREE STRUCTURE - FULL EXPORT\n")
	writer.WriteString(fmt.Sprintf("Timestamp: %s\n", time.Now().Format(time.RFC3339)))
	writer.WriteString("═══════════════════════════════════════════\n\n")
	
	stats := &TreeStats{}
	ob.writeNodeRecursive(writer, ob.Root, 0, stats)
	
	writer.WriteString("\n═══════════════════════════════════════════\n")
	writer.WriteString(fmt.Sprintf("Total Nodes: %d\n", stats.TotalNodes))
	writer.WriteString(fmt.Sprintf("Price Levels: %d\n", stats.PriceLevels))  // <- ИСПРАВЛЕНО
	writer.WriteString(fmt.Sprintf("Total Orders: %d\n", stats.TotalOrders))
	writer.WriteString(fmt.Sprintf("Total Volume: %.2f\n", float64(stats.TotalVolume)/PRICE_DECIMALS))
	writer.WriteString("═══════════════════════════════════════════\n")
	
	fmt.Printf("✓ Полное дерево экспортировано в %s\n", filename)
	return nil
}

// writeNodeRecursive записывает узел в файл
func (ob *OrderBook) writeNodeRecursive(writer *bufio.Writer, node interface{}, depth int, stats *TreeStats) {
	indent := ""
	for i := 0; i < depth; i++ {
		indent += "  "
	}
	
	switch n := node.(type) {
	case *VerkleNode:
		stats.TotalNodes++
		
		childCount := 0
		for i := 0; i < VERKLE_WIDTH; i++ {
			if n.Children[i] != nil {
				childCount++
			}
		}
		
		writer.WriteString(fmt.Sprintf("%s├─ [%s] %s (hash: %x..., children: %d)\n",
			indent, n.NodeType.String(), n.Metadata, n.Hash[:4], childCount))
		
		for i := 0; i < VERKLE_WIDTH; i++ {
			if n.Children[i] != nil {
				ob.writeNodeRecursive(writer, n.Children[i], depth+1, stats)
			}
		}
		
	case *PriceLevel:
		stats.TotalNodes++
		stats.PriceLevels++  // <- ИСПРАВЛЕНО
		
		ordersCount := 0
		for _, slot := range n.Slots {
			ordersCount += len(slot.Orders)
		}
		stats.TotalOrders += ordersCount
		
		if ordersCount == 0 && n.TotalVolume == 0 {
			return
		}
		
		writer.WriteString(fmt.Sprintf("%s├─ [PRICE] %.2f (volume: %.2f, orders: %d)\n",
			indent, 
			float64(n.Price)/PRICE_DECIMALS,
			float64(n.TotalVolume)/PRICE_DECIMALS,
			ordersCount))
	}
}

func main() {
	fmt.Println("🌳 Verkle Tree Orderbook Simulation")
	fmt.Println("━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━")
	fmt.Println("✓ Memory pools активны")
	fmt.Println("✓ GC оптимизирован")
	fmt.Println("✓ Периодическое хеширование: каждые 500ms")
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
	numOperations := 100_000
	
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
	
	fmt.Printf("  • Маркет-мейкеры: %d\n", mmCount)
	fmt.Printf("  • Киты: %d\n", whaleCount)
	fmt.Printf("  • Боты: %d\n", botCount)
	fmt.Printf("  • Retail: %d\n", retailCount)
	fmt.Println()
	
	// Инициализация: создаем начальную ликвидность от MM
	fmt.Println("💧 Создание начальной ликвидности...")
	
	addedOrders := make([]uint64, 0, numOperations)
	
	for i := 0; i < mmCount; i++ {
		profile := traderProfiles[i]
		
		// Каждый MM создает 5-10 ордеров на обе стороны
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
	
	bestBid := ob.bestBidAtomic.Load()
	bestAsk := ob.bestAskAtomic.Load()
	fmt.Printf("  • Initial BestBid: %.2f\n", float64(bestBid)/PRICE_DECIMALS)
	fmt.Printf("  • Initial BestAsk: %.2f\n", float64(bestAsk)/PRICE_DECIMALS)

	if bestBid >= bestAsk {
		fmt.Printf("⚠️ ОШИБКА: BestBid >= BestAsk! Проверьте generatePrice()\n")
	}
	
	startTime := time.Now()
	
for i := 0; i < numOperations; i++ {
    profile := traderProfiles[rand.Intn(len(traderProfiles))]
    r := rand.Float32()
    
    // 25% - маркет ордера
    if r < 0.25 {
        size := generateSize(profile)
        side := BUY
        if rand.Float32() < 0.5 {
            side = SELL
        }
        ob.ExecuteMarketOrder(profile.ID, size, side)
        
    // 35% - лимитные ордера (25% → 60%)
    } else if r < 0.60 {
        size := generateSize(profile)
        
        side := BUY
        if rand.Float32() < 0.5 {
            side = SELL
        }
        
        price := generatePriceWithMagnetism(basePrice, profile, side)
        
        order := ob.AddLimitOrder(profile.ID, price, size, side)
        addedOrders = append(addedOrders, order.ID)
        
    // 20% - отмена ордеров (60% → 80%)
    } else if r < 0.80 {
        if len(addedOrders) == 0 {
            continue
        }
        
        idx := rand.Intn(len(addedOrders))
        orderID := addedOrders[idx]
        
        if ob.CancelOrder(orderID) {
            addedOrders = append(addedOrders[:idx], addedOrders[idx+1:]...)
        }
        
    // 20% - модификация ордеров (80% → 100%)
    } else {
        if len(addedOrders) == 0 {
            continue
        }
        
        orderID := addedOrders[rand.Intn(len(addedOrders))]
        
        // ПОЛУЧАЕМ СТОРОНУ СУЩЕСТВУЮЩЕГО ОРДЕРА
        ob.mu.RLock()
        existingOrder, exists := ob.OrderIndex[orderID]
        ob.mu.RUnlock()
        
        if !exists {
            continue // Ордер был отменен или исполнен
        }
        
        modType := rand.Intn(3)
        
        switch modType {
        case 0:
            // Изменение размера
            newSize := generateSize(profile)
            ob.ModifyOrder(orderID, nil, &newSize)
            
        case 1:
            // Изменение цены (используем сторону СУЩЕСТВУЮЩЕГО ордера!)
            newPrice := generatePriceWithMagnetism(basePrice, profile, existingOrder.Side)
            ob.ModifyOrder(orderID, &newPrice, nil)
            
        case 2:
            // Изменение цены и размера (используем сторону СУЩЕСТВУЮЩЕГО ордера!)
            newPrice := generatePriceWithMagnetism(basePrice, profile, existingOrder.Side)
            newSize := generateSize(profile)
            ob.ModifyOrder(orderID, &newPrice, &newSize)
        }
    }
    
    // Периодическая очистка
    if i%1000 == 0 {
        ob.CleanupEmptyLevels()
    }
}

	elapsed := time.Since(startTime)
	
	// Финальная очистка
	ob.CleanupEmptyLevels()
	
	// Финальная статистика
	fmt.Println("\n🏁 ФИНАЛЬНАЯ СТАТИСТИКА")
	ob.PrintStats()
	
	//ob.PrintTreeStructure()
	
	// Вариант 1: Компактный вывод (топ-10 уровней)
	ob.PrintTreeStructure(TREE_PRINT_COMPACT)
	
	// Вариант 2: Только статистика
	// ob.PrintTreeStructure(TREE_PRINT_SUMMARY)
	
	// Вариант 3: ПОЛНОЕ дерево (может быть очень большим!)
	// ob.PrintTreeStructure(TREE_PRINT_FULL)
	
	// Полное дерево в файл (чтобы не забить консоль)
	// Или если это первое использование err в блоке:
	if err := ob.ExportTreeToTextFile("orderbook_tree_full.txt"); err != nil {
		fmt.Printf("Ошибка экспорта дерева: %v\n", err)
	}
	
	
	tps := float64(numOperations) / elapsed.Seconds()
	fmt.Printf("⚡ Производительность: %.0f операций/сек\n", tps)
	fmt.Printf("⏱  Общее время: %v\n", elapsed)
	
	// Экспорт
	fmt.Println("\n📁 Экспорт состояния дерева...")
	err := ob.ExportToJSONCompact("orderbook_compact.json")
	if err != nil {
		fmt.Printf("Ошибка экспорта: %v\n", err)
	}
	
	// Ждем последнего хеша
	time.Sleep(HASH_INTERVAL + 100*time.Millisecond)
	
	fmt.Println("\n✅ Симуляция завершена")
}