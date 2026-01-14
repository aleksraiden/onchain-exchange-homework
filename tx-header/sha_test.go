package main

import (
	"crypto/sha256"
	"crypto/rand"
	"testing"
)

// Размер данных: 700,000 байт
const dataSize = 700000

func BenchmarkSHA256_700KB(b *testing.B) {
	// Подготовка данных (исключаем из замера времени)
	data := make([]byte, dataSize)
	_, err := rand.Read(data)
	if err != nil {
		b.Fatal(err)
	}

	b.ResetTimer() // Сброс таймера, начинаем замер только хеширования

	for i := 0; i < b.N; i++ {
		// Используем Sum256, так как она наиболее оптимизирована
		// и не требует аллокации структуры hash.Hash в куче, если не нужно
		_ = sha256.Sum256(data)
	}
    
    // Устанавливаем кол-во байт для корректного вывода MB/s
	b.SetBytes(int64(dataSize))
}