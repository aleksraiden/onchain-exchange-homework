// mm-bybit-mirror.go
package main

import (
	"bytes"
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

	"tx-generator/tx"

	"github.com/gorilla/websocket"
	"google.golang.org/protobuf/proto"
)

const (
	bybitWSURL      = "wss://stream.bybit.com/v5/public/linear"
	symbol          = "BTCUSDT"
	orderbookDepth  = 50
	ordersPerLevel  = 10
	baseQtyPerOrder = 0.001 // BTC
	priceTickSize   = 0.1
	outputFile      = "mm-txs.bin"

	priceDecimals   = 2 // BTCUSDT-PERP → 2 знака
	priceMultiplier = 100
	qtyDecimals     = 8 // BTC → 8 знаков
	qtyMultiplier   = 100_000_000
)

var lastRawMessage []byte
var lastRawMu sync.Mutex

type Level struct {
	Price float64
	Qty   float64
}

type OrderBook struct {
	Bids []Level
	Asks []Level
	mu   sync.RWMutex
}

type ManagedOrder struct {
	OrderID  []byte // 16 байт user_generated_id
	Side     bool   // true = buy
	Price    float64
	Quantity float64
	InTop10  bool // флаг, находится ли сейчас в топ-10
}

var (
	ob            = &OrderBook{}
	managedOrders = make(map[string]*ManagedOrder) // key = user_generated_id hex
	uidCounter    uint64
	nonce         uint64
	txCreated     uint64 // общее количество созданных транзакций
	privKey       ed25519.PrivateKey
	signerUID     uint64 = 123456789 // тестовый UID
	allTxs        [][]byte
	muTxs         sync.Mutex

	ctx, cancel = context.WithCancel(context.Background())
)

func init() {
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		log.Fatal(err)
	}
	privKey = priv

	// Graceful shutdown
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-sigChan
		log.Println("Получен сигнал завершения. Отмена всех ордеров и сохранение tx...")
		cancelAllOrders()
		saveAllTxs()
		os.Exit(0)
	}()
}

