// main.go
package main

import (
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/binary"
	"fmt"
	"io"
	mrand "math/rand"
	"os"
	"path/filepath"
	"time"

	"github.com/google/uuid"
	"github.com/klauspost/compress/gzip"
	"github.com/klauspost/compress/s2"
	"github.com/klauspost/compress/zstd"
	"github.com/pierrec/lz4/v4"
	"google.golang.org/protobuf/proto"
	"tx-generator/tx" // Проверьте правильность пути import
)

// CompressionFunc теперь принимает словарь (или nil)
type CompressionFunc func(data []byte, dict []byte) ([]byte, error)

// DecompressionFunc — функция распаковки (возвращает оригинальные байты)
type DecompressionFunc func(compressed []byte, dict []byte) ([]byte, error)

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
		tx.OpCode_META_NOOP: 			100,
		tx.OpCode_META_RESERVE: 		5,   
		tx.OpCode_ORD_CREATE: 			15_000, 
		tx.OpCode_ORD_CANCEL: 			10_000,
		tx.OpCode_ORD_CANCEL_ALL: 		100,   
		tx.OpCode_ORD_CANCEL_REPLACE: 	3_000,
		tx.OpCode_ORD_AMEND: 			25_000,
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
	
	var realTxCounter uint64 = 0	//счетчик реальных транзакций (с учетом батчинга)

	for opCode, count := range txCounts {
		for i := 0; i < count; i++ {
			u := users[mrand.Intn(len(users))]

			// Текущий таймстемп
			now := uint64(time.Now().Unix())

			header := &tx.TransactionHeader{
				ChainVersion:    0x01000001,
				OpCode:          opCode,
				AuthType:        tx.TxAuthType_UID,
				ExecutionMode:   tx.TxExecMode_DEFAULT,
				MarketCode:      tx.Markets_PERPETUAL,
				MarketSymbol:	 uint32(mrand.Intn(128)),	//случайный рынок, но с небольшой выборки, близко к реалу 
				SignerUid:       u.uid,
				Nonce:           u.nonce,
				MinHeight:       now - 5, // now - 5
				MaxHeight:       now + 5, // now + 5
				Signature:       make([]byte, 64), // placeholder
			}

			//txx := &tx.Transaction{Header: header}
			txx := &tx.Transaction{
				HeaderData: &tx.Transaction_Header{
					Header: header,
				},
			}

			switch opCode {
			case tx.OpCode_META_NOOP: 
				p := &tx.MetaNoopPayload{Payload: []byte{0x00}}
				txx.Payload = &tx.Transaction_MetaNoop{MetaNoop: p}
				
				//Для системных транзакций обнулим поля 
				header.MarketCode 	= tx.Markets_UNDEFINED
				header.MarketSymbol = 0
				
				realTxCounter++

			case tx.OpCode_META_RESERVE: 
				p := &tx.MetaReservePayload{Payload: []byte{0x00}}
				txx.Payload = &tx.Transaction_MetaReserve{MetaReserve: p}
				
				//Для системных транзакций обнулим поля 
				header.MarketCode 	= tx.Markets_UNDEFINED
				header.MarketSymbol = 0
				
				realTxCounter++

			case tx.OpCode_ORD_CREATE:
				// Случайным образом решаем, это одиночный ордер или батч
				itemsCount := 1
				if mrand.Intn(10) == 0 { // 10% шанс на батч
					itemsCount = mrand.Intn(11) + 2 // 2 to 6 orders
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
						ExecType:  tx.TimeInForce_GTC, // или рандом
						Quantity:  uint64(mrand.Intn(10000) + 100),
						Price:     price,
						// Опциональные поля можно заполнять по желанию
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

				p := &tx.OrderCancelPayload{
					OrderId: ids,
				}
				txx.Payload = &tx.Transaction_OrderCancel{OrderCancel: p}

			case tx.OpCode_ORD_CANCEL_ALL: 
				p := &tx.OrderCancelAllPayload{Payload: []byte{0x00}}
				txx.Payload = &tx.Transaction_OrderCancelAll{OrderCancelAll: p}
				realTxCounter++

			case tx.OpCode_ORD_CANCEL_REPLACE: 
				// Для Replace используем вложенный OrderCreatePayload с одним ордером
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

			case tx.OpCode_ORD_AMEND: // ORD_AMEND (поддерживает repeated AmendItem)
				itemsCount := 1
				if mrand.Intn(10) == 0 { 
					itemsCount = mrand.Intn(11) + 2
				}
				
				realTxCounter += uint64(itemsCount)

				amends := make([]*tx.AmendItem, 0, itemsCount)
				for k := 0; k < itemsCount; k++ {
					amend := &tx.AmendItem{
						OrderId: genUUIDv7(),
					}

					// Рандомно меняем Quantity или Price
					if mrand.Intn(2) == 0 {
						q := uint64(mrand.Intn(10000) + 100)
						amend.Quantity = &q
					}
					if mrand.Intn(2) == 0 {
						pr := uint64(mrand.Intn(100000) + 1000)
						amend.Price = &pr
					}
					// Если ничего не выпало, меняем Quantity
					if amend.Quantity == nil && amend.Price == nil {
						q := uint64(mrand.Intn(10000) + 100)
						amend.Quantity = &q
					}
					amends = append(amends, amend)
				}

				p := &tx.OrderAmendPayload{
					Amends: amends,
				}

				txx.Payload = &tx.Transaction_OrderAmend{OrderAmend: p}
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
	avgRealSize := float64(totalSize) / float64(realTxCounter)

	fmt.Printf("\nГенерация завершена:\n")
	fmt.Printf("  • Всего транзакций:        %d\n", totalTx)
	fmt.Printf("  • Всего real tx:        %d\n", realTxCounter)	
	fmt.Printf("  • Общий размер (байты, несжатый файл):    %d\n", totalSize)
	fmt.Printf("  • Средний размер tx:       %.1f байт\n", avgSize)
	fmt.Printf("  • Средний размер real tx:       %.1f байт\n", avgRealSize)
	

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
	
	/** Раскоментировать для генерации обучающего словаря 
	// Генерация чанков для создания словарей для zstd
	// Сохраняем по 10 транзакций в файл в папку blocks/
	err2 := SplitAndSaveTxBlocks(allTxBytes, "samples4", "tx_")
	if err2 != nil {
		fmt.Printf("Ошибка при разбиении на блоки: %v\n", err2)
		os.Exit(1)
	}
	**/	

	// Сжатие разными алгоритмами
	compressAndSave(allTxBytes, "zstd", zstdCompress, zstdDecompress, nil)
	compressAndSave(allTxBytes, "zstd-dict", zstdCompress, zstdDecompress, zstdDict)
	compressAndSave(allTxBytes, "lz4", lz4Compress, lz4Decompress, nil)
	compressAndSave(allTxBytes, "s2", s2Compress, s2Decompress, nil)
	compressAndSave(allTxBytes, "gzip", gzipCompress, gzipDecompress, nil)
}

// ──────────────────────────────────────────────────────────────
// Helpers
// ──────────────────────────────────────────────────────────────
// genUUIDv7 генерирует новый UUIDv7 и возвращает его в формате *tx.OrderID
func genUUIDv7() *tx.OrderID {
	id, err := uuid.NewV7()
	if err != nil {
		panic(fmt.Sprintf("failed to generate uuidv7: %v", err))
	}
	idBytes := id[:] // [16]byte -> []byte
	return &tx.OrderID{Id: idBytes}
}

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

// SplitAndSaveTxBlocks разбивает транзакции на блоки по 10 штук и сохраняет каждый блок
// в отдельный файл в указанной директории.
func SplitAndSaveTxBlocks(allTxBytes [][]byte, outputDir, prefix string) error {
	const txPerBlock = 10

	// Создаём директорию, если её нет
	if err := os.MkdirAll(outputDir, 0755); err != nil {
		return fmt.Errorf("не удалось создать директорию %s: %w", outputDir, err)
	}

	totalTx := len(allTxBytes)
	if totalTx == 0 {
		fmt.Println("Нет транзакций для сохранения")
		return nil
	}

	blockNum := 1
	for i := 0; i < totalTx; i += txPerBlock {
		end := i + txPerBlock
		if end > totalTx {
			end = totalTx
		}

		// Формируем имя файла: block-0001.bin, block-0002.bin и т.д.
		filename := filepath.Join(outputDir, fmt.Sprintf("%s%04d.bin", prefix, blockNum))
		f, err := os.Create(filename)
		if err != nil {
			return fmt.Errorf("не удалось создать файл %s: %w", filename, err)
		}

		// Записываем транзакции блока подряд
		blockSize := 0
		for j := i; j < end; j++ {
			n, err := f.Write(allTxBytes[j])
			if err != nil {
				f.Close()
				return fmt.Errorf("ошибка записи в %s: %w", filename, err)
			}
			blockSize += n
		}

		f.Close()

		fmt.Printf("Сохранён блок #%04d: %s (%d транзакций, %d байт)\n",
			blockNum, filename, end-i, blockSize)

		blockNum++
	}

	fmt.Printf("Всего создано блоков: %d (по %d транзакций в каждом, последний может быть меньше)\n",
		blockNum-1, txPerBlock)

	return nil
}

// compressAndSave — с замерами и сжатия, и распаковки
func compressAndSave(
	allTxBytes [][]byte,
	algoName string,
	compressFunc CompressionFunc,
	decompressFunc DecompressionFunc,
	dict []byte, // nil = без словаря
) {
	var buf bytes.Buffer
	for _, b := range allTxBytes {
		buf.Write(b)
	}
	raw := buf.Bytes()
	origSize := len(raw)

	// Сжатие
	startCompress := time.Now()
	compressed, err := compressFunc(raw, dict)
	if err != nil {
		fmt.Printf("Ошибка сжатия %s: %v\n", algoName, err)
		return
	}
	compressDuration := time.Since(startCompress).Milliseconds()
	compSize := len(compressed)

	// Распаковка (проверяем корректность + замер времени)
	startDecompress := time.Now()
	decompressed, err := decompressFunc(compressed, dict)
	if err != nil {
		fmt.Printf("Ошибка распаковки %s: %v\n", algoName, err)
		return
	}
	decompressDuration := time.Since(startDecompress).Milliseconds()

	// Проверка целостности
	if !bytes.Equal(raw, decompressed) {
		fmt.Printf("Ошибка: распакованные данные не совпадают с оригиналом (%s)!\n", algoName)
		return
	}

	ratio := float64(origSize) / float64(compSize)
	savings := 100 * (1 - float64(compSize)/float64(origSize))

	filename := fmt.Sprintf("txs.bin.%s", algoName)
	if len(dict) > 0 {
		filename += ".dict"
	}

	if err := os.WriteFile(filename, compressed, 0644); err != nil {
		panic(err)
	}

	fmt.Printf("Алгоритм %-10s → %s (%d байт)\n", algoName, filename, compSize)
	fmt.Printf("   Коэффициент: %.2fx   Экономия: %.1f%%\n", ratio, savings)
	fmt.Printf("   Сжатие:   %4d мс    Распаковка: %4d мс\n\n", compressDuration, decompressDuration)
}

// ──────────────────────────────────────────────────────────────
// Разные функции сжатия (можно легко добавлять новые)
// ──────────────────────────────────────────────────────────────

func zstdCompress(data []byte, dict []byte) ([]byte, error) {
	opts := []zstd.EOption{zstd.WithEncoderLevel(zstd.SpeedFastest)} //Default
	if dict != nil {
		opts = append(opts, zstd.WithEncoderDict(dict))
	}

	opts = append(opts, zstd.WithEncoderConcurrency(8))

	enc, err := zstd.NewWriter(nil, opts...)
	if err != nil {
		return nil, err
	}
	defer enc.Close()

	return enc.EncodeAll(data, nil), nil
}

func zstdDecompress(compressed, dict []byte) ([]byte, error) {
	opts := []zstd.DOption{}
	if len(dict) > 0 {
		opts = append(opts, zstd.WithDecoderDicts(dict))
	}

	opts = append(opts, zstd.WithDecoderConcurrency(8))

	dec, err := zstd.NewReader(nil, opts...)
	if err != nil {
		return nil, err
	}
	defer dec.Close()
	return dec.DecodeAll(compressed, nil)
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

func lz4Decompress(compressed, dict []byte) ([]byte, error) {
	//dst := make([]byte, 0, len(compressed)*10) // грубая оценка

	maxUncompressed := len(compressed) * 10
	if maxUncompressed < 1024*1024 { // минимум 1 МБ для безопасности
		maxUncompressed = 1024 * 1024
	}

	// Шаг 2: Выделяем буфер достаточного размера
	dst := make([]byte, maxUncompressed)

	//var d lz4.Decompressor
	n, err := lz4.UncompressBlock(compressed, dst)
	if err != nil {
		return nil, err
	}
	return dst[:n], nil
}

// S2 — тоже не поддерживает → игнорируем
func s2Compress(data []byte, dict []byte) ([]byte, error) {
	return s2.Encode(nil, data), nil
}

func s2Decompress(compressed, dict []byte) ([]byte, error) {
	return s2.Decode(nil, compressed)
}

func gzipCompress(data []byte, dict []byte) ([]byte, error) {
	var buf bytes.Buffer
	w := gzip.NewWriter(&buf)
	_, err := w.Write(data)
	if err != nil {
		return nil, err
	}
	if err := w.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func gzipDecompress(compressed []byte, dict []byte) ([]byte, error) {
	r, err := gzip.NewReader(bytes.NewReader(compressed))
	if err != nil {
		return nil, err
	}
	defer r.Close()

	var out bytes.Buffer
	_, err = io.Copy(&out, r)
	if err != nil {
		return nil, err
	}
	return out.Bytes(), nil
}



//Перегенерация прото 
// protoc --go_out=. tx.proto


//Тренировка словаря
// zstd --train --maxdict=131072 --train-cover=k=32,d=8,steps=256 txs_pretrain.bin -o dictionary.zstd

// zstd --train --maxdict=131072 --train-cover=k=32,d=8,steps=256 tx_* -o ../dictionary_v4.zstd

// zstd --train --maxdict=131072 --train-cover=k=32,d=8,steps=256 tx_* -o ../dictionary_v7.zstd

// 1048576
