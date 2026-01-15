// mm-bybit-mirror.go
package main

import (
	//"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"log"
	"math"
	"os"
	"os/signal"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"tx-generator/tx" // Убедитесь, что путь к пакету корректный

	"github.com/gorilla/websocket"
	"google.golang.org/protobuf/proto"
)

const (
	bybitWSURL     = "wss://stream.bybit.com/v5/public/linear"
	orderbookDepth = 50
	mirrorDepth    = 11
	ordersPerLevel = 10		//Разделяем ценовой уровень на несколько ордеров (если он превышает maxLevelSizeForSplitting)
	outputFile     = "mm-txs.bin"
	
	
	maxLevelSizeForSplitting = 1000000.0	//Не разделять уровень если размер меньше, иначе ставим ordersPerLevel

	DebugSymbol = "TONUSDT"	//Для этого символа выводим подробный лог 
)

// Конфигурация торговой пары
type SymbolConfig struct {
	Name      string
	ID        uint32 // MarketCode
	PriceDecs int    // Точность цены (знаков после запятой)
	QtyDecs   int    // Точность объема
	TickSize  float64
	MinQty    float64
}

var symbols = []SymbolConfig{
	{"BTCUSDT", 101, 2, 3, 0.1, 0.001},
	{"ETHUSDT", 102, 2, 2, 0.01, 0.01},
	{"SOLUSDT", 103, 3, 1, 0.001, 0.1},
	{"XRPUSDT", 104, 4, 1, 0.0001, 1},
	{"XMRUSDT", 105, 2, 3, 0.01, 0.001},
	{"DOGEUSDT", 106, 5, 0, 0.00001, 10},
	{"SUIUSDT", 107, 4, 1, 0.0001, 1},
	{"LTCUSDT", 108, 2, 2, 0.01, 0.1},
	{"HYPEUSDT", 109, 4, 1, 0.0001, 1},
	{"PUMPFUNUSDT", 110, 6, 0, 0.000001, 100},
	{"TONUSDT", 111, 4, 1, 0.0001, 0.1},
}

var (
	uidCounter  uint64
	nonce       uint64
	txCreated   uint64 
	lastTxCount uint64 
	privKey     ed25519.PrivateKey
	signerUID   uint64 = 123456789
	allTxs      [][]byte
	muTxs       sync.Mutex

	ctx, cancel = context.WithCancel(context.Background())

	markets = make(map[string]*MarketState)
)

type Level struct {
	Price float64
	Qty   float64
}

type OrderBook struct {
	Bids []Level
	Asks []Level
}

type ManagedOrder struct {
	OrderID  []byte // 16 байт
	Side     bool   // true = buy
	Price    float64
	Quantity float64
	
	LevelIdx int // Индекс ценового уровня (0 = best)
	SplitIdx int // Позиция внутри уровня (0 = первый)
}

type MarketState struct {
	Config         SymbolConfig
	OB             OrderBook
	ManagedOrders  map[string]*ManagedOrder // Key: "buy-lvl-0-split-1"
	LastRawMessage []byte                   
	mu             sync.RWMutex
}

func init() {
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		log.Fatal(err)
	}
	privKey = priv

	for _, cfg := range symbols {
		markets[cfg.Name] = &MarketState{
			Config:        cfg,
			ManagedOrders: make(map[string]*ManagedOrder),
			OB:            OrderBook{Bids: make([]Level, 0), Asks: make([]Level, 0)},
		}
	}

	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-sigChan
		log.Println("Получен сигнал завершения. Отмена всех ордеров и сохранение tx...")
		cancelAllOrdersGlobal()
		saveAllTxs()
		os.Exit(0)
	}()
}

func main() {
	log.Printf("Зеркальный MM Bybit запускается для %d пар. Depth: %d (source %d)", len(symbols), mirrorDepth, orderbookDepth)

	ws, _, err := websocket.DefaultDialer.Dial(bybitWSURL, nil)
	if err != nil {
		log.Fatal("dial:", err)
	}

	go func() {
		<-ctx.Done()
		ws.Close()
	}()

	go runNoopGenerator(100 * time.Millisecond)

	subscribe(ws)

	for {
		_, message, err := ws.ReadMessage()
		if err != nil {
			if websocket.IsUnexpectedCloseError(err, websocket.CloseGoingAway, websocket.CloseAbnormalClosure) {
				log.Printf("WS error: %v", err)
			}
			return
		}
		processMessage(message)
	}
}

