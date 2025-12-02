// kzg_crypto.go
package verkletree

import (
	"sync"
	"fmt"
	"math/big"

	"github.com/consensys/gnark-crypto/ecc/bls12-381/fr"
	"github.com/consensys/gnark-crypto/ecc/bls12-381/fr/polynomial"
	kzg_bls12381 "github.com/consensys/gnark-crypto/ecc/bls12-381/kzg"
	"github.com/zeebo/blake3"
)

// Пулы для переиспользования объектов
var (
    frElementPool = sync.Pool{
        New: func() interface{} {
            return make([]fr.Element, 256)
        },
    }
    
    polynomialPool = sync.Pool{
        New: func() interface{} {
            return make(polynomial.Polynomial, 256)
        },
    }
)

// Функции для работы с пулами
func getFrElementSlice(size int) []fr.Element {
    if size <= 256 {
        slice := frElementPool.Get().([]fr.Element)
        return slice[:size]
    }
    return make([]fr.Element, size)
}

func putFrElementSlice(slice []fr.Element) {
    if cap(slice) == 256 {
        frElementPool.Put(slice[:256])
    }
}

// InitSRS инициализирует Structured Reference String для KZG
// size - размер SRS (должен быть >= максимальной степени полинома)
// Для production использовать церемонию trusted setup или загрузить готовый SRS
func InitSRS(size int) (*kzg_bls12381.SRS, error) {
    // Округляем до ближайшей степени двойки для оптимизации
    srsSize := nextPowerOfTwo(size)
    
    // Генерируем новый SRS для тестирования
    // ВАЖНО: В production использовать церемонию trusted setup!
    
    srs, err := kzg_bls12381.NewSRS(uint64(srsSize), new(big.Int).SetInt64(42))
    if err != nil {
        return nil, fmt.Errorf("ошибка генерации SRS: %w", err)
    }
    
    return srs, nil
}

// nextPowerOfTwo возвращает следующую степень двойки >= n
func nextPowerOfTwo(n int) int {
    if n <= 0 {
        return 1
    }
    
    // Проверяем является ли уже степенью двойки
    if n&(n-1) == 0 {
        return n
    }
    
    power := 1
    for power < n {
        power *= 2
    }
    return power
}

// GetRequiredSRSSize возвращает необходимый размер SRS для NodeWidth
func GetRequiredSRSSize(nodeWidth int) int {
    // SRS должен быть как минимум равен NodeWidth
    // Добавляем небольшой запас (1.5x) для безопасности
    return nextPowerOfTwo(nodeWidth * 3 / 2)
}

// commitPolynomial создает KZG коммитмент для полинома
func commitPolynomial(values []fr.Element, srs *kzg_bls12381.SRS) ([]byte, error) {
    if srs == nil {
        return hashValues(values), nil
    }
    
    // Используем values напрямую как полином, без копирования
    poly := polynomial.Polynomial(values)
    
    // Создаем KZG коммитмент
    digest, err := kzg_bls12381.Commit(poly, srs.Pk)
    if err != nil {
        return nil, fmt.Errorf("ошибка создания KZG commitment: %w", err)
    }
    
    commitmentBytes := digest.Bytes()
    return commitmentBytes[:], nil
}


// hashValues - fallback функция для хеширования значений (когда SRS недоступен)
func hashValues(values []fr.Element) []byte {
	hasher := blake3.New()
	
	for i := range values {
		coeffBytes := values[i].Bytes()
		hasher.Write(coeffBytes[:])
	}
	
	return hasher.Sum(nil)
}

// openPolynomial создает proof открытия полинома в точке
func openPolynomial(values []fr.Element, point fr.Element, srs *kzg_bls12381.SRS) ([]byte, fr.Element, error) {
	if srs == nil {
		// Если SRS не настроен, просто вычисляем значение в точке
		poly := make(polynomial.Polynomial, len(values))
		copy(poly, values)
		value := poly.Eval(&point)
		return nil, value, nil
	}
	
	// Создаем полином
	poly := make(polynomial.Polynomial, len(values))
	copy(poly, values)
	
	// Создаем KZG proof
	openingProof, err := kzg_bls12381.Open(poly, point, srs.Pk)
	if err != nil {
		return nil, fr.Element{}, fmt.Errorf("ошибка создания KZG proof: %w", err)
	}
	
	// Вычисляем значение в точке
	value := poly.Eval(&point)
	
	// Сериализуем proof (берем H - это Digest)
	proofBytes := openingProof.H.Bytes()
	
	return proofBytes[:], value, nil
}


// verifyOpening проверяет KZG proof открытия
func verifyOpening(commitment []byte, proof []byte, point fr.Element, value fr.Element, srs *kzg_bls12381.SRS) (bool, error) {
	if srs == nil {
		return false, fmt.Errorf("SRS не настроен для верификации")
	}
	
	// Десериализуем коммитмент
	var digest kzg_bls12381.Digest
	_, err := digest.SetBytes(commitment)
	if err != nil {
		return false, fmt.Errorf("ошибка десериализации коммитмента: %w", err)
	}
	
	// Десериализуем proof
	var proofDigest kzg_bls12381.Digest
	_, err = proofDigest.SetBytes(proof)
	if err != nil {
		return false, fmt.Errorf("ошибка десериализации proof: %w", err)
	}
	
	// Создаем OpeningProof
	openingProof := kzg_bls12381.OpeningProof{
		H:            proofDigest,
		ClaimedValue: value,
	}
	
	// Проверяем proof используя VerifyingKey из SRS
	err = kzg_bls12381.Verify(&digest, &openingProof, point, srs.Vk)
	if err != nil {
		return false, nil
	}
	
	return true, nil
}

// valuesToPolynomial конвертирует массив значений в полином
func valuesToPolynomial(values []fr.Element) []fr.Element {
	// Просто возвращаем копию значений как коэффициенты полинома
	result := make([]fr.Element, len(values))
	copy(result, values)
	return result
}

// hashToFieldElement хеширует байты в элемент поля Fr
func hashToFieldElement(data []byte) fr.Element {
	hasher := blake3.New()
	hasher.Write(data)
	hash := hasher.Sum(nil)
	
	var elem fr.Element
	elem.SetBytes(hash)
	
	return elem
}
