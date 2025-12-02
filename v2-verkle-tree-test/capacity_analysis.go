package verkletree

import (
	"fmt"
	"math/big"
	kzg_bls12381 "github.com/consensys/gnark-crypto/ecc/bls12-381/kzg"
)

// TreeCapacity рассчитывает емкость дерева
type TreeCapacity struct {
	Depth     int
	NodeWidth int
	MaxItems  *big.Int
}

// CalculateCapacity возвращает максимальную емкость
func CalculateCapacity(depth, width int) *TreeCapacity {
	// Емкость = width ^ depth
	maxItems := big.NewInt(int64(width))
	depth64 := big.NewInt(int64(depth))
	maxItems.Exp(maxItems, depth64, nil)
	
	return &TreeCapacity{
		Depth:     depth,
		NodeWidth: width,
		MaxItems:  maxItems,
	}
}

// FormatCapacity форматирует емкость человекочитаемо
func (tc *TreeCapacity) FormatCapacity() string {
	f := new(big.Float).SetInt(tc.MaxItems)
	
	// Конвертируем в разные единицы
	million := big.NewFloat(1e6)
	billion := big.NewFloat(1e9)
	trillion := big.NewFloat(1e12)
	
	if f.Cmp(trillion) >= 0 {
		f.Quo(f, trillion)
		val, _ := f.Float64()
		return fmt.Sprintf("%.2f триллионов", val)
	} else if f.Cmp(billion) >= 0 {
		f.Quo(f, billion)
		val, _ := f.Float64()
		return fmt.Sprintf("%.2f миллиардов", val)
	} else if f.Cmp(million) >= 0 {
		f.Quo(f, million)
		val, _ := f.Float64()
		return fmt.Sprintf("%.2f миллионов", val)
	}
	
	return tc.MaxItems.String()
}

// TreePerformance оценивает производительность
type TreePerformance struct {
	Depth              int
	NodeWidth          int
	AvgPathLength      int
	HashOpsPerInsert   int
	MemoryPerNode      int    // bytes
	EstimatedInsertUS  int    // microseconds
	EstimatedReadUS    int    // microseconds
}

// EstimatePerformance оценивает производительность операций
func EstimatePerformance(depth, width int, useKZG bool) *TreePerformance {
	perf := &TreePerformance{
		Depth:            depth,
		NodeWidth:        width,
		AvgPathLength:    depth,
		HashOpsPerInsert: depth, // Хешируем на каждом уровне
	}
	
	// Память на узел
	pointerSize := 8 // 64-bit
	perf.MemoryPerNode = width * pointerSize
	
	if useKZG {
		// KZG commitment ~ 48 bytes + операции на кривой
		perf.MemoryPerNode += 48
		// KZG очень медленный
		perf.EstimatedInsertUS = depth * 100 // ~100μs на KZG коммитмент
		perf.EstimatedReadUS = depth * 2     // Чтение быстрее
	} else {
		// Blake3 commitment ~ 32 bytes
		perf.MemoryPerNode += 32
		// Blake3 очень быстрый
		perf.EstimatedInsertUS = depth * 5   // ~5μs на Blake3
		perf.EstimatedReadUS = depth * 1     // ~1μs на уровень
	}
	
	return perf
}

// Предустановленные конфигурации
const (
	// ConfigSmall - малые приложения (до 16M)
	ConfigSmallDepth = 4
	ConfigSmallWidth = 64
	
	// ConfigMedium - средние сервисы (до 68B)
	ConfigMediumDepth = 6
	ConfigMediumWidth = 64
	
	// ConfigLarge - крупные системы (до 4.4T)
	ConfigLargeDepth = 6
	ConfigLargeWidth = 128
	
	// ConfigGlobal - глобальные базы (до 281T)
	ConfigGlobalDepth = 8
	ConfigGlobalWidth = 64
	
	// ConfigMassive - для будущего (практически бесконечно)
	ConfigMassiveDepth = 16
	ConfigMassiveWidth = 64
)

// NewWithPreset создает дерево с предустановленной конфигурацией
func NewWithPreset(preset string, srs *kzg_bls12381.SRS, dataStore Storage) (*VerkleTree, error) {
	var depth, width int
	
	switch preset {
	case "small":
		depth, width = ConfigSmallDepth, ConfigSmallWidth
	case "medium":
		depth, width = ConfigMediumDepth, ConfigMediumWidth
	case "large":
		depth, width = ConfigLargeDepth, ConfigLargeWidth
	case "global":
		depth, width = ConfigGlobalDepth, ConfigGlobalWidth
	case "massive":
		depth, width = ConfigMassiveDepth, ConfigMassiveWidth
	default:
		return nil, fmt.Errorf("неизвестный preset: %s", preset)
	}
	
	tree, err := New(depth, width, srs, dataStore)
	if err != nil {
		return nil, err
	}
	
	// Для фиксированной глубины отключаем автоматический рост
	tree.config.AutoGrowDepth = false
	
	return tree, nil
}