func runNoopGenerator(interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			generateMetaNoop()
		}
	}
}

func generateMetaNoop() {
	p := &tx.MetaNoopPayload{Payload: []byte{}}
	header := createHeader(tx.OpCode_META_NOOP, 0)
	txx := buildTx(header, p)
	appendTx(txx)
}

func subscribe(ws *websocket.Conn) {
	args := make([]string, len(symbols))
	for i, s := range symbols {
		args[i] = fmt.Sprintf("orderbook.%d.%s", orderbookDepth, s.Name)
	}

	chunkSize := 10
	for i := 0; i < len(args); i += chunkSize {
		end := i + chunkSize
		if end > len(args) {
			end = len(args)
		}
		sub := map[string]interface{}{
			"op":   "subscribe",
			"args": args[i:end],
		}
		if err := ws.WriteJSON(sub); err != nil {
			log.Fatal("subscribe:", err)
		}
		time.Sleep(100 * time.Millisecond)
	}
}

func processMessage(raw []byte) {
	var msg struct {
		Topic string          `json:"topic"`
		Type  string          `json:"type"`
		Data  json.RawMessage `json:"data"`
	}

	if err := json.Unmarshal(raw, &msg); err != nil {
		return
	}

	parts := strings.Split(msg.Topic, ".")
	if len(parts) < 3 {
		return
	}
	symbolName := parts[2]

	market, exists := markets[symbolName]
	if !exists {
		return
	}

	market.mu.Lock()
	market.LastRawMessage = make([]byte, len(raw))
	copy(market.LastRawMessage, raw)
	market.mu.Unlock()

	if strings.HasPrefix(msg.Topic, "orderbook") {
		updateOrderBook(market, msg.Data)
	}
}

func updateOrderBook(m *MarketState, data json.RawMessage) {
	var update struct {
		B      [][]string `json:"b"`
		A      [][]string `json:"a"`
	}

	if err := json.Unmarshal(data, &update); err != nil {
		return
	}

	m.mu.Lock()
	if len(update.B) > 0 {
		m.OB.Bids = convertLevels(update.B)
	}
	if len(update.A) > 0 {
		m.OB.Asks = convertLevels(update.A)
	}
	m.mu.Unlock()

	generateMirrorOrders(m)
	logMarketStatus(m)
}

func logMarketStatus(m *MarketState) {
	m.mu.RLock()
	bidsEmpty := len(m.OB.Bids) == 0
	asksEmpty := len(m.OB.Asks) == 0
	managedCount := len(m.ManagedOrders)
	
	var bestBid, bestAsk Level
	if !bidsEmpty { bestBid = m.OB.Bids[0] }
	if !asksEmpty { bestAsk = m.OB.Asks[0] }
	
	m.mu.RUnlock()

	timestamp := time.Now().Format("15:04:05.000")
	currentTxCount := atomic.LoadUint64(&txCreated)
	deltaTx := currentTxCount - atomic.LoadUint64(&lastTxCount)
	atomic.StoreUint64(&lastTxCount, currentTxCount)

	if bidsEmpty && asksEmpty {
		log.Printf("\x1b[31m[%s %s] EMPTY BOOK! TX: %d (+%d)\x1b[0m\n", 
			timestamp, m.Config.Name, currentTxCount, deltaTx)
	} else {
		spread := bestAsk.Price - bestBid.Price
		log.Printf("[%s] %-8s | Bid %10.*f | Ask %10.*f | Spr %.2f | Active: %3d | TX: %d (+%d)",
			timestamp,
			m.Config.Name,
			m.Config.PriceDecs, bestBid.Price,
			m.Config.PriceDecs, bestAsk.Price,
			spread,
			managedCount,
			currentTxCount,
			deltaTx,
		)
	}
}

func convertLevels(levels [][]string) []Level {
	result := make([]Level, 0, len(levels))
	for _, lv := range levels {
		if len(lv) != 2 {
			continue
		}
		p, _ := strconv.ParseFloat(lv[0], 64)
		q, _ := strconv.ParseFloat(lv[1], 64)
		result = append(result, Level{Price: p, Qty: q})
	}
	return result
}

