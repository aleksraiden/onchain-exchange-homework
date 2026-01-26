package merkletree

import (
	"fmt"
	"math/rand"
	"testing"
	"time"
)

// BenchmarkUpdateStrategies сравнение стратегий обновления
func BenchmarkUpdateStrategies(b *testing.B) {
	updateCounts := []int{1000, 10000, 100000}

	for _, count := range updateCounts {
		b.Run(fmt.Sprintf("Updates_%d", count), func(b *testing.B) {

			// 1. Обычные вставки
			b.Run("Regular", func(b *testing.B) {
				tree := New[*Account](DefaultConfig())

				// Предзаполнение
				for i := uint64(0); i < 50000; i++ {
					tree.Insert(NewAccount(i, StatusUser))
				}

				b.ResetTimer()

				for i := 0; i < b.N; i++ {
					for j := 0; j < count; j++ {
						uid := uint64(50000 + i*count + j)
						tree.Insert(NewAccount(uid, StatusUser))
					}
				}

				b.ReportMetric(float64(count*b.N)/b.Elapsed().Seconds(), "ops/sec")
			})

			// 2. Batch вставки
			b.Run("Batch", func(b *testing.B) {
				tree := New[*Account](DefaultConfig())

				for i := uint64(0); i < 50000; i++ {
					tree.Insert(NewAccount(i, StatusUser))
				}

				b.ResetTimer()

				for i := 0; i < b.N; i++ {
					items := make([]*Account, count)
					for j := 0; j < count; j++ {
						uid := uint64(50000 + i*count + j)
						items[j] = NewAccount(uid, StatusUser)
					}
					tree.InsertBatch(items)
				}

				b.ReportMetric(float64(count*b.N)/b.Elapsed().Seconds(), "ops/sec")
			})

			// 3. Optimized Tree (lazy hashing)
			b.Run("Optimized", func(b *testing.B) {
				tree := NewOptimizedTree[*Account](DefaultConfig())

				for i := uint64(0); i < 50000; i++ {
					tree.Insert(NewAccount(i, StatusUser))
				}

				b.ResetTimer()

				for i := 0; i < b.N; i++ {
					for j := 0; j < count; j++ {
						uid := uint64(50000 + i*count + j)
						tree.Insert(NewAccount(uid, StatusUser))
					}
					// Вычисляем корень только в конце
					tree.ComputeRoot()
				}

				b.ReportMetric(float64(count*b.N)/b.Elapsed().Seconds(), "ops/sec")
			})

			// 4. Write Buffer
			b.Run("WriteBuffer", func(b *testing.B) {
				tree := New[*Account](DefaultConfig())

				for i := uint64(0); i < 50000; i++ {
					tree.Insert(NewAccount(i, StatusUser))
				}

				buffer := NewWriteBuffer(tree, 1000, 10*time.Millisecond)
				defer buffer.Close()

				b.ResetTimer()

				for i := 0; i < b.N; i++ {
					for j := 0; j < count; j++ {
						uid := uint64(50000 + i*count + j)
						buffer.Insert(NewAccount(uid, StatusUser))
					}
					buffer.Flush()
				}

				b.ReportMetric(float64(count*b.N)/b.Elapsed().Seconds(), "ops/sec")
			})
		})
	}
}

// BenchmarkManagerUpdateStrategies сравнение на уровне менеджера
func BenchmarkManagerUpdateStrategies(b *testing.B) {
	numTrees := 10
	updatesPerTree := 1000

	b.Run("Sequential", func(b *testing.B) {
		manager := NewManager[*Account](DefaultConfig())

		// Создаем деревья
		for i := 0; i < numTrees; i++ {
			treeName := fmt.Sprintf("tree_%d", i)
			tree, _ := manager.CreateTree(treeName)

			// Предзаполнение
			for j := uint64(0); j < 5000; j++ {
				tree.Insert(NewAccount(j+uint64(i*10000), StatusUser))
			}
		}

		b.ResetTimer()

		for i := 0; i < b.N; i++ {
			batch := manager.BeginBatch()

			for treeIdx := 0; treeIdx < numTrees; treeIdx++ {
				treeName := fmt.Sprintf("tree_%d", treeIdx)

				for j := 0; j < updatesPerTree; j++ {
					uid := uint64(i*numTrees*updatesPerTree + treeIdx*updatesPerTree + j + 100000)
					batch.InsertToTree(treeName, NewAccount(uid, StatusUser))
				}
			}

			batch.Commit()
		}

		totalOps := numTrees * updatesPerTree * b.N
		b.ReportMetric(float64(totalOps)/b.Elapsed().Seconds(), "ops/sec")
	})

	b.Run("Parallel", func(b *testing.B) {
		manager := NewManager[*Account](DefaultConfig())

		for i := 0; i < numTrees; i++ {
			treeName := fmt.Sprintf("tree_%d", i)
			tree, _ := manager.CreateTree(treeName)

			for j := uint64(0); j < 5000; j++ {
				tree.Insert(NewAccount(j+uint64(i*10000), StatusUser))
			}
		}

		b.ResetTimer()

		for i := 0; i < b.N; i++ {
			batch := manager.BeginParallelBatch()

			for treeIdx := 0; treeIdx < numTrees; treeIdx++ {
				treeName := fmt.Sprintf("tree_%d", treeIdx)

				for j := 0; j < updatesPerTree; j++ {
					uid := uint64(i*numTrees*updatesPerTree + treeIdx*updatesPerTree + j + 100000)
					batch.InsertToTree(treeName, NewAccount(uid, StatusUser))
				}
			}

			batch.Commit()
		}

		totalOps := numTrees * updatesPerTree * b.N
		b.ReportMetric(float64(totalOps)/b.Elapsed().Seconds(), "ops/sec")
	})
}

