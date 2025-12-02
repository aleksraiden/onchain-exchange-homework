// Создайте файл proof_analysis.go

package verkletree

//import (
	//"fmt"
	//"math"
//)

// ProofStructure описывает структуру proof
type ProofStructure struct {
	// Blake3 хеши от листа до предкорневого узла
	Blake3Hashes [][]byte
	
	// KZG proof для root (opening proof)
	KZGProof []byte
	
	// Claimed value
	Value []byte
}

// ProofMetrics метрики производительности proof
type ProofMetrics struct {
	TreeDepth        int
	NodeWidth        int
	Architecture     string // "full_kzg", "hybrid", "merkle"
	
	// Размеры
	ProofSizeBytes   int
	RootSizeBytes    int
	
	// Производительность
	GenerateTimeUS   int  // Время генерации (микросекунды)
	VerifyTimeUS     int  // Время верификации
	
	// Стоимость операций
	HashOps          int  // Количество хеш операций
	KZGOps           int  // Количество KZG операций
}

// AnalyzeProofArchitecture анализирует разные архитектуры
func AnalyzeProofArchitecture(depth, width int) map[string]*ProofMetrics {
	metrics := make(map[string]*ProofMetrics)
	
	// 1. ПОЛНЫЙ MERKLE (только Blake3)
	merkle := &ProofMetrics{
		TreeDepth:    depth,
		NodeWidth:    width,
		Architecture: "merkle_blake3",
	}
	
	// Proof = sibling hashes на каждом уровне
	// Для width-ary дерева: (width-1) хешей на уровень
	merkle.ProofSizeBytes = depth * (width - 1) * 32
	merkle.RootSizeBytes = 32
	
	// Генерация: нужно хешировать путь от листа до корня
	// + собрать все sibling хеши
	merkle.HashOps = depth + (depth * (width - 1)) // путь + siblings
	merkle.GenerateTimeUS = merkle.HashOps * 1      // Blake3 ~1μs
	
	// Верификация: пересчитать root из leaf + siblings
	merkle.VerifyTimeUS = depth * 2 // Blake3 быстрый
	merkle.KZGOps = 0
	
	metrics["merkle"] = merkle
	
	// 2. ГИБРИД (Blake3 + KZG root)
	hybrid := &ProofMetrics{
		TreeDepth:    depth,
		NodeWidth:    width,
		Architecture: "hybrid_blake3_kzg",
	}
	
	// Proof = Blake3 хеши промежуточных узлов + KZG opening proof для root
	// Оптимизация: не нужны все siblings, только путь!
	hybrid.ProofSizeBytes = depth*32 + 48 // путь в Blake3 + KZG proof
	hybrid.RootSizeBytes = 48             // KZG commitment
	
	// Генерация: 
	// - Blake3 путь: depth операций
	// - KZG opening на root: 1 операция
	hybrid.HashOps = depth
	hybrid.KZGOps = 1
	hybrid.GenerateTimeUS = depth*1 + 100 // Blake3 + KZG opening
	
	// Верификация:
	// - Пересчитать путь: depth Blake3
	// - Проверить KZG proof: 1 pairing operation (~1ms)
	hybrid.VerifyTimeUS = depth*1 + 1000 // Blake3 + KZG verify
	
	metrics["hybrid"] = hybrid
	
	// 3. ПОЛНЫЙ VERKLE (KZG везде)
	fullKZG := &ProofMetrics{
		TreeDepth:    depth,
		NodeWidth:    width,
		Architecture: "full_kzg_verkle",
	}
	
	// Классический Verkle: один aggregated proof для всего пути
	// Размер не зависит от depth! (главное преимущество)
	fullKZG.ProofSizeBytes = 48 + 32 // KZG proof + claimed value
	fullKZG.RootSizeBytes = 48
	
	// Генерация: KZG opening на каждом уровне
	fullKZG.HashOps = 0
	fullKZG.KZGOps = depth
	fullKZG.GenerateTimeUS = depth * 100 // KZG медленный
	
	// Верификация: одна KZG проверка (можно aggregated)
	fullKZG.VerifyTimeUS = 1000 // одна pairing операция
	
	metrics["full_kzg"] = fullKZG
	
	return metrics
}

// MultiProofMetrics метрики для множественных пруфов
type MultiProofMetrics struct {
	NumProofs        int
	Architecture     string
	
	TotalProofSize   int
	GenerateTimeUS   int
	VerifyTimeUS     int
	
	// Батчинг эффективность
	BatchingGain     float64 // Во сколько раз эффективнее чем N отдельных
}

// AnalyzeMultiProof анализирует мульти-пруфы
func AnalyzeMultiProof(depth, width, numProofs int) map[string]*MultiProofMetrics {
	metrics := make(map[string]*MultiProofMetrics)
	
	// 1. MERKLE - мульти-пруф с дедупликацией общих узлов
	merkle := &MultiProofMetrics{
		NumProofs:    numProofs,
		Architecture: "merkle_blake3",
	}
	
	// Если пруфы перекрываются, можем переиспользовать общие узлы
	// В среднем, для N случайных путей: ~N * depth * 0.7 уникальных узлов
	uniqueNodes := int(float64(numProofs*depth) * 0.7)
	merkle.TotalProofSize = uniqueNodes * 32
	merkle.GenerateTimeUS = uniqueNodes * 1
	merkle.VerifyTimeUS = numProofs * depth * 2
	merkle.BatchingGain = float64(numProofs*depth*32) / float64(merkle.TotalProofSize)
	
	metrics["merkle"] = merkle
	
	// 2. ГИБРИД - пути + один общий KZG proof для root
	hybrid := &MultiProofMetrics{
		NumProofs:    numProofs,
		Architecture: "hybrid_blake3_kzg",
	}
	
	// Общий root commitment + пути для каждого элемента
	// Пути могут пересекаться, используем дедупликацию
	uniquePaths := int(float64(numProofs*depth) * 0.7)
	hybrid.TotalProofSize = uniquePaths*32 + 48 // пути + один KZG proof
	hybrid.GenerateTimeUS = uniquePaths*1 + 100  // Blake3 + один KZG
	hybrid.VerifyTimeUS = numProofs*depth*1 + 1000 // все пути + один KZG verify
	hybrid.BatchingGain = float64(numProofs*(depth*32+48)) / float64(hybrid.TotalProofSize)
	
	metrics["hybrid"] = hybrid
	
	// 3. ПОЛНЫЙ VERKLE - aggregated multi-opening proof
	fullKZG := &MultiProofMetrics{
		NumProofs:    numProofs,
		Architecture: "full_kzg_verkle",
	}
	
	// Verkle поддерживает multi-opening: один proof для N значений!
	// Размер практически не растет с количеством пруфов
	fullKZG.TotalProofSize = 48 + numProofs*32 // один KZG + N values
	fullKZG.GenerateTimeUS = depth*100 + numProofs*50 // KZG openings + aggregation
	fullKZG.VerifyTimeUS = 1500 // одна aggregated проверка
	fullKZG.BatchingGain = float64(numProofs*80) / float64(fullKZG.TotalProofSize)
	
	metrics["full_kzg"] = fullKZG
	
	return metrics
}