// ГЛАВНАЯ ЛОГИКА ОБНОВЛЕНИЯ
func generateMirrorOrders(m *MarketState) {
	m.mu.RLock()
	bids := append([]Level{}, m.OB.Bids...)
	asks := append([]Level{}, m.OB.Asks...)
	m.mu.RUnlock()

	sort.Slice(bids, func(i, j int) bool { return bids[i].Price > bids[j].Price })
	sort.Slice(asks, func(i, j int) bool { return asks[i].Price < asks[j].Price })

	// Мапа для отслеживания ключей, которые актуальны в этом цикле
	activeKeys := make(map[string]bool)

	// Проходим по уровням индексно (0..10)
	for i := 0; i < min(mirrorDepth, len(bids)); i++ {
		l := bids[i]
		createOrAmendOrders(m, true, l.Price, l.Qty, i, activeKeys)
	}
	for i := 0; i < min(mirrorDepth, len(asks)); i++ {
		l := asks[i]
		createOrAmendOrders(m, false, l.Price, l.Qty, i, activeKeys)
	}

	cancelOutdatedOrders(m, activeKeys)
}

func createOrAmendOrders(m *MarketState, isBuy bool, price, totalQty float64, levelIdx int, activeKeys map[string]bool) {
	levelValueUSDT := price * totalQty

	var numOrders int
	if levelValueUSDT > maxLevelSizeForSplitting {
		numOrders = ordersPerLevel
	} else {
		numOrders = 1
	}

	qtyPerOrder := totalQty / float64(numOrders)
	if qtyPerOrder <= 0 {
		return
	}

	for i := 0; i < numOrders; i++ {
		adjPrice := price
		if numOrders > 1 {
			if i%2 == 0 {
				adjPrice += m.Config.TickSize * float64(i) / 10.0
			} else {
				adjPrice -= m.Config.TickSize * float64(i) / 10.0
			}
		}

		key := fmt.Sprintf("%t-lvl-%d-split-%d", isBuy, levelIdx, i)
		activeKeys[key] = true

		m.mu.Lock()
		existing, ok := m.ManagedOrders[key]
		if ok {
			// Ордер уже есть, проверяем изменения
			// ВАЖНО: Обновляем индексы в существующем ордере (на случай ребалансировки стакана, хотя ключи жесткие)
			existing.LevelIdx = levelIdx
			existing.SplitIdx = i
			
			priceDiff := math.Abs(existing.Price - adjPrice)
			qtyDiff := math.Abs(existing.Quantity - qtyPerOrder)
			
			if priceDiff > m.Config.TickSize/10.0 || qtyDiff > m.Config.MinQty/10.0 {
				amendOrder(m, existing, adjPrice, qtyPerOrder)
			}
		} else {
			// Ордера нет - создаем
			uid := generateUserGeneratedID()
			
			// Создаем структуру
			newOrder := &ManagedOrder{
				OrderID:  uid,
				Side:     isBuy,
				Price:    adjPrice,
				Quantity: qtyPerOrder,
				LevelIdx: levelIdx, // <-- Запоминаем уровень
				SplitIdx: i,        // <-- Запоминаем сплит
			}
			m.ManagedOrders[key] = newOrder
			
			// Передаем newOrder целиком, чтобы логгер внутри createNewOrder видел индексы
			createNewOrder(m, newOrder) 
		}
		m.mu.Unlock()
	}
}

// Обновляем cancelOutdatedOrders, чтобы она собирала пачку на удаление
func cancelOutdatedOrders(m *MarketState, activeKeys map[string]bool) {
	m.mu.Lock()
	
	// Собираем список ордеров на удаление
	var toCancel []*ManagedOrder
	for key, order := range m.ManagedOrders {
		// Если ключа нет в списке активных на этом цикле - добавляем в список на удаление
		if !activeKeys[key] {
			toCancel = append(toCancel, order)
			delete(m.ManagedOrders, key) // Удаляем из мапы сразу под локом
		}
	}
	m.mu.Unlock()

	// Отправляем батч-отмену (если есть что отменять) уже БЕЗ лока, 
	// чтобы не тормозить другие горутины во время подписи транзакции
	if len(toCancel) > 0 {
		cancelOrdersBatch(m, toCancel)
	}
}

