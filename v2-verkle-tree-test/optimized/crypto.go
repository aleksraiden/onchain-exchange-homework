// optimized/crypto.go

package optimized

import (
	"fmt"
	"sync" // ✅ Добавили
	
	"github.com/consensys/gnark-crypto/ecc/bls12-381/fr"
	kzg_bls12381 "github.com/consensys/gnark-crypto/ecc/bls12-381/kzg"
	"github.com/zeebo/blake3"
)

// HashUserID создает blake3 хеш от ID пользователя
func HashUserID(userID string) [32]byte {
	hasher := blake3.New()
	hasher.Write([]byte(userID))
	hash := hasher.Sum(nil)
	
	var result [32]byte
	copy(result[:], hash)
	return result
}

// BatchBlake3Hash - параллельное хеширование batch данных
func BatchBlake3Hash(data [][]byte, workers int) [][]byte {
	if len(data) == 0 {
		return nil
	}
	
	results := make([][]byte, len(data))
	
	// Для малых batch - последовательно
	if len(data) < workers*2 {
		for i, d := range data {
			hasher := blake3.New()
			hasher.Write(d)
			results[i] = hasher.Sum(nil)
		}
		return results
	}
	
	// Параллельное хеширование
	var wg sync.WaitGroup
	chunkSize := (len(data) + workers - 1) / workers
	
	for w := 0; w < workers; w++ {
		start := w * chunkSize
		if start >= len(data) {
			break
		}
		
		end := start + chunkSize
		if end > len(data) {
			end = len(data)
		}
		
		wg.Add(1)
		go func(startIdx, endIdx int) {
			defer wg.Done()
			for i := startIdx; i < endIdx; i++ {
				hasher := blake3.New()
				hasher.Write(data[i])
				results[i] = hasher.Sum(nil)
			}
		}(start, end)
	}
	
	wg.Wait()
	return results
}

// ComputeKZGCommitment вычисляет KZG commitment для root
func ComputeKZGCommitment(values []fr.Element, srs *kzg_bls12381.SRS) ([]byte, error) {
	if len(values) != NodeWidth {
		return nil, fmt.Errorf("invalid values length: expected %d, got %d", NodeWidth, len(values))
	}
	
	// Создаем polynomial
	poly := make([]fr.Element, NodeWidth)
	copy(poly, values)
	
	// KZG Commit
	commitment, err := kzg_bls12381.Commit(poly, srs.Pk)
	if err != nil {
		return nil, fmt.Errorf("KZG commit failed: %w", err)
	}
	
	// Сериализуем в bytes
	return commitment.Marshal(), nil
}

// hashToFieldElement преобразует hash (32 bytes) в fr.Element
func hashToFieldElement(hash []byte) fr.Element {
	var elem fr.Element
	
	if len(hash) == 0 {
		elem.SetZero()
		return elem
	}
	
	// SetBytes интерпретирует байты как big-endian число
	// Автоматически применяется modulo если число больше field modulus
	elem.SetBytes(hash)
	
	return elem
}

// evaluatePolynomial вычисляет значение polynomial в точке используя метод Горнера
func evaluatePolynomial(coeffs []fr.Element, point fr.Element) fr.Element {
	if len(coeffs) == 0 {
		var zero fr.Element
		return zero
	}
	
	// Horner's method: p(x) = a0 + x(a1 + x(a2 + x(a3 + ...)))
	result := coeffs[len(coeffs)-1]
	
	for i := len(coeffs) - 2; i >= 0; i-- {
		result.Mul(&result, &point)
		result.Add(&result, &coeffs[i])
	}
	
	return result
}
