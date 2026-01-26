package models

import (
	"encoding/binary"
	"github.com/zeebo/blake3"
)

// OrderSide сторона ордера
type OrderSide uint8

const (
	Buy OrderSide = iota
	Sell
)

// OrderType тип ордера
type OrderType uint8

const (
	Limit OrderType = iota
	Market
	StopLimit
	StopMarket
)

// Order представляет торговый ордер
type Order struct {
	OrderID   uint64    // Уникальный ID ордера
	UserID    uint64    // ID пользователя
	Symbol    uint32    // ID торговой пары (BTC/USD = 1, ETH/USD = 2, ...)
	Price     uint64    // Цена (в микро-единицах, price * 1e6)
	Quantity  uint64    // Количество
	Filled    uint64    // Заполнено
	Side      OrderSide // Buy/Sell
	Type      OrderType // Limit/Market/...
	Timestamp int64     // Unix timestamp
	key       [8]byte   // Кешированный ключ
	_         [3]byte   // Padding
}

// ID реализует интерфейс Hashable
func (o *Order) ID() uint64 {
	return o.OrderID
}

// Key реализует интерфейс Hashable
func (o *Order) Key() [8]byte {
	return o.key
}

// Hash реализует интерфейс Hashable
func (o *Order) Hash() [32]byte {
	hasher := blake3.New()
	hasher.Write(o.key[:])
	binary.Write(hasher, binary.BigEndian, o.UserID)
	binary.Write(hasher, binary.BigEndian, o.Symbol)
	binary.Write(hasher, binary.BigEndian, o.Price)
	binary.Write(hasher, binary.BigEndian, o.Quantity)
	binary.Write(hasher, binary.BigEndian, o.Filled)
	hasher.Write([]byte{byte(o.Side), byte(o.Type)})
	binary.Write(hasher, binary.BigEndian, o.Timestamp)
	var result [32]byte
	copy(result[:], hasher.Sum(nil))
	return result
}

// NewOrder создает новый ордер
func NewOrder(orderID, userID uint64, symbol uint32, price, quantity uint64, side OrderSide, typ OrderType) *Order {
	order := &Order{
		OrderID:   orderID,
		UserID:    userID,
		Symbol:    symbol,
		Price:     price,
		Quantity:  quantity,
		Filled:    0,
		Side:      side,
		Type:      typ,
		Timestamp: 0, // можно time.Now().Unix()
	}
	binary.BigEndian.PutUint64(order.key[:], orderID)
	return order
}

// IsFilled проверяет, полностью ли заполнен ордер
func (o *Order) IsFilled() bool {
	return o.Filled >= o.Quantity
}

// RemainingQuantity возвращает оставшееся количество
func (o *Order) RemainingQuantity() uint64 {
	if o.Filled >= o.Quantity {
		return 0
	}
	return o.Quantity - o.Filled
}
