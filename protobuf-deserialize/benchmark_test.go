package benchmark

import (
	"crypto/rand"
	"fmt"
	"math/big"
	"sync"
	"testing"
	"time"
)

// Генерация случайного ордера
func generateRandomOrder() *Order {
	sig := make([]byte, 32)
	rand.Read(sig)
	
	sym := make([]byte, 4)
	rand.Read(sym)
	
	userOrderID := make([]byte, 16)
	rand.Read(userOrderID)
	
	randUint64 := func() uint64 {
		n, _ := rand.Int(rand.Reader, big.NewInt(1<<62))
		return n.Uint64()
	}
	
	randUint32 := func() uint32 {
		n, _ := rand.Int(rand.Reader, big.NewInt(256))
		return uint32(n.Uint64())
	}
	
	return &Order{
		Uid:           randUint64(),
		Signature:     sig,
		OpCode:        randUint32(),
		Symbol:        sym,
		Nonce:         randUint64(),
		Flags:         randUint32(),
		Price:         randUint64(),
		Amount:        randUint64(),
		OrdersFlag:    randUint32(),
		UserOrderId:   string(userOrderID),
		LinkedOrderId: randUint64(),
	}
}

// Генерация и сериализация тестовых данных
func generateSerializedOrders(count int) [][]byte {
	serialized := make([][]byte, count)
	for i := 0; i < count; i++ {
		order := generateRandomOrder()
		data, _ := order.MarshalVT()
		serialized[i] = data
	}
	return serialized
}

// Десериализация с одним потоком
func deserializeSingle(data [][]byte) ([]*Order, time.Duration) {
	start := time.Now()
	orders := make([]*Order, len(data))
	
	for i, d := range data {
		order := &Order{}
		_ = order.UnmarshalVT(d)
		orders[i] = order
	}
	
	elapsed := time.Since(start)
	return orders, elapsed
}

// Десериализация с несколькими воркерами
func deserializeParallel(data [][]byte, workers int) ([]*Order, time.Duration) {
	start := time.Now()
	orders := make([]*Order, len(data))
	
	var wg sync.WaitGroup
	chunkSize := len(data) / workers
	if chunkSize == 0 {
		chunkSize = 1
	}
	
	for w := 0; w < workers; w++ {
		wg.Add(1)
		startIdx := w * chunkSize
		endIdx := startIdx + chunkSize
		if w == workers-1 {
			endIdx = len(data)
		}
		
		go func(start, end int) {
			defer wg.Done()
			for i := start; i < end; i++ {
				order := &Order{}
				_ = order.UnmarshalVT(data[i])
				orders[i] = order
			}
		}(startIdx, endIdx)
	}
	
	wg.Wait()
	elapsed := time.Since(start)
	return orders, elapsed
}

// Основной бенчмарк
func TestOrderDeserialization(t *testing.T) {
	const orderCount = 100000
	
	fmt.Println("=== Protobuf Order Deserialization Benchmark ===")
	fmt.Printf("Generating %d random orders...\n", orderCount)
	
	serializedOrders := generateSerializedOrders(orderCount)
	totalSize := 0
	for _, data := range serializedOrders {
		totalSize += len(data)
	}
	
	fmt.Printf("Total serialized size: %.2f MB\n", float64(totalSize)/(1024*1024))
	fmt.Printf("Average message size: %d bytes\n\n", totalSize/orderCount)
	
	// Тест с одним потоком
	fmt.Println("--- Single Thread ---")
	_, singleElapsed := deserializeSingle(serializedOrders)
	fmt.Printf("Total time: %v\n", singleElapsed)
	fmt.Printf("Average per message: %v\n", singleElapsed/time.Duration(orderCount))
	fmt.Printf("Throughput: %.0f msg/sec\n\n", float64(orderCount)/singleElapsed.Seconds())
	
	// Тесты с разным количеством воркеров
	workerCounts := []int{4, 8, 16, 32, 48}
	
	fmt.Println("Worker Tests:")
	fmt.Println("Workers | Total Time | Avg/Msg | Throughput | Speedup")
	fmt.Println("--------|------------|---------|------------|--------")
	
	for _, workers := range workerCounts {
		_, elapsed := deserializeParallel(serializedOrders, workers)
		avgPerMsg := elapsed / time.Duration(orderCount)
		throughput := float64(orderCount) / elapsed.Seconds()
		speedup := float64(singleElapsed) / float64(elapsed)
		
		fmt.Printf("%-7d | %-10v | %-7v | %10.0f | %.2fx\n",
			workers, elapsed, avgPerMsg, throughput, speedup)
	}
}

// Стандартные Go бенчмарки
func BenchmarkDeserializeSingle(b *testing.B) {
	order := generateRandomOrder()
	data, _ := order.MarshalVT()
	
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		o := &Order{}
		_ = o.UnmarshalVT(data)
	}
}

func BenchmarkDeserializeParallel4(b *testing.B) {
	benchmarkParallelDeserialize(b, 4)
}

func BenchmarkDeserializeParallel8(b *testing.B) {
	benchmarkParallelDeserialize(b, 8)
}

func BenchmarkDeserializeParallel16(b *testing.B) {
	benchmarkParallelDeserialize(b, 16)
}

func BenchmarkDeserializeParallel32(b *testing.B) {
	benchmarkParallelDeserialize(b, 32)
}

func BenchmarkDeserializeParallel48(b *testing.B) {
	benchmarkParallelDeserialize(b, 48)
}

func benchmarkParallelDeserialize(b *testing.B, workers int) {
	serializedOrders := generateSerializedOrders(1000)
	
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		deserializeParallel(serializedOrders, workers)
	}
}
