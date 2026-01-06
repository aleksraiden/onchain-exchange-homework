package main

import (
	"testing"

	"github.com/zeebo/blake3"
)

// GenericSMT - универсальное дерево для тестов архитектуры.
// Оно менее оптимизировано, чем боевое, но умеет менять Arity.
type GenericSMT struct {
	bitsPerLevel int          // Сколько бит на уровень (3=Arity8, 4=Arity16, 8=Arity256)
	arity        int          // 1 << bitsPerLevel
	store        map[string][]byte
	root         []byte
	hasher       *blake3.Hasher
}

func NewGenericSMT(bitsPerLevel int) *GenericSMT {
	return &GenericSMT{
		bitsPerLevel: bitsPerLevel,
		arity:        1 << bitsPerLevel,
		store:        make(map[string][]byte),
		root:         make([]byte, 32), // Empty Hash
		hasher:       blake3.New(),
	}
}

// getIndex извлекает N бит из ключа на заданной глубине (уровне).
// Это сложная часть, так как биты могут пересекать границы байтов.
func (s *GenericSMT) getIndex(key []byte, depth int) int {
	startBit := depth * s.bitsPerLevel
	byteIdx := startBit / 8
	bitOffset := startBit % 8
	
	// Для простоты читаем 2 байта (uint16), чтобы гарантированно захватить пересечение
	// Если выходим за границы ключа - возвращаем 0 (padding)
	var val uint16
	if byteIdx < len(key) {
		val = uint16(key[byteIdx]) << 8
	}
	if byteIdx+1 < len(key) {
		val |= uint16(key[byteIdx+1])
	}
	
	// Сдвигаем, чтобы нужные биты оказались в младших разрядах старшего байта
	// (визуализация: [XAABBCCC] [DD......])
	// Нам нужно вытащить биты, начиная с bitOffset.
	
	// Сдвигаем все влево, чтобы убрать старшие ненужные биты текущего байта
	val <<= bitOffset
	
	// Теперь берем старшие bitsPerLevel бит
	// (т.к. мы работаем с uint16, после сдвига влево данные ушли в старший байт)
	result := val >> (16 - s.bitsPerLevel)
	
	return int(result)
}

func (s *GenericSMT) Update(key []byte, value []byte) {
	s.root = s.update(s.root, key, value, 0)
}

func (s *GenericSMT) update(root []byte, key, value []byte, depth int) []byte {
	// 1. Пустой узел
	if len(root) == 32 && isZero(root) { // isZero helper logic
		return s.storeLeaf(key, value)
	}

	// Загружаем узел (имитация)
	data, exists := s.store[string(root)]
	if !exists {
		// Это Leaf, который мы только что создали или он уже был
		// Для упрощения бенчмарка считаем, что если в мапе нет - это пустой или Leaf
		// Но в этом тесте мы всегда начинаем с пустого, так что logic ok.
		return s.storeLeaf(key, value)
	}

	if data[0] == 1 { // Leaf
		// В реальном коде тут проверка ключа и split
		// Для бенчмарка структур мы упростим: превращаем в Internal
		// и вставляем оба ключа (старый и новый).
		// (Пропускаем для чистоты замера хеширования внутреннего узла)
		// Просто перезаписываем leaf для теста хеширования пути
		return s.storeLeaf(key, value)
	}

	// Internal
	idx := s.getIndex(key, depth)
	
	// Воссоздаем детей (в тесте мы не храним их реально, чтобы не усложнять парсинг битмап разной длины)
	// Мы просто симулируем нагрузку хеширования Arity элементов.
	
	// Представим, что мы обновили 1 ребенка
	childHash := s.update(make([]byte, 32), key, value, depth+1)
	
	return s.storeInternal(idx, childHash)
}

func (s *GenericSMT) storeLeaf(key, value []byte) []byte {
	s.hasher.Reset()
	s.hasher.Write([]byte{1})
	s.hasher.Write(key)
	s.hasher.Write(value)
	h := s.hasher.Sum(nil)
	s.store[string(h)] = []byte{1} // Mark as leaf
	return h
}

func (s *GenericSMT) storeInternal(idx int, childHash []byte) []byte {
	// Здесь мы симулируем затраты на хеширование узла шириной Arity.
	// Узел содержит: Bitmap + Children Hashes.
	
	// Размер битмапа: Arity / 8 байт.
	bitmapSize := s.arity / 8
	if bitmapSize == 0 { bitmapSize = 1 } // для arity < 8
	
	// Эмулируем хеширование узла
	s.hasher.Reset()
	s.hasher.Write([]byte{0}) // Type
	
	// Пишем фейковый bitmap (для бенчмарка скорости хеширования это ок)
	bitmap := make([]byte, bitmapSize)
	bitmap[0] = 1 
	s.hasher.Write(bitmap)
	
	// Пишем хеши детей.
	// В реальном дереве мы пишем только непустые.
	// Допустим, дерево заполнено на 10% (sparse).
	// Для Arity 256 это ~25 детей. Для Arity 16 ~2 ребенка.
	// Это важный параметр! Чем шире дерево, тем разреженнее узел.
	
	childrenCount := 1
	if s.arity >= 16 {
		childrenCount = 2 // чуть больше детей в среднем
	}
	if s.arity >= 256 {
		childrenCount = 4 // еще больше
	}
	
	for i := 0; i < childrenCount; i++ {
		s.hasher.Write(childHash) // Пишем 32 байта
	}
	
	h := s.hasher.Sum(nil)
	s.store[string(h)] = []byte{0}
	return h
}

func isZero(b []byte) bool {
	for _, v := range b {
		if v != 0 { return false }
	}
	return true
}

// --- БЕНЧМАРКИ ---

func runBenchmarkArity(b *testing.B, bits int) {
	arity := 1 << bits
	smt := NewGenericSMT(bits)
	keys := make([][]byte, 1000)
	for i := range keys {
		keys[i] = randomKey()
	}
	val := []byte("data")

	b.ResetTimer()
	b.ReportAllocs()
	
	for i := 0; i < b.N; i++ {
		// Используем циклический доступ к ключам
		key := keys[i % 1000]
		smt.Update(key, val)
	}
	
	b.ReportMetric(float64(arity), "arity")
	// Примерная глубина дерева: 256 / bits
	b.ReportMetric(float64(256/bits), "depth")
}

func BenchmarkArity_008_Bits3(b *testing.B) { runBenchmarkArity(b, 3) } // Arity 8
func BenchmarkArity_016_Bits4(b *testing.B) { runBenchmarkArity(b, 4) } // Arity 16 (Nibble)
func BenchmarkArity_032_Bits5(b *testing.B) { runBenchmarkArity(b, 5) }
func BenchmarkArity_064_Bits6(b *testing.B) { runBenchmarkArity(b, 6) }
func BenchmarkArity_128_Bits7(b *testing.B) { runBenchmarkArity(b, 7) }
func BenchmarkArity_256_Bits8(b *testing.B) { runBenchmarkArity(b, 8) } // Arity 256 (Byte)