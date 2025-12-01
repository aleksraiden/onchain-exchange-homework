package main

import (
	"crypto/sha256"
	"fmt"
	"log"
	"runtime"
	"runtime/debug"
	"sync"
	"sync/atomic"
	"time"

	// Самая авторитетная библиотека для Bitcoin/Schnorr в Go
	"github.com/btcsuite/btcd/btcec/v2"
	"github.com/btcsuite/btcd/btcec/v2/schnorr"
)

// --- КОНФИГУРАЦИЯ ---
const (
	NumUniqueKeys = 30_000
	NumMessages   = 50_000
	// Здесь BatchSize выступает просто как размер чанка для воркера,
	// так как встроенного BatchVerify в этой библиотеке нет.
	BatchSize     = 128    
	MsgsPerKey    = 4      
)

// SignedItem хранит данные. 
// Schnorr Public Key - 32 байта (x-only coordinate).
// Signature - 64 байта.
type SignedItem struct {
	PubKey []byte 
	Msg    []byte
	Sig    []byte
}

func main() {
	// Максимизация ресурсов
	numCPU := runtime.NumCPU()
	runtime.GOMAXPROCS(numCPU)
	debug.SetGCPercent(-1) // Отключаем GC для чистоты теста

	fmt.Printf("=== BENCHMARK: SCHNORR (secp256k1) ===\n")
	fmt.Printf("Library:       btcsuite/btcd/btcec/v2\n")
	fmt.Printf("CPU Cores:     %d\n", numCPU)
	fmt.Printf("Batch Size:    %d (Parallel Chunking)\n", BatchSize)
	fmt.Printf("Msgs Per Key:  %d\n", MsgsPerKey)
	fmt.Println("======================================")

	// --- ЭТАП 1: Генерация ключей ---
	startGen := time.Now()
	
	// Храним приватные ключи в памяти
	privKeys := make([]*btcec.PrivateKey, NumUniqueKeys)
	// Для Schnorr публичный ключ - это 32 байта
	pubKeysBytes := make([][]byte, NumUniqueKeys)

	var wgKey sync.WaitGroup
	keyChunk := NumUniqueKeys / numCPU
	if keyChunk == 0 { keyChunk = NumUniqueKeys }

	for i := 0; i < numCPU; i++ {
		start := i * keyChunk
		end := start + keyChunk
		if i == numCPU-1 { end = NumUniqueKeys }
		if start >= end { continue }

		wgKey.Add(1)
		go func(s, e int) {
			defer wgKey.Done()
			for k := s; k < e; k++ {
				// Генерация ключа secp256k1
				priv, err := btcec.NewPrivateKey()
				if err != nil {
					log.Fatal(err)
				}
				privKeys[k] = priv
				// Сериализация в 32-байтный формат Schnorr (BIP-340)
				pubKeysBytes[k] = schnorr.SerializePubKey(priv.PubKey())
			}
		}(start, end)
	}
	wgKey.Wait()
	fmt.Printf("1. Keys Generated in %v\n", time.Since(startGen))

	// --- ЭТАП 2: Подпись (Schnorr) ---
	startSign := time.Now()
	allItems := make([]SignedItem, NumMessages)

	var wgGen sync.WaitGroup
	chunkSize := NumMessages / numCPU
	if chunkSize == 0 { chunkSize = NumMessages }

	for i := 0; i < numCPU; i++ {
		start := i * chunkSize
		end := start + chunkSize
		if i == numCPU-1 { end = NumMessages }
		if start >= end { continue }

		wgGen.Add(1)
		go func(s, e int) {
			defer wgGen.Done()
			h := sha256.New()
			for j := s; j < e; j++ {
				keyIdx := (j / MsgsPerKey) % NumUniqueKeys
				
				h.Reset()
				h.Write([]byte(fmt.Sprintf("msg-%d", j)))
				msgHash := h.Sum(nil)

				// Подпись Schnorr
				sig, err := schnorr.Sign(privKeys[keyIdx], msgHash)
				if err != nil {
					log.Fatal(err)
				}
				
				// Serialize возвращает 64 байта
				sigBytes := sig.Serialize()

				allItems[j] = SignedItem{
					PubKey: pubKeysBytes[keyIdx],
					Msg:    msgHash,
					Sig:    sigBytes,
				}
			}
		}(start, end)
	}
	wgGen.Wait()
	fmt.Printf("2. Signing DONE in %v\n", time.Since(startSign))

	// --- ЭТАП 3: Верификация (Параллельная) ---
	// ВАЖНО: В Go нет стандартного BatchVerify для Schnorr, который объединяет математику.
	// Поэтому мы делаем максимально быструю параллельную проверку.
	fmt.Print("3. Verifying... ")
	
	var wgWorkers sync.WaitGroup
	var processedCount int64
	var hasError int32
	
	runtime.GC() // Очистка перед стартом
	startVerify := time.Now()

	// Используем Static Sharding (как в лучшем примере на Go)
	workerChunk := NumMessages / numCPU

	for w := 0; w < numCPU; w++ {
		start := w * workerChunk
		end := start + workerChunk
		if w == numCPU-1 { end = NumMessages }
		if start >= end { continue }

		wgWorkers.Add(1)
		go func(myItems []SignedItem) {
			defer wgWorkers.Done()
			localProcessed := 0

			// Проходим по куску данных
			for _, item := range myItems {
				// 1. Парсинг подписи (дорого)
				sig, err := schnorr.ParseSignature(item.Sig)
				if err != nil {
					atomic.StoreInt32(&hasError, 1)
					continue
				}

				// 2. Парсинг ключа (дорого)
				pk, err := schnorr.ParsePubKey(item.PubKey)
				if err != nil {
					atomic.StoreInt32(&hasError, 1)
					continue
				}

				// 3. Верификация
				if !sig.Verify(item.Msg, pk) {
					atomic.StoreInt32(&hasError, 1)
				}
				
				localProcessed++
			}
			atomic.AddInt64(&processedCount, int64(localProcessed))
		}(allItems[start:end])
	}

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