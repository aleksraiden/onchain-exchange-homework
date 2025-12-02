package optimized

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"os"
)

// BenchmarkResultSummary - результат одного benchmark
type BenchmarkResultSummary struct {
	NsPerOp     float64            `json:"ns_per_op"`
	BytesPerOp  int64              `json:"bytes_per_op"`
	AllocsPerOp int64              `json:"allocs_per_op"`
	OpsPerSec   float64            `json:"ops_per_sec"`
	Extra       map[string]float64 `json:"extra,omitempty"`
}

// runBenchmark - запускает benchmark и возвращает агрегацию
func runBenchmark(t *testing.T, bench func(*testing.B)) BenchmarkResultSummary {
	res := testing.Benchmark(bench)

	ns := float64(res.NsPerOp())        // ns/op [web:3]
	bytes := res.AllocedBytesPerOp()    // B/op [web:3]
	allocs := res.AllocsPerOp()         // allocs/op [web:3]
	opsPerSec := 0.0
	if ns > 0 {
		opsPerSec = 1e9 / ns
	}

	return BenchmarkResultSummary{
		NsPerOp:     ns,
		BytesPerOp:  bytes,
		AllocsPerOp: allocs,
		OpsPerSec:   opsPerSec,
	}
}

func fmtTime(ns float64) string {
	switch {
	case ns < 1e3:
		return fmt.Sprintf("%.1f ns", ns)
	case ns < 1e6:
		return fmt.Sprintf("%.1f µs", ns/1e3)
	default:
		return fmt.Sprintf("%.1f ms", ns/1e6)
	}
}

func fmtOps(ops float64) string {
	switch {
	case ops >= 1e6:
		return fmt.Sprintf("%.1f M ops/sec", ops/1e6)
	case ops >= 1e3:
		return fmt.Sprintf("%.1f K ops/sec", ops/1e3)
	default:
		return fmt.Sprintf("%.1f ops/sec", ops)
	}
}

func printLine(t *testing.T, name string, r BenchmarkResultSummary) {
	t.Logf("  %-30s %12s   %18s   %6d B/op   %6d allocs/op",
		name, fmtTime(r.NsPerOp), fmtOps(r.OpsPerSec), r.BytesPerOp, r.AllocsPerOp)
}

