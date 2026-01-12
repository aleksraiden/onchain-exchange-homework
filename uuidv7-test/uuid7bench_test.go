package uuid7bench

import (
	"testing"

	uuid7 "github.com/GoWebProd/uuid7"
	googleuuid "github.com/google/uuid"
)

func Benchmark_GoWebProd_New(b *testing.B) {
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_ = uuid7.New() // основной способ
	}
}

func Benchmark_Google_NewV7(b *testing.B) {
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_, _ = googleuuid.NewV7() // возвращает error
	}
}

func Benchmark_Google_NewV7_Must(b *testing.B) {
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_ = googleuuid.Must(googleuuid.NewV7())
	}
}