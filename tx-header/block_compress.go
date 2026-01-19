// main.go
package main

import (
	"bytes"
	"crypto/ed25519"
	voied25519 "github.com/oasisprotocol/curve25519-voi/primitives/ed25519"
	"crypto/rand"
	"fmt"
	"io"
	mrand "math/rand"
	"os"
	"time"
	
	"encoding/binary"

	"github.com/google/uuid"
	"github.com/klauspost/compress/gzip"
	"github.com/klauspost/compress/s2"
	"github.com/klauspost/compress/zstd"
	"github.com/pierrec/lz4/v4"
	"github.com/zeebo/blake3" // Подключаем BLAKE3
	"google.golang.org/protobuf/proto"
	
	tx "tx-generator/tx"

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
	expKey *voied25519.ExpandedPublicKey // Для супер-быстрой проверки (1.5 КБ)
	nonce uint64
}

// Константы 
const (
	//Для нового формата блока с транзакциями 
	BlockMagicNumber = 0xBA	//Заголовок нашего формата
	SigSize          = 64
	
	TxFrameSeparator = 0xEE	//Разделитель протобафов 
)

// ID Алгоритмов сжатия (1 байт)
const (
	AlgoNone = 0
	AlgoZstd = 1
	AlgoLZ4  = 2
	AlgoS2   = 3
	AlgoGzip = 4	//Not supported now
)

// EncodeBlock создает самодостаточный бинарный блок
func EncodeBlock(txs []*tx.Transaction, algoID uint8, dict []byte) ([]byte, error) {
	count := uint64(len(txs))

	// 1. Подготовка буферов
	sigBuf := make([]byte, count*SigSize)
	rawBodyBuf := bytes.NewBuffer(make([]byte, 0, count*150))
	var lenBuf [10]byte // Для Varint

	// 2. Разделение (Split) и Фрейминг (Framing)
	for i, txx := range txs {
		header := txx.GetHeader()
		if header == nil { return nil, fmt.Errorf("tx %d header is nil", i) }

		// A. Signatures
		if len(header.Signature) != SigSize {
			if len(header.Signature) == 0 {
				// Заполним нулями, если пусто (чтобы не ломать структуру)
			} else {
				return nil, fmt.Errorf("bad sig len at %d", i)
			}
		}
		copy(sigBuf[i*SigSize:], header.Signature)

		// B. Clean Body
		originalSig := header.Signature
		header.Signature = nil

		pbBytes, err := proto.Marshal(txx)
		if err != nil { return nil, err }

		header.Signature = originalSig // Возвращаем на место

		// C. Framing [0xEE] [Len] [Body]
		rawBodyBuf.WriteByte(TxFrameSeparator)
		n := binary.PutUvarint(lenBuf[:], uint64(len(pbBytes)))
		rawBodyBuf.Write(lenBuf[:n])
		rawBodyBuf.Write(pbBytes)
	}

	src := rawBodyBuf.Bytes()
	uncompressedSize := uint64(len(src))

	// 3. Сжатие
	var compressedBody []byte
	var err error

	switch algoID {
	case AlgoZstd:
		compressedBody, err = zstdCompress(src, dict)
	case AlgoLZ4:
		compressedBody, err = lz4Compress(src, nil)
	case AlgoS2:
		compressedBody, err = s2Compress(src, nil)
	case AlgoGzip:
		compressedBody, err = gzipCompress(src, nil)
	case AlgoNone:
		compressedBody = src
	default:
		return nil, fmt.Errorf("unsupported algo id: %d", algoID)
	}
	
	if err != nil { return nil, err }

	compressedSize := uint64(len(compressedBody))

	// 4. Сборка заголовка и тела
	// Header size: 1 (Magic) + 1 (Algo) + 8 (Count) + 8 (UncompSize) + 8 (CompSize) = 26 bytes
	finalSize := 26 + len(sigBuf) + len(compressedBody)
	finalBuf := bytes.NewBuffer(make([]byte, 0, finalSize))

	// HEADER
	finalBuf.WriteByte(BlockMagicNumber)
	finalBuf.WriteByte(algoID)                         // Байт алгоритма
	binary.Write(finalBuf, binary.LittleEndian, count)
	binary.Write(finalBuf, binary.LittleEndian, uncompressedSize)
	binary.Write(finalBuf, binary.LittleEndian, compressedSize) // Размер сжатых данных

	// PAYLOAD
	finalBuf.Write(sigBuf)
	finalBuf.Write(compressedBody)

	return finalBuf.Bytes(), nil
}

