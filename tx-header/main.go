package main

import (
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/binary"
	//"encoding/hex"
	"fmt"
	mrand "math/rand"
	"os"
	"time"

	"tx-generator/tx" // ← подставь свой реальный путь к пакету protobuf
	"google.golang.org/protobuf/proto"
	
	"github.com/klauspost/compress/flate"
	"github.com/klauspost/compress/s2"
	"github.com/klauspost/compress/zstd"
	"github.com/pierrec/lz4/v4"
)

type CompressionFunc func(data []byte, dict []byte) ([]byte, error)

func main() {
	var zstdDict []byte
	if data, err := os.ReadFile("./dictionary.zstd"); err == nil {
		zstdDict = data
		fmt.Println("Словарь zstd загружен:", len(zstdDict), "байт")
	} else {
		fmt.Println("Словарь zstd не найден — сжимаем без него")
	}


	mrand.Seed(time.Now().UnixNano())

	txCounts := map[uint32]int{
		0x00: 1000,  // META_NOOP
		0xFF: 5,   // META_RESERVE
		0x60: 7_000, // ORD_CREATE
		0x64: 16_000,  // ORD_CANCEL
		0x65: 1_000,  // ORD_CANCEL_BATCH
		0x66: 1_000,  // ORD_CANCEL_ALL
		0x6A: 9_000,  // ORD_CANCEL_REPLACE
		0x6B: 15_000,  // ORD_AMEND
		0x6C: 2_000,  // ORD_AMEND_BATCH
	}

	// Пул пользователей
	type User struct {
		uid   uint64
		priv  ed25519.PrivateKey
		nonce uint64
	}
	users := make([]*User, 0, 100)
	for i := 1; i <= 100; i++ {
		_, priv, err := ed25519.GenerateKey(rand.Reader)
		if err != nil {
			panic(err)
		}
		users = append(users, &User{uid: uint64(i), priv: priv, nonce: 0})
	}

	var allTxBytes [][]byte

	for opCode, count := range txCounts {
		for i := 0; i < count; i++ {
			u := users[mrand.Intn(len(users))]

			header := &tx.TransactionHeader{
				ChainVersion:    0x01000001,
				PayloadSize:     0, // заполним позже
				ReservedFlag:    0,
				OpCode:          opCode,
				AuthType:        0,
				ExecutionMode:   0,
				ReservedPadding: 0,
				MarketCode:      randomUint32(),
				SignerUid:       u.uid,
				Nonce:           u.nonce,
				MinHeight:       0,
				MaxHeight:       0,
				Signature:       make([]byte, 64), // placeholder
			}

			txx := &tx.Transaction{Header: header}
			var realPayload proto.Message // ← сохраняем реальное сообщение для marshal

			switch opCode {
			case 0x00: // META_NOOP
				p := &tx.MetaNoopPayload{Payload: []byte{0x00}} // ← или Dummy, если так назвал
				realPayload = p
				txx.Payload = &tx.Transaction_MetaNoop{MetaNoop: p}

			case 0xFF: // META_RESERVE
				p := &tx.MetaReservePayload{Payload: []byte{0x00}}
				realPayload = p
				txx.Payload = &tx.Transaction_MetaReserve{MetaReserve: p}

			case 0x60: // ORD_CREATE
				isMarket := mrand.Intn(2) == 0
				price := uint64(0)
				if !isMarket {
					price = uint64(mrand.Intn(100000) + 1000)
				}
				p := &tx.OrderCreatePayload{
					IsBuy:           mrand.Intn(2) == 0,
					IsMarket:        isMarket,
					Quantity:        uint64(mrand.Intn(10000) + 100),
					Price:           price,
					Flags:           uint32(mrand.Intn(16)),
					UserGeneratedId: randomBytes(16),
				}
				realPayload = p
				txx.Payload = &tx.Transaction_OrderCreate{OrderCreate: p}

			case 0x64: // ORD_CANCEL
				p := &tx.OrderCancelPayload{}
				if mrand.Intn(2) == 0 {
					p.IdType = &tx.OrderCancelPayload_OrderId{OrderId: &tx.OrderID{Id: randomBytes(16)}}
				} else {
					p.IdType = &tx.OrderCancelPayload_UserGeneratedId{UserGeneratedId: randomBytes(16)}
				}
				realPayload = p
				txx.Payload = &tx.Transaction_OrderCancel{OrderCancel: p}

			case 0x65: // ORD_CANCEL_BATCH
				p := &tx.OrderCancelBatchPayload{}
				for j := 0; j < mrand.Intn(5)+1; j++ {
					p.OrderIds = append(p.OrderIds, &tx.OrderID{Id: randomBytes(16)})
				}
				for j := 0; j < mrand.Intn(4); j++ {
					p.UserGeneratedIds = append(p.UserGeneratedIds, randomBytes(16))
				}
				realPayload = p
				txx.Payload = &tx.Transaction_OrderCancelBatch{OrderCancelBatch: p}

			case 0x66: // ORD_CANCEL_ALL
				p := &tx.OrderCancelAllPayload{}
				realPayload = p
				txx.Payload = &tx.Transaction_OrderCancelAll{OrderCancelAll: p}

			case 0x6A: // ORD_CANCEL_REPLACE
				p := &tx.OrderCancelReplacePayload{}
				if mrand.Intn(2) == 0 {
					p.CancelIdType = &tx.OrderCancelReplacePayload_OrderId{OrderId: &tx.OrderID{Id: randomBytes(16)}}
				} else {
					p.CancelIdType = &tx.OrderCancelReplacePayload_UserGeneratedId{UserGeneratedId: randomBytes(16)}
				}
				p.IsBuy = mrand.Intn(2) == 0
				p.IsMarket = mrand.Intn(2) == 0
				p.Quantity = uint64(mrand.Intn(10000) + 100)
				p.Price = uint64(mrand.Intn(100000) + 1000)
				p.Flags = uint32(mrand.Intn(16))
				p.NewUserGeneratedId = randomBytes(16)
				realPayload = p
				txx.Payload = &tx.Transaction_OrderCancelReplace{OrderCancelReplace: p}

			case 0x6B: // ORD_AMEND
				p := &tx.OrderAmendPayload{}
				if mrand.Intn(2) == 0 {
					p.IdType = &tx.OrderAmendPayload_OrderId{OrderId: &tx.OrderID{Id: randomBytes(16)}}
				} else {
					p.IdType = &tx.OrderAmendPayload_UserGeneratedId{UserGeneratedId: randomBytes(16)}
				}
				if mrand.Intn(2) == 0 {
					q := uint64(mrand.Intn(10000) + 100)
					p.NewQuantity = &q
				}
				if mrand.Intn(2) == 0 {
					pr := uint64(mrand.Intn(100000) + 1000)
					p.NewPrice = &pr
				}
				if p.NewQuantity == nil && p.NewPrice == nil {
					q := uint64(mrand.Intn(10000) + 100)
					p.NewQuantity = &q
				}
				realPayload = p
				txx.Payload = &tx.Transaction_OrderAmend{OrderAmend: p}

			case 0x6C: // ORD_AMEND_BATCH
				p := &tx.OrderAmendBatchPayload{}
				for j := 0; j < mrand.Intn(3)+1; j++ {
					item := &tx.OrderAmendBatchPayload_AmendItem{}
					if mrand.Intn(2) == 0 {
						item.IdType = &tx.OrderAmendBatchPayload_AmendItem_OrderId{OrderId: &tx.OrderID{Id: randomBytes(16)}}
					} else {
						item.IdType = &tx.OrderAmendBatchPayload_AmendItem_UserGeneratedId{UserGeneratedId: randomBytes(16)}
					}
					if mrand.Intn(2) == 0 {
						q := uint64(mrand.Intn(10000) + 100)
						item.NewQuantity = &q
					}
					if mrand.Intn(2) == 0 {
						pr := uint64(mrand.Intn(100000) + 1000)
						item.NewPrice = &pr
					}
					if item.NewQuantity == nil && item.NewPrice == nil {
						q := uint64(mrand.Intn(10000) + 100)
						item.NewQuantity = &q
					}
					p.Amends = append(p.Amends, item)
				}
				realPayload = p
				txx.Payload = &tx.Transaction_OrderAmendBatch{OrderAmendBatch: p}
			}

			// Считаем размер payload
			var payloadBytes []byte
			if realPayload != nil {
				var err error
				payloadBytes, err = proto.Marshal(realPayload)
				if err != nil {
					panic(err)
				}
				header.PayloadSize = uint32(len(payloadBytes))
			}

			// Временная сериализация для подписи
			tempBytes, err := proto.Marshal(txx)
			if err != nil {
				panic(err)
			}

			// Подпись
			sig := ed25519.Sign(u.priv, tempBytes)
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

	// После цикла генерации
	totalTx := len(allTxBytes)
	var totalSize int
	for _, b := range allTxBytes {
		totalSize += len(b)
	}

	avgSize := float64(totalSize) / float64(totalTx)

	fmt.Printf("\nГенерация завершена:\n")
	fmt.Printf("  • Всего транзакций:        %d\n", totalTx)
	fmt.Printf("  • Общий размер (байты, несжатый файл):    %d\n", totalSize)
	fmt.Printf("  • Средний размер tx:       %.1f байт\n", avgSize)

	// Запись в файл
	f, err := os.Create("txs.bin")
	if err != nil {
		panic(err)
	}
	defer f.Close()

	for _, b := range allTxBytes {
		_, err := f.Write(b)
		if err != nil {
			panic(err)
		}
	}

	fmt.Printf("Сгенерировано %d транзакций → txs.bin\n", len(allTxBytes))
	
	
	
	
	
	
	// Сжатие разными алгоритмами
	compressAndSave(allTxBytes, "zstd",    zstdCompress,    nil)
	compressAndSave(allTxBytes, "zstd-dict",    zstdCompress,    zstdDict)
	compressAndSave(allTxBytes, "lz4",     lz4Compress,     nil)
	compressAndSave(allTxBytes, "s2",      s2Compress,      nil)
	compressAndSave(allTxBytes, "s2-better",      s2CompressBetter,      nil)
	compressAndSave(allTxBytes, "s2-best",      s2CompressBest,      nil)
	compressAndSave(allTxBytes, "gzip",    gzipCompress,    nil)
}

// ──────────────────────────────────────────────────────────────
// Helpers
// ──────────────────────────────────────────────────────────────

func randomUint32() uint32 {
	for {
		b := make([]byte, 4)
		_, err := rand.Read(b)
		if err != nil {
			panic(err)
		}
		val := binary.LittleEndian.Uint32(b)
		if val != 0 && val != 0xFFFFFFFF {
			return val
		}
	}
}

func randomBytes(n int) []byte {
	b := make([]byte, n)
	_, err := rand.Read(b)
	if err != nil {
		panic(err)
	}
	return b
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// Сохраняет конкатенированные байты в файл
func saveBytes(filename string, data [][]byte) {
	f, err := os.Create(filename)
	if err != nil {
		panic(err)
	}
	defer f.Close()

	for _, b := range data {
		_, err := f.Write(b)
		if err != nil {
			panic(err)
		}
	}
}

func compressAndSave(
    allTxBytes [][]byte,
    algoName string,
    compressFunc CompressionFunc,
    dict []byte, // nil = без словаря
) {
    var buf bytes.Buffer
    for _, b := range allTxBytes {
        buf.Write(b)
    }
    raw := buf.Bytes()

    start := time.Now()

    compressed, err := compressFunc(raw, dict)
    if err != nil {
        fmt.Printf("Ошибка сжатия %s: %v\n", algoName, err)
        return
    }

    duration := time.Since(start).Milliseconds()

    filename := fmt.Sprintf("txs.bin.%s", algoName)
    if len(dict) > 0 {
        filename += ".dict"
    }

    if err := os.WriteFile(filename, compressed, 0644); err != nil {
        panic(err)
    }

    origSize := len(raw)
    compSize := len(compressed)
    ratio := float64(origSize) / float64(compSize)
    savings := 100 * (1 - float64(compSize)/float64(origSize))

    fmt.Printf("Алгоритм %-8s → %s (%d байт)\n", algoName, filename, compSize)
    fmt.Printf("   Коэффициент: %.2fx   Экономия: %.1f%%\n", ratio, savings)
    fmt.Printf("   Время: %d мс\n\n", duration)
}

// Универсальная функция сжатия + статистика
func compressAndSave_Old(allTxBytes [][]byte, algoName string, compressFunc func([]byte) ([]byte, error)) {
	var buf bytes.Buffer
	for _, b := range allTxBytes {
		buf.Write(b)
	}
	raw := buf.Bytes()
	
	start := time.Now()

	compressed, err := compressFunc(raw)
	if err != nil {
		fmt.Printf("Ошибка сжатия %s: %v\n", algoName, err)
		return
	}
	
	duration := time.Since(start).Milliseconds()

	filename := fmt.Sprintf("txs.bin.%s", algoName)
	err = os.WriteFile(filename, compressed, 0644)
	if err != nil {
		panic(err)
	}

	ratio := float64(len(raw)) / float64(len(compressed))
	savings := 100 * (1 - float64(len(compressed))/float64(len(raw)))

	fmt.Printf("Алгоритм %-6s → %s (%d байт)\n", algoName, filename, len(compressed))
	fmt.Printf("   Коэффициент сжатия: %.2fx   Экономия: %.1f%%\n", ratio, savings)
	fmt.Printf("   Время сжатия:       %d мс\n\n", duration)
}

// ──────────────────────────────────────────────────────────────
// Разные функции сжатия (можно легко добавлять новые)
// ──────────────────────────────────────────────────────────────

func zstdCompress(data []byte, dict []byte) ([]byte, error) {
    opts := []zstd.EOption{zstd.WithEncoderLevel(zstd.SpeedDefault)}
    if dict != nil {
        opts = append(opts, zstd.WithEncoderDict(dict))
		opts = append(opts, zstd.WithEncoderConcurrency(8))
    }

    enc, err := zstd.NewWriter(nil, opts...)
    if err != nil {
        return nil, err
    }
    defer enc.Close()

    return enc.EncodeAll(data, nil), nil
}

// LZ4 — не поддерживает словари → просто игнорируем dict
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

// S2 — тоже не поддерживает → игнорируем
func s2Compress(data []byte, dict []byte) ([]byte, error) {
    return s2.Encode(nil, data), nil
}

func s2CompressBetter(data []byte, dict []byte) ([]byte, error) {
    return s2.EncodeBetter(nil, data), nil
}

func s2CompressBest(data []byte, dict []byte) ([]byte, error) {
    return s2.EncodeBest(nil, data), nil
}

func gzipCompress(data []byte, dict []byte) ([]byte, error) {
	var buf bytes.Buffer
	w, err := flate.NewWriter(&buf, flate.BestSpeed)
	if err != nil {
		return nil, err
	}
	_, err = w.Write(data)
	if err != nil {
		return nil, err
	}
	if err := w.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}


//Тренировка словаря
// zstd --train --maxdict=131072 --train-cover=k=32,d=8,steps=256 txs_pretrain.bin -o dictionary.zstd

// zstd --train --maxdict=131072 --train-cover=k=32,d=8,steps=256 tx_* -o ../dictionary.zstd
