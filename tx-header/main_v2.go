// main.go
package main

import (
	"crypto/ed25519"
	voied25519 "github.com/oasisprotocol/curve25519-voi/primitives/ed25519"
	"crypto/rand"
	"fmt"
	mrand "math/rand"
	"os"
	"runtime"
	"time"
	"sync"
	"sync/atomic"
	
	"encoding/binary"

	"github.com/google/uuid"
	"github.com/zeebo/blake3" // Подключаем BLAKE3
	"google.golang.org/protobuf/proto"
	
	tx "tx-generator/tx"
)

// User структура для эмуляции БД пользователей
type User struct {
	uid   uint64
	priv  ed25519.PrivateKey
	pub   ed25519.PublicKey // Храним публичный ключ для проверки
	expKey *voied25519.ExpandedPublicKey // Для супер-быстрой проверки (1.5 КБ)
	nonce uint64
}

// ----------------------------------------------------------------
// BLOOM FILTER (Zero Alloc, Bitwise)
// ----------------------------------------------------------------

const BloomSize = 1024 * 1024 * 8 // 8 MB (влезает в L3 кеш современных CPU)

type FastBloom struct {
	data [BloomSize]uint64 // Битовое поле
}

// Простейший хеш для UUIDv7 (берем энтропию из конца)
func bloomHash(id []byte) (uint64, uint64) {
	// UUIDv7: последние 8 байт - это чистый рандом (var + rand_b)
	// Используем их как хеш. Это супер-быстро (1 mov инструкция)
	h1 := binary.LittleEndian.Uint64(id[8:])
	// Для второго хеша возьмем смещение (или просто h1 + const)
	h2 := h1 * 0x9e3779b97f4a7c15 // Fibonacci hashing mixer
	return h1, h2
}

func (b *FastBloom) Add(id []byte) {
	h1, h2 := bloomHash(id)
	// Ставим 2 бита (можно 3 для точности)
	idx1 := h1 & (BloomSize*64 - 1)
	idx2 := h2 & (BloomSize*64 - 1)
	
	// Atomic OR (Lock-free write)
	// data[word] |= bit
	atomic.OrUint64(&b.data[idx1/64], 1<<(idx1%64))
	atomic.OrUint64(&b.data[idx2/64], 1<<(idx2%64))
}

// MayContain возвращает false, если элемента ТОЧНО нет.
func (b *FastBloom) MayContain(id []byte) bool {
	h1, h2 := bloomHash(id)
	idx1 := h1 & (BloomSize*64 - 1)
	idx2 := h2 & (BloomSize*64 - 1)

	// Atomic Load (Lock-free read)
	val1 := atomic.LoadUint64(&b.data[idx1/64])
	if val1&(1<<(idx1%64)) == 0 { return false }

	val2 := atomic.LoadUint64(&b.data[idx2/64])
	if val2&(1<<(idx2%64)) == 0 { return false }

	return true // Возможно есть (надо проверить в Map)
}

func (b *FastBloom) Clear() {
	// Просто зануляем память. Это быстро (memset).
	// В Go это делается присваиванием новой пустой структуры или циклом.
	// Для массива это безопасно делать даже под нагрузкой (просто будут ложно-отрицательные, что ок).
	for i := range b.data {
		atomic.StoreUint64(&b.data[i], 0)
	}
}

var (
	// Флаги состояния
	AdaptiveBloomEnabled atomic.Bool
	DuplicateCounter     atomic.Int64 // Считает кол-во дублей за интервал
)

// Конфигурация порогов
const (
	DDoSThresholdStart = 1000 // Если дублей > 1000 в сек -> ВКЛЮЧИТЬ защиту
	DDoSThresholdStop  = 100  // Если дублей < 100 в сек -> ВЫКЛЮЧИТЬ защиту
)

// Фоновый монитор
func startDDoSMonitor(bloom *FastBloom) {
	go func() {
		ticker := time.NewTicker(1 * time.Second)
		defer ticker.Stop()

		for range ticker.C {
			// 1. Снимаем показания и сбрасываем счетчик
			dups := DuplicateCounter.Swap(0)
			
			isEnabled := AdaptiveBloomEnabled.Load()

			if !isEnabled && dups > DDoSThresholdStart {
				// --- НАЧАЛО АТАКИ ---
				fmt.Printf("⚠️ DDoS DETECTED (%d dups/sec). Activating Bloom Shield!\n", dups)
				// Очищаем Блум перед использованием, чтобы он был свежим
				bloom.Clear() 
				AdaptiveBloomEnabled.Store(true)
				
			} else if isEnabled && dups < DDoSThresholdStop {
				// --- КОНЕЦ АТАКИ ---
				fmt.Println("✅ Attack subsided. Disabling Bloom Shield.")
				AdaptiveBloomEnabled.Store(false)
			}
		}
	}()
}

// ----------------------------------------------------------------
// UUIDv7 VALIDATOR & SHARDED CACHE
// ----------------------------------------------------------------

// Константы 
const (
	CacheShards = 256 // 256 шардов (по байту), степень двойки
	
	// Допустимое отклонение времени в миллисекундах (например, +/- 30 секунд)
	// Для теста ставим побольше, так как мы генерируем данные заранее.
	// В проде здесь будет 1000-5000 мс.
	MaxTimeDeviationMS = 60000 
	
	//Для нового формата блока с транзакциями 
	BlockMagicNumber = 0xBA	//Заголовок нашего формата
	SigSize          = 64
	
	TxFrameSeparator = 0xEE	//Разделитель протобафов 
)

// ShardedCache - потокобезопасный кеш для проверки уникальности
type ShardedCache struct {
	shards [CacheShards]*cacheShard
}

type cacheShard struct {
	sync.RWMutex
	// Используем массив [16]byte как ключ (Go умеет это делать без аллокаций в map)
	items map[[16]byte]struct{}
}

// FastTimeKeeper - позволяет читать время без syscall в горячем цикле
var globalTime atomic.Int64

// Запускаем это в main() один раз
func startFastTimeKeeper() {
	// Инициализация
	globalTime.Store(time.Now().UnixMilli())
	
	go func() {
		ticker := time.NewTicker(10 * time.Millisecond) // Обновляем раз в 10мс (достаточно для UUIDv7)
		defer ticker.Stop()
		for t := range ticker.C {
			globalTime.Store(t.UnixMilli())
		}
	}()
}

func NewShardedCache(prefillCount int) *ShardedCache {
	sc := &ShardedCache{}
	for i := 0; i < CacheShards; i++ {
		sc.shards[i] = &cacheShard{
			items: make(map[[16]byte]struct{}),
		}
	}

	// Предзаполнение случайными UUIDv7
	if prefillCount > 0 {
		fmt.Printf("Предзаполнение кеша (%d UUIDs)...\n", prefillCount)
		for i := 0; i < prefillCount; i++ {
			u, _ := uuid.NewV7()
			// Имитируем добавление
			sc.Seen(u[:])
		}
	}
	return sc
}

// Seen проверяет наличие и добавляет, если нет.
// Возвращает true, если элемент УЖЕ БЫЛ (дубликат).
func (sc *ShardedCache) Seen(id []byte) bool {
	if len(id) != 16 {
		return false // Некорректная длина - не считаем дублем (валидатор отловит)
	}

	// ВАЖНО: UUIDv7 упорядочен по времени (начало).
	// Чтобы размазать нагрузку по шардам, берем ПОСЛЕДНИЙ байт (там рандом).
	shardIdx := id[15] 
	shard := sc.shards[shardIdx]

	// Преобразуем слайс в массив для ключа мапы
	var key [16]byte
	copy(key[:], id)

	// 1. Быстрая проверка (Read Lock)
	shard.RLock()
	_, exists := shard.items[key]
	shard.RUnlock()
	if exists {
		return true
	}

	// 2. Запись (Write Lock)
	shard.Lock()
	// Double check внутри блокировки
	if _, exists := shard.items[key]; exists {
		shard.Unlock()
		return true
	}
	shard.items[key] = struct{}{}
	shard.Unlock()
	return false
}

// Put - принудительная вставка без проверки (используется, когда Bloom сказал "Нет")
// Экономит RLock/RUnlock
func (sc *ShardedCache) Put(id []byte) {
	shardIdx := id[15]
	shard := sc.shards[shardIdx]

	var key [16]byte
	copy(key[:], id)

	shard.Lock()
	shard.items[key] = struct{}{}
	shard.Unlock()
}

// Быстрый хеш для UUID (просто берем кусок байт, так как UUIDv7 уже рандомный в конце)
// Inline candidate
func fastHash(id []byte) uint32 {
    // Берем последние 4 байта UUID (там максимальная энтропия)
    // id[12], id[13], id[14], id[15]
    return binary.LittleEndian.Uint32(id[12:])
}

// IsValidUUIDv7 проверяет формат И время
func IsValidUUIDv7(id []byte) bool {
	if len(id) != 16 {
		return false
	}

	// 1. Проверка версии (7) и варианта (2) - битовые маски
	// octet 6: 0111xxxx -> high nibble == 7
	if (id[6] >> 4) != 7 {
		return false
	}
	// octet 8: 10xxxxxx -> high 2 bits == 2 (binary 10)
	if (id[8] >> 6) != 2 {
		return false
	}

	// 2. ИЗВЛЕЧЕНИЕ ВРЕМЕНИ (Big Endian 48 bit)
	// UUIDv7: 0-5 байты = Unix Timestamp (ms)
	ts := uint64(id[0])<<40 | uint64(id[1])<<32 | uint64(id[2])<<24 |
		uint64(id[3])<<16 | uint64(id[4])<<8 | uint64(id[5])

	// 3. ПРОВЕРКА ВРЕМЕНИ (Zero allocation, atomic read)
	now := uint64(globalTime.Load())
	
	// Вычисляем дельту (избегаем переполнения uint)
	var diff uint64
	if ts > now {
		diff = ts - now // Время из будущего
	} else {
		diff = now - ts // Время из прошлого
	}

	return diff <= MaxTimeDeviationMS
}





