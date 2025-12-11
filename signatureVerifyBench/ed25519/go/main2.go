package main

import (
	"crypto/rand"
	"crypto/sha256"
	"fmt"
	"log"
	"runtime"
	"sync"
	"sync/atomic"
	"time"

	"github.com/oasisprotocol/curve25519-voi/primitives/ed25519"
)

// --- КОНФИГУРАЦИЯ ---
const (
	NumUniqueKeys = 30_000 // Количество уникальных ключей
	NumMessages   = 50_000 // Общее количество сообщений
	BatchSize     = 512    // Размер батча
	NumWorkers    = 48     // Количество потоков
	MsgsPerKey    = 4      // Сколько сообщений генерируем на один ключ
	
	// Новые параметры для повторных запусков
	NumIterations   = 10_000 // Количество итераций теста
	IntervalMs      = 400    // Интервал между итерациями в миллисекундах
)

// SignedItem - структура данных
type SignedItem struct {
	PubKey ed25519.PublicKey
	Msg    []byte
	Sig    []byte
}

// TestStats - статистика одного запуска теста
type TestStats struct {
	VerifyDuration time.Duration
	PerSignature   float64
}

func main() {
	runtime.GOMAXPROCS(runtime.NumCPU())
	fmt.Printf("=== BENCHMARK: VOI + REPEATED TESTS ===\n")
	fmt.Printf("CPU Cores: %d\n", runtime.NumCPU())
	fmt.Printf("Workers: %d\n", NumWorkers)
	fmt.Printf("Batch Size: %d\n", BatchSize)
	fmt.Printf("Msgs Per Key: %d\n", MsgsPerKey)
	fmt.Printf("Iterations: %d\n", NumIterations)
	fmt.Printf("Interval: %d ms\n", IntervalMs)
	fmt.Println("=========================================")

	// --- ЭТАП 1: Генерация ключей (один раз) ---
	startGen := time.Now()
	keyRing := make([]ed25519.PrivateKey, NumUniqueKeys)
	pubKeyRing := make([]ed25519.PublicKey, NumUniqueKeys)
	fmt.Print("1. Generating keys... ")
	
	for i := 0; i < NumUniqueKeys; i++ {
		pub, priv, err := ed25519.GenerateKey(rand.Reader)
		if err != nil {
			log.Fatalf("Key gen failed: %v", err)
		}
		keyRing[i] = priv
		pubKeyRing[i] = pub
	}
	fmt.Printf("DONE in %v\n", time.Since(startGen))

	// --- ЭТАП 2: Подпись сообщений (один раз) ---
	startSign := time.Now()
	allItems := make([]SignedItem, NumMessages)
	fmt.Print("2. Signing messages... ")
	
	var wgGen sync.WaitGroup
	chunkSize := NumMessages / runtime.NumCPU()
	if chunkSize == 0 {
		chunkSize = NumMessages
	}
	
	for i := 0; i < runtime.NumCPU(); i++ {
		startIdx := i * chunkSize
		endIdx := startIdx + chunkSize
		if i == runtime.NumCPU()-1 {
			endIdx = NumMessages
		}
		if startIdx >= endIdx {
			continue
		}
		
		wgGen.Add(1)
		go func(start, end int) {
			defer wgGen.Done()
			h := sha256.New()
			for j := start; j < end; j++ {
				keyIdx := (j / MsgsPerKey) % NumUniqueKeys
				h.Reset()
				h.Write([]byte(fmt.Sprintf("msg-%d", j)))
				msgHash := h.Sum(nil)
				sig := ed25519.Sign(keyRing[keyIdx], msgHash)
				allItems[j] = SignedItem{
					PubKey: pubKeyRing[keyIdx],
					Msg:    msgHash,
					Sig:    sig,
				}
			}
		}(startIdx, endIdx)
	}
	wgGen.Wait()
	fmt.Printf("DONE in %v\n", time.Since(startSign))

	// --- ЭТАП 3: Повторные запуски верификации ---
	fmt.Printf("\n3. Starting %d verification iterations...\n", NumIterations)
	
	stats := make([]TestStats, 0, NumIterations)
	startAllTests := time.Now()
	
	for iteration := 0; iteration < NumIterations; iteration++ {
		if iteration > 0 {
			time.Sleep(time.Duration(IntervalMs) * time.Millisecond)
		}
		
		testStat := runVerificationTest(allItems, iteration)
		stats = append(stats, testStat)
		
		// Прогресс каждые 100 итераций
		if (iteration+1)%100 == 0 {
			fmt.Printf("Progress: %d/%d iterations completed\n", iteration+1, NumIterations)
		}
	}
	
	totalDuration := time.Since(startAllTests)
	fmt.Println("\n=== ALL ITERATIONS COMPLETED ===")
	
	// --- ВЫЧИСЛЕНИЕ И ВЫВОД СТАТИСТИКИ ---
	var totalVerifyDuration time.Duration
	var totalPerSig float64
	
	for _, stat := range stats {
		totalVerifyDuration += stat.VerifyDuration
		totalPerSig += stat.PerSignature
	}
	
	avgVerifyDuration := float64(totalVerifyDuration.Microseconds()) / float64(NumIterations) / 1000.0
	avgPerSignature := totalPerSig / float64(NumIterations)
	
	fmt.Println("\n=== STATISTICS ===")
	fmt.Printf("Total iterations: %d\n", NumIterations)
	fmt.Printf("Total test time: %.2f sec\n", totalDuration.Seconds())
	fmt.Printf("Total signatures verified: %d\n", NumMessages*NumIterations)
	fmt.Println("\n--- AVERAGE METRICS ---")
	fmt.Printf("Avg time per batch group: %.3f ms\n", avgVerifyDuration)
	fmt.Printf("Avg time per signature: %.5f ms\n", avgPerSignature)
	fmt.Printf("Avg throughput: %.0f sigs/sec\n", 1000.0/avgPerSignature)
}

