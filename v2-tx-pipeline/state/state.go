// state/state.go
package state

import (
	"bytes"
	"context"
	"encoding/binary"
	"sort"
	"sync"
	"time"

	"github.com/cockroachdb/pebble"
	blake3 "github.com/zeebo/blake3"
)

// ──────────────────────────────────────────────────────────────────────────────
// Константы и префиксы
// ──────────────────────────────────────────────────────────────────────────────

const (
	PrefixAccount    byte = 0x01
	PrefixBalance    byte = 0x02
	PrefixPosition   byte = 0x03
	PrefixOrder      byte = 0x10
	PrefixOrderBook  byte = 0x20
	PrefixTrade      byte = 0x30
	PrefixMetadata   byte = 0xFF

	SyncInterval = 3 * time.Second
	BatchSize    = 8192
)

var (
	RootKey = []byte{PrefixMetadata, 0x01}
)

// ──────────────────────────────────────────────────────────────────────────────
// Типы сущностей (заглушки — замените на реальные структуры + Marshal/Unmarshal)
// ──────────────────────────────────────────────────────────────────────────────

type Account struct {
	ID    uint64
	Name  string // пример
	// ...
}

func (a *Account) Marshal() []byte {
	// Здесь должен быть protobuf / msgpack / custom binary
	// Пока возвращаем заглушку
	return []byte("account:" + a.Name)
}

type Balance struct {
	AccountID uint64
	AssetID   uint32
	Amount    uint64
}

func (b *Balance) Marshal() []byte {
	return []byte("balance")
}

type Position struct {
	AccountID    uint64
	InstrumentID uint32
	Size         int64
	// ...
}

func (p *Position) Marshal() []byte {
	return []byte("position")
}

type Order struct {
	ID           uint64
	InstrumentID uint32
	Side         uint8 // 0 = buy, 1 = sell
	Price        uint64
	Qty          uint64
	// ...
}

func (o *Order) Marshal() []byte {
	return []byte("order")
}

type OrderBookState struct {
	InstrumentID uint32
	// bids, asks, levels, etc.
}

func (ob *OrderBookState) Marshal() []byte {
	return []byte("orderbook")
}

// ──────────────────────────────────────────────────────────────────────────────
// Композитные ключи для мап
// ──────────────────────────────────────────────────────────────────────────────

type balanceKey struct {
	AccountID uint64
	AssetID   uint32
}

type positionKey struct {
	AccountID    uint64
	InstrumentID uint32
}

// ──────────────────────────────────────────────────────────────────────────────
// Функции формирования ключей для Pebble
// ──────────────────────────────────────────────────────────────────────────────

func accountKey(id uint64) []byte {
	b := make([]byte, 1+8)
	b[0] = PrefixAccount
	binary.BigEndian.PutUint64(b[1:], id)
	return b
}

func balanceKeyBytes(k balanceKey) []byte {
	b := make([]byte, 1+8+4)
	b[0] = PrefixBalance
	binary.BigEndian.PutUint64(b[1:], k.AccountID)
	binary.BigEndian.PutUint32(b[9:], k.AssetID)
	return b
}

func positionKeyBytes(k positionKey) []byte {
	b := make([]byte, 1+8+4)
	b[0] = PrefixPosition
	binary.BigEndian.PutUint64(b[1:], k.AccountID)
	binary.BigEndian.PutUint32(b[9:], k.InstrumentID)
	return b
}

func orderKey(id uint64) []byte {
	b := make([]byte, 1+8)
	b[0] = PrefixOrder
	binary.BigEndian.PutUint64(b[1:], id)
	return b
}

func orderBookKey(instrumentID uint32) []byte {
	b := make([]byte, 1+4)
	b[0] = PrefixOrderBook
	binary.BigEndian.PutUint32(b[1:], instrumentID)
	return b
}

func tradeKey(id uint64) []byte {
	b := make([]byte, 1+8)
	b[0] = PrefixTrade
	binary.BigEndian.PutUint64(b[1:], id)
	return b
}

// ──────────────────────────────────────────────────────────────────────────────
// Основная структура состояния
// ──────────────────────────────────────────────────────────────────────────────

type State struct {
	mu sync.RWMutex

	accounts        map[uint64]*Account
	balances        map[balanceKey]*Balance
	positions       map[positionKey]*Position
	orders          map[uint64]*Order
	orderBooks      map[uint32]*OrderBookState
	// tradeHistory можно сделать ring-buffer или отдельным файлом позже

	dirtyAccounts   map[uint64]struct{}
	dirtyBalances   map[balanceKey]struct{}
	dirtyPositions  map[positionKey]struct{}
	dirtyOrders     map[uint64]struct{}
	dirtyOrderBooks map[uint32]struct{}
	// dirtyTrades     map[uint64]struct{}   // пока опущено для простоты

	db          *pebble.DB
	currentRoot [32]byte
	version     uint64

	writeCh chan writeTask

	ctx    context.Context
	cancel context.CancelFunc
}

type writeTask struct {
	key   []byte
	value []byte // nil = delete
}

// ──────────────────────────────────────────────────────────────────────────────
// NewState — создание + загрузка из диска
// ──────────────────────────────────────────────────────────────────────────────

