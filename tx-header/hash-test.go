package main

import (
	"crypto/sha256"
	"fmt"
	"math/rand"
	"testing"
	"time"
	
	"tx-generator/tx"

	"github.com/zeebo/blake3"
	"google.golang.org/protobuf/proto"
)

// Протобаф структуры здесь (скопировать из предоставленного tx.proto и скомпилировать с protoc для Go).
// Предполагаю, что у вас есть сгенерированный tx.pb.go с сообщениями как Transaction, OrderCreatePayload и т.д.
// Для простоты, я использую placeholder для proto marshal; замените на реальный import вашего пакета.
// import "/tx"  // Ваш go_package

// Функция для генерации случайной транзакции (OrderCreate для примера).
func generateRandomTx() []byte {
	// Создаём header.
	header := &tx.TransactionHeader{
		ChainVersion:   rand.Uint32(),
		PayloadSize:    uint32(rand.Intn(100) + 1), // Маленький payload.
		ReservedFlag:   rand.Uint32(),
		OpCode:         0x60, // ORD_CREATE.
		AuthType:       rand.Uint32() % 4,
		ExecutionMode:  rand.Uint32() % 6,
		ReservedPadding: rand.Uint32(),
		MarketCode:     rand.Uint32(),
		SignerUid:      rand.Uint64(),
		Nonce:          rand.Uint64(),
		MinHeight:      rand.Uint64(),
		MaxHeight:      rand.Uint64(),
		Signature:      make([]byte, 64), // Фикс 64 байта.
	}
	rand.Read(header.Signature) // Заполняем случайными.

	// Payload для ORD_CREATE.
	payload := &tx.OrderCreatePayload{
		IsBuy:            rand.Intn(2) == 1,
		IsMarket:         rand.Intn(2) == 1,
		Quantity:         rand.Uint64(),
		Price:            rand.Uint64(),
		Flags:            rand.Uint32(),
		UserGeneratedId:  make([]byte, 16),
	}
	rand.Read(payload.UserGeneratedId)

	// Полная tx.
	tx := &tx.Transaction{
		Header: header,
		Payload: &tx.Transaction_OrderCreate{OrderCreate: payload}, // oneof.
	}

	// Marshal в bytes (сериализованная tx для hashing, как в txHash).
	data, err := proto.Marshal(tx)
	if err != nil {
		panic(err)
	}
	return data
}

// Бенчмарк для SHA-256.
func BenchmarkSHA256(b *testing.B) {
	// Генерируем 100k tx заранее (чтобы не мерять генерацию).
	//txs := make([][]byte, 50000)
	//for i := range txs {
	//	txs[i] = generateRandomTx()
	//}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		for _, tx := range txs {
			_ = sha256.Sum256(tx)
		}
	}
}

// Бенчмарк для BLAKE3.
func BenchmarkBLAKE3(b *testing.B) {
	//txs := make([][]byte, 50000)
	//for i := range txs {
	//	txs[i] = generateRandomTx()
	//}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		for _, tx := range txs {
			h := blake3.New()
			h.Write(tx)
			_ = h.Sum(nil)
		}
	}
}

// Оптимизированный BLAKE3 с reuse hasher (для real-world в loops).
func BenchmarkBLAKE3Reuse(b *testing.B) {
	//txs := make([][]byte, 50000)
	//for i := range txs {
	//	txs[i] = generateRandomTx()
	//}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		h := blake3.New()
		for _, tx := range txs {
			h.Reset() // Reuse для speedup на small inputs.
			h.Write(tx)
			_ = h.Sum(nil)
		}
	}
}


var txs [][]byte

func main() {
	
	txs := make([][]byte, 50000)
	for i := range txs {
		txs[i] = generateRandomTx()
	}
	
	fmt.Printf("Prepared %d transactions\n", len(txs))
	
	
	
	// Запуск бенчмарков (go test -bench . или вручную).
	// Для примера, симулируем вывод.
	start := time.Now()
	res := testing.Benchmark(BenchmarkSHA256)
	fmt.Printf("SHA256_v1: %s, %d ns/op\n", res.T, res.NsPerOp())

	res = testing.Benchmark(BenchmarkBLAKE3)
	fmt.Printf("BLAKE3_v1: %s, %d ns/op\n", res.T, res.NsPerOp())

	res = testing.Benchmark(BenchmarkBLAKE3Reuse)
	fmt.Printf("BLAKE3_v2: %s, %d ns/op\n", res.T, res.NsPerOp())

	fmt.Printf("Elapsed: %v\n", time.Since(start))
}