func main() {
	
	mrand.Seed(time.Now().UnixNano())
	
	startFastTimeKeeper()

	// OpCodes:
	txCounts := map[tx.OpCode]int{
		tx.OpCode_META_NOOP:           100,
		tx.OpCode_META_RESERVE:        50,
		tx.OpCode_ORD_CREATE:          15_000,
		tx.OpCode_ORD_CANCEL:          10_000,
		tx.OpCode_ORD_CANCEL_ALL:      1000,
		tx.OpCode_ORD_CANCEL_REPLACE:  3_000,
		tx.OpCode_ORD_AMEND:           25_000,
	}

	// 1. Генерируем 10,000 пользователей
	fmt.Println("Генерация 10,000 пользователей...")
	users := make([]*User, 0, 10000)
	for i := 1; i <= 10000; i++ {
		pub, priv, err := ed25519.GenerateKey(rand.Reader)
		if err != nil {
			panic(err)
		}
		
		// 1. РАСПАКОВКА (Делается 1 раз при создании аккаунта)
		expKey, err := voied25519.NewExpandedPublicKey(voied25519.PublicKey(pub))
		if err != nil { panic(err) }
		
		users = append(users, &User{
			uid:   uint64(i),
			priv:  priv,
			pub:   pub,
			expKey: expKey,
			nonce: 0,
		})
	}
	fmt.Println("Пользователи готовы.")
	
	// Массив для хранения исходных структур транзакций (нужен для теста сжатия)
    var allTxsStructs []*tx.Transaction
	
	// Константа для Meta-Transactions
    const MetaBatchSize = 512 //256
	
	// НОВЫЙ Массив для Мета-Транзакций (блоков)
    var allMetaTxBytes [][]byte
    
    // Буфер для накопления текущей пачки
    currentBatch := make([]*tx.Transaction, 0, MetaBatchSize)

	var allTxBytes [][]byte
	var realTxCounter uint64 = 0

	for opCode, count := range txCounts {
		for i := 0; i < count; i++ {
			// Выбираем случайного пользователя
			u := users[mrand.Intn(len(users))]

			now := uint64(time.Now().Unix())

			header := &tx.TransactionHeader{
				ChainType:     tx.ChainType_LOCALNET,
				ChainVersion:  1,
				OpCode:        opCode,
				AuthType:      tx.TxAuthType_UID,
				ExecutionMode: tx.TxExecMode_DEFAULT,
				MarketCode:    tx.Markets_PERPETUAL,
				MarketSymbol:  uint32(mrand.Intn(128)),
				SignerUid:     u.uid,
				Nonce:         u.nonce,
				MinHeight:     now - 5,
				MaxHeight:     now + 5,
				Signature:	   nil,
			}

			txx := &tx.Transaction{
				HeaderData: &tx.Transaction_Header{
					Header: header,
				},
			}

			// Заполнение Payload (без изменений)
			switch opCode {
			case tx.OpCode_META_NOOP:
				p := &tx.MetaNoopPayload{Payload: []byte{0x00}}
				txx.Payload = &tx.Transaction_MetaNoop{MetaNoop: p}
				header.MarketCode = tx.Markets_UNDEFINED
				header.MarketSymbol = 0
				realTxCounter++

			case tx.OpCode_META_RESERVE:
				p := &tx.MetaReservePayload{Payload: []byte{0x00}}
				txx.Payload = &tx.Transaction_MetaReserve{MetaReserve: p}
				header.MarketCode = tx.Markets_UNDEFINED
				header.MarketSymbol = 0
				realTxCounter++

			case tx.OpCode_ORD_CREATE:
				itemsCount := 1
				if mrand.Intn(10) == 0 {
					itemsCount = mrand.Intn(11) + 2
				}
				realTxCounter += uint64(itemsCount)
				orders := make([]*tx.OrderItem, 0, itemsCount)
				for k := 0; k < itemsCount; k++ {
					isMarket := mrand.Intn(2) == 0
					price := uint64(0)
					if !isMarket {
						price = uint64(mrand.Intn(100000) + 1000)
					}
					side := tx.Side_BUY
					if mrand.Intn(2) == 0 {
						side = tx.Side_SELL
					}
					oType := tx.OrderType_LIMIT
					if isMarket {
						oType = tx.OrderType_MARKET
					}
					orders = append(orders, &tx.OrderItem{
						OrderId:   genUUIDv7(),
						Side:      side,
						OrderType: oType,
						ExecType:  tx.TimeInForce_GTC,
						Quantity:  uint64(mrand.Intn(10000) + 100),
						Price:     price,
					})
				}
				p := &tx.OrderCreatePayload{Orders: orders}
				txx.Payload = &tx.Transaction_OrderCreate{OrderCreate: p}

			case tx.OpCode_ORD_CANCEL:
				itemsCount := 1
				if mrand.Intn(10) == 0 {
					itemsCount = mrand.Intn(11) + 2
				}
				realTxCounter += uint64(itemsCount)
				ids := make([]*tx.OrderID, 0, itemsCount)
				for k := 0; k < itemsCount; k++ {
					ids = append(ids, genUUIDv7())
				}
				p := &tx.OrderCancelPayload{OrderId: ids}
				txx.Payload = &tx.Transaction_OrderCancel{OrderCancel: p}

			case tx.OpCode_ORD_CANCEL_ALL:
				p := &tx.OrderCancelAllPayload{Payload: []byte{0x00}}
				txx.Payload = &tx.Transaction_OrderCancelAll{OrderCancelAll: p}
				realTxCounter++

			case tx.OpCode_ORD_CANCEL_REPLACE:
				newOrderPayload := &tx.OrderCreatePayload{
					Orders: []*tx.OrderItem{
						{
							OrderId:   genUUIDv7(),
							Side:      tx.Side_BUY,
							OrderType: tx.OrderType_LIMIT,
							Quantity:  uint64(mrand.Intn(10000) + 100),
							Price:     uint64(mrand.Intn(100000) + 1000),
						},
					},
				}
				p := &tx.OrderCancelReplacePayload{
					CanceledOrderId: genUUIDv7(),
					ReplacedOrder:   newOrderPayload,
				}
				txx.Payload = &tx.Transaction_OrderCancelReplace{OrderCancelReplace: p}
				realTxCounter++

			case tx.OpCode_ORD_AMEND:
				itemsCount := 1
				if mrand.Intn(10) == 0 {
					itemsCount = mrand.Intn(11) + 2
				}
				realTxCounter += uint64(itemsCount)
				amends := make([]*tx.AmendItem, 0, itemsCount)
				for k := 0; k < itemsCount; k++ {
					amend := &tx.AmendItem{OrderId: genUUIDv7()}
					if mrand.Intn(2) == 0 {
						q := uint64(mrand.Intn(10000) + 100)
						amend.Quantity = &q
					}
					if mrand.Intn(2) == 0 {
						pr := uint64(mrand.Intn(100000) + 1000)
						amend.Price = &pr
					}
					if amend.Quantity == nil && amend.Price == nil {
						q := uint64(mrand.Intn(10000) + 100)
						amend.Quantity = &q
					}
					amends = append(amends, amend)
				}
				p := &tx.OrderAmendPayload{Amends: amends}
				txx.Payload = &tx.Transaction_OrderAmend{OrderAmend: p}
			}

			//v2 - транзакция отдельно от подписи 
			dataToSign, err := proto.Marshal(txx)
			if err != nil {
				panic(err)
			}

			// Считаем BLAKE3 хеш
			hash := blake3.Sum256(dataToSign)

			// Подписываем хеш
			sig := ed25519.Sign(u.priv, hash[:])
			
			// Сохраняем подпись в структуру (чтобы она была полноценной)
            header.Signature = sig
			
			
			// Финальная сериализация - собираем raw bytes 
			// 4. Сборка пакета: [Sig (64)] + [ProtoBytes (N)]
			// Оптимизация: выделяем массив сразу нужного размера
			finalBytes := make([]byte, 64+len(dataToSign))
			
			// Копируем подпись в начало
			copy(finalBytes[0:64], sig)
			// Копируем протобаф следом
			copy(finalBytes[64:], dataToSign)
			
			allTxBytes = append(allTxBytes, finalBytes)
			
			// Добавляем структуру в коллекцию для бенчмарка сжатия
            // Важно: txx внутри цикла создается заново (txx := &tx.Transaction{...}),
            // поэтому можно просто сохранить указатель.
            allTxsStructs = append(allTxsStructs, txx)
			
 			
			// 2. НОВОЕ: Добавляем в Meta-Batch
            // Нам нужно скопировать структуру или использовать указатель.
            // Так как txx создается заново в каждой итерации (txx := &tx.Transaction{...}),
            // можно безопасно сохранить указатель.
            currentBatch = append(currentBatch, txx)

            // Если набрали пачку — пакуем
            if len(currentBatch) >= MetaBatchSize {
                // Создаем лист
                metaTx := &tx.TransactionList{
                    Txs: currentBatch,
                }
                // Сериализуем БОЛЬШОЙ объект
                metaBytes, err := proto.Marshal(metaTx)
                if err != nil { panic(err) }
                
                allMetaTxBytes = append(allMetaTxBytes, metaBytes)
                
                // Сбрасываем буфер (alloc new slice to avoid side effects if proto keeps refs)
                currentBatch = make([]*tx.Transaction, 0, MetaBatchSize)
            }
			
			
			
			
			u.nonce++
		}
	}

	// Статистика
	totalTx := len(allTxBytes)
	var totalSize int
	for _, b := range allTxBytes {
		totalSize += len(b)
	}

	
	// Если остались "хвосты" в буфере
    if len(currentBatch) > 0 {
        metaTx := &tx.TransactionList{Txs: currentBatch}
        metaBytes, _ := proto.Marshal(metaTx)
        allMetaTxBytes = append(allMetaTxBytes, metaBytes)
    }

	avgSize := float64(totalSize) / float64(totalTx)
	avgRealSize := float64(totalSize) / float64(realTxCounter)
	
	fmt.Printf("\nГенерация завершена:\n")
	fmt.Printf("  • Всего транзакций:        %d\n", totalTx)
	fmt.Printf("  • Всего real tx:           %d\n", realTxCounter)
	fmt.Printf("  • Мета-Транзакций (блоков по %d): %d\n", MetaBatchSize, len(allMetaTxBytes))
	fmt.Printf("  • Общий размер:            %d байт\n", totalSize)
	fmt.Printf("  • Средний размер tx:       %.1f байт\n", avgSize)
	fmt.Printf("  • Средний размер real tx:  %.1f байт\n", avgRealSize)

	// Запись в файл
	f, err := os.Create("txs.bin")
	if err != nil {
		panic(err)
	}
	defer f.Close()
	for _, b := range allTxBytes {
		if _, err := f.Write(b); err != nil {
			panic(err)
		}
	}
	fmt.Printf("Сгенерировано %d транзакций → txs.bin\n", len(allTxBytes))



	
	return
	// 3. Запуск полного бенчмарка (Распаковка -> Поиск юзера -> Хеш -> Проверка)
	benchmarkFullPipeline(allTxBytes, users)
	
	// 3. НОВЫЙ Multi-Core бенчмарк
	benchmarkParallelPipeline(allTxBytes, users)
	
	// 4. Batched (Умная группировка)
	benchmarkBatchedPipeline(allTxBytes, users)
	
	//5. Fixed batching 
	benchmarkFixedBatchPipeline(allTxBytes, users)
	
	//
	benchmarkBatchCryptoPipeline(allTxBytes, users)
	
	// НОВЫЙ БЕНЧМАРК
    benchmarkMetaTxDecoding(allMetaTxBytes)
	
	//Обработка мета-батчей (как на валидаторах)
	benchmarkMetaTxCryptoPipeline(allMetaTxBytes, users)

	// Сравнение ExpKey с обычным параллелизмом
	benchmarkParallelPipelineExp(allTxBytes, users)

	// Сравнение ExpKey с BatchCrypto (Fixed Batching)
	benchmarkBatchCryptoPipelineExp(allTxBytes, users)

	// Сравнение ExpKey с MetaTx + BatchVerifier
	benchmarkMetaTxCryptoPipelineExp(allMetaTxBytes, users)
	
	//Проверка UUId-ов
	//benchmarkLogicPipeline(allTxBytes, users)
	benchmarkLogicPipelineShardedCache(allTxBytes, users)
	
	//С блум-фильтром перед кешем
	benchmarkLogicPipelineShardedCacheBloom(allTxBytes, users)
	
}

