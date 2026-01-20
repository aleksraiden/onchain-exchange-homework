// sharded_cache.go
package main

import (
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"github.com/google/uuid"
)

// KeyHasher используется для получения индекса шарда из ключа
type KeyHasher func(key []byte) uint32

// ShardedSet — потокобезопасный шардированный set с настраиваемым размером ключа
type ShardedSet struct {
	shards    	[]*shard
	shardMask 	uint32
	hasher    	KeyHasher
	keySize 	int
}

type shard struct {
	sync.RWMutex
	items map[string]struct{} // string — универсальный контейнер для фиксированных байт
}

// NewShardedSet создаёт новый шардированный set
// keySizeBytes — ожидаемый размер ключа в байтах (16, 32, 20 и т.д.)
// shardCount — должно быть степенью двойки (256, 512, 1024...)
// hasher — функция, которая превращает ключ в номер шарда (можно использовать последние байты)
func NewShardedSet(shardCount int, keySizeBytes int, hasher KeyHasher) *ShardedSet {
	if shardCount <= 0 || (shardCount&(shardCount-1)) != 0 {
		panic("shardCount должен быть степенью двойки")
	}
	if keySizeBytes <= 0 {
		panic("keySizeBytes должен быть > 0")
	}

	shards := make([]*shard, shardCount)
	for i := range shards {
		shards[i] = &shard{
			items: make(map[string]struct{}, 16384), // начальная ёмкость на шард
		}
	}

	if hasher == nil {
		// дефолтный хешер — последние 4 байта как uint32 little-endian
		hasher = func(key []byte) uint32 {
			if len(key) < 4 {
				return 0
			}
			return uint32(key[len(key)-4]) |
				uint32(key[len(key)-3])<<8 |
				uint32(key[len(key)-2])<<16 |
				uint32(key[len(key)-1])<<24
		}
	}

	return &ShardedSet{
		shards:    shards,
		shardMask: uint32(shardCount - 1),
		hasher:    hasher,
		keySize:   keySizeBytes, // сохраняем для валидации
	}
}

// Seen — возвращает true если элемент уже был (дубликат)
func (s *ShardedSet) Seen(key []byte) bool {
	if len(key) != s.keySize {
		return false // или panic — решайте по бизнес-логике
	}

	shardIdx := s.hasher(key) & s.shardMask
	sh := s.shards[shardIdx]

	sh.RLock()
	_, exists := sh.items[string(key)]
	sh.RUnlock()

	if exists {
		return true
	}

	sh.Lock()
	_, exists = sh.items[string(key)]
	if !exists {
		sh.items[string(key)] = struct{}{}
	}
	sh.Unlock()

	return exists
}

// Put — безусловная вставка (используется после Bloom "возможно новый")
func (s *ShardedSet) Put(key []byte) {
	if len(key) != s.keySize {
		return // или panic
	}

	shardIdx := s.hasher(key) & s.shardMask
	sh := s.shards[shardIdx]

	sh.Lock()
	sh.items[string(key)] = struct{}{}
	sh.Unlock()
}


func TestShardedSet_Concurrency() {
	const (
		shardCount   = 512
		keySize      = 16
		goroutines   = 16
		opsPerWorker = 50000
	)

	cache := NewShardedSet(shardCount, keySize, func(k []byte) uint32 {
		// простой, но достаточно равномерный для теста
		return uint32(k[12])<<24 | uint32(k[13])<<16 | uint32(k[14])<<8 | uint32(k[15])
	})

	var wg sync.WaitGroup
	var duplicates atomic.Int64
	var total atomic.Int64

	start := time.Now()

	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		go func(workerID int) {
			defer wg.Done()

			for j := 0; j < opsPerWorker; j++ {
				// каждый воркер генерирует свой диапазон uuid
				u := uuid.New()
				// слегка модифицируем чтобы были и дубликаты
				if j%7 == 0 {
					// ~14% дубликатов
					u[15] = byte(workerID % 256)
				}

				isDup := cache.Seen(u[:])
				total.Add(1)
				if isDup {
					duplicates.Add(1)
				}
			}
		}(i)
	}

	wg.Wait()
	duration := time.Since(start)

	ops := total.Load()
	dups := duplicates.Load()
	throughput := float64(ops) / duration.Seconds()

	fmt.Println("Concurrency test results:")
	fmt.Println("  Total operations:     %d", ops)
	fmt.Println("  Detected duplicates:  %d (%.1f%%)", dups, float64(dups)/float64(ops)*100)
	fmt.Println("  Duration:             %v", duration.Round(time.Millisecond))
	fmt.Println("  Throughput:           %.0f ops/sec", throughput)
	fmt.Println("  Throughput per core:  %.0f ops/sec", throughput/float64(goroutines))
}

/***
func main() {


// Примеры создания

// Для 16-байтных UUIDv7 (как было раньше)
var DefaultUUIDHasher = func(k []byte) uint32 {
	return uint32(k[15]) // или можно взять больше байт
}

cache := NewShardedSet(256, 16, DefaultUUIDHasher)

// Для 32-байтных blake3
cache32 := NewShardedSet(512, 32, nil) // будет использовать последние 4 байта

// Очень агрессивный вариант распределения (последние 2 байта → 65536 возможных шардов)
cache65536 := NewShardedSet(65536, 32, func(k []byte) uint32 {
	if len(k) < 2 {
		return 0
	}
	return uint32(k[len(k)-2]) | uint32(k[len(k)-1])<<8
})

TestShardedSet_Concurrency()

}***/