package main

import (
	"fmt"
	"runtime"
	"time"
	"math/big"

	"github.com/consensys/gnark-crypto/ecc"
	"github.com/consensys/gnark-crypto/ecc/bls12-381/fr"
	"github.com/consensys/gnark-crypto/ecc/bls12-381/kzg"
)

func main() {
	// Используем все ядра
	runtime.GOMAXPROCS(runtime.NumCPU())

	// 1. Setup (Генерация SRS - Trusted Setup)
	// Для размера 256. В реальности вы загрузите это из файла.
	size := uint64(256)
	srs, _ := kzg.NewSRS(ecc.NextPowerOfTwo(size), new(big.Int).SetInt64(42))

	// Подготовка тестовых данных (чтобы не тратить время в бенчмарке)
	// Имитируем 100,000 батчей (узлов) по 256 элементов
	batchCount := 100000 
	data := make([][]fr.Element, batchCount)
	for i := 0; i < batchCount; i++ {
		data[i] = make([]fr.Element, size)
		for j := 0; j < int(size); j++ {
			data[i][j].SetRandom()
		}
	}

	fmt.Printf("Starting benchmark on %d cores...\n", runtime.NumCPU())
	fmt.Printf("Computing %d commitments (size 256 each)...\n", batchCount)

	start := time.Now()

	// Параллельная обработка
	// В реальном коде используйте worker pool, здесь для простоты Semaphore
	sem := make(chan struct{}, runtime.NumCPU())
	
	for i := 0; i < batchCount; i++ {
		sem <- struct{}{}
		go func(idx int) {
			defer func() { <-sem }()
			// Самая тяжелая операция: Commit
			_, err := kzg.Commit(data[idx], srs.Pk)
			if err != nil {
				panic(err)
			}
		}(i)
	}

	// Ждем завершения
	for i := 0; i < cap(sem); i++ {
		sem <- struct{}{}
	}

	elapsed := time.Since(start)
	opsPerSec := float64(batchCount) / elapsed.Seconds()

	fmt.Printf("Done in %s\n", elapsed)
	fmt.Printf("Throughput: %.2f commitments/sec\n", opsPerSec)
}