// Отмена пачкой
func cancelOrdersBatch(m *MarketState, orders []*ManagedOrder) {
	protoIDs := make([]*tx.OrderID, 0, len(orders))

	for _, order := range orders {
		protoIDs = append(protoIDs, &tx.OrderID{Id: order.OrderID})
/*		
		if m.Config.Name == DebugSymbol {
			sideStr := "SELL"
			if order.Side {
				sideStr = "BUY"
			}
			// Добавили sideStr (%s)
			log.Printf("[orderID: %x] [L:%d S:%d] %s Cancel (batch)", 
				order.OrderID[:8], order.LevelIdx, order.SplitIdx, sideStr)
		}
*/
	}

	if m.Config.Name == DebugSymbol {
		log.Printf(">>> Batch cancel sent for %d orders", len(orders))
	}

	p := &tx.OrderCancelPayload{
		OrderId: protoIDs,
	}
	
	txx := buildTx(createHeader(tx.OpCode_ORD_CANCEL, m.Config.ID), p)
	appendTx(txx)
}

// -----------------------------------------------------------
// TX Builders
// -----------------------------------------------------------

func createNewOrder(m *MarketState, order *ManagedOrder) {
	side := tx.Side_SELL
	sideStr := "SELL"
	if order.Side {
		side = tx.Side_BUY
		sideStr = "BUY"
	}

	if m.Config.Name == DebugSymbol {
		// Добавили [L:%d S:%d]
		log.Printf("[orderID: %x] [L:%d S:%d] New: %s, %.4f, %.4f USDT", 
			order.OrderID[:8], order.LevelIdx, order.SplitIdx, 
			sideStr, order.Quantity, order.Price)
	}

	priceMult := uint64(math.Pow(10, float64(m.Config.PriceDecs)))
	qtyMult := uint64(math.Pow(10, float64(m.Config.QtyDecs)))

	item := &tx.OrderItem{
		OrderId:   &tx.OrderID{Id: order.OrderID},
		Side:      side,
		OrderType: tx.OrderType_LIMIT,
		ExecType:  tx.TimeInForce_GTC,
		Quantity:  uint64(order.Quantity * float64(qtyMult)),
		Price:     uint64(order.Price * float64(priceMult)),
	}

	p := &tx.OrderCreatePayload{
		Orders: []*tx.OrderItem{item},
	}

	txx := buildTx(createHeader(tx.OpCode_ORD_CREATE, m.Config.ID), p)
	appendTx(txx)
}

func amendOrder(m *MarketState, order *ManagedOrder, newPrice, newQty float64) {
	priceMult := uint64(math.Pow(10, float64(m.Config.PriceDecs)))
	qtyMult := uint64(math.Pow(10, float64(m.Config.QtyDecs)))

	var priceUintPtr *uint64
	var qtyUintPtr *uint64
	var debugChanges string

	// Проверка изменения цены
	if math.Abs(order.Price - newPrice) > m.Config.TickSize/10.0 {
		val := uint64(math.Round(newPrice * float64(priceMult)))
		priceUintPtr = &val
		if m.Config.Name == DebugSymbol {
			debugChanges += fmt.Sprintf(" P: %.4f->%.4f", order.Price, newPrice)
		}
	}

	// Проверка изменения объема
	if math.Abs(order.Quantity - newQty) > m.Config.MinQty/10.0 {
		val := uint64(math.Round(newQty * float64(qtyMult)))
		qtyUintPtr = &val
		if m.Config.Name == DebugSymbol {
			debugChanges += fmt.Sprintf(" Q: %.4f->%.4f", order.Quantity, newQty)
		}
	}

	// Если изменений нет — выходим
	if priceUintPtr == nil && qtyUintPtr == nil {
		return
	}

	// --- ЛОГГИРОВАНИЕ ---
	if m.Config.Name == DebugSymbol {
		sideStr := "SELL"
		if order.Side {
			sideStr = "BUY"
		}
		// Добавили sideStr (%s) перед словом Amend
		log.Printf("[orderID: %x] [L:%d S:%d] %s Amend:%s", 
			order.OrderID[:8], order.LevelIdx, order.SplitIdx, sideStr, debugChanges)
	}
	// --------------------

	item := &tx.AmendItem{
		OrderId:  &tx.OrderID{Id: order.OrderID},
		Quantity: qtyUintPtr,
		Price:    priceUintPtr,
	}

	p := &tx.OrderAmendPayload{
		Amends: []*tx.AmendItem{item},
	}

	txx := buildTx(createHeader(tx.OpCode_ORD_AMEND, m.Config.ID), p)
	appendTx(txx)

	if priceUintPtr != nil {
		order.Price = newPrice
	}
	if qtyUintPtr != nil {
		order.Quantity = newQty
	}
}