// BenchmarkLazyVsEagerHashing сравнение lazy vs eager
func BenchmarkLazyVsEagerHashing(b *testing.B) {
	sizes := []int{100, 1000, 10000}

	for _, size := range sizes {
		b.Run(fmt.Sprintf("Size_%d", size), func(b *testing.B) {

			b.Run("Eager", func(b *testing.B) {
				tree := New[*Account](DefaultConfig())

				b.ResetTimer()

				for i := 0; i < b.N; i++ {
					for j := 0; j < size; j++ {
						tree.Insert(NewAccount(uint64(i*size+j), StatusUser))
					}
					tree.ComputeRoot()
				}
			})

			b.Run("Lazy", func(b *testing.B) {
				tree := NewOptimizedTree[*Account](DefaultConfig())

				b.ResetTimer()

				for i := 0; i < b.N; i++ {
					for j := 0; j < size; j++ {
						tree.Insert(NewAccount(uint64(i*size+j), StatusUser))
					}
					tree.ComputeRoot()
				}
			})

			b.Run("LazyBatch", func(b *testing.B) {
				tree := NewOptimizedTree[*Account](DefaultConfig())

				b.ResetTimer()

				for i := 0; i < b.N; i++ {
					items := make([]*Account, size)
					for j := 0; j < size; j++ {
						items[j] = NewAccount(uint64(i*size+j), StatusUser)
					}
					tree.InsertBatch(items)
					tree.ComputeRoot()
				}
			})
		})
	}
}

// BenchmarkHashingOverhead измеряет overhead хеширования
func BenchmarkHashingOverhead(b *testing.B) {
	tree := NewOptimizedTree[*Account](DefaultConfig())

	// Заполняем дерево
	for i := uint64(0); i < 100000; i++ {
		tree.Insert(NewAccount(i, StatusUser))
	}

	b.Run("InsertOnly", func(b *testing.B) {
		b.ResetTimer()

		for i := 0; i < b.N; i++ {
			tree.Insert(NewAccount(uint64(100000+i), StatusUser))
		}
	})

	b.Run("InsertAndHash", func(b *testing.B) {
		b.ResetTimer()

		for i := 0; i < b.N; i++ {
			tree.Insert(NewAccount(uint64(200000+i), StatusUser))
			tree.ComputeRoot()
		}
	})

	b.Run("BatchInsertAndHash", func(b *testing.B) {
		batchSize := 1000

		b.ResetTimer()

		for i := 0; i < b.N; i++ {
			items := make([]*Account, batchSize)
			for j := 0; j < batchSize; j++ {
				items[j] = NewAccount(uint64(300000+i*batchSize+j), StatusUser)
			}
			tree.InsertBatch(items)
			tree.ComputeRoot()
		}
	})
}

// BenchmarkCacheEfficiency влияние размера кеша
func BenchmarkCacheEfficiency(b *testing.B) {
	cacheSizes := []int{0, 1000, 10000, 100000}

	for _, cacheSize := range cacheSizes {
		b.Run(fmt.Sprintf("Cache_%d", cacheSize), func(b *testing.B) {
			cfg := &Config{
				MaxDepth:    3,
				CacheSize:   cacheSize,
				CacheShards: 8,
			}

			tree := New[*Account](cfg)

			// Заполняем
			for i := uint64(0); i < 100000; i++ {
				tree.Insert(NewAccount(i, StatusUser))
			}

			b.ResetTimer()

			// Случайные обновления
			rng := rand.New(rand.NewSource(42))
			for i := 0; i < b.N; i++ {
				uid := uint64(rng.Intn(100000))
				acc, _ := tree.Get(uid)
				// Меняем статус вместо баланса
				if acc.Status == StatusUser {
					acc.Status = StatusMM
				} else {
					acc.Status = StatusUser
				}
				tree.Insert(acc)
			}
		})
	}
}

// BenchmarkDirtyNodeTracking overhead отслеживания dirty nodes
func BenchmarkDirtyNodeTracking(b *testing.B) {
	b.Run("WithTracking", func(b *testing.B) {
		tree := NewOptimizedTree[*Account](DefaultConfig())

		b.ResetTimer()

		for i := 0; i < b.N; i++ {
			tree.Insert(NewAccount(uint64(i), StatusUser))

			if i%1000 == 0 {
				tree.ComputeRoot()
				dirtyCount := tree.GetDirtyNodeCount()
				_ = dirtyCount
			}
		}
	})

	b.Run("WithoutTracking", func(b *testing.B) {
		tree := New[*Account](DefaultConfig())

		b.ResetTimer()

		for i := 0; i < b.N; i++ {
			tree.Insert(NewAccount(uint64(i), StatusUser))

			if i%1000 == 0 {
				tree.ComputeRoot()
			}
		}
	})
}