// CryptoTask - структура, передаваемая от Decoder-воркеров к Verifier-воркерам
type CryptoTask struct {
	PubKey    ed25519.PublicKey
	Signature []byte
	Data      []byte // Данные, готовые для хеширования (уже смаршаленные с пустой подписью)
}

type BatchItem struct {
	Signature []byte
	Data      []byte
}

type CryptoTaskBatch struct {
	PubKey ed25519.PublicKey
	Items  []BatchItem 
}

// SmartBatch - пачка транзакций ОДНОГО юзера (один ключ на всех)
type SmartBatch struct {
	PubKey ed25519.PublicKey
	Items  []BatchItem
}

// MixedBatch - пачка транзакций РАЗНЫХ юзеров (ключ внутри каждого элемента)
type MixedBatch []CryptoTask

// Структура для передачи данных от Decoder к Verifier
// Содержит всё необходимое для проверки целого блока
type MetaBatchTask struct {
	PubKeys []voied25519.PublicKey
	Msgs    [][]byte
	Sigs    [][]byte
}

//Extended key 
// Задача для одиночного пайплайна с ExpKey
type CryptoTaskExp struct {
	ExpKey    *voied25519.ExpandedPublicKey
	Signature []byte
	Data      []byte
}

// Батч задач с ExpKey
type CryptoTaskBatchExp struct {
	Items []CryptoTaskExp
}

// Мета-задача для блока транзакций с ExpKey
type MetaBatchTaskExp struct {
	ExpKeys []*voied25519.ExpandedPublicKey
	Msgs    [][]byte
	Sigs    [][]byte
}

func benchmarkParallelPipelineExp(allTxs [][]byte, users []*User) {
	if len(allTxs) == 0 { return }
	count := len(allTxs)

	// Конфигурация
	var (
		Decoders  = runtime.NumCPU()
		Verifiers = runtime.NumCPU()
		BufSize   = 10000
	)

	fmt.Printf("\n=== PARALLEL PIPELINE EXP (Expanded Keys) (%d tx) ===\n", count)
	fmt.Printf("Config: Decoders=%d, Verifiers=%d\n", Decoders, Verifiers)

	runtime.GC()
	rawChan := make(chan []byte, BufSize)
	// Канал теперь передает задачу с ExpandedKey
	cryptoChan := make(chan CryptoTaskExp, BufSize)
	
	var wgD, wgV sync.WaitGroup
	var validCount int64

	start := time.Now()

	// STAGE 1: DECODERS
	for i := 0; i < Decoders; i++ {
		wgD.Add(1)
		go func() {
			defer wgD.Done()
			var txx tx.Transaction
			emptySig := make([]byte, 64)

			for b := range rawChan {
				txx.Reset()
				if proto.Unmarshal(b, &txx) != nil { continue }
				h := txx.GetHeader()
				if h == nil { continue }
				uid := h.SignerUid
				if uid == 0 || uid > uint64(len(users)) { continue }

				// БЕРЕМ EXPANDED KEY ИЗ ЮЗЕРА
				expKey := users[uid-1].expKey
				sig := h.Signature
				h.Signature = emptySig
				data, _ := proto.Marshal(&txx)

				cryptoChan <- CryptoTaskExp{
					ExpKey:    expKey,
					Signature: sig,
					Data:      data,
				}
			}
		}()
	}

	// STAGE 2: VERIFIERS (Используем Verify у ExpKey)
	for i := 0; i < Verifiers; i++ {
		wgV.Add(1)
		go func() {
			defer wgV.Done()
			for task := range cryptoChan {
				hash := blake3.Sum256(task.Data)
				
				// БЫСТРАЯ ПРОВЕРКА
				// Verify принимает (msg, sig). Так как мы подписывали хеш, передаем хеш как msg.
				if voied25519.VerifyExpanded(task.ExpKey, hash[:], task.Signature) {
					atomic.AddInt64(&validCount, 1)
				}
			}
		}()
	}

	go func() {
		for _, b := range allTxs { rawChan <- b }
		close(rawChan)
	}()

	wgD.Wait()
	close(cryptoChan)
	wgV.Wait()

	dur := time.Since(start)
	fmt.Printf("Speed:            %10s | %.0f tx/sec\n", dur, float64(count)/dur.Seconds())
	fmt.Printf("Valid:            %d/%d\n", validCount, count)
}

func benchmarkBatchCryptoPipelineExp(allTxs [][]byte, users []*User) {
	if len(allTxs) == 0 { return }
	count := len(allTxs)

	const BatchSize = 512
	var (
		Decoders  = runtime.NumCPU()
		Verifiers = runtime.NumCPU()
		BufSize   = 1000
	)
	
	fmt.Printf("\n=== BATCH PIPELINE EXP (Fixed Batch + ExpKey Verify) ===\n")
	fmt.Printf("Config: Decoders=%d, Verifiers=%d, BatchSize=%d\n", Decoders, Verifiers, BatchSize)

	runtime.GC()
	rawChan := make(chan []byte, 100000)
	// Передаем батч задач с ExpKey
	cryptoChan := make(chan CryptoTaskBatchExp, BufSize)
	var wgD, wgV sync.WaitGroup
	var validCount int64

	start := time.Now()

	// STAGE 1: DECODE & GROUP
	for i := 0; i < Decoders; i++ {
		wgD.Add(1)
		go func() {
			defer wgD.Done()
			var txx tx.Transaction
			emptySig := make([]byte, 64)
			
			batch := make([]CryptoTaskExp, 0, BatchSize)

			flush := func() {
				if len(batch) > 0 {
					toSend := make([]CryptoTaskExp, len(batch))
					copy(toSend, batch)
					cryptoChan <- CryptoTaskBatchExp{Items: toSend}
					batch = batch[:0]
				}
			}

			for b := range rawChan {
				txx.Reset()
				if proto.Unmarshal(b, &txx) != nil { continue }
				h := txx.GetHeader()
				if h == nil { continue }
				uid := h.SignerUid
				if uid == 0 || uid > uint64(len(users)) { continue }

				expKey := users[uid-1].expKey
				sig := h.Signature
				h.Signature = emptySig
				data, _ := proto.Marshal(&txx)

				batch = append(batch, CryptoTaskExp{
					ExpKey:    expKey,
					Signature: sig,
					Data:      data,
				})

				if len(batch) >= BatchSize {
					flush()
				}
			}
			flush()
		}()
	}

	// STAGE 2: VERIFIERS (Loop with ExpKey)
	for i := 0; i < Verifiers; i++ {
		wgV.Add(1)
		go func() {
			defer wgV.Done()
			var locValid int64
			for batch := range cryptoChan {
				for _, task := range batch.Items {
					hash := blake3.Sum256(task.Data)
					// Используем ускоренную проверку
					if voied25519.VerifyExpanded(task.ExpKey, hash[:], task.Signature) {
						locValid++
					}
				}
			}
			atomic.AddInt64(&validCount, locValid)
		}()
	}

	go func() {
		for _, b := range allTxs { rawChan <- b }
		close(rawChan)
	}()

	wgD.Wait()
	close(cryptoChan)
	wgV.Wait()

	dur := time.Since(start)
	fmt.Printf("Speed:            %10s | %.0f tx/sec\n", dur, float64(count)/dur.Seconds())
	fmt.Printf("Valid:            %d/%d\n", validCount, count)
}