func cancelOrder(m *MarketState, order *ManagedOrder) {
	if m.Config.Name == DebugSymbol {
		log.Printf("[orderID: %x] Cancel (outdated)", order.OrderID[:8])
	}

	p := &tx.OrderCancelPayload{
		OrderId: []*tx.OrderID{{Id: order.OrderID}},
	}
	txx := buildTx(createHeader(tx.OpCode_ORD_CANCEL, m.Config.ID), p)
	appendTx(txx)
}

// Обновляем глобальную отмену, чтобы она тоже использовала батчинг
func cancelAllOrdersGlobal() {
	countOrders := 0
	countTxs := 0

	for _, m := range markets {
		m.mu.Lock()
		var toCancel []*ManagedOrder
		for _, order := range m.ManagedOrders {
			toCancel = append(toCancel, order)
		}
		// Очищаем мапу полностью, но создаем новую, чтобы не сломать ссылки (или просто удаляем ключи)
		// Проще пересоздать, если мы выходим, но для graceful shutdown достаточно range delete или присвоения.
		// В данном случае мы просто собрали список.
		m.ManagedOrders = make(map[string]*ManagedOrder)
		m.mu.Unlock()

		if len(toCancel) > 0 {
			cancelOrdersBatch(m, toCancel)
			countOrders += len(toCancel)
			countTxs++
		}
	}
	log.Printf("Глобальная отмена: %d ордеров упаковано в %d транзакций", countOrders, countTxs)
}

func appendTx(tx []byte) {
	if tx == nil {
		return
	}
	muTxs.Lock()
	allTxs = append(allTxs, tx)
	atomic.AddUint64(&txCreated, 1)
	muTxs.Unlock()
}

func saveAllTxs() {
	muTxs.Lock()
	defer muTxs.Unlock()

	f, err := os.Create(outputFile)
	if err != nil {
		log.Printf("Ошибка сохранения: %v", err)
		return
	}
	defer f.Close()

	var totalBytes int64
	for _, b := range allTxs {
		n, err := f.Write(b)
		if err != nil {
			log.Printf("Ошибка записи транзакции: %v", err)
			break
		}
		totalBytes += int64(n)
	}

	count := len(allTxs)
	var avgSize float64
	if count > 0 {
		avgSize = float64(totalBytes) / float64(count)
	}

	totalCreated := atomic.LoadUint64(&txCreated)
	
	// Выводим размер в МБ и средний размер одной транзакции
	log.Printf("Файл %s сохранен. Всего записано: %d (создано всего: %d). Общий объем: %.2f MB. Средний размер tx: %.1f байт", 
		outputFile, count, totalCreated, float64(totalBytes)/1024.0/1024.0, avgSize)
}

func createHeader(opCode tx.OpCode, marketSymbol uint32) *tx.TransactionHeader {
	newNonce := atomic.AddUint64(&nonce, 1)
	
	// Текущий таймстемп
	now := uint64(time.Now().Unix())
	
	return &tx.TransactionHeader{
		ChainType:	  tx.ChainType_LOCALNET,
		ChainVersion: 1,
		OpCode:       opCode,
		AuthType:     tx.TxAuthType_UID,
		SignerUid:    signerUID,
		Nonce:        newNonce,
		MarketCode:   tx.Markets_PERPETUAL,
		MarketSymbol: marketSymbol,
		MinHeight:    now - 5, // now - 5
		MaxHeight:    now + 5, // now + 5
		Signature:    make([]byte, 64),
	}
}

func buildTx(header *tx.TransactionHeader, payload proto.Message) []byte {
	txx := &tx.Transaction{
		HeaderData: &tx.Transaction_Header{
			Header: header,
		},
	}

	switch p := payload.(type) {
	case *tx.OrderCreatePayload:
		txx.Payload = &tx.Transaction_OrderCreate{OrderCreate: p}
	case *tx.OrderAmendPayload:
		txx.Payload = &tx.Transaction_OrderAmend{OrderAmend: p}
	case *tx.OrderCancelPayload:
		txx.Payload = &tx.Transaction_OrderCancel{OrderCancel: p}
	case *tx.MetaNoopPayload:
		txx.Payload = &tx.Transaction_MetaNoop{MetaNoop: p}
	default:
		log.Printf("Unknown payload for opcode: %d", header.OpCode)
		return nil
	}

	temp, _ := proto.Marshal(txx)
	sig := ed25519.Sign(privKey, temp)
	header.Signature = sig

	final, _ := proto.Marshal(txx)
	return final
}

func generateUserGeneratedID() []byte {
	b := make([]byte, 16)
	rand.Read(b)
	return b
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}