func main() {
	log.Printf("Зеркальный MM Bybit → %s (top%d, по %d ордеров на уровень)", symbol, orderbookDepth, ordersPerLevel)

	ws, _, err := websocket.DefaultDialer.Dial(bybitWSURL, nil)
	if err != nil {
		log.Fatal("dial:", err)
	}

	// Запускаем обработку в фоне
	go func() {
		<-ctx.Done()
		ws.Close()
	}()

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

func subscribe(ws *websocket.Conn) {
	sub := map[string]interface{}{
		"op":   "subscribe",
		"args": []string{fmt.Sprintf("orderbook.%d.%s", orderbookDepth, symbol)},
	}
	if err := ws.WriteJSON(sub); err != nil {
		log.Fatal("subscribe:", err)
	}
	log.Println("Подписка отправлена")
}

func processMessage(raw []byte) {
	lastRawMu.Lock()
	lastRawMessage = make([]byte, len(raw))
	copy(lastRawMessage, raw)
	lastRawMu.Unlock()

	var msg struct {
		Topic string          `json:"topic"`
		Type  string          `json:"type"`
		Data  json.RawMessage `json:"data"`
	}

	if err := json.Unmarshal(raw, &msg); err != nil {
		return
	}

	if strings.HasPrefix(msg.Topic, "orderbook") {
		updateOrderBook(msg.Data)
	}
}

func updateOrderBook(data json.RawMessage) {
	var update struct {
		Symbol string     `json:"s"`
		B      [][]string `json:"b"` // bids
		A      [][]string `json:"a"` // asks
		U      int64      `json:"u"`
	}

	if err := json.Unmarshal(data, &update); err != nil {
		return
	}

	ob.mu.Lock()

	if len(update.B) > 0 {
		ob.Bids = convertLevels(update.B)
	}

	if len(update.A) > 0 {
		ob.Asks = convertLevels(update.A)
	}
	ob.mu.Unlock()

	generateMirrorOrders()

	//вывод топ-1
	logTop1Prices()

}

// Новая функция для вывода топ-1
func logTop1Prices() {
	ob.mu.RLock()
	bidsEmpty := len(ob.Bids) == 0
	asksEmpty := len(ob.Asks) == 0
	ob.mu.RUnlock()

	timestamp := time.Now().Format("15:04:05.000")
	txCount := atomic.LoadUint64(&txCreated)

	if bidsEmpty && asksEmpty {
		// Стакан пустой → логируем полный последний JSON для отладки
		lastRawMu.Lock()
		rawCopy := make([]byte, len(lastRawMessage))
		copy(rawCopy, lastRawMessage)
		lastRawMu.Unlock()

		var pretty bytes.Buffer
		if err := json.Indent(&pretty, rawCopy, "", "  "); err == nil {
			log.Printf("\x1b[31m[%s] ПУСТОЙ СТАКАН! TX: %d | Последний JSON:\x1b[0m\n%s\n",
				timestamp, txCount, pretty.String())
		} else {
			log.Printf("\x1b[31m[%s] ПУСТОЙ СТАКАН! TX: %d | Сырой (невалидный JSON):\x1b[0m %s\n",
				timestamp, txCount, string(rawCopy))
		}
	} else {
		// Нормальный стакан → обычный красивый лог
		ob.mu.RLock()
		bestBid := ob.Bids[0]
		bestAsk := ob.Asks[0]
		ob.mu.RUnlock()

		spread := bestAsk.Price - bestBid.Price

		log.Printf("\x1b[32m[%s]\x1b[0m Top-1: \x1b[34mBid %10.2f\x1b[0m (qty %.6f) | \x1b[31mAsk %10.2f\x1b[0m (qty %.6f) | Spread %.2f | TX: %d",
			timestamp,
			bestBid.Price, bestBid.Qty,
			bestAsk.Price, bestAsk.Qty,
			spread,
			txCount,
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

func generateMirrorOrders() {
	ob.mu.RLock()
	bids := append([]Level{}, ob.Bids...)
	asks := append([]Level{}, ob.Asks...)
	ob.mu.RUnlock()

	sort.Slice(bids, func(i, j int) bool { return bids[i].Price > bids[j].Price })
	sort.Slice(asks, func(i, j int) bool { return asks[i].Price < asks[j].Price })

	// Текущие активные уровни (bid/ask + price)
	currentLevels := make(map[string]bool)
	for i := 0; i < min(orderbookDepth, len(bids)); i++ {
		l := bids[i]
		currentLevels[fmt.Sprintf("buy-%.8f", l.Price)] = true
		createOrAmendOrders(true, l.Price, l.Qty)
	}
	for i := 0; i < min(orderbookDepth, len(asks)); i++ {
		l := asks[i]
		currentLevels[fmt.Sprintf("sell-%.8f", l.Price)] = true
		createOrAmendOrders(false, l.Price, l.Qty)
	}

	// Отмена ордеров, которые вышли из топ-10
	cancelOutdatedOrders(currentLevels)
}

func createOrAmendOrders(isBuy bool, price, totalQty float64) {
	const minQtyToSplit = 1.0 // BTC

	var qtyPerOrder float64
	var numOrders int

	if totalQty < minQtyToSplit {
		// Если меньше 1 BTC — ставим одним ордером
		qtyPerOrder = totalQty
		numOrders = 1
	} else {
		// Делим на ordersPerLevel частей
		qtyPerOrder = totalQty / float64(ordersPerLevel)
		numOrders = ordersPerLevel
	}

	if qtyPerOrder < 0.0001 { // минимальный фильтр Bybit
		return
	}

	for i := 0; i < numOrders; i++ {
		// Небольшое рандомное смещение цены внутри тика (чтобы не пересекаться)
		adjPrice := price
		if i%2 == 0 {
			adjPrice += priceTickSize * float64(i) / 10
		} else {
			adjPrice -= priceTickSize * float64(i) / 10
		}

		// Ключ для хранения ордера (теперь без i, если numOrders == 1)
		key := fmt.Sprintf("%t-%.8f", isBuy, adjPrice)
		if numOrders > 1 {
			key += fmt.Sprintf("-%d", i)
		}

		uid := generateUserGeneratedID()

		if existing, ok := managedOrders[key]; ok {
			amendOrder(existing, adjPrice, qtyPerOrder)
		} else {
			createNewOrder(uid, isBuy, adjPrice, qtyPerOrder)
			managedOrders[key] = &ManagedOrder{
				OrderID:  uid,
				Side:     isBuy,
				Price:    adjPrice,
				Quantity: qtyPerOrder,
				InTop10:  true,
			}
		}
	}
}

func cancelOutdatedOrders(current map[string]bool) {
	for key, order := range managedOrders {
		if !order.InTop10 {
			continue
		}
		priceKey := fmt.Sprintf("%t-%.8f", order.Side, order.Price)
		if !current[priceKey] {
			cancelOrder(order)
			order.InTop10 = false
			delete(managedOrders, key)
		}
	}
}

func createNewOrder(uid []byte, isBuy bool, price, qty float64) {
	p := &tx.OrderCreatePayload{
		IsBuy:           isBuy,
		IsMarket:        false,
		Quantity:        uint64(qty * qtyMultiplier),
		Price:           uint64(price * priceMultiplier),
		Flags:           0,
		UserGeneratedId: uid,
	}

	txx := buildTx(createHeader(0x60), p)
	appendTx(txx)
}

func amendOrder(order *ManagedOrder, newPrice, newQty float64) {
	// Конвертируем float64 → uint64
	priceUint := uint64(math.Round(newPrice * priceMultiplier)) // 2 знака после точки
	qtyUint := uint64(math.Round(newQty * qtyMultiplier))       // 8 знаков, как для BTC

	p := &tx.OrderAmendPayload{
		IdType: &tx.OrderAmendPayload_UserGeneratedId{
			UserGeneratedId: order.OrderID,
		},
		NewPrice:    &priceUint,
		NewQuantity: &qtyUint,
	}

	txx := buildTx(createHeader(0x6B), p)
	appendTx(txx)

	order.Price = newPrice
	order.Quantity = newQty
}

func cancelOrder(order *ManagedOrder) {
	p := &tx.OrderCancelPayload{
		IdType: &tx.OrderCancelPayload_UserGeneratedId{
			UserGeneratedId: order.OrderID,
		},
	}

	txx := buildTx(createHeader(0x64), p)
	appendTx(txx)
}

func cancelAllOrders() {
	for _, order := range managedOrders {
		cancelOrder(order)
	}
	log.Printf("Отменено %d ордеров", len(managedOrders))
}

func appendTx(tx []byte) {
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

	for _, b := range allTxs {
		f.Write(b)
	}

	totalCreated := atomic.LoadUint64(&txCreated)
	log.Printf("Сохранено %d транзакций (создано всего %d) в %s", len(allTxs), totalCreated, outputFile)
}

func createHeader(opCode uint32) *tx.TransactionHeader {
	nonce++
	return &tx.TransactionHeader{
		ChainVersion: 0x01000001,
		OpCode:       opCode,
		AuthType:     0,
		SignerUid:    signerUID,
		Nonce:        nonce,
		MarketCode:   0x00000001,
		MinHeight:    0,
		MaxHeight:    0,
		Signature:    make([]byte, 64),
	}
}

func buildTx(header *tx.TransactionHeader, payload proto.Message) []byte {
	payloadBytes, _ := proto.Marshal(payload)
	header.PayloadSize = uint32(len(payloadBytes))

	txx := &tx.Transaction{Header: header}
	// Здесь нужно правильно заполнить oneof (как в твоём основном коде)
	// Для примера используем switch по opCode
	switch header.OpCode {
	case 0x60:
		txx.Payload = &tx.Transaction_OrderCreate{OrderCreate: payload.(*tx.OrderCreatePayload)}
	case 0x6B:
		txx.Payload = &tx.Transaction_OrderAmend{OrderAmend: payload.(*tx.OrderAmendPayload)}
	case 0x64:
		txx.Payload = &tx.Transaction_OrderCancel{OrderCancel: payload.(*tx.OrderCancelPayload)}
	default:
		log.Printf("Неизвестный opCode: %d", header.OpCode)
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
