package main

import (
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

	"github.com/zeebo/blake3"
)

// Константы системы
const (
	VERKLE_WIDTH      = 16      // Ширина Verkle дерева
	PRICE_DECIMALS    = 100     // Точность цены (2 знака после запятой)
	MAX_TRADERS       = 1000   // Максимальное количество трейдеров
	HASH_INTERVAL     = 500 * time.Millisecond // Интервал хеширования
	
	// Слоты для распределения ордеров с описанием
	SLOT_MM_LIQUIDATION = 0     // Ликвидации маркет-мейкеров
	SLOT_VIP            = 1     // VIP-трейдеры
	SLOT_SMALL_RETAIL   = 2     // Мелкие retail ордера (<$10)
	SLOT_RETAIL_START   = 3     // Начало диапазона для retail
	SLOT_RETAIL_END     = 14    // Конец диапазона для retail
	SLOT_RESERVED       = 15    // Зарезервированный слот
)

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
				Orders: make([]*Order, 0, 16), // Предаллокация
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
				Orders:   make([]*Order, 0, 16),
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
	IsLeaf   bool                `json:"is_leaf"`
	Children []interface{}       `json:"children"` // PriceLevelJSON или VerkleNodeJSON
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

// ExportToJSON экспортирует состояние ордербука в JSON файл
/****
func (ob *OrderBook) ExportToJSON(filename string) error {
	ob.mu.RLock()
	defer ob.mu.RUnlock()
	
	// Пересчитываем дерево и хеш
	ob.rebuildTree()
	ob.computeRootHash()
	
	// Собираем данные
	state := OrderBookStateJSON{
		Symbol:          ob.Symbol,
		RootHash:        hex.EncodeToString(ob.LastRootHash[:]),
		ActiveOrders:    len(ob.OrderIndex),
		BuyLevelsCount:  len(ob.BuyLevels),
		SellLevelsCount: len(ob.SellLevels),
		BestBid:         float64(ob.BestBid) / PRICE_DECIMALS,
		BestAsk:         float64(ob.BestAsk) / PRICE_DECIMALS,
		Stats: StatsJSON{
			TotalOperations:   ob.stats.TotalOperations,
			TotalOrders:       ob.stats.TotalOrders,
			TotalMatches:      ob.stats.TotalMatches,
			TotalCancels:      ob.stats.TotalCancels,
			TotalModifies:     ob.stats.TotalModifies,
			TotalMarketOrders: ob.stats.TotalMarketOrders,
			HashCount:         ob.stats.HashCount,
		},
		Tree:       ob.serializeVerkleNode(ob.Root),
		BuyLevels:  ob.serializeLevels(ob.BuyLevels),
		SellLevels: ob.serializeLevels(ob.SellLevels),
	}
	
	// Конвертируем в JSON с отступами
	jsonData, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return fmt.Errorf("ошибка сериализации JSON: %w", err)
	}
	
	// Записываем в файл
	err = os.WriteFile(filename, jsonData, 0644)
	if err != nil {
		return fmt.Errorf("ошибка записи файла: %w", err)
	}
	
	fmt.Printf("✓ Состояние дерева экспортировано в %s (%.2f KB)\n", 
		filename, float64(len(jsonData))/1024)
	
	return nil
}
***/
// ExportToJSON - обновленная версия с трейдами
func (ob *OrderBook) ExportToJSON(filename string) error {
	ob.mu.RLock()
	defer ob.mu.RUnlock()
	
	ob.rebuildTree()
	ob.computeRootHash()
	
	spread := 0.0
	if ob.BestAsk > 0 && ob.BestBid > 0 {
		spread = float64(ob.BestAsk-ob.BestBid) / PRICE_DECIMALS
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
		BestBid:         float64(ob.BestBid) / PRICE_DECIMALS,
		BestAsk:         float64(ob.BestAsk) / PRICE_DECIMALS,
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

// serializeVerkleNode рекурсивно сериализует узел Verkle дерева
func (ob *OrderBook) serializeVerkleNode(node *VerkleNode) VerkleNodeJSON {
	result := VerkleNodeJSON{
		Hash:     hex.EncodeToString(node.Hash[:]),
		IsLeaf:   node.IsLeaf,
		Children: make([]interface{}, 0),
	}
	
	for i := 0; i < VERKLE_WIDTH; i++ {
		switch child := node.Children[i].(type) {
		case *VerkleNode:
			result.Children = append(result.Children, ob.serializeVerkleNode(child))
		case *PriceLevel:
			result.Children = append(result.Children, ob.serializePriceLevel(child))
		default:
			// Пустой узел - пропускаем
		}
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
	if ob.BestAsk > 0 && ob.BestBid > 0 {
		spread = float64(ob.BestAsk-ob.BestBid) / PRICE_DECIMALS
	}
	
	state := CompactState{
		Symbol:          ob.Symbol,
		RootHash:        hex.EncodeToString(ob.LastRootHash[:]),
		ActiveOrders:    len(ob.OrderIndex),
		BuyLevelsCount:  len(ob.BuyLevels),
		SellLevelsCount: len(ob.SellLevels),
		BestBid:         float64(ob.BestBid) / PRICE_DECIMALS,
		BestAsk:         float64(ob.BestAsk) / PRICE_DECIMALS,
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
		Trades:      make([]*Trade, 0, 1000), // Предаллокация для трейдов
		Root:        &VerkleNode{IsLeaf: false},
		BestBid:     0,
		BestAsk:     0,
		hashTicker:  time.NewTicker(HASH_INTERVAL),
		stopChan:    make(chan struct{}),
		hashRequest: make(chan struct{}, 1),
	}
	
	go ob.periodicHasher()
	go ob.hashWorker()
	
	return ob
}

// Stop останавливает ордербук и фоновые горутины
func (ob *OrderBook) Stop() {
	close(ob.stopChan)
	ob.hashTicker.Stop()
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
	
	ob.mu.Lock()
	defer ob.mu.Unlock()
	
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
			if side == SELL {
				if ob.BestAsk == 0 || price < ob.BestAsk {
					ob.BestAsk = price
				}
			} else if side == BUY {
				if ob.BestBid == 0 || price > ob.BestBid {
					ob.BestBid = price
				}
			}
		}
		
		// Добавляем в слот (остаток неисполненного объема)
		remainingSize := order.RemainingSize()
		slot := level.Slots[order.Slot]
		slot.Orders = append(slot.Orders, order)
		slot.Volume += remainingSize
		level.TotalVolume += remainingSize
		
		// Индексируем ордер
		ob.OrderIndex[order.ID] = order
	}
	
	atomic.AddUint64(&ob.stats.TotalOrders, 1)
	atomic.AddUint64(&ob.stats.TotalOperations, 1)
	
	return order
}

// updateBestPrices пересчитывает BestBid/BestAsk (вызывать под lock)
func (ob *OrderBook) updateBestPrices() {
    ob.BestBid = 0
    ob.BestAsk = 0
    
    for price := range ob.BuyLevels {
        if price > ob.BestBid {
            ob.BestBid = price
        }
    }
    
    for price := range ob.SellLevels {
        if ob.BestAsk == 0 || price < ob.BestAsk {
            ob.BestAsk = price
        }
    }
}

// tryMatchUnsafe пытается совместить ордер (вызывается под lock)
func (ob *OrderBook) tryMatchUnsafe(takerOrder *Order) {
	if takerOrder.IsFilled() {
		return // Ордер уже полностью исполнен
	}
	
	var bestPrice uint64
	var canMatch bool
	
	if takerOrder.Side == BUY {
		bestPrice = ob.BestAsk
		canMatch = ob.BestAsk > 0 && bestPrice <= takerOrder.Price
	} else {
		bestPrice = ob.BestBid
		canMatch = ob.BestBid > 0 && bestPrice >= takerOrder.Price
	}
	
	if !canMatch {
		return
	}
	
	// Получаем противоположную сторону книги
	oppositeLevels := ob.SellLevels
	if takerOrder.Side == SELL {
		oppositeLevels = ob.BuyLevels
	}
	
	level := oppositeLevels[bestPrice]
	if level == nil {
		return
	}
	
	// Исполняем ордер по приоритету слотов (0 -> 15)
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
			slot.Volume -= executeSize
			level.TotalVolume -= executeSize
			
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
			
			// Логируем трейд (закомментируйте для производительности)
			// fmt.Printf("⚡ TRADE #%d: %s %.2f @ %.2f (taker:#%d maker:#%d) [partial: T=%v M=%v]\n",
			// 	trade.TradeID, trade.TakerSide, float64(executeSize)/PRICE_DECIMALS,
			// 	float64(bestPrice)/PRICE_DECIMALS, takerOrder.ID, makerOrder.ID,
			// 	trade.TakerPartial, trade.MakerPartial)
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
	
	// Если taker ордер не исполнен полностью - остается в книге
	// Если исполнен полностью - удаляем из индекса
	if takerOrder.IsFilled() {
		delete(ob.OrderIndex, takerOrder.ID)
		// НЕ возвращаем в пул - он еще используется в вызывающем коде
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
	
	// ИСПРАВЛЕНИЕ: Проверяем что уровень еще существует
	level, levelExists := levels[order.Price]
	if !levelExists {
		// Уровень был удален, но ордер остался в индексе
		// Просто удаляем из индекса и возвращаем в пул
		delete(ob.OrderIndex, orderID)
		putOrderToPool(order)
		atomic.AddUint64(&ob.stats.TotalCancels, 1)
//		fmt.Printf("✗ Отменен ордер #%d (уровень уже удален)\n", orderID)
		return true
	}
	
	slot := level.Slots[order.Slot]
	
	// Удаляем ордер из слота
	//found := false
	for i, o := range slot.Orders {
		if o.ID == orderID {
			slot.Orders = append(slot.Orders[:i], slot.Orders[i+1:]...)
			slot.Volume -= order.Size  // <- Уменьшаем volume слота
			level.TotalVolume -= order.Size
			//found = true
			break
		}
	}

	// ДОБАВЬТЕ ПРОВЕРКУ КОНСИСТЕНТНОСТИ:
	// Если ордеров больше нет, volume должен быть 0
	if len(slot.Orders) == 0 {
		slot.Volume = 0
	}
	
	// Удаляем из индекса
	delete(ob.OrderIndex, orderID)
	
	// Возвращаем ордер в пул
	putOrderToPool(order)
	
	// Если уровень пустой, удаляем его и возвращаем в пул
	// Если уровень пустой, удаляем его и возвращаем в пул
	if level.TotalVolume == 0 {
		deletedPrice := level.Price
		delete(levels, level.Price)
		putPriceLevelToPool(level)
		
		// Обновляем BestBid/BestAsk ТОЛЬКО если удалили именно best уровень
		if order.Side == BUY && deletedPrice == ob.BestBid {
			// Ищем новый BestBid
			ob.BestBid = 0
			for price := range ob.BuyLevels {
				if price > ob.BestBid {
					ob.BestBid = price
				}
			}
		} else if order.Side == SELL && deletedPrice == ob.BestAsk {
			// Ищем новый BestAsk
			ob.BestAsk = 0
			for price := range ob.SellLevels {
				if ob.BestAsk == 0 || price < ob.BestAsk {
					ob.BestAsk = price
				}
			}
		}
	}
	
	atomic.AddUint64(&ob.stats.TotalOperations, 1)
	atomic.AddUint64(&ob.stats.TotalCancels, 1)
/*	
	if found {
		fmt.Printf("✗ Отменен ордер #%d\n", orderID)
	} else {
		fmt.Printf("✗ Отменен ордер #%d (не найден в слоте)\n", orderID)
	}
*/	
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
func (ob *OrderBook) ModifyOrder(orderID uint64, newPrice *uint64, newSize *uint64) bool {
	ob.mu.Lock()
	defer ob.mu.Unlock()
	
	order, exists := ob.OrderIndex[orderID]
	if !exists {
		return false
	}
	
	// Определяем что меняется
	priceChanged := newPrice != nil && *newPrice != order.Price
	sizeChanged := newSize != nil && *newSize != order.Size
	
	if !priceChanged && !sizeChanged {
		return true // Ничего не изменилось
	}
	
	levels := ob.BuyLevels
	if order.Side == SELL {
		levels = ob.SellLevels
	}
	
	// ИСПРАВЛЕНИЕ: Проверяем что уровень существует
	oldLevel, levelExists := levels[order.Price]
	if !levelExists {
		// Уровень был удален - не можем модифицировать
		return false
	}
	
	oldSlot := oldLevel.Slots[order.Slot]
	
	// Случай 1: Меняется только объем - остаемся в том же узле
	if !priceChanged && sizeChanged {
		newSizeVal := *newSize
		oldSize := order.Size
		
		// Обновляем объемы
		oldSlot.Volume -= oldSize
		oldLevel.TotalVolume -= oldSize
		
		order.Size = newSizeVal
		
		// Проверяем, нужно ли сменить слот из-за нового размера
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
			targetSlot.Volume += newSizeVal
			
/*			fmt.Printf("↻ Изменен ордер #%d: новый объем %.2f, перемещен в слот %d\n",
				orderID, float64(newSizeVal)/PRICE_DECIMALS, newSlot) */
		} else {
			// Остаемся в том же слоте
			oldSlot.Volume += newSizeVal
/*			fmt.Printf("↻ Изменен ордер #%d: новый объем %.2f (слот %d)\n",
				orderID, float64(newSizeVal)/PRICE_DECIMALS, order.Slot) */
		}
		
		oldLevel.TotalVolume += newSizeVal
		atomic.AddUint64(&ob.stats.TotalModifies, 1)
		return true
	}
	
	// Случай 2: Меняется цена (возможно и объем) - атомарный перенос в другой узел
	// Случай 2: Меняется цена (возможно и объем) - атомарный перенос в другой узел
	if priceChanged {
		newPriceVal := *newPrice
		newSizeVal := order.Size
		if sizeChanged {
			newSizeVal = *newSize
		}
    
		// ВАЖНО: Сначала удаляем из старого слота
		orderFound := false
		for i, o := range oldSlot.Orders {
			if o.ID == orderID {
				oldSlot.Orders = append(oldSlot.Orders[:i], oldSlot.Orders[i+1:]...)
				oldSlot.Volume -= order.Size
				oldLevel.TotalVolume -= order.Size
				orderFound = true
				break
			}
		}
    
		if !orderFound {
			// Ордер не найден в слоте - ошибка консистентности
			fmt.Printf("⚠️  ОШИБКА: Ордер #%d не найден в слоте %d уровня %.2f\n",
				orderID, order.Slot, float64(order.Price)/PRICE_DECIMALS)
			return false
		}
		
		// Проверка консистентности старого слота
		if len(oldSlot.Orders) == 0 {
			oldSlot.Volume = 0
		}
    
		// Если старый уровень стал пустым, удаляем его
		if oldLevel.TotalVolume == 0 {
			deletedPrice := oldLevel.Price
			delete(levels, order.Price)
			putPriceLevelToPool(oldLevel)
			
			// Обновляем BestBid/BestAsk если удалили best уровень
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
		}
    
		// ТЕПЕРЬ обновляем ордер (после удаления из старого места!)
		order.Price = newPriceVal
		order.Size = newSizeVal
		order.Slot = ob.determineSlot(order)
    
		// Получаем или создаем новый уровень цены
		newLevel, exists := levels[newPriceVal]
		if !exists {
			newLevel = getPriceLevelFromPool()
			newLevel.Price = newPriceVal
			newLevel.TotalVolume = 0
			levels[newPriceVal] = newLevel
			
			// Обновляем BestBid/BestAsk при создании нового уровня
			if order.Side == SELL {
				if ob.BestAsk == 0 || newPriceVal < ob.BestAsk {
					ob.BestAsk = newPriceVal
				}
			} else if order.Side == BUY {
				if ob.BestBid == 0 || newPriceVal > ob.BestBid {
					ob.BestBid = newPriceVal
				}
			}
		}
    
		// Добавляем в новый слот
		newSlot := newLevel.Slots[order.Slot]
		newSlot.Orders = append(newSlot.Orders, order)
		newSlot.Volume += newSizeVal
		newLevel.TotalVolume += newSizeVal
		
		atomic.AddUint64(&ob.stats.TotalModifies, 1)
		atomic.AddUint64(&ob.stats.TotalOperations, 1)
		
		// Проверяем матчинг с новой ценой
		ob.tryMatchUnsafe(order)
    }
    return true
}

// rebuildTree перестраивает Verkle дерево
func (ob *OrderBook) rebuildTree() {
	allLevels := make([]*PriceLevel, 0, len(ob.BuyLevels)+len(ob.SellLevels))
	
	for _, level := range ob.BuyLevels {
		allLevels = append(allLevels, level)
	}
	for _, level := range ob.SellLevels {
		allLevels = append(allLevels, level)
	}
	
	sort.Slice(allLevels, func(i, j int) bool {
		return allLevels[i].Price < allLevels[j].Price
	})
	
	if len(allLevels) == 0 {
		ob.Root = &VerkleNode{IsLeaf: false}
		return
	}
	
	ob.Root = &VerkleNode{IsLeaf: false}
	
	for i, level := range allLevels {
		childIndex := i % VERKLE_WIDTH
		ob.Root.Children[childIndex] = level
	}
}

// computeRootHash вычисляет Blake3 хеш корня дерева
func (ob *OrderBook) computeRootHash() {
	ob.LastRootHash = ob.hashNode(ob.Root)
}

// hashNode рекурсивно вычисляет хеш узла
func (ob *OrderBook) hashNode(node *VerkleNode) [32]byte {
	hasher := blake3.New()
	
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
	
	// Принудительно пересчитываем хеш перед выводом статистики
	ob.rebuildTree()
	ob.computeRootHash()
	atomic.AddUint64(&ob.stats.HashCount, 1)
	
	totalOperations := atomic.LoadUint64(&ob.stats.TotalOperations)
	totalOrders := atomic.LoadUint64(&ob.stats.TotalOrders)
	totalMatches := atomic.LoadUint64(&ob.stats.TotalMatches)
	totalCancels := atomic.LoadUint64(&ob.stats.TotalCancels)
	totalModifies := atomic.LoadUint64(&ob.stats.TotalModifies)
	totalMarketOrders := atomic.LoadUint64(&ob.stats.TotalMarketOrders)
	hashCount := atomic.LoadUint64(&ob.stats.HashCount)
	rootHash := ob.LastRootHash
	
	// ИСПРАВЛЕНИЕ: Считываем длины ПОД lock
	activeOrders := len(ob.OrderIndex)
	buyLevels := len(ob.BuyLevels)
	sellLevels := len(ob.SellLevels)
	
	ob.mu.Unlock()
	
	fmt.Printf("\n═══════════════════════════════════════════\n")
	fmt.Printf("Статистика %s:\n", ob.Symbol)
	fmt.Printf("  • Активных ордеров: %d\n", activeOrders)
	fmt.Printf("  • Всего добавлено: %d\n", totalOrders)
	fmt.Printf("  • Маркет-ордеров: %d\n", totalMarketOrders)
	fmt.Printf("  • Матчей: %d\n", totalMatches)
	fmt.Printf("  • Отмен: %d\n", totalCancels)
	fmt.Printf("  • Изменений: %d\n", totalModifies)
	fmt.Printf("  • BUY уровней: %d\n", buyLevels)
	fmt.Printf("  • SELL уровней: %d\n", sellLevels)
	fmt.Printf("  • Всего операций (Tx): %d\n", totalOperations)
	fmt.Printf("  • Хешей посчитано: %d\n", hashCount)
	fmt.Printf("  • Root hash: %x...\n", rootHash[:16])
	fmt.Printf("═══════════════════════════════════════════\n\n")
}


// Симулятор с высокой нагрузкой
func main() {
	fmt.Println("🚀 Оптимизированный ордербук с Verkle деревом")
	fmt.Println("   • Memory pools для минимизации GC")
	fmt.Println("   • Периодическое хеширование (500ms)")
	fmt.Println("   • Атомарные операции для счетчиков")
	fmt.Println("   • Оптимизированное изменение ордеров\n")
	
	rand.Seed(time.Now().UnixNano())
	ob := NewOrderBook("BTC")
	defer ob.Stop()
	
	basePrice := uint64(6500000) // $65000
	
	// Симулируем высокую нагрузку
	numOperations := 10_000
	//operationTypes := []string{"add", "cancel", "modify"}
	
	addedOrders := make([]uint64, 0)
	
	startTime := time.Now()

	for i := 0; i < numOperations; i++ {
		// Распределение операций:
		// 25% - маркет ордера
		// 25% - лимитные добавления
		// 25% - отмены
		// 25% - изменения
		
		r := rand.Float32()
		
		if r < 0.15 {
			// МАРКЕТ ОРДЕР (15%)
			traderID := uint32(rand.Intn(MAX_TRADERS) + 1)
			size := uint64(rand.Intn(10000) + 100)
			side := BUY
			if rand.Float32() < 0.5 {
				side = SELL
			}
			ob.ExecuteMarketOrder(traderID, size, side)
			
		} else if r < 0.50 {
			// ЛИМИТНЫЙ ОРДЕР (25%)
			traderID := uint32(rand.Intn(MAX_TRADERS) + 1)
			priceOffset := uint64(rand.Intn(20000) - 10000)
			price := basePrice + priceOffset
			size := uint64(rand.Intn(10000) + 100)
			side := BUY
			if rand.Float32() < 0.5 {
				side = SELL
			}
			
			order := ob.AddLimitOrder(traderID, price, size, side)
			addedOrders = append(addedOrders, order.ID)
			
		} else if r < 0.75 {
			// ОТМЕНА (25%)
			if len(addedOrders) > 0 {
				idx := rand.Intn(len(addedOrders))
				orderID := addedOrders[idx]
				if ob.CancelOrder(orderID) {
					addedOrders = append(addedOrders[:idx], addedOrders[idx+1:]...)
				}
			}
			
		} else {
			// ИЗМЕНЕНИЕ (25%)
			if len(addedOrders) > 0 {
				orderID := addedOrders[rand.Intn(len(addedOrders))]
				
				modType := rand.Intn(3)
				switch modType {
				case 0: // Только объем
					newSize := uint64(rand.Intn(10000) + 100)
					ob.ModifyOrder(orderID, nil, &newSize)
					
				case 1: // Только цена
					priceOffset := uint64(rand.Intn(20000) - 10000)
					newPrice := basePrice + priceOffset
					ob.ModifyOrder(orderID, &newPrice, nil)
					
				case 2: // Цена и объем
					priceOffset := uint64(rand.Intn(20000) - 10000)
					newPrice := basePrice + priceOffset
					newSize := uint64(rand.Intn(10000) + 100)
					ob.ModifyOrder(orderID, &newPrice, &newSize)
				}
			}
		}
		
		// Статистика каждые N операций
		if (i+1)%50_000 == 0 {
			ob.PrintStats()
		}
	}
	
	elapsed := time.Since(startTime)
	
	// Финальная статистика
	fmt.Println("\n🏁 ФИНАЛЬНАЯ СТАТИСТИКА")
	ob.PrintStats()
	
	tps := float64(numOperations) / elapsed.Seconds()
	fmt.Printf("⚡ Производительность: %.0f операций/сек\n", tps)
	fmt.Printf("⏱  Общее время: %v\n", elapsed)
	
	//JSON export 
	// Экспортируем состояние дерева
	fmt.Println("\n📁 Экспорт состояния дерева...")
	
	// Полный экспорт (может быть большим)
	err := ob.ExportToJSON("orderbook_full.json")
	if err != nil {
		fmt.Printf("Ошибка экспорта: %v\n", err)
	}
	
	// Компактный экспорт
	err = ob.ExportToJSONCompact("orderbook_compact.json")
	if err != nil {
		fmt.Printf("Ошибка экспорта: %v\n", err)
	}
	
	// Ждем последнего хеша
	time.Sleep(HASH_INTERVAL + 100*time.Millisecond)
	
	fmt.Println("\n✅ Симуляция завершена")
}
