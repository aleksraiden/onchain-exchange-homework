// optimized/config.go

package optimized

import (
	"runtime" // ✅ Добавили
	
	kzg_bls12381 "github.com/consensys/gnark-crypto/ecc/bls12-381/kzg"
)

const (
	// Фиксированные параметры дерева
	TreeDepth     = 8   // Глубина дерева (фиксированная)
	NodeWidth     = 128 // Ширина узла (фиксированная)
	MaxValueSize  = 8 * 1024 // 8 KB
	StemSize      = 31  // Размер stem (префикса ключа)
	
	// Кэш параметры
	DefaultCacheSize = 5000 // LRU cache для горячих узлов
	
	// Parallel параметры
	MinWorkers = 4 // Минимум воркеров
)

// Config - конфигурация Verkle Tree
type Config struct {
	// KZG SRS для криптографии
	KZGConfig *kzg_bls12381.SRS
	
	// NodeMask - предвычисленная маска для быстрого getNodeIndex
	// NodeMask = NodeWidth - 1 = 127 (0b01111111)
	NodeMask int
	
	// Parallel workers
	Workers int
	
	// LRU cache size
	CacheSize int
	
	// Lazy commit (всегда true)
	LazyCommit bool
	
	// Async mode (всегда true)
	AsyncMode bool
	
	// HashOnly - использовать только Blake3, без KZG
	// Это быстрее, но не позволяет генерировать криптографические proof'ы
	HashOnly bool
}

// NewConfig создает оптимизированную конфигурацию
func NewConfig(srs *kzg_bls12381.SRS) *Config {
	workers := runtime.GOMAXPROCS(0)
	if workers < MinWorkers {
		workers = MinWorkers
	}
	
	return &Config{
		KZGConfig:  srs,
		NodeMask:   NodeWidth - 1, // 127
		Workers:    workers,
		CacheSize:  DefaultCacheSize,
		LazyCommit: true,
		AsyncMode:  true,
		HashOnly:   false, // KZG включен по умолчанию
	}
}

// TODO: убрать, рефактор NewConfigHashOnly создает конфигурацию БЕЗ KZG (быстрее)
func NewConfigHashOnly() *Config {
	workers := runtime.GOMAXPROCS(0)
	if workers < MinWorkers {
		workers = MinWorkers
	}

	return &Config{
		KZGConfig:  nil, // KZG отключен
		NodeMask:   NodeWidth - 1,
		Workers:    workers,
		CacheSize:  DefaultCacheSize,
		LazyCommit: true,
		AsyncMode:  true,
		HashOnly:   true, // ✅ Только Blake3
	}
}