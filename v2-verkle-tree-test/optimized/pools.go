// optimized/pools.go

package optimized

import (
	"sync"
	"github.com/consensys/gnark-crypto/ecc/bls12-381/fr"
)

// Memory pools для переиспользования объектов

var (
	// Pool для fr.Element слайсов (128 элементов)
	frElementPool128 = sync.Pool{
		New: func() interface{} {
			slice := make([]fr.Element, NodeWidth)
			return &slice
		},
	}
	
	// Pool для byte буферов (1KB)
	byteBufferPool1K = sync.Pool{
		New: func() interface{} {
			buf := make([]byte, 1024)
			return &buf
		},
	}
	
	// Pool для byte буферов (8KB - MaxValueSize)
	byteBufferPool8K = sync.Pool{
		New: func() interface{} {
			buf := make([]byte, MaxValueSize)
			return &buf
		},
	}
	
	// Pool для hash буферов (32 bytes)
	hashBufferPool = sync.Pool{
		New: func() interface{} {
			buf := make([]byte, 32)
			return &buf
		},
	}
)

// GetFrElementSlice получает слайс из пула
func GetFrElementSlice() []fr.Element {
	ptr := frElementPool128.Get().(*[]fr.Element)
	slice := *ptr
	
	// Обнуляем
	for i := 0; i < NodeWidth; i++ {
		slice[i].SetZero()
	}
	
	return slice
}

// PutFrElementSlice возвращает слайс в пул
func PutFrElementSlice(slice []fr.Element) {
	if cap(slice) == NodeWidth {
		fullSlice := slice[:NodeWidth]
		frElementPool128.Put(&fullSlice)
	}
}

// GetByteBuffer получает byte буфер из пула
func GetByteBuffer(size int) []byte {
	var bufPtr *[]byte
	
	if size <= 1024 {
		bufPtr = byteBufferPool1K.Get().(*[]byte)
	} else {
		bufPtr = byteBufferPool8K.Get().(*[]byte)
	}
	
	buf := *bufPtr
	return buf[:size]
}

// PutByteBuffer возвращает буфер в пул
func PutByteBuffer(buf []byte) {
	capacity := cap(buf)
	fullBuf := buf[:capacity]
	
	if capacity == 1024 {
		byteBufferPool1K.Put(&fullBuf)
	} else if capacity == MaxValueSize {
		byteBufferPool8K.Put(&fullBuf)
	}
}

// GetHashBuffer получает hash буфер (32 bytes)
func GetHashBuffer() []byte {
	ptr := hashBufferPool.Get().(*[]byte)
	return *ptr
}

// PutHashBuffer возвращает hash буфер в пул
func PutHashBuffer(buf []byte) {
	if cap(buf) == 32 {
		fullBuf := buf[:32]
		hashBufferPool.Put(&fullBuf)
	}
}
