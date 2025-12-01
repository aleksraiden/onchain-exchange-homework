package main

import (
//	"bytes"
	"crypto/rand"
	"crypto/sha256"
	"fmt"
	"log"
	"runtime"
//	"sort"
	"sync"
	"sync/atomic"
	"time"

	// Используем библиотеку VOI
	"github.com/oasisprotocol/curve25519-voi/primitives/ed25519"
)

// --- КОНФИГУРАЦИЯ ---
const (
	NumUniqueKeys = 30_000 // Количество уникальных ключей
	NumMessages   = 50_000 // Общее количество сообщений
	BatchSize     = 512    // Размер батча
	NumWorkers    = 19     // Количество потоков

	// ВАЖНО: Сколько сообщений генерируем на один ключ.
	// Это создает дубликаты ключей в данных.
	// Если поставить 1, то сортировка только замедлит процесс (нет выгоды).
	MsgsPerKey = 4
)

// SignedItem - структура данных
type SignedItem struct {
	PubKey ed25519.PublicKey
	Msg    []byte
	Sig    []byte
}

func main() {
	runtime.GOMAXPROCS(runtime.NumCPU())

	fmt.Printf("=== BENCHMARK: VOI + IN-BATCH SORTING ===\n")
	fmt.Printf("CPU Cores:     %d\n", runtime.NumCPU())
	fmt.Printf("Workers:       %d\n", NumWorkers)
	fmt.Printf("Batch Size:    %d\n", BatchSize)
	fmt.Printf("Msgs Per Key:  %d (Creates duplicates)\n", MsgsPerKey)
	fmt.Println("=========================================")

	// --- ЭТАП 1: Генерация ключей ---
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

	// --- ЭТАП 2: Подпись сообщений (с перемешиванием) ---
	startSign := time.Now()
	allItems := make([]SignedItem, NumMessages)

	fmt.Print("2. Signing messages... ")
	
	var wgGen sync.WaitGroup
	chunkSize := NumMessages / runtime.NumCPU()
	if chunkSize == 0 { chunkSize = NumMessages }

	for i := 0; i < runtime.NumCPU(); i++ {
		startIdx := i * chunkSize
		endIdx := startIdx + chunkSize
		if i == runtime.NumCPU()-1 {
			endIdx = NumMessages
		}
		if startIdx >= endIdx { continue }

		wgGen.Add(1)
		go func(start, end int) {
			defer wgGen.Done()
			h := sha256.New()
			
			for j := start; j < end; j++ {
				// Логика выбора ключа:
				// (j / 3) % 30000 -> Ключ 0, Ключ 0, Ключ 0, Ключ 1...
				// Это гарантирует наличие повторов
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

	// --- ЭТАП 3: Пакетная проверка с СОРТИРОВКОЙ ---
	fmt.Print("3. Batch Verification (With Sorting)... ")
	
	jobs := make(chan []SignedItem, NumWorkers*4)
	var wgWorkers sync.WaitGroup
	var processedCount int64
	var hasError int32

	startVerify := time.Now()

	for w := 0; w < NumWorkers; w++ {
		wgWorkers.Add(1)
		go func(workerID int) {
			defer wgWorkers.Done()

			verifier := ed25519.NewBatchVerifierWithCapacity(BatchSize)

			for batch := range jobs {
				
				// === НОВАЯ ЛОГИКА: СОРТИРОВКА ===
				// Мы сортируем слайс прямо перед проверкой.
				// Это группирует одинаковые ключи рядом: [KeyA, KeyB, KeyA] -> [KeyA, KeyA, KeyB]
				// VOI обрабатывает последовательные одинаковые ключи быстрее.
//				sort.Slice(batch, func(i, j int) bool {
//					return bytes.Compare(batch[i].PubKey, batch[j].PubKey) < 0
//				})
				// ===============================

				// 1. Добавляем элементы (теперь они идут упорядоченно по ключам)
				for _, item := range batch {
					verifier.Add(item.PubKey, item.Msg, item.Sig)
				}

				// 2. Проверяем
				ok, _ := verifier.Verify(nil)
				
				if !ok {
					fmt.Printf("[Worker %d] Batch failed!\n", workerID)
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
	fmt.Printf("DONE\n")

	// --- РЕЗУЛЬТАТЫ ---
	fmt.Println("\n=== RESULTS ===")
	if atomic.LoadInt32(&hasError) == 1 {
		fmt.Println("STATUS: FAILED")
	} else {
		fmt.Println("STATUS: SUCCESS")
	}
	
	seconds := durationVerify.Seconds()
	tps := float64(NumMessages) / seconds
	
	fmt.Printf("Processed:     %d signatures\n", atomic.LoadInt64(&processedCount))
	fmt.Printf("Total Time:    %.2f ms\n", float64(durationVerify.Milliseconds()))
	fmt.Printf("Throughput:    %.0f sigs/sec\n", tps)
	fmt.Printf("Per Signature: %.5f ms\n", (seconds*1000)/float64(NumMessages))
}