func runVerificationTest(allItems []SignedItem, iteration int) TestStats {
	jobs := make(chan []SignedItem, NumWorkers*4)
	var wgWorkers sync.WaitGroup
	var processedCount int64
	var hasError int32
	
	startVerify := time.Now()
	
	// Запуск workers
	for w := 0; w < NumWorkers; w++ {
		wgWorkers.Add(1)
		go func(workerID int) {
			defer wgWorkers.Done()
			verifier := ed25519.NewBatchVerifierWithCapacity(BatchSize)
			
			for batch := range jobs {
				for _, item := range batch {
					verifier.Add(item.PubKey, item.Msg, item.Sig)
				}
				
				ok, _ := verifier.Verify(nil)
				if !ok {
					atomic.StoreInt32(&hasError, 1)
				}
				
				atomic.AddInt64(&processedCount, int64(len(batch)))
				verifier.Reset()
			}
		}(w)
	}
	
	// Producer
	go func() {
		numBatches := (NumMessages + BatchSize - 1) / BatchSize
		for i := 0; i < numBatches; i++ {
			start := i * BatchSize
			end := start + BatchSize
			if end > NumMessages {
				end = NumMessages
			}
			jobs <- allItems[start:end]
		}
		close(jobs)
	}()
	
	wgWorkers.Wait()
	durationVerify := time.Since(startVerify)
	
	// Проверка на ошибки (только первые 10 итераций)
	if iteration < 10 && atomic.LoadInt32(&hasError) == 1 {
		fmt.Printf("[Iteration %d] WARNING: Verification failed!\n", iteration)
	}
	
	seconds := durationVerify.Seconds()
	perSignature := (seconds * 1000) / float64(NumMessages)
	
	return TestStats{
		VerifyDuration: durationVerify,
		PerSignature:   perSignature,
	}
}