func benchmarkMetaTxCryptoPipelineExp(metaBlocks [][]byte, users []*User) {
	if len(metaBlocks) == 0 { return }
	
	testList := &tx.TransactionList{}
	_ = proto.Unmarshal(metaBlocks[0], testList)
	txsPerBlock := len(testList.Txs)
	totalTxs := len(metaBlocks) * txsPerBlock

	var (
		Decoders  = runtime.NumCPU()
		Verifiers = runtime.NumCPU()
		BufSize   = 1000
	)

	fmt.Printf("\n=== META-TX + EXP KEY PIPELINE ===\n")
	fmt.Printf("Input: %d blocks (~%d txs total)\n", len(metaBlocks), totalTxs)
	fmt.Printf("Config: Decoders=%d, Verifiers=%d\n", Decoders, Verifiers)

	runtime.GC()
	rawChan := make(chan []byte, BufSize)
	cryptoChan := make(chan MetaBatchTaskExp, BufSize)
	
	var wgD, wgV sync.WaitGroup
	var validCount int64

	start := time.Now()

	// STAGE 1: DECODE & PREPARE
	for i := 0; i < Decoders; i++ {
		wgD.Add(1)
		go func() {
			defer wgD.Done()
			txList := &tx.TransactionList{}
			emptySig := make([]byte, 64)

			for blockData := range rawChan {
				txList.Reset()
				if err := proto.Unmarshal(blockData, txList); err != nil { continue }

				count := len(txList.Txs)
				task := MetaBatchTaskExp{
					ExpKeys: make([]*voied25519.ExpandedPublicKey, 0, count),
					Msgs:    make([][]byte, 0, count),
					Sigs:    make([][]byte, 0, count),
				}

				for _, txx := range txList.Txs {
					h := txx.GetHeader()
					if h == nil { continue }
					uid := h.SignerUid
					if uid == 0 || uid > uint64(len(users)) { continue }

					expKey := users[uid-1].expKey

					sig := h.Signature
					h.Signature = emptySig
					data, _ := proto.Marshal(txx)
					txHash := blake3.Sum256(data)

					hashCopy := make([]byte, 32)
					copy(hashCopy, txHash[:])

					task.ExpKeys = append(task.ExpKeys, expKey)
					task.Msgs    = append(task.Msgs, hashCopy)
					task.Sigs    = append(task.Sigs, sig)
				}
				cryptoChan <- task
			}
		}()
	}

	// STAGE 2: VERIFY
	for i := 0; i < Verifiers; i++ {
		wgV.Add(1)
		go func() {
			defer wgV.Done()
			var locValid int64
			for task := range cryptoChan {
				for k := 0; k < len(task.ExpKeys); k++ {
					// ИСПРАВЛЕНИЕ 4: VerifyExpanded
					if voied25519.VerifyExpanded(task.ExpKeys[k], task.Msgs[k], task.Sigs[k]) {
						locValid++
					}
				}
			}
			atomic.AddInt64(&validCount, locValid)
		}()
	}

	go func() {
		for _, b := range metaBlocks { rawChan <- b }
		close(rawChan)
	}()

	wgD.Wait()
	close(cryptoChan)
	wgV.Wait()

	dur := time.Since(start)
	fmt.Printf("Speed:            %10s | %.0f tx/sec\n", dur, float64(totalTxs)/dur.Seconds())
	fmt.Printf("Valid:            %d/%d\n", validCount, totalTxs)
	fmt.Printf("=======================================\n")
}

//=================================================================================================================


// benchmarkFullPipeline - ЭТАЛОН (Single Thread)
func benchmarkFullPipeline(allTxs [][]byte, users []*User) {
	if len(allTxs) == 0 { return }
	count := len(allTxs)
	fmt.Printf("\n=== BASELINE: SINGLE-CORE PIPELINE (%d tx) ===\n", count)

	runtime.GC()
	emptySig := make([]byte, 64)
	validCount := 0
	
	var txx tx.Transaction

	start := time.Now()
	for _, b := range allTxs {
		txx.Reset()
		
		// 1. Unmarshal
		if err := proto.Unmarshal(b, &txx); err != nil { continue }

		// 2. Key Lookup
		h := txx.GetHeader()
		if h == nil { continue }
		uid := h.SignerUid
		if uid == 0 || uid > uint64(len(users)) { continue }
		
		pubKey := users[uid-1].pub
		signature := h.Signature

		// 3. Prepare (Zero Sig + Marshal)
		h.Signature = emptySig
		dataToVerify, _ := proto.Marshal(&txx)

		// 4. Hash + Verify
		hash := blake3.Sum256(dataToVerify)
		if ed25519.Verify(pubKey, hash[:], signature) {
			validCount++
		}
	}
	durFull := time.Since(start)

	fmt.Printf("Скорость:         %10s | %.0f tx/sec\n", durFull, float64(count)/durFull.Seconds())
	fmt.Printf("Latnecy (avg):    %.2f µs/tx\n", float64(durFull.Microseconds())/float64(count))
	fmt.Printf("Valid:            %d/%d\n", validCount, count)
}

// benchmarkParallelPipeline - МНОГОПОТОЧНЫЙ (Pipeline Pattern)
func benchmarkParallelPipeline(allTxs [][]byte, users []*User) {
	if len(allTxs) == 0 { return }
	count := len(allTxs)

	// --- КОНФИГУРАЦИЯ ЯДЕР ---
	const (
		WorkersDecoder = 8 // Кол-во горутин для парсинга и подготовки
		WorkersCrypto  = 16 // Кол-во горутин для хеширования и проверки подписи
		ChannelBuffer  = 10000 // Буфер, чтобы воркеры не простаивали
	)

	fmt.Printf("\n=== MULTI-CORE PIPELINE (%d tx) ===\n", count)
	fmt.Printf("Config: Decoders=%d, Verifiers=%d\n", WorkersDecoder, WorkersCrypto)

	runtime.GC()
	
	// Каналы
	// rawChan: подаем сырые байты
	rawChan := make(chan []byte, ChannelBuffer)
	// cryptoChan: передаем подготовленные задачи на проверку
	cryptoChan := make(chan CryptoTask, ChannelBuffer)
	
	// Счетчики
	var validCount int64
	var wgDecoders sync.WaitGroup
	var wgVerifiers sync.WaitGroup

	start := time.Now()

	// ---------------------------------------------------------
	// STAGE 1: DECODERS (Protobuf Unmarshal -> Key Lookup -> Marshal Clean)
	// ---------------------------------------------------------
	for i := 0; i < WorkersDecoder; i++ {
		wgDecoders.Add(1)
		go func() {
			defer wgDecoders.Done()
			
			// У каждого воркера своя структура, чтобы не лочить память
			var txx tx.Transaction
			emptySig := make([]byte, 64)

			for b := range rawChan {
				txx.Reset()
				if err := proto.Unmarshal(b, &txx); err != nil { continue }

				h := txx.GetHeader()
				if h == nil { continue }
				
				uid := h.SignerUid
				// Простой эмулятор базы данных (без мьютексов, так как read-only массив)
				if uid == 0 || uid > uint64(len(users)) { continue }
				
				pubKey := users[uid-1].pub
				sig := h.Signature // Копируем слайс (ссылку)

				// Подготовка к хешированию (самая дорогая часть Stage 1)
				h.Signature = emptySig
				data, _ := proto.Marshal(&txx)
				
				// Отправляем дальше
				cryptoChan <- CryptoTask{
					PubKey:    pubKey,
					Signature: sig,
					Data:      data,
				}
			}
		}()
	}

	// ---------------------------------------------------------
	// STAGE 2: VERIFIERS (Blake3 -> Ed25519)
	// ---------------------------------------------------------
	for i := 0; i < WorkersCrypto; i++ {
		wgVerifiers.Add(1)
		go func() {
			defer wgVerifiers.Done()
			for task := range cryptoChan {
				hash := blake3.Sum256(task.Data)
				if ed25519.Verify(task.PubKey, hash[:], task.Signature) {
					atomic.AddInt64(&validCount, 1)
				}
			}
		}()
	}

	// ---------------------------------------------------------
	// FEEDER (Main Thread)
	// ---------------------------------------------------------
	// Запускаем подачу данных
	go func() {
		for _, b := range allTxs {
			rawChan <- b
		}
		close(rawChan) // Закрываем вход, когда данные кончились
	}()

	// Ждем завершения декодеров
	wgDecoders.Wait()
	// Как только декодеры закончили, закрываем канал для крипто-воркеров
	close(cryptoChan)
	// Ждем завершения крипто-воркеров
	wgVerifiers.Wait()

	durFull := time.Since(start)

	// Расчет ускорения
	opsPerSec := float64(count) / durFull.Seconds()
	
	fmt.Printf("Скорость:         %10s | %.0f tx/sec\n", durFull, opsPerSec)
	fmt.Printf("Valid:            %d/%d\n", validCount, count)
	fmt.Println("===========================================")
}

