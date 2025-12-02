// memory_pools.go

package verkletree

import (
	"sync"
	
	"github.com/consensys/gnark-crypto/ecc/bls12-381/fr"
)

// ==========================================
// POOL 1: fr.Element slices (для KZG)
// ==========================================

var frElementPool256 = sync.Pool{
	New: func() interface{} {
		// Создаем слайс на 256 элементов (max для NodeWidth)
		slice := make([]fr.Element, 256)
		return &slice
	},
}

var frElementPool128 = sync.Pool{
	New: func() interface{} {
		slice := make([]fr.Element, 128)
		return &slice
	},
}

var frElementPool64 = sync.Pool{
	New: func() interface{} {
		slice := make([]fr.Element, 64)
		return &slice
	},
}

// ==========================================
// POOL 2: []byte buffers (для хеширования, сериализации)
// ==========================================

var byteBufferPool1K = sync.Pool{
	New: func() interface{} {
		buf := make([]byte, 1024)
		return &buf
	},
}

var byteBufferPool4K = sync.Pool{
	New: func() interface{} {
		buf := make([]byte, 4096)
		return &buf
	},
}

var byteBufferPool8K = sync.Pool{
	New: func() interface{} {
		buf := make([]byte, 8192) // MaxValueSize
		return &buf
	},
}

// getByteBuffer получает []byte буфер из пула
func getByteBuffer(minSize int) []byte {
	var bufPtr *[]byte
	
	if minSize <= 1024 {
		bufPtr = byteBufferPool1K.Get().(*[]byte)
	} else if minSize <= 4096 {
		bufPtr = byteBufferPool4K.Get().(*[]byte)
	} else {
		bufPtr = byteBufferPool8K.Get().(*[]byte)
	}
	
	buf := *bufPtr
	// Обнуляем для безопасности
	for i := 0; i < minSize && i < len(buf); i++ {
		buf[i] = 0
	}
	
	return buf[:minSize]
}

// putByteBuffer возвращает буфер в пул
func putByteBuffer(buf []byte) {
	capacity := cap(buf)
	fullBuf := buf[:capacity]
	
	if capacity == 1024 {
		byteBufferPool1K.Put(&fullBuf)
	} else if capacity == 4096 {
		byteBufferPool4K.Put(&fullBuf)
	} else if capacity == 8192 {
		byteBufferPool8K.Put(&fullBuf)
	}
}

// ==========================================
// POOL 3: Hash buffers (для blake3)
// ==========================================

var hashBufferPool = sync.Pool{
	New: func() interface{} {
		buf := make([]byte, 32) // Blake3 hash size
		return &buf
	},
}

// getHashBuffer получает буфер для хеша (32 байта)
func getHashBuffer() []byte {
	bufPtr := hashBufferPool.Get().(*[]byte)
	return *bufPtr
}

// putHashBuffer возвращает hash буфер в пул
func putHashBuffer(buf []byte) {
	if cap(buf) == 32 {
		fullBuf := buf[:32]
		hashBufferPool.Put(&fullBuf)
	}
}

// ==========================================
// POOL 4: Commitment byte arrays (48 bytes для BLS12-381)
// ==========================================

var commitmentBufferPool = sync.Pool{
	New: func() interface{} {
		buf := make([]byte, 48) // BLS12-381 G1 point size
		return &buf
	},
}

// getCommitmentBuffer получает буфер для KZG commitment
func getCommitmentBuffer() []byte {
	bufPtr := commitmentBufferPool.Get().(*[]byte)
	return *bufPtr
}

// putCommitmentBuffer возвращает commitment буфер в пул
func putCommitmentBuffer(buf []byte) {
	if cap(buf) == 48 {
		fullBuf := buf[:48]
		commitmentBufferPool.Put(&fullBuf)
	}
}

// ==========================================
// СТАТИСТИКА ПУЛОВ (для debugging)
// ==========================================

// PoolStats - статистика использования пулов
type PoolStats struct {
	FrElement256Gets   int64
	FrElement256Puts   int64
	ByteBuffer1KGets   int64
	ByteBuffer1KPuts   int64
	HashBufferGets     int64
	HashBufferPuts     int64
	CommitmentGets     int64
	CommitmentPuts     int64
}

var poolStats PoolStats

// GetPoolStats возвращает статистику использования пулов
func GetPoolStats() PoolStats {
	return poolStats
}

// ResetPoolStats сбрасывает статистику
func ResetPoolStats() {
	poolStats = PoolStats{}
}