// TestOptimizedTreeCorrectness проверка корректности
func TestOptimizedTreeCorrectness(t *testing.T) {
	regularTree := New[*Account](DefaultConfig())
	optimizedTree := NewOptimizedTree[*Account](DefaultConfig())

	// Вставляем одинаковые данные
	for i := uint64(0); i < 10000; i++ {
		acc := NewAccountDeterministic(i, StatusUser)
		regularTree.Insert(acc)
		optimizedTree.Insert(acc)
	}

	// Корни должны совпадать
	root1 := regularTree.ComputeRoot()
	root2 := optimizedTree.ComputeRoot()

	if root1 != root2 {
		t.Errorf("Корни не совпадают:\nRegular:    %x\nOptimized:  %x",
			root1[:16], root2[:16])
	}

	// Проверка Get
	for i := uint64(0); i < 100; i++ {
		acc1, ok1 := regularTree.Get(i)
		acc2, ok2 := optimizedTree.Get(i)

		if ok1 != ok2 {
			t.Errorf("Get(%d): existence mismatch", i)
		}

		if ok1 && acc1.UID != acc2.UID {
			t.Errorf("Get(%d): data mismatch", i)
		}
	}

	t.Logf("✓ Regular и Optimized деревья идентичны")
	t.Logf("  Root: %x", root1[:16])
	t.Logf("  Size: %d", regularTree.Size())
}

// TestWriteBufferCorrectness проверка write buffer
func TestWriteBufferCorrectness(t *testing.T) {
	tree := New[*Account](DefaultConfig())
	buffer := NewWriteBuffer(tree, 100, 50*time.Millisecond)
	defer buffer.Close()

	// Вставляем через буфер
	for i := uint64(0); i < 1000; i++ {
		buffer.Insert(NewAccount(i, StatusUser))
	}

	// Ждем автоматического flush
	time.Sleep(100 * time.Millisecond)

	// Проверяем
	if tree.Size() != 1000 {
		t.Errorf("Ожидалось 1000 элементов, получено %d", tree.Size())
	}

	// Проверяем данные
	for i := uint64(0); i < 1000; i++ {
		if _, ok := tree.Get(i); !ok {
			t.Errorf("Элемент %d не найден", i)
		}
	}

	t.Logf("✓ WriteBuffer работает корректно")
}

// BenchmarkRealWorldScenario реалистичный сценарий
func BenchmarkRealWorldScenario(b *testing.B) {
	// Сценарий: биржа с обновлениями статусов аккаунтов

	b.Run("Standard", func(b *testing.B) {
		manager := NewManager[*Account](DefaultConfig())
		manager.CreateTree("accounts")
		tree, _ := manager.GetTree("accounts")

		// Начальное состояние: 100K аккаунтов
		for i := uint64(0); i < 100000; i++ {
			tree.Insert(NewAccount(i, StatusUser))
		}

		b.ResetTimer()

		rng := rand.New(rand.NewSource(42))

		for i := 0; i < b.N; i++ {
			// Симуляция: 1000 обновлений
			batch := manager.BeginBatch()

			for j := 0; j < 1000; j++ {
				// Случайный аккаунт
				uid := uint64(rng.Intn(100000))
				acc, _ := tree.Get(uid)

				// Обновляем статус (симуляция изменения состояния)
				newStatus := AccountStatus(rng.Intn(5))
				acc.Status = newStatus
				batch.InsertToTree("accounts", acc)
			}

			batch.Commit()

			// Каждые 10 итераций - вычисляем корень (снапшот)
			if i%10 == 0 {
				tree.ComputeRoot()
			}
		}
	})

	b.Run("Optimized", func(b *testing.B) {
		cfg := &Config{
			MaxDepth:    3,
			CacheSize:   50000, // Кеш на 50% горячих данных
			CacheShards: 10,
		}

		manager := NewManager[*Account](cfg)
		manager.CreateTree("accounts")
		tree, _ := manager.GetTree("accounts")

		for i := uint64(0); i < 100000; i++ {
			tree.Insert(NewAccount(i, StatusUser))
		}

		buffer := NewWriteBuffer(tree, 500, 10*time.Millisecond)
		defer buffer.Close()

		b.ResetTimer()

		rng := rand.New(rand.NewSource(42))

		for i := 0; i < b.N; i++ {
			for j := 0; j < 1000; j++ {
				uid := uint64(rng.Intn(100000))
				acc, _ := tree.Get(uid)
				// Обновляем статус
				newStatus := AccountStatus(rng.Intn(5))
				acc.Status = newStatus
				buffer.Insert(acc)
			}

			buffer.Flush()

			if i%10 == 0 {
				tree.ComputeRoot()
			}
		}
	})
}
