// main.go
package main

import (
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"fmt"
	"io"
	mrand "math/rand"
	"os"
	"runtime"
	"time"
	"sync"
	"sync/atomic"

	"github.com/google/uuid"
	"github.com/klauspost/compress/gzip"
	"github.com/klauspost/compress/s2"
	"github.com/klauspost/compress/zstd"
	"github.com/pierrec/lz4/v4"
	"github.com/zeebo/blake3" // Подключаем BLAKE3
	"google.golang.org/protobuf/proto"
	"tx-generator/tx"
)

// CompressionFunc теперь принимает словарь (или nil)
type CompressionFunc func(data []byte, dict []byte) ([]byte, error)

// DecompressionFunc — функция распаковки (возвращает оригинальные байты)
type DecompressionFunc func(compressed []byte, dict []byte) ([]byte, error)

// User структура для эмуляции БД пользователей
type User struct {
	uid   uint64
	priv  ed25519.PrivateKey
	pub   ed25519.PublicKey // Храним публичный ключ для проверки
	nonce uint64
}

func main() {
	var zstdDict []byte
	if data, err := os.ReadFile("./dictionary_v7.zstd"); err == nil {
		zstdDict = data
		fmt.Println("Словарь zstd_v7 загружен:", len(zstdDict), "байт")
	} else {
		fmt.Println("Словарь zstd_v7 не найден — сжимаем без него")
	}

	mrand.Seed(time.Now().UnixNano())

	// OpCodes:
	txCounts := map[tx.OpCode]int{
		tx.OpCode_META_NOOP:           100,
		tx.OpCode_META_RESERVE:        5,
		tx.OpCode_ORD_CREATE:          15_000,
		tx.OpCode_ORD_CANCEL:          10_000,
		tx.OpCode_ORD_CANCEL_ALL:      100,
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
		users = append(users, &User{
			uid:   uint64(i),
			priv:  priv,
			pub:   pub,
			nonce: 0,
		})
	}
	fmt.Println("Пользователи готовы.")

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
				Signature:     make([]byte, 64), // placeholder
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

			// 2. Хеширование и Подпись
			// Сначала маршалим с пустой подписью (она уже пустая init-е)
			dataToSign, err := proto.Marshal(txx)
			if err != nil {
				panic(err)
			}

			// Считаем BLAKE3 хеш
			hash := blake3.Sum256(dataToSign)

			// Подписываем ХЕШ
			sig := ed25519.Sign(u.priv, hash[:])
			header.Signature = sig

			// Финальная сериализация
			finalBytes, err := proto.Marshal(txx)
			if err != nil {
				panic(err)
			}

			allTxBytes = append(allTxBytes, finalBytes)
			u.nonce++
		}
	}

	// Статистика
	totalTx := len(allTxBytes)
	var totalSize int
	for _, b := range allTxBytes {
		totalSize += len(b)
	}

	avgSize := float64(totalSize) / float64(totalTx)
	avgRealSize := float64(totalSize) / float64(realTxCounter)

	fmt.Printf("\nГенерация завершена:\n")
	fmt.Printf("  • Всего транзакций:        %d\n", totalTx)
	fmt.Printf("  • Всего real tx:           %d\n", realTxCounter)
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

	// Сжатие
	compressAndSave(allTxBytes, "zstd", zstdCompress, zstdDecompress, nil)
	compressAndSave(allTxBytes, "zstd-dict", zstdCompress, zstdDecompress, zstdDict)
	compressAndSave(allTxBytes, "lz4", lz4Compress, lz4Decompress, nil)

	// 3. Запуск полного бенчмарка (Распаковка -> Поиск юзера -> Хеш -> Проверка)
	benchmarkFullPipeline(allTxBytes, users)
	
	// 3. НОВЫЙ Multi-Core бенчмарк
	benchmarkParallelPipeline(allTxBytes, users)
	
	// 4. Batched (Умная группировка)
	benchmarkBatchedPipeline(allTxBytes, users)
	
	//5. Fixed batching 
	benchmarkFixedBatchPipeline(allTxBytes, users)
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

// 3. SMART BATCHING (NEW)
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

func compressAndSave(allTxBytes [][]byte, algoName string, compressFunc CompressionFunc, decompressFunc DecompressionFunc, dict []byte) {
	var buf bytes.Buffer
	for _, b := range allTxBytes {
		buf.Write(b)
	}
	raw := buf.Bytes()
	
	startCompress := time.Now()
	compressed, err := compressFunc(raw, dict)
	if err != nil {
		fmt.Printf("Skip %s: %v\n", algoName, err)
		return
	}
	durComp := time.Since(startCompress)

	filename := fmt.Sprintf("txs.bin.%s", algoName)
	if len(dict) > 0 {
		filename += ".dict"
	}
	os.WriteFile(filename, compressed, 0644)

	ratio := float64(len(raw)) / float64(len(compressed))
	fmt.Printf("Сжатие %-10s: %.2fx (Size: %d) Time: %v\n", algoName, ratio, len(compressed), durComp)
}

// --- Функции сжатия (оставил только используемые для краткости, остальные без изменений) ---

func zstdCompress(data []byte, dict []byte) ([]byte, error) {
	opts := []zstd.EOption{zstd.WithEncoderLevel(zstd.SpeedFastest), zstd.WithEncoderConcurrency(8)}
	if dict != nil {
		opts = append(opts, zstd.WithEncoderDict(dict))
	}
	enc, err := zstd.NewWriter(nil, opts...)
	if err != nil {
		return nil, err
	}
	return enc.EncodeAll(data, nil), nil
}

func zstdDecompress(compressed, dict []byte) ([]byte, error) {
	opts := []zstd.DOption{zstd.WithDecoderConcurrency(8)}
	if len(dict) > 0 {
		opts = append(opts, zstd.WithDecoderDicts(dict))
	}
	dec, err := zstd.NewReader(nil, opts...)
	if err != nil {
		return nil, err
	}
	return dec.DecodeAll(compressed, nil)
}

func lz4Compress(data []byte, dict []byte) ([]byte, error) {
	bound := lz4.CompressBlockBound(len(data))
	dst := make([]byte, bound)
	var c lz4.Compressor
	n, err := c.CompressBlock(data, dst)
	if err != nil {
		return nil, err
	}
	return dst[:n], nil
}

func lz4Decompress(compressed, dict []byte) ([]byte, error) {
	dst := make([]byte, len(compressed)*10) // упрощенно
	n, err := lz4.UncompressBlock(compressed, dst)
	if err != nil {
		return nil, err
	}
	return dst[:n], nil
}

// Заглушки для S2/Gzip, если они нужны были
func s2Compress(data []byte, dict []byte) ([]byte, error) { return s2.Encode(nil, data), nil }
func s2Decompress(compressed, dict []byte) ([]byte, error) { return s2.Decode(nil, compressed) }
func gzipCompress(data []byte, dict []byte) ([]byte, error) {
	var buf bytes.Buffer
	w := gzip.NewWriter(&buf)
	w.Write(data)
	w.Close()
	return buf.Bytes(), nil
}
func gzipDecompress(compressed []byte, dict []byte) ([]byte, error) {
	r, _ := gzip.NewReader(bytes.NewReader(compressed))
	return io.ReadAll(r)
}