// TestBenchmarkReport - живой отчет на основе реальных benchmark-запусков
func TestBenchmarkReport(t *testing.T) {
	t.Log("\n" + strings.Repeat("=", 100))
	t.Log("📊 VERKLE TREE - ЖИВОЙ BENCHMARK ОТЧЕТ")
	t.Log(strings.Repeat("=", 100))
	t.Log("\n⏳ Запуск внутренних benchmarks (может занять пару минут)...")

	results := map[string]BenchmarkResultSummary{}

	// INSERT
	results["Insert"] = runBenchmark(t, BenchmarkInsert)
	results["Batch8"] = runBenchmark(t, BenchmarkBatchInsert8)
	results["Batch16"] = runBenchmark(t, BenchmarkBatchInsert16)
	results["Batch32"] = runBenchmark(t, BenchmarkBatchInsert32)
	results["Batch64"] = runBenchmark(t, BenchmarkBatchInsert64)
	results["Batch128"] = runBenchmark(t, BenchmarkBatchInsert128)

	// GET
	results["Get"] = runBenchmark(t, BenchmarkGet)
	results["GetCold"] = runBenchmark(t, BenchmarkGetColdCache)

	// Proof gen
	results["ProofGen"] = runBenchmark(t, BenchmarkProofGeneration)
	results["ProofGenPar"] = runBenchmark(t, BenchmarkProofGenerationParallel)

	// Bundled
	results["Bundled10"] = runBenchmark(t, BenchmarkBundledProof10)
	results["Bundled50"] = runBenchmark(t, BenchmarkBundledProof50)
	results["Bundled100"] = runBenchmark(t, BenchmarkBundledProof100)

	// Verification
	results["VerifySingle"] = runBenchmark(t, BenchmarkProofVerification)
	results["VerifyBundled"] = runBenchmark(t, BenchmarkBundledProofVerification)

	// Full workflow
	results["FullWorkflow"] = runBenchmark(t, BenchmarkFullWorkflow)

	t.Log("\n" + strings.Repeat("=", 100))
	t.Log("⚡ INSERT")
	t.Log(strings.Repeat("─", 100))
	printLine(t, "Insert", results["Insert"])
	printLine(t, "BatchInsert-8", results["Batch8"])
	printLine(t, "BatchInsert-16", results["Batch16"])
	printLine(t, "BatchInsert-32", results["Batch32"])
	printLine(t, "BatchInsert-64", results["Batch64"])
	printLine(t, "BatchInsert-128", results["Batch128"])

	// эффективность batch 128 vs single
	nsSingle := results["Insert"].NsPerOp
	nsBatch128PerElem := results["Batch128"].NsPerOp / 128.0
	if nsBatch128PerElem > 0 {
		t.Logf("  👉 Batch(128) ускорение относительно single: %.1fx",
			nsSingle/nsBatch128PerElem)
	}

	t.Log("\n🔥 GET")
	t.Log(strings.Repeat("─", 100))
	printLine(t, "Get (cache hit)", results["Get"])
	printLine(t, "Get (cold cache)", results["GetCold"])
	if results["Get"].NsPerOp > 0 {
		t.Logf("  👉 Cache ускоряет чтение в %.1fx раз",
			results["GetCold"].NsPerOp/results["Get"].NsPerOp)
	}

	t.Log("\n📜 Proof Generation")
	t.Log(strings.Repeat("─", 100))
	printLine(t, "Single (seq)", results["ProofGen"])
	printLine(t, "Single (parallel)", results["ProofGenPar"])
	if results["ProofGenPar"].NsPerOp > 0 {
		t.Logf("  👉 Параллелизм даёт ускорение: %.1fx",
			results["ProofGen"].NsPerOp/results["ProofGenPar"].NsPerOp)
	}
	printLine(t, "Bundled (10 users)", results["Bundled10"])
	printLine(t, "Bundled (50 users)", results["Bundled50"])
	printLine(t, "Bundled (100 users)", results["Bundled100"])

	t.Log("\n🏆 Bundled vs Single (50 users)")
	t.Log(strings.Repeat("─", 100))
	singleTotal50 := results["ProofGen"].NsPerOp * 50
	bundled50 := results["Bundled50"].NsPerOp
	speedup := singleTotal50 / bundled50
	t.Logf("  Single 50x total:  %s", fmtTime(singleTotal50))
	t.Logf("  Bundled (50):      %s", fmtTime(bundled50))
	t.Logf("  👉 Ускорение Bundled: %.1fx", speedup)

	t.Log("\n✅ Verification")
	t.Log(strings.Repeat("─", 100))
	printLine(t, "Single proof verify", results["VerifySingle"])
	printLine(t, "Bundled (50 users)", results["VerifyBundled"])

	t.Log("\n🔄 Full workflow (Insert + Proof + Verify)")
	t.Log(strings.Repeat("─", 100))
	printLine(t, "Full workflow", results["FullWorkflow"])

	t.Log("\n🚀 Пропускная способность (примерно):")
	t.Log(strings.Repeat("─", 100))
	t.Logf("  Insert:              %s", fmtOps(results["Insert"].OpsPerSec))
	t.Logf("  Get (cache hit):     %s", fmtOps(results["Get"].OpsPerSec))
	t.Logf("  Proof gen (parallel):%s", fmtOps(results["ProofGenPar"].OpsPerSec))
	t.Logf("  Full workflow:       %s", fmtOps(results["FullWorkflow"].OpsPerSec))

	t.Log("\n" + strings.Repeat("=", 100))
	t.Log("✅ Живой BENCHMARK отчёт завершён")
	t.Log(strings.Repeat("=", 100))

	// опционально: сохранить JSON (сейчас только логируем длину)
	if data, err := json.MarshalIndent(results, "", "  "); err == nil {
		t.Logf("JSON results size: %d bytes", len(data))
	}
	
	// Сохранение в JSON-файл
	filename := "benchmark_results.json"
	data, err := json.MarshalIndent(results, "", "  ")
	if err != nil {
		t.Logf("⚠️  Не удалось сериализовать результаты в JSON: %v", err)
		return
	}

	if err := os.WriteFile(filename, data, 0o644); err != nil {
		t.Logf("⚠️  Не удалось записать файл %s: %v", filename, err)
		return
	}

	t.Logf("📄 Результаты benchmarks сохранены в файл: %s (%.1f KB)", filename, float64(len(data))/1024.0)
}