// 3. SMART BATCHING (Обновленная: с замерами стадий)
func benchmarkBatchedPipeline(allTxs [][]byte, users []*User) {
	if len(allTxs) == 0 { return }
	count := len(allTxs)
	
	const (
		Decoders = 8
		Verifiers = 16
		BufSize = 1000
		MaxBatch = 50
	)
	
	fmt.Printf("\n=== SMART BATCHING PIPELINE (%d tx) ===\n", count)
	fmt.Printf("Config: Decoders=%d, Verifiers=%d, Strategy=By User (Burst)\n", Decoders, Verifiers)

	// --- STAGE 1: PREPARATION ONLY (Decoder + Grouper throughput) ---
	// Запускаем только декодеры, верификаторы - заглушки, которые сразу возвращают OK
	runtime.GC()
	rawChan := make(chan []byte, 100000)
	cryptoChan := make(chan SmartBatch, BufSize)
	var wgD, wgV sync.WaitGroup

	start := time.Now()

	// Decoders
	for i := 0; i < Decoders; i++ {
		wgD.Add(1)
		go func() {
			defer wgD.Done()
			var txx tx.Transaction
			emptySig := make([]byte, 64)
			var batch []BatchItem
			var lastUID uint64
			var lastPub ed25519.PublicKey

			flush := func() {
				if len(batch) > 0 {
					toSend := make([]BatchItem, len(batch))
					copy(toSend, batch)
					cryptoChan <- SmartBatch{PubKey: lastPub, Items: toSend}
					batch = batch[:0]
				}
			}

			for b := range rawChan {
				txx.Reset()
				if proto.Unmarshal(b, &txx) != nil { continue }
				h := txx.GetHeader()
				if h == nil { continue }
				uid := h.SignerUid

				if uid != lastUID || len(batch) >= MaxBatch {
					flush()
				}
				if uid != lastUID {
					if uid == 0 || uid > uint64(len(users)) { lastUID = 0; continue }
					lastUID = uid
					lastPub = users[uid-1].pub
				}
				sig := h.Signature
				h.Signature = emptySig
				data, _ := proto.Marshal(&txx)
				batch = append(batch, BatchItem{Signature: sig, Data: data})
			}
			flush()
		}()
	}

	// Dummy Verifiers (Просто вычитывают канал)
	for i := 0; i < Verifiers; i++ {
		wgV.Add(1)
		go func() {
			defer wgV.Done()
			for range cryptoChan {
				// No op (simulation of instant verify)
			}
		}()
	}

	// Feeder
	go func() {
		for _, b := range allTxs { rawChan <- b }
		close(rawChan)
	}()

	wgD.Wait()
	close(cryptoChan)
	wgV.Wait()

	durPrep := time.Since(start)
	fmt.Printf("1. Prep (Decode+Group): %10s | %.0f ops/sec | %.2f µs/op\n",
		durPrep, float64(count)/durPrep.Seconds(), float64(durPrep.Microseconds())/float64(count))

	// --- STAGE 2: FULL PIPELINE ---
	runtime.GC()
	rawChan = make(chan []byte, 100000)
	cryptoChan = make(chan SmartBatch, BufSize)
	var validCount int64

	start = time.Now()

	// Decoders (Same logic)
	for i := 0; i < Decoders; i++ {
		wgD.Add(1)
		go func() {
			defer wgD.Done()
			var txx tx.Transaction
			emptySig := make([]byte, 64)
			var batch []BatchItem
			var lastUID uint64
			var lastPub ed25519.PublicKey

			flush := func() {
				if len(batch) > 0 {
					toSend := make([]BatchItem, len(batch))
					copy(toSend, batch)
					cryptoChan <- SmartBatch{PubKey: lastPub, Items: toSend}
					batch = batch[:0]
				}
			}

			for b := range rawChan {
				txx.Reset()
				if proto.Unmarshal(b, &txx) != nil { continue }
				h := txx.GetHeader()
				if h == nil { continue }
				uid := h.SignerUid

				if uid != lastUID || len(batch) >= MaxBatch {
					flush()
				}
				if uid != lastUID {
					if uid == 0 || uid > uint64(len(users)) { lastUID = 0; continue }
					lastUID = uid
					lastPub = users[uid-1].pub
				}
				sig := h.Signature
				h.Signature = emptySig
				data, _ := proto.Marshal(&txx)
				batch = append(batch, BatchItem{Signature: sig, Data: data})
			}
			flush()
		}()
	}

	// Real Verifiers
	for i := 0; i < Verifiers; i++ {
		wgV.Add(1)
		go func() {
			defer wgV.Done()
			var locValid int64
			for b := range cryptoChan {
				pub := b.PubKey
				for _, item := range b.Items {
					h := blake3.Sum256(item.Data)
					if ed25519.Verify(pub, h[:], item.Signature) {
						locValid++
					}
				}
			}
			atomic.AddInt64(&validCount, locValid)
		}()
	}

	go func() {
		for _, b := range allTxs { rawChan <- b }
		close(rawChan)
	}()

	wgD.Wait()
	close(cryptoChan)
	wgV.Wait()

	durFull := time.Since(start)
	
	fmt.Printf("2. FULL PIPELINE:       %10s | %.0f ops/sec | %.2f µs/op\n",
		durFull, float64(count)/durFull.Seconds(), float64(durFull.Microseconds())/float64(count))
	fmt.Printf("   Valid: %d/%d\n", validCount, count)
}

// 4. FIXED BATCHING PIPELINE (Новая функция)
func benchmarkFixedBatchPipeline(allTxs [][]byte, users []*User) {
	if len(allTxs) == 0 { return }
	count := len(allTxs)

	// Настройки батчинга
	const (
		BatchSize = 100  // Собираем по 10 штук
		MaxCap    = 100 // Максимальный лимит (защита)
		
		Decoders  = 8
		Verifiers = 32
		BufSize   = 1000
	)
	
	// Ограничиваем размер батча
	actualBatchSize := BatchSize
	if actualBatchSize > MaxCap { actualBatchSize = MaxCap }

	fmt.Printf("\n=== FIXED BATCHING PIPELINE (%d tx) ===\n", count)
	fmt.Printf("Config: Decoders=%d, Verifiers=%d, BatchSize=%d\n", Decoders, Verifiers, actualBatchSize)

	// --- STAGE 1: PREPARATION ONLY ---
	runtime.GC()
	rawChan := make(chan []byte, 100000)
	// Канал передает []CryptoTask (срез задач с разными ключами)
	cryptoChan := make(chan MixedBatch, BufSize)
	var wgD, wgV sync.WaitGroup

	start := time.Now()

	// Decoders
	for i := 0; i < Decoders; i++ {
		wgD.Add(1)
		go func() {
			defer wgD.Done()
			var txx tx.Transaction
			emptySig := make([]byte, 64)
			
			// Накапливаем задачи с разными ключами
			batch := make(MixedBatch, 0, actualBatchSize)

			flush := func() {
				if len(batch) > 0 {
					// Копируем, чтобы отвязать память
					toSend := make(MixedBatch, len(batch))
					copy(toSend, batch)
					cryptoChan <- toSend
					batch = batch[:0]
				}
			}

			for b := range rawChan {
				txx.Reset()
				if proto.Unmarshal(b, &txx) != nil { continue }
				h := txx.GetHeader()
				if h == nil { continue }
				uid := h.SignerUid
				if uid == 0 || uid > uint64(len(users)) { continue }

				pub := users[uid-1].pub
				sig := h.Signature
				h.Signature = emptySig
				data, _ := proto.Marshal(&txx)

				batch = append(batch, CryptoTask{
					PubKey:    pub,
					Signature: sig,
					Data:      data,
				})

				if len(batch) >= actualBatchSize {
					flush()
				}
			}
			flush()
		}()
	}

	// Dummy Verifiers
	for i := 0; i < Verifiers; i++ {
		wgV.Add(1)
		go func() {
			defer wgV.Done()
			for range cryptoChan {
				// No op
			}
		}()
	}

	go func() {
		for _, b := range allTxs { rawChan <- b }
		close(rawChan)
	}()

	wgD.Wait()
	close(cryptoChan)
	wgV.Wait()

	durPrep := time.Since(start)
	fmt.Printf("1. Prep (Decode+Group): %10s | %.0f ops/sec | %.2f µs/op\n",
		durPrep, float64(count)/durPrep.Seconds(), float64(durPrep.Microseconds())/float64(count))

	// --- STAGE 2: FULL PIPELINE ---
	runtime.GC()
	rawChan = make(chan []byte, 100000)
	cryptoChan = make(chan MixedBatch, BufSize)
	var validCount int64

	start = time.Now()

	// Decoders
	for i := 0; i < Decoders; i++ {
		wgD.Add(1)
		go func() {
			defer wgD.Done()
			var txx tx.Transaction
			emptySig := make([]byte, 64)
			batch := make(MixedBatch, 0, actualBatchSize)

			flush := func() {
				if len(batch) > 0 {
					toSend := make(MixedBatch, len(batch))
					copy(toSend, batch)
					cryptoChan <- toSend
					batch = batch[:0]
				}
			}

			for b := range rawChan {
				txx.Reset()
				if proto.Unmarshal(b, &txx) != nil { continue }
				h := txx.GetHeader()
				if h == nil { continue }
				uid := h.SignerUid
				if uid == 0 || uid > uint64(len(users)) { continue }

				pub := users[uid-1].pub
				sig := h.Signature
				h.Signature = emptySig
				data, _ := proto.Marshal(&txx)

				batch = append(batch, CryptoTask{
					PubKey:    pub,
					Signature: sig,
					Data:      data,
				})

				if len(batch) >= actualBatchSize {
					flush()
				}
			}
			flush()
		}()
	}

	// Real Verifiers
	for i := 0; i < Verifiers; i++ {
		wgV.Add(1)
		go func() {
			defer wgV.Done()
			var locValid int64
			for batch := range cryptoChan {
				for _, task := range batch {
					h := blake3.Sum256(task.Data)
					if ed25519.Verify(task.PubKey, h[:], task.Signature) {
						locValid++
					}
				}
			}
			atomic.AddInt64(&validCount, locValid)
		}()
	}

	go func() {
		for _, b := range allTxs { rawChan <- b }
		close(rawChan)
	}()

	wgD.Wait()
	close(cryptoChan)
	wgV.Wait()

	durFull := time.Since(start)

	fmt.Printf("2. FULL PIPELINE:       %10s | %.0f ops/sec | %.2f µs/op\n",
		durFull, float64(count)/durFull.Seconds(), float64(durFull.Microseconds())/float64(count))
	fmt.Printf("   Valid: %d/%d\n", validCount, count)
	fmt.Println("===========================================")
}

