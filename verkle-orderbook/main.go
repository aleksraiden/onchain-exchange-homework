package main

import (
	"encoding/binary"
	"fmt"
	"math/rand"
	"sort"
	"sync"
	"sync/atomic"
	"time"

	"github.com/zeebo/blake3"
)

// Константы системы
const (
	VERKLE_WIDTH      = 16      // Ширина Verkle дерева
	PRICE_DECIMALS    = 100     // Точность цены (2 знака после запятой)
	MAX_TRADERS       = 10000   // Максимальное количество трейдеров
	HASH_INTERVAL     = 500 * time.Millisecond // Интервал хеширования
	
	// Слоты для распределения ордеров
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

// Вспомогательные функции для работы с пулами
func getOrderFromPool() *Order {
	return orderPool.Get().(*Order)
}

func putOrderToPool(o *Order) {
	// Очищаем данные перед возвратом в пул
	*o = Order{}
	orderPool.Put(o)
}

func getSlotFromPool() *Slot {
	s := slotPool.Get().(*Slot)
	s.Orders = s.Orders[:0] // Сбрасываем length, но сохраняем capacity
	s.Volume = 0
	return s
}

func putSlotToPool(s *Slot) {
	// ИСПРАВЛЕНИЕ: НЕ возвращаем ордера в пул здесь!
	// Они управляются через OrderIndex и возвращаются при Cancel
	s.Orders = s.Orders[:0]
	s.Volume = 0
	slotPool.Put(s)
}

func getPriceLevelFromPool() *PriceLevel {
	pl := priceLevelPool.Get().(*PriceLevel)
	
	// Инициализируем слоты если они nil
	for i := 0; i < VERKLE_WIDTH; i++ {
		if pl.Slots[i] == nil {
			pl.Slots[i] = getSlotFromPool()
		}
	}
	
	return pl
}

func putPriceLevelToPool(pl *PriceLevel) {
	// Возвращаем слоты в пул
	for i := 0; i < VERKLE_WIDTH; i++ {
		if pl.Slots[i] != nil {
			putSlotToPool(pl.Slots[i])
		}
	}
	pl.Price = 0
	pl.TotalVolume = 0
	priceLevelPool.Put(pl)
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

// Order - структура ордера
type Order struct {
	ID       uint64  // Уникальный последовательный ID
	TraderID uint32  // ID трейдера
	Price    uint64  // Цена в целых числах (умножена на 100)
	Size     uint64  // Объем ордера
	Side     Side    // Сторона (BUY/SELL)
	Slot     uint8   // Слот в Verkle дереве
}

// PriceLevel - уровень цены, содержит слоты с ордерами
type PriceLevel struct {
	Price       uint64              // Цена этого уровня
	TotalVolume uint64              // Суммарный объем всех ордеров на уровне
	Slots       [VERKLE_WIDTH]*Slot // 16 слотов для распределения ордеров
}

// Slot - слот внутри ценового уровня
type Slot struct {
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
	BuyLevels    map[uint64]*PriceLevel   // Bid уровни (цена -> PriceLevel)
	SellLevels   map[uint64]*PriceLevel   // Ask уровни (цена -> PriceLevel)
	OrderIndex   map[uint64]*Order        // Индекс всех ордеров по ID
	Root         *VerkleNode              // Корень Verkle дерева
	LastRootHash [32]byte                 // Последний вычисленный root hash
	
	mu           sync.RWMutex             // Mutex для защиты структур данных
	hashTicker   *time.Ticker             // Ticker для периодического хеширования
	stopChan     chan struct{}            // Канал для остановки хеширования
	
	stats        Stats                    // Статистика для мониторинга
}

// Stats - статистика ордербука
type Stats struct {
	TotalOrders      uint64
	TotalMatches     uint64
	TotalCancels     uint64
	TotalModifies    uint64
	LastHashTime     time.Time
	HashCount        uint64
}

// NewOrderBook создает новый ордербук
func NewOrderBook(symbol string) *OrderBook {
	ob := &OrderBook{
		Symbol:      symbol,
		nextOrderID: 0,
		BuyLevels:   make(map[uint64]*PriceLevel),
		SellLevels:  make(map[uint64]*PriceLevel),
		OrderIndex:  make(map[uint64]*Order),
		Root:        &VerkleNode{IsLeaf: false},
		hashTicker:  time.NewTicker(HASH_INTERVAL),
		stopChan:    make(chan struct{}),
	}
	
	// Запускаем горутину для периодического хеширования
	go ob.periodicHasher()
	
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
			ob.mu.RLock()
			ob.rebuildTree()
			ob.computeRootHash()
			atomic.AddUint64(&ob.stats.HashCount, 1)
			ob.stats.LastHashTime = time.Now()
			rootHash := ob.LastRootHash
			ob.mu.RUnlock()
			
			fmt.Printf("⏱  Периодический хеш [%s]: %x...\n", 
				time.Now().Format("15:04:05.000"), rootHash[:8])
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
	// Получаем ордер из пула
	order := getOrderFromPool()
	order.ID = atomic.AddUint64(&ob.nextOrderID, 1)
	order.TraderID = traderID
	order.Price = price
	order.Size = size
	order.Side = side
	order.Slot = ob.determineSlot(order)
	
	ob.mu.Lock()
	defer ob.mu.Unlock()
	
	// Добавляем в соответствующую сторону книги
	levels := ob.BuyLevels
	if side == SELL {
		levels = ob.SellLevels
	}
	
	// Получаем или создаем уровень цены
	level, exists := levels[price]
	if !exists {
		level = getPriceLevelFromPool()
		level.Price = price
		level.TotalVolume = 0
		levels[price] = level
	}
	
	// Добавляем ордер в соответствующий слот
	slot := level.Slots[order.Slot]
	slot.Orders = append(slot.Orders, order)
	slot.Volume += size
	level.TotalVolume += size
	
	// Индексируем ордер
	ob.OrderIndex[order.ID] = order
	atomic.AddUint64(&ob.stats.TotalOrders, 1)
/*	
	fmt.Printf("✓ Ордер #%d: %s %.2f размер %.2f трейдер %d слот %d\n",
		order.ID, side, float64(price)/PRICE_DECIMALS, float64(size)/PRICE_DECIMALS,
		traderID, order.Slot) */
	
	// Проверяем возможность матчинга (без lock, т.к. уже под lock)
	ob.tryMatchUnsafe(order)
	
	return order
}

// tryMatchUnsafe пытается совместить ордер (вызывается под lock)
func (ob *OrderBook) tryMatchUnsafe(order *Order) {
	oppositeLevels := ob.SellLevels
	if order.Side == SELL {
		oppositeLevels = ob.BuyLevels
	}
	
	prices := make([]uint64, 0, len(oppositeLevels))
	for price := range oppositeLevels {
		prices = append(prices, price)
	}
	
	if len(prices) == 0 {
		return
	}
	
	// Сортируем
	if order.Side == BUY {
		sort.Slice(prices, func(i, j int) bool { return prices[i] < prices[j] })
	} else {
		sort.Slice(prices, func(i, j int) bool { return prices[i] > prices[j] })
	}
	
	// Проверяем возможность матчинга
	for _, price := range prices {
		canMatch := false
		if order.Side == BUY && price <= order.Price {
			canMatch = true
		} else if order.Side == SELL && price >= order.Price {
			canMatch = true
		}
		
		if canMatch {
/*			level := oppositeLevels[price]
			fmt.Printf("⚡ МАТЧ: Ордер #%d (%s %.2f) <-> уровень %.2f (объем %.2f)\n",
				order.ID, order.Side, float64(order.Price)/PRICE_DECIMALS,
				float64(price)/PRICE_DECIMALS, float64(level.TotalVolume)/PRICE_DECIMALS) */
			atomic.AddUint64(&ob.stats.TotalMatches, 1)
			// Хеш будет посчитан по таймеру, а не здесь
		}
	}
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
	found := false
	for i, o := range slot.Orders {
		if o.ID == orderID {
			slot.Orders = append(slot.Orders[:i], slot.Orders[i+1:]...)
			slot.Volume -= order.Size
			level.TotalVolume -= order.Size
			found = true
			break
		}
	}
	
	// Удаляем из индекса
	delete(ob.OrderIndex, orderID)
	
	// Возвращаем ордер в пул
	putOrderToPool(order)
	
	// Если уровень пустой, удаляем его и возвращаем в пул
	if level.TotalVolume == 0 {
		delete(levels, level.Price)
		putPriceLevelToPool(level)
	}
	
	atomic.AddUint64(&ob.stats.TotalCancels, 1)
	
	if found {
//		fmt.Printf("✗ Отменен ордер #%d\n", orderID)
	} else {
		fmt.Printf("✗ Отменен ордер #%d (не найден в слоте)\n", orderID)
	}
	
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
	if priceChanged {
		newPriceVal := *newPrice
		newSizeVal := order.Size
		if sizeChanged {
			newSizeVal = *newSize
		}
		
		// Удаляем из старого слота и уровня
		for i, o := range oldSlot.Orders {
			if o.ID == orderID {
				oldSlot.Orders = append(oldSlot.Orders[:i], oldSlot.Orders[i+1:]...)
				oldSlot.Volume -= order.Size
				oldLevel.TotalVolume -= order.Size
				break
			}
		}
		
		// Если старый уровень стал пустым, удаляем его
		if oldLevel.TotalVolume == 0 {
			delete(levels, order.Price)
			putPriceLevelToPool(oldLevel)
		}
		
		// Обновляем ордер
//		oldPrice := order.Price
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
		}
		
		// Добавляем в новый слот
		newSlot := newLevel.Slots[order.Slot]
		newSlot.Orders = append(newSlot.Orders, order)
		newSlot.Volume += newSizeVal
		newLevel.TotalVolume += newSizeVal
/*		
		fmt.Printf("↻ Изменен ордер #%d: цена %.2f→%.2f, объем %.2f, слот %d\n",
			orderID, float64(oldPrice)/PRICE_DECIMALS, float64(newPriceVal)/PRICE_DECIMALS,
			float64(newSizeVal)/PRICE_DECIMALS, order.Slot)
*/		
		atomic.AddUint64(&ob.stats.TotalModifies, 1)
		
		// Проверяем матчинг с новой ценой
		ob.tryMatchUnsafe(order)
		
		return true
	}
	
	return false
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
	ob.mu.RLock()
	defer ob.mu.RUnlock()
	
	totalOrders := atomic.LoadUint64(&ob.stats.TotalOrders)
	totalMatches := atomic.LoadUint64(&ob.stats.TotalMatches)
	totalCancels := atomic.LoadUint64(&ob.stats.TotalCancels)
	totalModifies := atomic.LoadUint64(&ob.stats.TotalModifies)
	hashCount := atomic.LoadUint64(&ob.stats.HashCount)
	
	fmt.Printf("\n═══════════════════════════════════════════\n")
	fmt.Printf("Статистика %s:\n", ob.Symbol)
	fmt.Printf("  • Активных ордеров: %d\n", len(ob.OrderIndex))
	fmt.Printf("  • Всего добавлено: %d\n", totalOrders)
	fmt.Printf("  • Матчей: %d\n", totalMatches)
	fmt.Printf("  • Отмен: %d\n", totalCancels)
	fmt.Printf("  • Изменений: %d\n", totalModifies)
	fmt.Printf("  • BUY уровней: %d\n", len(ob.BuyLevels))
	fmt.Printf("  • SELL уровней: %d\n", len(ob.SellLevels))
	fmt.Printf("  • Хешей посчитано: %d\n", hashCount)
	fmt.Printf("  • Root hash: %x...\n", ob.LastRootHash[:16])
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
	numOperations := 10000
	operationTypes := []string{"add", "cancel", "modify"}
	
	addedOrders := make([]uint64, 0)
	
	startTime := time.Now()
	
	for i := 0; i < numOperations; i++ {
		opType := operationTypes[rand.Intn(len(operationTypes))]
		
		switch opType {
		case "add":
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
			
		case "cancel":
			if len(addedOrders) > 0 {
				idx := rand.Intn(len(addedOrders))
				orderID := addedOrders[idx]
				if ob.CancelOrder(orderID) {
					// Удаляем из списка
					addedOrders = append(addedOrders[:idx], addedOrders[idx+1:]...)
				}
			}
			
		case "modify":
			if len(addedOrders) > 0 {
				orderID := addedOrders[rand.Intn(len(addedOrders))]
				
				// Случайно выбираем что менять
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
		
		// Статистика каждые 1000 операций
		if (i+1)%1000 == 0 {
			ob.PrintStats()
		}
		
		// Небольшая задержка для демонстрации
		//time.Sleep(10 * time.Millisecond)
	}
	
	elapsed := time.Since(startTime)
	
	// Финальная статистика
	fmt.Println("\n🏁 ФИНАЛЬНАЯ СТАТИСТИКА")
	ob.PrintStats()
	
	tps := float64(numOperations) / elapsed.Seconds()
	fmt.Printf("⚡ Производительность: %.0f операций/сек\n", tps)
	fmt.Printf("⏱  Общее время: %v\n", elapsed)
	
	// Ждем последнего хеша
	time.Sleep(HASH_INTERVAL + 100*time.Millisecond)
	
	fmt.Println("\n✅ Симуляция завершена")
}