// DecodeBlock читает заголовок, определяет алгоритм и распаковывает
func DecodeBlock(data []byte, dict []byte) ([]*tx.Transaction, error) {
	buf := bytes.NewReader(data)

	// 1. Читаем Заголовок (26 байт)
	magic, err := buf.ReadByte()
	if err != nil { return nil, err }
	if magic != BlockMagicNumber { return nil, fmt.Errorf("bad magic: %x", magic) }

	algoID, err := buf.ReadByte() // Читаем ID алгоритма
	if err != nil { return nil, err }

	var count, uncompressedSize, compressedSize uint64
	if err := binary.Read(buf, binary.LittleEndian, &count); err != nil { return nil, err }
	if err := binary.Read(buf, binary.LittleEndian, &uncompressedSize); err != nil { return nil, err }
	if err := binary.Read(buf, binary.LittleEndian, &compressedSize); err != nil { return nil, err }

	// 2. Читаем Подписи
	sigBlockSize := int(count) * SigSize
	sigBlock := make([]byte, sigBlockSize)
	if _, err := io.ReadFull(buf, sigBlock); err != nil { return nil, err }

	// 3. Читаем Сжатое Тело (ровно compressedSize байт)
	// В сетевом коде это будет io.ReadFull(conn, buffer)
	compressedBody := make([]byte, compressedSize)
	if _, err := io.ReadFull(buf, compressedBody); err != nil { return nil, err }

	// 4. Распаковка (Switch по algoID из заголовка!)
	var rawBody []byte
	
	switch algoID {
	case AlgoZstd:
		rawBody, err = zstdDecompress(compressedBody, dict)
	case AlgoLZ4:
		// Для LZ4 критически важно знать uncompressedSize
		rawBody = make([]byte, uncompressedSize)
		var n int
		n, err = lz4.UncompressBlock(compressedBody, rawBody)
		if err == nil { rawBody = rawBody[:n] }
	case AlgoS2:
		rawBody, err = s2Decompress(compressedBody, nil)
	case AlgoGzip:
		rawBody, err = gzipDecompress(compressedBody, nil)
	case AlgoNone:
		rawBody = compressedBody
	default:
		return nil, fmt.Errorf("unknown compression algo in header: %d", algoID)
	}

	if err != nil { return nil, fmt.Errorf("decompress error: %v", err) }
	
	if uint64(len(rawBody)) != uncompressedSize {
		return nil, fmt.Errorf("size mismatch: header said %d, got %d", uncompressedSize, len(rawBody))
	}

	// 5. Парсинг (Framing + Merge)
	result := make([]*tx.Transaction, count)
	bodyReader := bytes.NewReader(rawBody)

	for i := uint64(0); i < count; i++ {
		// A. Check Separator
		sep, err := bodyReader.ReadByte()
		if err != nil { return nil, err }
		if sep != TxFrameSeparator {
			return nil, fmt.Errorf("framing error at tx %d", i)
		}

		// B. Read Len
		l, err := binary.ReadUvarint(bodyReader)
		if err != nil { return nil, err }

		// C. Read Proto
		pbBytes := make([]byte, l)
		if _, err := io.ReadFull(bodyReader, pbBytes); err != nil { return nil, err }

		// D. Unmarshal
		t := &tx.Transaction{}
		if err := proto.Unmarshal(pbBytes, t); err != nil { return nil, err }

		// E. Inject Sig
		sigStart := int(i) * SigSize
		mySig := make([]byte, SigSize)
		copy(mySig, sigBlock[sigStart : sigStart+SigSize])
		
		if t.GetHeader() == nil {
			t.HeaderData = &tx.Transaction_Header{Header: &tx.TransactionHeader{}}
		}
		t.GetHeader().Signature = mySig
		
		result[i] = t
	}

	return result, nil
}