// 5. BATCH CRYPTO VERIFICATION (Исправленная версия с BatchVerifier)
func benchmarkBatchCryptoPipeline(allTxs [][]byte, users []*User) {
	if len(allTxs) == 0 { return }
	count := len(allTxs)

	const (
		BatchSize = 512 //лучшие результаты 
		Decoders  = 16
		Verifiers = 32
		BufSize   = 1000
	)
	
	fmt.Printf("\n=== BATCH CRYPTO PIPELINE (%d tx) ===\n", count)
	fmt.Printf("Config: Decoders=%d, Verifiers=%d, BatchVerify=Enabled (Voi BatchVerifier)\n", Decoders, Verifiers)

	// --- STAGE 1: PREPARATION ---
	runtime.GC()
	rawChan := make(chan []byte, 100000)
	cryptoChan := make(chan MixedBatch, BufSize)
	var wgD, wgV sync.WaitGroup
	var validCount int64

	start := time.Now()

	// Decoders (Без изменений)
	for i := 0; i < Decoders; i++ {
		wgD.Add(1)
		go func() {
			defer wgD.Done()
			var txx tx.Transaction
			emptySig := make([]byte, 64)
			
			batch := make(MixedBatch, 0, BatchSize)

			flush := func() {
				if len(batch) > 0 {
					toSend := make(MixedBatch, len(batch))
					copy(toSend, batch)
					cryptoChan <- toSend
					batch = batch[:0]
				}
			}

			for b := range rawChan {
				txx.Reset()
				if proto.Unmarshal(b, &txx) != nil { continue }
				h := txx.GetHeader()
				if h == nil { continue }
				uid := h.SignerUid
				if uid == 0 || uid > uint64(len(users)) { continue }

				pub := users[uid-1].pub
				sig := h.Signature
				h.Signature = emptySig
				data, _ := proto.Marshal(&txx)

				batch = append(batch, CryptoTask{
					PubKey:    pub,
					Signature: sig,
					Data:      data,
				})

				if len(batch) >= BatchSize {
					flush()
				}
			}
			flush()
		}()
	}

	// Verifiers (С ИСПОЛЬЗОВАНИЕМ VOI BatchVerifier)
	for i := 0; i < Verifiers; i++ {
		wgV.Add(1)
		go func() {
			defer wgV.Done()
			
			// 1. Создаем верификатор с запасом емкости
			verifier := voied25519.NewBatchVerifierWithCapacity(BatchSize)
			
			// Опции (можно nil, если стандартные)
			// opts := &voied25519.VerifyOptions{} 

			for batch := range cryptoChan {
				// Сбрасываем верификатор перед новым батчем (Reset очищает внутренние буферы)
				// Внимание: В curve25519-voi нет метода Reset() у BatchVerifier в старых версиях,
				// но в новых он есть, или проще создавать новый, если аллокации дешевые.
				// Самый надежный способ в Go для Voi - создавать New на каждый цикл, 
				// так как он внутри использует sync.Pool или сложные структуры.
				// Но для макс. скорости попробуем пересоздавать:
				verifier = voied25519.NewBatchVerifierWithCapacity(len(batch))

				for _, task := range batch {
					h := blake3.Sum256(task.Data)
					
					// Voi.Add копирует данные, поэтому можно передавать слайс хеша
					// (но лучше скопировать в temp буфер, если blake3 возвращает массив)
					// blake3 возвращает [32]byte, Add требует []byte
					
					verifier.Add(
						voied25519.PublicKey(task.PubKey), 
						h[:], 
						task.Signature,
					)
				}

				// 2. ПРОВЕРКА
				// Verify возвращает (bool, error) или просто bool (зависит от версии, обычно (bool, bool))
				// Проверяем успех
				valid, _ := verifier.Verify(nil) // nil = random source (не нужен для проверки)
				
				if valid {
					atomic.AddInt64(&validCount, int64(len(batch)))
				}
			}
		}()
	}

	go func() {
		for _, b := range allTxs { rawChan <- b }
		close(rawChan)
	}()

	wgD.Wait()
	close(cryptoChan)
	wgV.Wait()

	durFull := time.Since(start)

	fmt.Printf("2. BATCH CRYPTO:        %10s | %.0f ops/sec | %.2f µs/op\n",
		durFull, float64(count)/durFull.Seconds(), float64(durFull.Microseconds())/float64(count))
	fmt.Printf("   Valid: %d/%d\n", validCount, count)
	fmt.Println("===========================================")
}

// ----------------------------------------------------------------
// БЕНЧМАРК META-TX (PROTOBUF BATCHING)
// ----------------------------------------------------------------
func benchmarkMetaTxDecoding(metaBlocks [][]byte) {
    if len(metaBlocks) == 0 { return }
    
    // Считаем общее число транзакций внутри блоков для корректного расчета TPS
    totalTxs := 0
    // Пробный декодинг одного блока, чтобы узнать размер
    testList := &tx.TransactionList{}
    _ = proto.Unmarshal(metaBlocks[0], testList)
    itemsPerBlock := len(testList.Txs)
    totalTxs = len(metaBlocks) * itemsPerBlock // Приблизительно, если хвост был неполный

    fmt.Printf("\n=== META-TX DECODING BENCHMARK ===\n")
    fmt.Printf("Input: %d blocks (approx %d txs total)\n", len(metaBlocks), totalTxs)
    fmt.Printf("Block Size: ~%.2f KB\n", float64(len(metaBlocks[0]))/1024.0)

    const Decoders = 8
    
    runtime.GC()
    
    // Канал с блоками
    blockChan := make(chan []byte, 1000)
    var wg sync.WaitGroup
    var decodedCount int64

    start := time.Now()

    for i := 0; i < Decoders; i++ {
        wg.Add(1)
        go func() {
            defer wg.Done()
            // Реиспользуем структуру списка, чтобы не аллоцировать память каждый раз
            txList := &tx.TransactionList{}
            
            var localCount int64
            
            for blockData := range blockChan {
                txList.Reset() // Сброс буфера Protobuf (Zero Allocation)
                
                if err := proto.Unmarshal(blockData, txList); err != nil {
                    continue
                }
                
                // Проходимся по списку, чтобы эмулировать доступ к данным 
                // (иначе компилятор может слишком сильно оптимизировать)
                for _, t := range txList.Txs {
                    if t.GetHeader() != nil {
                        localCount++
                    }
                }
            }
            atomic.AddInt64(&decodedCount, localCount)
        }()
    }

    // Feeder
    go func() {
        for _, b := range metaBlocks {
            blockChan <- b
        }
        close(blockChan)
    }()

    wg.Wait()

    dur := time.Since(start)

    fmt.Printf("Time:             %v\n", dur)
    fmt.Printf("Throughput (TXs): %.0f tx/sec\n", float64(decodedCount)/dur.Seconds())
    fmt.Printf("Throughput (Blk): %.0f blocks/sec\n", float64(len(metaBlocks))/dur.Seconds())
    fmt.Printf("Decoded Total:    %d\n", decodedCount)
    fmt.Printf("==================================\n")
}

