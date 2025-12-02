// Создайте файл memory_pools_test.go

package verkletree

import (
	"runtime"
	"testing"
	"strings"
	"time"
	
	"github.com/consensys/gnark-crypto/ecc/bls12-381/fr"
)

// TestMemoryPoolsImpact - измерение влияния memory pools
func TestMemoryPoolsImpact(t *testing.T) {
	operations := 10000
	
	t.Log("\n" + strings.Repeat("=", 100))
	t.Log("ВЛИЯНИЕ MEMORY POOLS НА ПРОИЗВОДИТЕЛЬНОСТЬ")
	t.Log(strings.Repeat("=", 100))
	
	// === БЕЗ POOLS (baseline) ===
	t.Log("\n1️⃣  БЕЗ Memory Pools (baseline)")
	
	runtime.GC() // Принудительный GC для чистоты эксперимента
	var m1 runtime.MemStats
	runtime.ReadMemStats(&m1)
	
	start := time.Now()
	
	// Симулируем аллокации без пулов
	for i := 0; i < operations; i++ {
		// Типичные аллокации в commitPolynomial
		_ = make([]fr.Element, 256)
		_ = make([]byte, 1024)
		_ = make([]byte, 32)
	}
	
	withoutPoolsTime := time.Since(start)
	
	var m2 runtime.MemStats
	runtime.ReadMemStats(&m2)
	
	allocsWithout := m2.Mallocs - m1.Mallocs
	bytesAllocWithout := m2.TotalAlloc - m1.TotalAlloc
	
	t.Logf("   Время: %v", withoutPoolsTime)
	t.Logf("   Аллокаций: %d", allocsWithout)
	t.Logf("   Память: %d MB", bytesAllocWithout/(1024*1024))
	
	// === С POOLS ===
	t.Log("\n2️⃣  С Memory Pools")
	
	runtime.GC()
	var m3 runtime.MemStats
	runtime.ReadMemStats(&m3)
	
	start = time.Now()
	
	// Те же операции но с пулами
	for i := 0; i < operations; i++ {
		// Берем из пулов
		elements := getFrElementSlice(256)
		buf1 := getByteBuffer(1024)
		buf2 := getHashBuffer()
		
		// Возвращаем в пулы
		putFrElementSlice(elements)
		putByteBuffer(buf1)
		putHashBuffer(buf2)
	}
	
	withPoolsTime := time.Since(start)
	
	var m4 runtime.MemStats
	runtime.ReadMemStats(&m4)
	
	allocsWith := m4.Mallocs - m3.Mallocs
	bytesAllocWith := m4.TotalAlloc - m3.TotalAlloc
	
	t.Logf("   Время: %v", withPoolsTime)
	t.Logf("   Аллокаций: %d", allocsWith)
	t.Logf("   Память: %d MB", bytesAllocWith/(1024*1024))
	
	// === СРАВНЕНИЕ ===
	t.Log("\n📊 РЕЗУЛЬТАТ:")
	
	timeSpeedup := float64(withoutPoolsTime) / float64(withPoolsTime)
	allocReduction := float64(allocsWithout-allocsWith) / float64(allocsWithout) * 100
	memReduction := float64(bytesAllocWithout-bytesAllocWith) / float64(bytesAllocWithout) * 100
	
	t.Logf("   Ускорение: %.2fx", timeSpeedup)
	t.Logf("   Снижение аллокаций: %.1f%%", allocReduction)
	t.Logf("   Снижение памяти: %.1f%%", memReduction)
	
	// === ЭКСТРАПОЛЯЦИЯ НА ВАШ СЦЕНАРИЙ ===
	t.Log("\n🎯 ВЛИЯНИЕ НА ВАШ СЦЕНАРИЙ (50K операций, 300ms budget):")
	
	// В вашем случае каждая операция делает ~2-3 commit
	commitsPerOp := 2.5
	totalCommits := 50000 * commitsPerOp
	
	timePerCommit := float64(withoutPoolsTime.Microseconds()) / float64(operations)
	totalTimeWithout := timePerCommit * totalCommits / 1000 // в миллисекундах
	
	timePerCommitWith := float64(withPoolsTime.Microseconds()) / float64(operations)
	totalTimeWith := timePerCommitWith * totalCommits / 1000
	
	savings := totalTimeWithout - totalTimeWith
	
	t.Logf("   Без pools: ~%.0f ms", totalTimeWithout)
	t.Logf("   С pools:   ~%.0f ms", totalTimeWith)
	t.Logf("   Экономия:  ~%.0f ms (%.1f%% от 300ms budget)", 
		savings, savings/300*100)
	
	if savings > 10 {
		t.Log("\n✅ РЕКОМЕНДАЦИЯ: Используйте memory pools - значительный эффект!")
	} else if savings > 5 {
		t.Log("\n✅ РЕКОМЕНДАЦИЯ: Используйте memory pools - умеренный эффект")
	} else {
		t.Log("\n⚠️  Memory pools дают небольшой эффект в этом сценарии")
	}
	
	t.Log("\n" + strings.Repeat("=", 100))
}

// BenchmarkWithoutPools - бенчмарк без пулов
func BenchmarkWithoutPools(b *testing.B) {
	for i := 0; i < b.N; i++ {
		_ = make([]fr.Element, 256)
		_ = make([]byte, 1024)
	}
}

// BenchmarkWithPools - бенчмарк с пулами
func BenchmarkWithPools(b *testing.B) {
	for i := 0; i < b.N; i++ {
		elements := getFrElementSlice(256)
		buf := getByteBuffer(1024)
		putFrElementSlice(elements)
		putByteBuffer(buf)
	}
}