// Новый бенчмарк для сжатия
func benchmarkSplitCompression(transactions []*tx.Transaction, dict []byte) {
	fmt.Println("\n=== BLOCK COMPRESSION TEST (V2 Header: AlgoID + Size) ===")
	
	// Теперь мапим на uint8 ID
	algos := []struct {
		name string
		algoID uint8 
		dict []byte
	}{
		{"None",   AlgoNone, nil},
		{"LZ4",    AlgoLZ4,  nil},
		{"S2",     AlgoS2,   nil},
		{"Gzip",   AlgoGzip, nil},
		{"Zstd",   AlgoZstd, nil},
		// Словарь передаем только в Encode, Decode возьмет его сам если алгоритм Zstd
		// Но в моей реализации Decode принимает dict аргументом "на всякий случай" для Zstd.
		{"ZstdDict", AlgoZstd, dict}, 
	}

	for _, a := range algos {
		// 1. Encode
		start := time.Now()
		// Передаем ID алгоритма
		compressedData, err := EncodeBlock(transactions, a.algoID, a.dict)
		if err != nil { 
			fmt.Printf("[%s] Encode Error: %v\n", a.name, err)
			continue 
		}
		encodeDur := time.Since(start)

		// 2. Decode
		// Обратите внимание: мы НЕ передаем a.algoID. 
		// Функция сама прочитает байт алгоритма из заголовка.
		startDec := time.Now()
		decodedTxs, err := DecodeBlock(compressedData, a.dict)
		if err != nil { 
			fmt.Printf("[%s] Decode Error: %v\n", a.name, err)
			continue 
		}
		decodeDur := time.Since(startDec)

		// Stats
		sizeKB := float64(len(compressedData)) / 1024.0

		fmt.Printf("[%s]\n", a.name)
		fmt.Printf("  Size:   %.2f KB\n", sizeKB)
		fmt.Printf("  Encode: %v\n", encodeDur)
		fmt.Printf("  Decode: %v\n", decodeDur)
		
		// Integrity check
		if len(decodedTxs) > 0 {
			orig := transactions[0].GetHeader().Signature
			dec := decodedTxs[0].GetHeader().Signature
			if !bytes.Equal(orig, dec) {
				fmt.Println("  ERROR: Sig Mismatch")
			}
		}
	}
}

// compressAndSaveBlockV2 кодирует транзакции в формат V2 и сохраняет в файл
// Возвращает размер записанного файла (для расчета ratio в следующих вызовах)
func compressAndSaveBlockV2(filename string, txs []*tx.Transaction, algo uint8, dict []byte, baselineSize int) int {
	start := time.Now()
	
	// 1. Кодируем блок (Split Sig/Body)
	encodedBytes, err := EncodeBlock(txs, algo, dict)
	if err != nil {
		fmt.Printf("Ошибка кодирования %s: %v\n", filename, err)
		return 0
	}
	dur := time.Since(start)

	// 2. Пишем на диск
	if err := os.WriteFile(filename, encodedBytes, 0644); err != nil {
		fmt.Printf("Ошибка записи %s: %v\n", filename, err)
		return 0
	}

	size := len(encodedBytes)
	
	// 3. Вывод статистики
	ratioString := "1.00x"
	if baselineSize > 0 {
		// Считаем коэффициент сжатия относительно несжатого V2 блока
		ratio := float64(baselineSize) / float64(size)
		ratioString = fmt.Sprintf("%.2fx", ratio)
	}

	//algoName := filename // упрощенно берем из имени
	fmt.Printf("Saved %-25s | Size: %9.2f KB | Ratio: %s | Time: %v\n", 
		filename, float64(size)/1024.0, ratioString, dur)
	
	return size
}