// 7. META-TX + BATCH CRYPTO (ULTIMATE BENCHMARK)
func benchmarkMetaTxCryptoPipeline(metaBlocks [][]byte, users []*User) {
	if len(metaBlocks) == 0 { return }
	
	// Вычисляем общее кол-во транзакций для статистики
	// (считаем по первому блоку)
	testList := &tx.TransactionList{}
	_ = proto.Unmarshal(metaBlocks[0], testList)
	txsPerBlock := len(testList.Txs)
	totalTxs := len(metaBlocks) * txsPerBlock

	const (
		//Decoders    = 16		
		ChannelBuf  = 1000
	)
	
	var Decoders    = runtime.NumCPU()
	var Verifiers   = runtime.NumCPU()	//e.g 48 for our Xeon

	fmt.Printf("\n=== META-TX + BATCH CRYPTO PIPELINE ===\n")
	fmt.Printf("Input: %d blocks (~%d txs total)\n", len(metaBlocks), totalTxs)
	fmt.Printf("Batch Size: %d (Defined by Proto)\n", txsPerBlock)
	fmt.Printf("Config: Decoders=%d, Verifiers=%d\n", Decoders, Verifiers)

	runtime.GC()
	
	// Канал входящих блоков (Meta-Tx bytes)
	rawChan := make(chan []byte, ChannelBuf)
	
	// Канал готовых пачек для проверки
	// Обратите внимание: мы передаем НЕ транзакции, а готовые крипто-примитивы
	cryptoChan := make(chan MetaBatchTask, ChannelBuf)
	
	var wgDecoders sync.WaitGroup
	var wgVerifiers sync.WaitGroup
	var validCount int64

	start := time.Now()

	// --- STAGE 1: DECODE & PREPARE ---
	for i := 0; i < Decoders; i++ {
		wgDecoders.Add(1)
		go func() {
			defer wgDecoders.Done()
			
			// Реиспользуем структуру списка
			txList := &tx.TransactionList{}
			emptySig := make([]byte, 64)

			for blockData := range rawChan {
				txList.Reset()
				if err := proto.Unmarshal(blockData, txList); err != nil {
					continue
				}

				// Подготавливаем структуру для верификатора
				// Аллоцируем слайсы под размер батча
				count := len(txList.Txs)
				task := MetaBatchTask{
					PubKeys: make([]voied25519.PublicKey, 0, count),
					Msgs:    make([][]byte, 0, count),
					Sigs:    make([][]byte, 0, count),
				}

				for _, txx := range txList.Txs {
					h := txx.GetHeader()
					if h == nil { continue }
					uid := h.SignerUid
					if uid == 0 || uid > uint64(len(users)) { continue }

					// 1. Получаем ключ (Эмуляция State Lookup)
					// Кастуем в voied25519
					pub := voied25519.PublicKey(users[uid-1].pub)

					// 2. Подготовка хеша
					sig := h.Signature
					h.Signature = emptySig // Обнуляем для хеширования
					data, _ := proto.Marshal(txx) // Маршалим одну TX
					txHash := blake3.Sum256(data)

					// Копируем хеш (важно, т.к. blake3 возвращает массив, а нам нужен слайс)
					hashCopy := make([]byte, 32)
					copy(hashCopy, txHash[:])

					// 3. Добавляем в задачу
					task.PubKeys = append(task.PubKeys, pub)
					task.Msgs    = append(task.Msgs, hashCopy)
					task.Sigs    = append(task.Sigs, sig)
				}

				// Отправляем готовую пачку верификатору
				if len(task.PubKeys) > 0 {
					cryptoChan <- task
				}
			}
		}()
	}

	// --- STAGE 2: BATCH VERIFY ---
	for i := 0; i < Verifiers; i++ {
		wgVerifiers.Add(1)
		go func() {
			defer wgVerifiers.Done()
			
			// Создаем верификатор с запасом (под размер батча)
			// txsPerBlock - это ожидаемый размер, но берем с запасом
			verifier := voied25519.NewBatchVerifierWithCapacity(txsPerBlock)

			for task := range cryptoChan {
				// Пересоздаем (или Reset, если версия либы позволяет)
				// NewBatchVerifierWithCapacity - дешевая операция (структура + слайсы)
				verifier = voied25519.NewBatchVerifierWithCapacity(len(task.PubKeys))

				for k := 0; k < len(task.PubKeys); k++ {
					verifier.Add(task.PubKeys[k], task.Msgs[k], task.Sigs[k])
				}

				// ПРОВЕРКА ВСЕГО БЛОКА ОДНИМ МАХОМ
				valid, _ := verifier.Verify(nil)
				
				if valid {
					atomic.AddInt64(&validCount, int64(len(task.PubKeys)))
				} else {
					// Fallback logic here if needed
				}
			}
		}()
	}

	// Feeder
	go func() {
		for _, b := range metaBlocks {
			rawChan <- b
		}
		close(rawChan)
	}()

	wgDecoders.Wait()
	close(cryptoChan)
	wgVerifiers.Wait()

	dur := time.Since(start)

	fmt.Printf("Time:             %v\n", dur)
	fmt.Printf("Throughput:       %.0f tx/sec\n", float64(totalTxs)/dur.Seconds())
	fmt.Printf("Valid:            %d/%d\n", validCount, totalTxs)
	fmt.Printf("=======================================\n")
}

//
func benchmarkLogicPipelineShardedCache(allTxs [][]byte, users []*User) {
	if len(allTxs) == 0 { return }
	count := len(allTxs)

	var (
		Decoders  = runtime.NumCPU()
		Verifiers = runtime.NumCPU()
		BufSize   = 10000
	)

	// Запускаем "быстрое время"
	//startFastTimeKeeper()
	
	// Кеш для дедупликации
	orderCache := NewShardedCache(100_000)

	fmt.Printf("\n=== LOGIC PIPELINE (Sig + UUIDv7 TimeCheck + Dedup, ShardedCache) ===\n", count)
	fmt.Printf("Config: Decoders=%d, Verifiers=%d. Deviation=%dms\n", Decoders, Verifiers, MaxTimeDeviationMS)

	runtime.GC()
	
	rawChan := make(chan []byte, BufSize)
	cryptoChan := make(chan CryptoTaskExp, BufSize)
	
	var wgD, wgV sync.WaitGroup
	var validCount int64
	var logicErrors int64

	start := time.Now()

	// STAGE 1: DECODE -> VALIDATE -> DEDUP
	for i := 0; i < Decoders; i++ {
		wgD.Add(1)
		go func() {
			defer wgD.Done()
			var txx tx.Transaction
			emptySig := make([]byte, 64)

			for b := range rawChan {
				txx.Reset()
				if proto.Unmarshal(b, &txx) != nil { continue }
				h := txx.GetHeader()
				if h == nil { continue }
				
				// --- ВАЛИДАЦИЯ ---
				var oid []byte
				
				switch p := txx.Payload.(type) {
				case *tx.Transaction_OrderCreate:
					if len(p.OrderCreate.Orders) > 0 {
						oid = p.OrderCreate.Orders[0].OrderId.Id
					}
				case *tx.Transaction_OrderCancel:
					if len(p.OrderCancel.OrderId) > 0 {
						oid = p.OrderCancel.OrderId[0].Id
					}
				}

				if len(oid) > 0 {
					// ВАЖНО ДЛЯ ТЕСТА: 
					// Так как мы сгенерировали транзакции давно, их UUIDv7 устарели.
					// Валидатор времени их отвергнет.
					// Чтобы тест показал ПРОПУСКНУЮ СПОСОБНОСТЬ (а не просто reject),
					// мы здесь "хакнем" первый байт времени, чтобы он казался свежим,
					// ИЛИ просто позволим ему отвергнуть (если хотим замерить скорость reject-а).
					
					// Для честности теста math operations - мы выполняем IsValidUUIDv7.
					// Если вернет false (по времени) - это ок, главное, что CPU потрачен на проверку.
					
					if !IsValidUUIDv7(oid) {
						atomic.AddInt64(&logicErrors, 1)
						// В реальности тут continue, но для теста криптографии
						// мы можем пропустить дальше, если хотим нагрузить верификаторы.
						// Но правильнее - отвергнуть.
						continue 
					}

					// Проверка на дубликаты
					if orderCache.Seen(oid) {
						atomic.AddInt64(&logicErrors, 1)
						continue
					}
				}
				// ----------------

				uid := h.SignerUid
				if uid == 0 || uid > uint64(len(users)) { continue }

				expKey := users[uid-1].expKey
				sig := h.Signature
				h.Signature = emptySig
				data, _ := proto.Marshal(&txx)

				cryptoChan <- CryptoTaskExp{
					ExpKey:    expKey,
					Signature: sig,
					Data:      data,
				}
			}
		}()
	}

	// STAGE 2: VERIFIERS
	for i := 0; i < Verifiers; i++ {
		wgV.Add(1)
		go func() {
			defer wgV.Done()
			for task := range cryptoChan {
				hash := blake3.Sum256(task.Data)
				if voied25519.VerifyExpanded(task.ExpKey, hash[:], task.Signature) {
					atomic.AddInt64(&validCount, 1)
				}
			}
		}()
	}

	go func() {
		for _, b := range allTxs { rawChan <- b }
		close(rawChan)
	}()

	wgD.Wait()
	close(cryptoChan)
	wgV.Wait()

	dur := time.Since(start)
	
	fmt.Printf("Speed:            %10s | %.0f tx/sec\n", dur, float64(count)/dur.Seconds())
	fmt.Printf("Valid:            %d\n", validCount)
	fmt.Printf("Logic Rejects:    %d (Time/Dup/Format)\n", logicErrors)
}