func NewState(dbPath string) (*State, error) {
	opts := &pebble.Options{
		MemTableSize:               128 << 20,
		MemTableStopWritesThreshold: 4,
		L0CompactionThreshold:      4,
		LBaseMaxBytes:              512 << 20,
	}

	db, err := pebble.Open(dbPath, opts)
	if err != nil {
		return nil, err
	}

	s := &State{
		accounts:        make(map[uint64]*Account, 100_000),
		balances:        make(map[balanceKey]*Balance, 1_000_000),
		positions:       make(map[positionKey]*Position, 500_000),
		orders:          make(map[uint64]*Order, 10_000_000),
		orderBooks:      make(map[uint32]*OrderBookState, 200),

		dirtyAccounts:   make(map[uint64]struct{}, 32*1024),
		dirtyBalances:   make(map[balanceKey]struct{}, 64*1024),
		dirtyPositions:  make(map[positionKey]struct{}, 32*1024),
		dirtyOrders:     make(map[uint64]struct{}, 256*1024),
		dirtyOrderBooks: make(map[uint32]struct{}, 1024),

		db:      db,
		writeCh: make(chan writeTask, 256*1024),
	}

	s.ctx, s.cancel = context.WithCancel(context.Background())
	go s.backgroundWriter()
	go s.backgroundRootCalculator()

	// Загрузка всего состояния из Pebble
	iter, err := db.NewIter(nil)
	if err != nil {
		return nil, err
	}
	defer iter.Close()

	for iter.First(); iter.Valid(); iter.Next() {
		key := iter.Key()
		if len(key) == 0 {
			continue
		}
		prefix := key[0]
		value := iter.Value()

		switch prefix {
		case PrefixAccount:
			id := binary.BigEndian.Uint64(key[1:])
			s.accounts[id] = &Account{ID: id} // заглушка
		case PrefixOrder:
			id := binary.BigEndian.Uint64(key[1:])
			s.orders[id] = &Order{ID: id} // заглушка
		// остальные типы — аналогично
		case PrefixMetadata:
			if bytes.Equal(key, RootKey) {
				copy(s.currentRoot[:], value)
			}
		}
	}

	return s, nil
}

// ──────────────────────────────────────────────────────────────────────────────
// Примеры методов обновления
// ──────────────────────────────────────────────────────────────────────────────

func (s *State) SetOrder(ord *Order) {
	s.mu.Lock()
	s.orders[ord.ID] = ord
	s.dirtyOrders[ord.ID] = struct{}{}
	s.mu.Unlock()

	go func() {
		s.writeCh <- writeTask{key: orderKey(ord.ID), value: ord.Marshal()}
	}()
}

func (s *State) CancelOrder(id uint64) {
	s.mu.Lock()
	delete(s.orders, id)
	s.dirtyOrders[id] = struct{}{}
	s.mu.Unlock()

	go func() {
		s.writeCh <- writeTask{key: orderKey(id), value: nil}
	}()
}

// ──────────────────────────────────────────────────────────────────────────────
// Фоновая запись в Pebble (batched)
// ──────────────────────────────────────────────────────────────────────────────

func (s *State) backgroundWriter() {
	ticker := time.NewTicker(250 * time.Millisecond)
	defer ticker.Stop()

	batch := s.db.NewBatch()
	count := 0

	for {
		select {
		case <-s.ctx.Done():
			return
		case task := <-s.writeCh:
			if task.value == nil {
				batch.Delete(task.key)
			} else {
				batch.Set(task.key, task.value)
			}
			count++
			if count >= BatchSize {
				_ = batch.Commit(pebble.NoSync)
				batch.Reset()
				count = 0
			}
		case <-ticker.C:
			if count > 0 {
				_ = batch.Commit(pebble.Sync)
				batch.Reset()
				count = 0
			}
		}
	}
}

// ──────────────────────────────────────────────────────────────────────────────
// Периодический расчёт Merkle root по dirty-данным
// ──────────────────────────────────────────────────────────────────────────────

func (s *State) backgroundRootCalculator() {
	ticker := time.NewTicker(SyncInterval)
	defer ticker.Stop()

	for {
		select {
		case <-s.ctx.Done():
			return
		case <-ticker.C:
			s.computeAndSaveRoot()
		}
	}
}

func (s *State) computeAndSaveRoot() {
	s.mu.RLock()

	type kv struct {
		key   []byte
		value []byte // nil = tombstone / deleted
	}

	var entries []kv

	// accounts
	for id := range s.dirtyAccounts {
		if acc, ok := s.accounts[id]; ok {
			entries = append(entries, kv{accountKey(id), acc.Marshal()})
		} else {
			entries = append(entries, kv{accountKey(id), nil})
		}
	}

	// orders
	for id := range s.dirtyOrders {
		if o, ok := s.orders[id]; ok {
			entries = append(entries, kv{orderKey(id), o.Marshal()})
		} else {
			entries = append(entries, kv{orderKey(id), nil})
		}
	}

	// balances, positions, orderBooks — добавьте аналогично по мере необходимости

	s.mu.RUnlock()

	if len(entries) == 0 {
		return
	}

	// Детерминированная сортировка по ключу
	sort.Slice(entries, func(i, j int) bool {
		return bytes.Compare(entries[i].key, entries[j].key) < 0
	})

	hasher := blake3.New(32, nil)

	for _, e := range entries {
		hasher.Write(e.key)
		if e.value != nil {
			hasher.Write(e.value)
		} else {
			hasher.Write([]byte{0xDE, 0xAD}) // tombstone marker
		}
	}

	newRoot := hasher.Sum(nil)

	s.mu.Lock()
	copy(s.currentRoot[:], newRoot)
	s.version++

	// Очистка dirty-сетов с сохранением capacity
	clear(s.dirtyAccounts)
	clear(s.dirtyOrders)
	// clear для остальных по мере добавления

	s.mu.Unlock()

	_ = s.db.Set(RootKey, newRoot, pebble.Sync)
}

// GetRoot — для ABCI / проверки
func (s *State) GetRoot() [32]byte {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.currentRoot
}

// Close — graceful shutdown
func (s *State) Close() error {
	s.cancel()
	<-time.After(500 * time.Millisecond) // дать шанс фоновым горутинам
	return s.db.Close()
}