func main() {
	var zstdDict []byte
	if data, err := os.ReadFile("./dictionary_v8.zstd"); err == nil {
		zstdDict = data
		fmt.Println("Словарь zstd_v8 загружен:", len(zstdDict), "байт")
	} else {
		fmt.Println("Словарь zstd_v8 не найден — сжимаем без него")
	}

	mrand.Seed(time.Now().UnixNano())
	
	// OpCodes:
	txCounts := map[tx.OpCode]int{
		tx.OpCode_META_NOOP:           100,
		tx.OpCode_META_RESERVE:        50,
		tx.OpCode_ORD_CREATE:          14_000,
		tx.OpCode_ORD_CANCEL:          10_000,
		tx.OpCode_ORD_CANCEL_ALL:      1000,
		tx.OpCode_ORD_CANCEL_REPLACE:  3_000,
		tx.OpCode_ORD_AMEND:           22_000,
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
	
	/****
	// Раскоментировать для генерации обучающего словаря 
	// Генерация чанков для создания словарей для zstd
	// Сохраняем по 10 транзакций в файл в папку blocks/
	err2 := SplitAndSaveTxBlocks(allTxsStructs, "samples5", "tx_")
	if err2 != nil {
		fmt.Printf("Ошибка при разбиении на блоки: %v\n", err2)
		os.Exit(1)
	}
	*****/	
	
	/** Новый формат блока для сжатия
		[ 1 byte  ] Magic Number (0xBA)
		[ 1 byte  ] Compression Algo ID (0-4)
		[ 8 bytes ] Transaction Count (N)
		[ 8 bytes ] Uncompressed Body Size (для аллокации буфера)
		[ 8 bytes ] Compressed Body Size (для чтения из сокета)
		-------------------------------------------------------
		[ N * 64  ] Signatures (плоский массив)
		[ K bytes ] Compressed Body
	**/
	
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

	// Сжатие
	compressAndSave(allTxBytes, "zstd", 		zstdCompress, 	zstdDecompress, nil)
	compressAndSave(allTxBytes, "zstd-dict", 	zstdCompress, 	zstdDecompress, zstdDict)
	compressAndSave(allTxBytes, "lz4", 			lz4Compress, 	lz4Decompress, nil)
	compressAndSave(allTxBytes, "S2", 			s2Compress, 	s2Decompress, nil)
	
	
	//TODO: оптимизировать структуру, сжимая транзакции но не подписи 
	fmt.Println("\n=== СОХРАНЕНИЕ БЛОКОВ V2 (SPLIT FORMAT) ===")
	
	
    // 1. Базовый файл (без сжатия) - нужен как эталон размера для ratio
    baseSize := compressAndSaveBlockV2("txs_v2.bin", allTxsStructs, AlgoNone, nil, 0)

    // 2. Zstd (стандартный)
    compressAndSaveBlockV2("txs_v2.bin.zstd", allTxsStructs, AlgoZstd, nil, baseSize)

    // 3. Zstd + Dictionary (если словарь загружен)
    if len(zstdDict) > 0 {
        compressAndSaveBlockV2("txs_v2.bin.zstd_dict", allTxsStructs, AlgoZstd, zstdDict, baseSize)
    }

    // 4. LZ4
    compressAndSaveBlockV2("txs_v2.bin.lz4", allTxsStructs, AlgoLZ4, nil, baseSize)

    // 6. S2
    compressAndSaveBlockV2("txs_v2.bin.s2", allTxsStructs, AlgoS2, nil, baseSize)
	
	
	
	
	// Запуск теста нового формата сжатия (Split Sig/Body)
    benchmarkSplitCompression(allTxsStructs, zstdDict)
	
	
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

// SplitAndSaveTxBlocks сохраняет образцы данных для тренировки словаря Zstd.
// Важно: мы сохраняем ТОЛЬКО тела транзакций (без подписей), так как словарь должен
// учиться сжимать структуру Protobuf, а не рандомные байты криптографии.
// SplitAndSaveTxBlocks генерирует образцы для обучения Zstd.
// Формат образца: [0xEE][VarintLen][ProtoBody]... (повторяется N раз)
func SplitAndSaveTxBlocks(txs []*tx.Transaction, outputDir string, prefix string) error {
	// Создаем папку
	if err := os.MkdirAll(outputDir, 0755); err != nil {
		return err
	}

	const samplesCount = 1000 // Количество файлов-образцов
	const txPerSample = 50   // Транзакций в одном файле

	// Проверка наличия данных
	if len(txs) < samplesCount*txPerSample {
		fmt.Printf("Warning: Not enough txs for training (have %d, want %d). Using all available.\n", 
			len(txs), samplesCount*txPerSample)
	}

	fmt.Printf("Генерация образцов (с фреймингом 0x%X + Varint) в '%s'...\n", TxFrameSeparator, outputDir)

	// Буфер для кодирования длины (Varint)
	var lenBuf [10]byte
	
	// Общий счетчик транзакций
	txIndex := 0

	for i := 0; i < samplesCount; i++ {
		if txIndex >= len(txs) { break }

		var sampleBuf bytes.Buffer

		// Собираем пачку транзакций в один файл
		for j := 0; j < txPerSample; j++ {
			if txIndex >= len(txs) { break }
			
			txx := txs[txIndex]
			txIndex++

			header := txx.GetHeader()
			
			// 1. Убираем подпись (чтобы учить словарь только на структуре)
			originalSig := header.Signature
			header.Signature = nil

			// 2. Маршалим
			pbBytes, err := proto.Marshal(txx)
			if err != nil { return err }

			// 3. Возвращаем подпись (на место)
			header.Signature = originalSig

			// 4. ПИШЕМ ФРЕЙМИНГ (ТОЧНО КАК В ENCODEBLOCK)
			
			// А. Разделитель
			sampleBuf.WriteByte(TxFrameSeparator)

			// Б. Длина (Varint)
			n := binary.PutUvarint(lenBuf[:], uint64(len(pbBytes)))
			sampleBuf.Write(lenBuf[:n])

			// В. Тело
			sampleBuf.Write(pbBytes)
		}

		// Сохраняем файл
		fileName := fmt.Sprintf("%s/%s%03d", outputDir, prefix, i)
		if err := os.WriteFile(fileName, sampleBuf.Bytes(), 0644); err != nil {
			return err
		}
	}
	
	fmt.Printf("Готово. Сохранено %d файлов. Теперь запустите:\n", samplesCount)
	fmt.Printf("zstd --train %s/* -o dictionary_v7_framed.zstd --maxdict=110KB\n", outputDir)
	return nil
}