func benchmarkLogicPipelineShardedCacheBloom(allTxs [][]byte, users []*User) {
	if len(allTxs) == 0 { return }
	count := len(allTxs)

	var (
		Decoders  = runtime.NumCPU()
		Verifiers = runtime.NumCPU()
		BufSize   = 10000
	)

	// Запускаем "быстрое время"
	//startFastTimeKeeper()
	
	// Кеш для дедупликации
	orderCache := NewShardedCache(100_000)
	bloom := &FastBloom{} // Создаем фильтр

	fmt.Printf("\n=== LOGIC PIPELINE (Sig + UUIDv7 TimeCheck + Dedup, ShardedCache + Bloom) ===\n", count)
	fmt.Printf("Config: Decoders=%d, Verifiers=%d. Deviation=%dms\n", Decoders, Verifiers, MaxTimeDeviationMS)

	runtime.GC()
	
	rawChan := make(chan []byte, BufSize)
	cryptoChan := make(chan CryptoTaskExp, BufSize)
	
	var wgD, wgV sync.WaitGroup
	var validCount int64
	var logicErrors int64

	start := time.Now()

	// STAGE 1: DECODE -> VALIDATE -> DEDUP
	for i := 0; i < Decoders; i++ {
		wgD.Add(1)
		go func() {
			defer wgD.Done()
			var txx tx.Transaction
			
			emptySig := make([]byte, 64)

			for b := range rawChan {
				
				//Быстрая проверка валидности
				if !FastProtoScan(b){
					return
				}
				
				txx.Reset()
				if proto.Unmarshal(b, &txx) != nil { continue }
				h := txx.GetHeader()
				if h == nil { continue }
				
				// --- ВАЛИДАЦИЯ ---
				var oid []byte
				
				switch p := txx.Payload.(type) {
				case *tx.Transaction_OrderCreate:
					if len(p.OrderCreate.Orders) > 0 {
						oid = p.OrderCreate.Orders[0].OrderId.Id
					}
				case *tx.Transaction_OrderCancel:
					if len(p.OrderCancel.OrderId) > 0 {
						oid = p.OrderCancel.OrderId[0].Id
					}
				}

				if len(oid) > 0 {
					// ВАЖНО ДЛЯ ТЕСТА: 
					// Так как мы сгенерировали транзакции давно, их UUIDv7 устарели.
					// Валидатор времени их отвергнет.
					// Чтобы тест показал ПРОПУСКНУЮ СПОСОБНОСТЬ (а не просто reject),
					// мы здесь "хакнем" первый байт времени, чтобы он казался свежим,
					// ИЛИ просто позволим ему отвергнуть (если хотим замерить скорость reject-а).
					
					// Для честности теста math operations - мы выполняем IsValidUUIDv7.
					// Если вернет false (по времени) - это ок, главное, что CPU потрачен на проверку.
					
					if !IsValidUUIDv7(oid) {
						atomic.AddInt64(&logicErrors, 1)
						// В реальности тут continue, но для теста криптографии
						// мы можем пропустить дальше, если хотим нагрузить верификаторы.
						// Но правильнее - отвергнуть.
						continue 
					}

					// 2. АДАПТИВНАЯ ДЕДУПЛИКАЦИЯ
					isWarMode := AdaptiveBloomEnabled.Load()
					//isDuplicate := false

					if isWarMode {
						// --- РЕЖИМ ВОЙНЫ (Включен Блум) ---
						// Сначала дешевый Блум
						if !bloom.MayContain(oid) {
							// Блум говорит: "Точно нет".
							// Мы НЕ идем в Cache.Seen (экономим RLock).
							// Мы сразу пишем.
							bloom.Add(oid)      // Греем Блум
							orderCache.Put(oid) // Fast Write
							// isDuplicate = false (по умолчанию)
						} else {
							// Блум говорит: "Возможно есть". Проверяем мапу честно.
							if orderCache.Seen(oid) {
								DuplicateCounter.Add(1) // +1 к счетчику дублей
								return // &tx.TxResult{Code: 102}, nil
							}
							// False positive Блума - не дубль, пропустили.
						}
					} else {
						// --- МИРНОЕ ВРЕМЯ (Блум выключен) ---
						// Сразу идем в мапу. Никакого хеширования Блума. Максимальная скорость.
						if orderCache.Seen(oid) {
							DuplicateCounter.Add(1) // +1 к счетчику дублей
							return //&tx.TxResult{Code: 102}, nil
						}
					}
				}
				// ----------------

				uid := h.SignerUid
				if uid == 0 || uid > uint64(len(users)) { continue }

				expKey := users[uid-1].expKey
				sig := h.Signature
				h.Signature = emptySig
				data, _ := proto.Marshal(&txx)

				cryptoChan <- CryptoTaskExp{
					ExpKey:    expKey,
					Signature: sig,
					Data:      data,
				}
			}
		}()
	}

	// STAGE 2: VERIFIERS
	for i := 0; i < Verifiers; i++ {
		wgV.Add(1)
		go func() {
			defer wgV.Done()
			for task := range cryptoChan {
				hash := blake3.Sum256(task.Data)
				if voied25519.VerifyExpanded(task.ExpKey, hash[:], task.Signature) {
					atomic.AddInt64(&validCount, 1)
				}
			}
		}()
	}

	go func() {
		for _, b := range allTxs { rawChan <- b }
		close(rawChan)
	}()

	wgD.Wait()
	close(cryptoChan)
	wgV.Wait()

	dur := time.Since(start)
	
	fmt.Printf("Speed:            %10s | %.0f tx/sec\n", dur, float64(count)/dur.Seconds())
	fmt.Printf("Valid:            %d\n", validCount)
	fmt.Printf("Logic Rejects:    %d (Time/Dup/Format)\n", logicErrors)
}




// ──────────────────────────────────────────────────────────────
// Helpers
// ──────────────────────────────────────────────────────────────
func genUUIDv7() *tx.OrderID {
	id, err := uuid.NewV7()
	if err != nil {
		panic(fmt.Sprintf("failed to generate uuidv7: %v", err))
	}
	idBytes := id[:]
	return &tx.OrderID{Id: idBytes}
}

func saveBytes(filename string, data [][]byte) {
	f, err := os.Create(filename)
	if err != nil {
		panic(err)
	}
	defer f.Close()
	for _, b := range data {
		f.Write(b)
	}
}

// Wire Types (из спецификации Protobuf)
const (
	WireVarint     = 0
	WireFixed64    = 1
	WireBytes      = 2
	WireStartGroup = 3 // Deprecated, но могут встретиться
	WireEndGroup   = 4 // Deprecated
	WireFixed32    = 5
)

// FastProtoScan - сверхбыстрая валидация структуры Protobuf без аллокаций.
// Возвращает true, если байты структурно корректны.
func FastProtoScan(buf []byte) bool {
	i := 0
	l := len(buf)

	for i < l {
		// --- 1. ЧИТАЕМ TAG (Key) ---
		// Tag - это Varint. Нам нужны нижние 3 бита (WireType).
		
		// Быстрый путь: однобайтовый тег (номера полей 1..15)
		// Это 99% случаев в HFT
		var wireType uint8
		if buf[i] < 0x80 {
			wireType = buf[i] & 7
			i++
		} else {
			// Медленный путь: многобайтовый тег
			// Пропускаем varint, пока не найдем байт без MSB (0x80)
			// (Макс 10 байт для 64 бит, но мы не парсим значение, просто скачем)
			start := i
			for {
				if i >= l { return false } // EOF посреди varint
				b := buf[i]
				i++
				if b < 0x80 {
					wireType = b & 7 // Берем тип из последнего байта?
					// НЕТ! WireType всегда в ПЕРВОМ байте Varint-а.
					// Ошибка в логике выше исправлена:
					// WireType сидит в младших 3 битах САМОГО ПЕРВОГО байта тега.
					wireType = buf[start] & 7
					break
				}
				if i-start > 10 { return false } // Varint too long
			}
		}

		if i > l { return false } // Вылетели

		// --- 2. ПРОПУСКАЕМ VALUE ---
		switch wireType {
		case WireVarint: // 0: int32, int64, uint32, bool, enum
			// Скачем пока байт >= 0x80
			for {
				if i >= l { return false }
				if buf[i] < 0x80 {
					i++
					break
				}
				i++
				// Защита от бесконечного/битого varint (макс 10 байт)
				// Для супер-скорости можно убрать счетчик, если доверяем i < l
			}

		case WireFixed64: // 1: fixed64, sfixed64, double
			i += 8

		case WireBytes: // 2: string, bytes, embedded messages, packed repeated
			// Сначала читаем длину (Varint)
			if i >= l { return false }
			
			// Декодируем длину (Varint) чтобы узнать, сколько прыгать
			// Тут нам нужно значение длины
			var length uint64
			var shift uint
			
			// Fast path decoding varint
			for {
				if i >= l { return false }
				b := buf[i]
				i++
				length |= uint64(b&0x7F) << shift
				if b < 0x80 {
					break
				}
				shift += 7
				if shift > 63 { return false } // Overflow
			}

			// Прыгаем на length
			// Важно: проверяем переполнение uint/int при сложении
			nextI := i + int(length)
			if nextI < i || nextI > l { // nextI < i ловит int overflow
				return false 
			}
			i = nextI

		case WireFixed32: // 5: fixed32, sfixed32, float
			i += 4

		// Группы (Deprecated) - обычно фейлим, но можно просто ретурн false
		case WireStartGroup, WireEndGroup:
			return false 
			
		default:
			return false // Неизвестный тип данных -> мусор
		}
	}

	// Если мы вышли ровно в конец массива - значит структура целая
	return i == l
}