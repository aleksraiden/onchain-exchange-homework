// memory_pools_bench_test.go (ИСПРАВЛЕННАЯ ВЕРСИЯ)

package verkletree

import (
	"sync"
	"testing"
	
	"github.com/consensys/gnark-crypto/ecc/bls12-381/fr"
	kzg_bls12381 "github.com/consensys/gnark-crypto/ecc/bls12-381/kzg"
)

// ==========================================
// ГЛОБАЛЬНЫЙ SRS - создаем ОДИН РАЗ
// ==========================================

var (
	globalTestSRS     *kzg_bls12381.SRS  // ✅ Правильный тип
	globalTestSRSOnce sync.Once
)

// getTestSRS возвращает переиспользуемый SRS
func getTestSRS() *kzg_bls12381.SRS {  // ✅ Правильный тип
	globalTestSRSOnce.Do(func() {
		var err error
		globalTestSRS, err = InitSRS(256)
		if err != nil {
			panic(err)
		}
	})
	return globalTestSRS
}

// Глобальные переменные для результатов
var (
	benchResultPoolsTest []fr.Element
	benchBufPoolsTest    []byte
)

// ==========================================
// БЫСТРЫЕ БЕНЧМАРКИ
// ==========================================

// BenchmarkMemoryPoolsWithout - бенчмарк без пулов
func BenchmarkMemoryPoolsWithout(b *testing.B) {
	var elements []fr.Element
	var buf []byte
	
	b.ResetTimer()
	
	for i := 0; i < b.N; i++ {
		elements = make([]fr.Element, 128)
		buf = make([]byte, 1024)
		
		elements[0].SetUint64(uint64(i))
		buf[0] = byte(i)
	}
	
	benchResultPoolsTest = elements
	benchBufPoolsTest = buf
}

// BenchmarkMemoryPoolsWith - бенчмарк с пулами
func BenchmarkMemoryPoolsWith(b *testing.B) {
	var elements []fr.Element
	var buf []byte
	
	b.ResetTimer()
	
	for i := 0; i < b.N; i++ {
		elements = getFrElementSlice(128)
		buf = getByteBuffer(1024)
		
		elements[0].SetUint64(uint64(i))
		buf[0] = byte(i)
		
		putFrElementSlice(elements)
		putByteBuffer(buf)
	}
	
	benchResultPoolsTest = elements
	benchBufPoolsTest = buf
}

// BenchmarkMemoryPoolsCommit - симуляция commit
func BenchmarkMemoryPoolsCommit(b *testing.B) {
	b.Run("WithoutPools", func(b *testing.B) {
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			values := make([]fr.Element, 128)
			
			for j := 0; j < 128; j++ {
				values[j].SetUint64(uint64(j))
			}
			
			_ = values[0].Bytes()
		}
	})
	
	b.Run("WithPools", func(b *testing.B) {
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			values := getFrElementSlice(128)
			
			for j := 0; j < 128; j++ {
				values[j].SetUint64(uint64(j))
			}
			
			_ = values[0].Bytes()
			putFrElementSlice(values)
		}
	})
}

// ==========================================
// ИНТЕГРАЦИОННЫЕ ТЕСТЫ (с деревом)
// ==========================================

// BenchmarkMemoryPoolsInsertShort - быстрая версия (10 операций)
func BenchmarkMemoryPoolsInsertShort(b *testing.B) {
	if testing.Short() {
		b.Skip("Skipping in short mode")
	}
	
	// Используем ГЛОБАЛЬНЫЙ SRS
	srs := getTestSRS()
	tree, _ := New(8, 128, srs, nil)
	tree.SetOptimizationLevel(OptimizationMax)
	
	b.ResetTimer()
	
	for i := 0; i < b.N; i++ {
		batch := tree.BeginBatch()
		
		// Только 10 вставок вместо 1000
		for j := 0; j < 10; j++ {
			userData := &UserData{
				Balances: map[string]float64{"USD": float64(j * 100)},
			}
			batch.AddUserData(poolsTestUserID(i*10+j), userData)
		}
		
		tree.CommitBatch(batch)
	}
}

// BenchmarkMemoryPoolsProofShort - быстрая версия генерации proof
func BenchmarkMemoryPoolsProofShort(b *testing.B) {
	if testing.Short() {
		b.Skip("Skipping in short mode")
	}
	
	// Используем ГЛОБАЛЬНЫЙ SRS
	srs := getTestSRS()
	tree, _ := New(8, 128, srs, nil)
	
	// Только 100 пользователей вместо 1000
	batch := tree.BeginBatch()
	for i := 0; i < 100; i++ {
		userData := &UserData{
			Balances: map[string]float64{"USD": float64(i * 100)},
		}
		batch.AddUserData(poolsTestUserID(i), userData)
	}
	tree.CommitBatch(batch)
	tree.WaitForCommit()
	
	b.ResetTimer()
	
	for i := 0; i < b.N; i++ {
		tree.GenerateProof(poolsTestUserID(i % 100))
	}
}

// poolsTestUserID генерирует ID пользователя
func poolsTestUserID(i int) string {
	return "pooluser_" + 
	       string(rune('0'+(i/1000000)%10)) +
	       string(rune('0'+(i/100000)%10)) +
	       string(rune('0'+(i/10000)%10)) +
	       string(rune('0'+(i/1000)%10)) +
	       string(rune('0'+(i/100)%10)) + 
	       string(rune('0'+(i/10)%10)) + 
	       string(rune('0'+i%10))
}
