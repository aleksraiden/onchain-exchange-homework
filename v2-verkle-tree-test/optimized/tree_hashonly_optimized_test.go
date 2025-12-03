// optimized/tree_hashonly_optimized_test.go

package optimized

import (
	"encoding/json"
    "fmt"
    "sync"
    "testing"
    "time"
)

// TestOptimizedWorkerScaling - тест с исправленными блокировками
func TestOptimizedWorkerScaling(t *testing.T) {
    workerCounts := []int{1, 2, 4, 8, 16}
    opsPerWorker := 1000
    
    t.Logf("\n🚀 OPTIMIZED Worker Scaling:")
    t.Logf("%-10s %-15s %-15s %-15s %-10s", "Workers", "Time", "Ops/Sec", "Speedup", "Status")
    t.Logf("%s", "--------------------------------------------------------------------------------")
    
    baselineTime := time.Duration(0)
    
    for _, workers := range workerCounts {
        config := NewConfigHashOnly()
        config.Workers = workers
        tree, _ := New(config, nil)
        
        start := time.Now()
        
        var wg sync.WaitGroup
        for w := 0; w < workers; w++ {
            wg.Add(1)
            go func(workerID int) {
                defer wg.Done()
                batch := tree.NewBatch()
                for i := 0; i < opsPerWorker; i++ {
                    userID := fmt.Sprintf("opt_%d_%d", workerID, i)
                    userData := &UserData{Balances: map[string]float64{"USD": float64(i)}}
                    data, _ := json.Marshal(userData)
                    batch.Add(userID, data)
                }
                tree.CommitBatch(batch)
            }(w)
        }
        wg.Wait()
        
        elapsed := time.Since(start)
        totalOps := workers * opsPerWorker
        opsPerSec := float64(totalOps) / elapsed.Seconds()
        
        speedup := 1.0
        if baselineTime > 0 {
            speedup = float64(baselineTime) / float64(elapsed)
        } else {
            baselineTime = elapsed
        }
        
        status := "❌"
        if speedup > 0.8*float64(workers) {
            status = "✅"
        }
        
        t.Logf("%-10d %-15v %-15.0f %-15.2fx %s", 
            workers, elapsed, opsPerSec, speedup, status)
        
        tree.Close()
    }
}
