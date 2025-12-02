// verkle_tree_test.go
package verkletree

import (
	"fmt"
	"math/rand" 
	"testing"
	"time"
	
	//kzg_bls12381 "github.com/consensys/gnark-crypto/ecc/bls12-381/kzg"
)

// Глобальный SRS для тестов
//var testSRS *kzg_bls12381.SRS = InitSRS(256) // 256 достаточно для наших тестов 

var testSRS, _ = InitSRS(256)

/*
func init() {
	// Инициализируем SRS для тестов
	testSRS, err := InitSRS(256) 
	if err != nil {
		panic(fmt.Sprintf("Не удалось инициализировать SRS: %v", err))
	}
}*/

// TestUserDataOperations тестирует работу с данными пользователей
func TestUserDataOperations(t *testing.T) {
	tree, err := New(4, 256, testSRS, nil)
	if err != nil {
		t.Fatalf("Ошибка создания дерева: %v", err)
	}
	
	// Создаем батч и добавляем пользователей
	batch := tree.BeginBatch()
	
	testUser := &UserData{
		Balances: map[string]float64{
			"USD": 1000.0,
			"BTC": 0.5,
			"ETH": 10.0,
		},
		Metadata: map[string]interface{}{
			"tier": "premium",
			"verified": true,
		},
		Timestamp: time.Now().Unix(),
	}
	
	err = batch.AddUserData("testuser", testUser)
	if err != nil {
		t.Fatalf("Ошибка добавления пользователя: %v", err)
	}
	
	// Коммитим
	root, err := tree.CommitBatch(batch)
	if err != nil {
		t.Fatalf("Ошибка коммита: %v", err)
	}
	
	if len(root) == 0 {
		t.Fatal("Корневой хеш пуст")
	}
	
	// Проверяем получение данных
	retrieved, err := tree.GetUserData("testuser")
	if err != nil {
		t.Fatalf("Ошибка получения данных: %v", err)
	}
	
	if retrieved.Balances["USD"] != 1000.0 {
		t.Errorf("Ожидалось USD=1000.0, получено %f", retrieved.Balances["USD"])
	}
	
	if retrieved.Metadata["tier"] != "premium" {
		t.Errorf("Ожидалось tier=premium, получено %v", retrieved.Metadata["tier"])
	}
	
	t.Logf("✓ Тест пройден: корень=%x, узлов=%d", root, tree.GetNodeCount())
}

// TestMultipleUsers тестирует работу с несколькими пользователями
func TestMultipleUsers(t *testing.T) {
	//srs := &kzg.SRS{}
	tree, err := New(4, 256, testSRS, nil)
	
	if err != nil {
		t.Fatalf("Ошибка создания дерева: %v", err)
	}
	
	batch := tree.BeginBatch()
	
	// Добавляем 100 пользователей
	userIDs := make([]string, 100)
	for i := 0; i < 100; i++ {
		userID := fmt.Sprintf("user%d", i)
		userIDs[i] = userID
		
		userData := &UserData{
			Balances: map[string]float64{
				"USD": float64(i * 100),
				"BTC": float64(i) * 0.01,
			},
			Timestamp: time.Now().Unix(),
		}
		
		batch.AddUserData(userID, userData)
	}
	
	tree.CommitBatch(batch)
	
	// Тестируем Has
	if !tree.Has("user50") {
		t.Error("user50 должен существовать")
	}
	
	if tree.Has("user999") {
		t.Error("user999 не должен существовать")
	}
	
	// Тестируем GetMultiple
	testIDs := []string{"user10", "user20", "user999"}
	results, err := tree.GetMultipleUserData(testIDs)
	if err != nil {
		t.Fatalf("Ошибка GetMultiple: %v", err)
	}
	
	if results[0] == nil {
		t.Error("user10 должен быть найден")
	}
	
	if results[2] != nil {
		t.Error("user999 не должен быть найден")
	}
	
	if results[0].Balances["USD"] != 1000.0 {
		t.Errorf("Неверный баланс для user10: %f", results[0].Balances["USD"])
	}
	
	t.Logf("✓ Тест множественных пользователей пройден")
}

// TestHashUserID тестирует хеширование ID
func TestHashUserID(t *testing.T) {
	userID := "testuser123"
	
	hash1 := HashUserID(userID)
	hash2 := HashUserID(userID)
	
	if len(hash1) != 32 {
		t.Errorf("Хеш должен быть 32 байта, получено %d", len(hash1))
	}
	
	// Хеши должны быть идентичны для одного ID
	if string(hash1) != string(hash2) {
		t.Error("Хеши для одинакового ID должны совпадать")
	}
	
	// Хеши должны различаться для разных ID
	hash3 := HashUserID("differentuser")
	if string(hash1) == string(hash3) {
		t.Error("Хеши для разных ID должны различаться")
	}
	
	t.Logf("✓ Hash test passed: %x", hash1)
}

// TestLargeData тестирует работу с большими данными (до 8KB)
func TestLargeData(t *testing.T) {
	tree, err := New(4, 256, testSRS, nil)
	
	if err != nil {
		t.Fatalf("Ошибка создания дерева: %v", err)
	}
	
	batch := tree.BeginBatch()
	
	// Создаем большую структуру данных
	largeBalances := make(map[string]float64)
	for i := 0; i < 100; i++ {
		currency := fmt.Sprintf("CURR%d", i)
		largeBalances[currency] = float64(i) * 123.45
	}
	
	userData := &UserData{
		Balances: largeBalances,
		Metadata: map[string]interface{}{
			"description": "User with many currencies",
			"note":        "This is a test user with extensive balance data",
		},
		Timestamp: time.Now().Unix(),
	}
	
	// Сериализуем и проверяем размер
	serialized, err := userData.Serialize()
	if err != nil {
		t.Fatalf("Ошибка сериализации: %v", err)
	}
	
	t.Logf("Размер сериализованных данных: %d байт", len(serialized))
	
	if len(serialized) > MaxValueSize {
		t.Fatalf("Данные превышают лимит: %d > %d", len(serialized), MaxValueSize)
	}
	
	// Добавляем в дерево
	err = batch.AddUserData("largeuser", userData)
	if err != nil {
		t.Fatalf("Ошибка добавления больших данных: %v", err)
	}
	
	tree.CommitBatch(batch)
	
	// Проверяем получение
	retrieved, err := tree.GetUserData("largeuser")
	if err != nil {
		t.Fatalf("Ошибка получения данных: %v", err)
	}
	
	if len(retrieved.Balances) != 100 {
		t.Errorf("Ожидалось 100 балансов, получено %d", len(retrieved.Balances))
	}
	
	t.Logf("✓ Тест больших данных пройден")
}

// BenchmarkBatchInsert бенчмарк батч-вставки
func BenchmarkBatchInsert(b *testing.B) {
	//srs := &kzg.SRS{}
	tree, err := New(4, 256, testSRS, nil)
	
	if err != nil {
		b.Fatalf("Ошибка создания дерева: %v", err)
	}
	
	b.ResetTimer()
	
	for i := 0; i < b.N; i++ {
		batch := tree.BeginBatch()
		
		for j := 0; j < 1000; j++ {
			userID := fmt.Sprintf("user%d_%d", i, j)
			userData := &UserData{
				Balances: map[string]float64{
					"USD": float64(j),
					"BTC": float64(j) * 0.001,
				},
				Timestamp: time.Now().Unix(),
			}
			batch.AddUserData(userID, userData)
		}
		
		tree.CommitBatch(batch)
	}
}

// BenchmarkGet бенчмарк чтения данных
func BenchmarkGet(b *testing.B) {
	//srs := &kzg.SRS{}
	tree, err := New(4, 256, testSRS, nil)
	
	if err != nil {
		b.Fatalf("Ошибка создания дерева: %v", err)
	}
	
	// Подготовка данных
	batch := tree.BeginBatch()
	for i := 0; i < 1000; i++ {
		userID := fmt.Sprintf("user%d", i)
		userData := &UserData{
			Balances: map[string]float64{"USD": float64(i)},
			Timestamp: time.Now().Unix(),
		}
		batch.AddUserData(userID, userData)
	}
	tree.CommitBatch(batch)
	
	b.ResetTimer()
	
	for i := 0; i < b.N; i++ {
		userID := fmt.Sprintf("user%d", i%1000)
		_, err := tree.GetUserData(userID)
		if err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkHas бенчмарк проверки наличия
func BenchmarkHas(b *testing.B) {
	//srs := &kzg.SRS{}
	tree, err := New(4, 256, testSRS, nil)
	
	if err != nil {
		b.Fatalf("Ошибка создания дерева: %v", err)
	}
	
	batch := tree.BeginBatch()
	for i := 0; i < 1000; i++ {
		userData := &UserData{
			Balances: map[string]float64{"USD": float64(i)},
		}
		batch.AddUserData(fmt.Sprintf("user%d", i), userData)
	}
	tree.CommitBatch(batch)
	
	b.ResetTimer()
	
	for i := 0; i < b.N; i++ {
		userID := fmt.Sprintf("user%d", i%1000)
		_ = tree.Has(userID)
	}
}

// TestKZGCommitment тестирует настоящий KZG commitment
func TestKZGCommitment(t *testing.T) {
	tree, err := New(4, 256, testSRS, nil)
	if err != nil {
		t.Fatalf("Ошибка создания дерева: %v", err)
	}
	
	batch := tree.BeginBatch()
	
	userData := &UserData{
		Balances: map[string]float64{
			"USD": 1000.0,
			"BTC": 0.5,
		},
		Timestamp: time.Now().Unix(),
	}
	
	err = batch.AddUserData("kzg_test_user", userData)
	if err != nil {
		t.Fatalf("Ошибка добавления пользователя: %v", err)
	}
	
	root1, err := tree.CommitBatch(batch)
	if err != nil {
		t.Fatalf("Ошибка коммита: %v", err)
	}
	
	// Добавляем еще данные
	batch2 := tree.BeginBatch()
	userData2 := &UserData{
		Balances: map[string]float64{
			"USD": 2000.0,
		},
	}
	batch2.AddUserData("kzg_test_user2", userData2)
	
	root2, err := tree.CommitBatch(batch2)
	if err != nil {
		t.Fatalf("Ошибка второго коммита: %v", err)
	}
	
	// Корни должны различаться
	if string(root1) == string(root2) {
		t.Error("Корни не должны совпадать после добавления новых данных")
	}
	
	t.Logf("✓ KZG commitment работает корректно")
	t.Logf("  Root 1: %x", root1[:16])
	t.Logf("  Root 2: %x", root2[:16])
}


// BenchmarkRealisticWorkload бенчмарк реалистичной нагрузки
func BenchmarkRealisticWorkload(b *testing.B) {
	srs := testSRS
	
	b.Run("100k_users_workflow", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			b.StopTimer()
			
			// Создаем дерево
			tree, err := New(4, 256, srs, nil)
			if err != nil {
				b.Fatal(err)
			}
			
			// Генерируем 100,000 пользователей
			userCount := 100000
			userIDs := make([]string, userCount)
			
			b.StartTimer()
			
			// === Фаза 1: Массовое создание пользователей ===
			batchSize := 10000 // Батчами по 10k для эффективности
			for batchStart := 0; batchStart < userCount; batchStart += batchSize {
				batch := tree.BeginBatch()
				
				batchEnd := batchStart + batchSize
				if batchEnd > userCount {
					batchEnd = userCount
				}
				
				for j := batchStart; j < batchEnd; j++ {
					userID := fmt.Sprintf("user_%d", j)
					userIDs[j] = userID
					
					// Случайный баланс от 0 до $1000
					balance := rand.Float64() * 1000.0
					
					userData := &UserData{
						Balances: map[string]float64{
							"USD": balance,
						},
						Timestamp: time.Now().Unix(),
					}
					
					if err := batch.AddUserData(userID, userData); err != nil {
						b.Fatal(err)
					}
				}
				
				// Коммитим батч
				_, err := tree.CommitBatch(batch)
				if err != nil {
					b.Fatal(err)
				}
			}
			
			b.StopTimer()
			b.Logf("✓ Создано %d пользователей, root: %x", userCount, tree.GetRoot()[:8])
			b.StartTimer()
			
			// === Фаза 2: Обновление 10 случайных пользователей ===
			selectedUsers := make([]string, 10)
			for j := 0; j < 10; j++ {
				randomIdx := rand.Intn(userCount)
				selectedUsers[j] = userIDs[randomIdx]
			}
			
			updateBatch := tree.BeginBatch()
			for _, userID := range selectedUsers {
				// Новый случайный баланс
				newBalance := rand.Float64() * 1000.0
				
				userData := &UserData{
					Balances: map[string]float64{
						"USD": newBalance,
					},
					Timestamp: time.Now().Unix(),
				}
				
				if err := updateBatch.AddUserData(userID, userData); err != nil {
					b.Fatal(err)
				}
			}
			
			newRoot, err := tree.CommitBatch(updateBatch)
			if err != nil {
				b.Fatal(err)
			}
			
			b.StopTimer()
			b.Logf("✓ Обновлено 10 пользователей, новый root: %x", newRoot[:8])
			b.StartTimer()
			
			// === Фаза 3: Генерация пруфов для обновленных пользователей ===
			for _, userID := range selectedUsers {
				_, err := tree.GenerateProof(userID)
				if err != nil {
					b.Fatal(err)
				}
			}
			
			b.StopTimer()
			b.Logf("✓ Сгенерировано 10 пруфов")
			
			// === Фаза 4: Генерация мульти-пруфа ===
			b.StartTimer()
			_, err = tree.GenerateMultiProof(selectedUsers)
			b.StopTimer()
			
			if err != nil {
				b.Fatal(err)
			}
			
			b.Logf("✓ Сгенерирован мульти-пруф для 10 пользователей")
		}
	})
}

// BenchmarkDetailedMetrics детальные метрики производительности
func BenchmarkDetailedMetrics(b *testing.B) {
	srs := testSRS
	tree, _ := New(4, 256, srs, nil)
	
	// Подготовка: создаем 100k пользователей
	b.Log("Подготовка данных...")
	userCount := 100000
	userIDs := make([]string, userCount)
	
	for batchStart := 0; batchStart < userCount; batchStart += 10000 {
		batch := tree.BeginBatch()
		batchEnd := batchStart + 10000
		if batchEnd > userCount {
			batchEnd = userCount
		}
		
		for j := batchStart; j < batchEnd; j++ {
			userID := fmt.Sprintf("user_%d", j)
			userIDs[j] = userID
			
			userData := &UserData{
				Balances: map[string]float64{
					"USD": rand.Float64() * 1000.0,
				},
				Timestamp: time.Now().Unix(),
			}
			
			batch.AddUserData(userID, userData)
		}
		
		tree.CommitBatch(batch)
	}
	
	b.Log("Данные готовы, запуск бенчмарков...")
	
	// Бенчмарк 1: Обновление одного пользователя
	b.Run("single_user_update", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			batch := tree.BeginBatch()
			
			userID := userIDs[rand.Intn(userCount)]
			userData := &UserData{
				Balances: map[string]float64{
					"USD": rand.Float64() * 1000.0,
				},
				Timestamp: time.Now().Unix(),
			}
			
			batch.AddUserData(userID, userData)
			tree.CommitBatch(batch)
		}
	})
	
	// Бенчмарк 2: Обновление 10 пользователей
	b.Run("10_users_update", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			batch := tree.BeginBatch()
			
			for j := 0; j < 10; j++ {
				userID := userIDs[rand.Intn(userCount)]
				userData := &UserData{
					Balances: map[string]float64{
						"USD": rand.Float64() * 1000.0,
					},
					Timestamp: time.Now().Unix(),
				}
				batch.AddUserData(userID, userData)
			}
			
			tree.CommitBatch(batch)
		}
	})
	
	// Бенчмарк 3: Генерация одного пруфа
	b.Run("single_proof_generation", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			userID := userIDs[rand.Intn(userCount)]
			_, err := tree.GenerateProof(userID)
			if err != nil {
				b.Fatal(err)
			}
		}
	})
	
	// Бенчмарк 4: Генерация мульти-пруфа для 10 пользователей
	b.Run("multi_proof_10_users", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			selectedUsers := make([]string, 10)
			for j := 0; j < 10; j++ {
				selectedUsers[j] = userIDs[rand.Intn(userCount)]
			}
			
			_, err := tree.GenerateMultiProof(selectedUsers)
			if err != nil {
				b.Fatal(err)
			}
		}
	})
	
	// Бенчмарк 5: Чтение данных
	b.Run("read_user_data", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			userID := userIDs[rand.Intn(userCount)]
			_, err := tree.GetUserData(userID)
			if err != nil {
				b.Fatal(err)
			}
		}
	})
	
	// Бенчмарк 6: Множественное чтение (10 пользователей)
	b.Run("read_10_users", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			selectedUsers := make([]string, 10)
			for j := 0; j < 10; j++ {
				selectedUsers[j] = userIDs[rand.Intn(userCount)]
			}
			
			_, err := tree.GetMultipleUserData(selectedUsers)
			if err != nil {
				b.Fatal(err)
			}
		}
	})
	
	// Бенчмарк 7: Проверка наличия пользователя (Has)
	b.Run("has_user_check", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			userID := userIDs[rand.Intn(userCount)]
			_ = tree.Has(userID)
		}
	})
}

// BenchmarkScalability бенчмарк масштабируемости
func BenchmarkScalability(b *testing.B) {
	srs := testSRS
	
	sizes := []int{1000, 10000, 50000, 100000}
	
	for _, size := range sizes {
		b.Run(fmt.Sprintf("users_%d", size), func(b *testing.B) {
			for i := 0; i < b.N; i++ {
				b.StopTimer()
				tree, _ := New(4, 256, srs, nil)
				b.StartTimer()
				
				// Создаем пользователей батчами
				batchSize := 5000
				for batchStart := 0; batchStart < size; batchStart += batchSize {
					batch := tree.BeginBatch()
					
					batchEnd := batchStart + batchSize
					if batchEnd > size {
						batchEnd = size
					}
					
					for j := batchStart; j < batchEnd; j++ {
						userID := fmt.Sprintf("user_%d", j)
						userData := &UserData{
							Balances: map[string]float64{
								"USD": rand.Float64() * 1000.0,
							},
						}
						batch.AddUserData(userID, userData)
					}
					
					tree.CommitBatch(batch)
				}
			}
		})
	}
}

func BenchmarkBatchInsertNoKZG(b *testing.B) {
    // Создаем дерево БЕЗ SRS (только Blake3)
    tree, _ := New(4, 256, nil, nil)
    
    b.ResetTimer()
    
    for i := 0; i < b.N; i++ {
        batch := tree.BeginBatch()
        
        for j := 0; j < 1000; j++ {
            userID := fmt.Sprintf("user%d_%d", i, j)
            userData := &UserData{
                Balances: map[string]float64{
                    "USD": float64(j),
                    "BTC": float64(j) * 0.001,
                },
                Timestamp: time.Now().Unix(),
            }
            batch.AddUserData(userID, userData)
        }
        
        tree.CommitBatch(batch)
    }
}

func BenchmarkBatchInsertWithKZG(b *testing.B) {
    tree, _ := New(4, 256, testSRS, nil)
    
    b.ResetTimer()
    
    for i := 0; i < b.N; i++ {
        batch := tree.BeginBatch()
        
        for j := 0; j < 1000; j++ {
            userID := fmt.Sprintf("user%d_%d", i, j)
            userData := &UserData{
                Balances: map[string]float64{
                    "USD": float64(j),
                },
            }
            batch.AddUserData(userID, userData)
        }
        
        tree.CommitBatch(batch)
    }
}

func BenchmarkAsyncCommit(b *testing.B) {
    tree, _ := New(4, 256, testSRS, nil)
    tree.EnableAsyncCommit(2)
    defer tree.DisableAsyncCommit()
    
    b.ResetTimer()
    
    for i := 0; i < b.N; i++ {
        batch := tree.BeginBatch()
        
        for j := 0; j < 1000; j++ {
            userID := fmt.Sprintf("user%d_%d", i, j)
            userData := &UserData{
                Balances: map[string]float64{"USD": float64(j)},
            }
            batch.AddUserData(userID, userData)
        }
        
        tree.CommitBatch(batch)
    }
    
    b.StopTimer()
    tree.WaitForCommit()  // Ждем завершения всех коммитов
}

func TestDifferentNodeWidths(t *testing.T) {
    widths := []int{8, 16, 32, 64, 128, 256}
    
    for _, width := range widths {
        t.Run(fmt.Sprintf("width_%d", width), func(t *testing.T) {
            tree, err := New(4, width, testSRS, nil)
            if err != nil {
                t.Fatalf("Ошибка создания дерева с width=%d: %v", width, err)
            }
            
            batch := tree.BeginBatch()
            
            // Добавляем больше пользователей чем ширина узла
            for i := 0; i < width*2; i++ {
                userID := fmt.Sprintf("user_%d_%d", width, i)
                userData := &UserData{
                    Balances: map[string]float64{
                        "USD": float64(i * 100),
                    },
                }
                
                if err := batch.AddUserData(userID, userData); err != nil {
                    t.Fatalf("Ошибка добавления пользователя: %v", err)
                }
            }
            
            root, err := tree.CommitBatch(batch)
            if err != nil {
                t.Fatalf("Ошибка коммита с width=%d: %v", width, err)
            }
            
            t.Logf("Width=%d: root=%x, nodes=%d", width, root[:8], tree.GetNodeCount())
            
            // Проверяем что можем получить данные
            retrieved, err := tree.GetUserData("user_" + fmt.Sprintf("%d_0", width))
            if err != nil {
                t.Fatalf("Ошибка получения данных: %v", err)
            }
            
            if retrieved.Balances["USD"] != 0 {
                t.Errorf("Неверные данные")
            }
        })
    }
}


// BenchmarkNodeWidthComparison сравнивает производительность для разных NodeWidth
func BenchmarkNodeWidthComparison(b *testing.B) {
    widths := []int{8, 16, 32, 64, 128, 256, 512, 1024}
    userCount := 100000
    
    for _, width := range widths {
        b.Run(fmt.Sprintf("width_%d", width), func(b *testing.B) {
            for i := 0; i < b.N; i++ {
                b.StopTimer()
                
                // Создаем дерево с конкретной шириной
                tree, err := New(6, width, testSRS, nil) // 6 уровней для 100k элементов
                if err != nil {
                    b.Fatal(err)
                }
                
                b.StartTimer()
                
                // Вставляем 100k пользователей батчами по 5000
                batchSize := 5000
                for batchStart := 0; batchStart < userCount; batchStart += batchSize {
                    batch := tree.BeginBatch()
                    
                    batchEnd := batchStart + batchSize
                    if batchEnd > userCount {
                        batchEnd = userCount
                    }
                    
                    for j := batchStart; j < batchEnd; j++ {
                        userID := fmt.Sprintf("user_%d_%d", width, j)
                        userData := &UserData{
                            Balances: map[string]float64{
                                "USD": rand.Float64() * 1000.0,
                            },
                            Timestamp: time.Now().Unix(),
                        }
                        
                        if err := batch.AddUserData(userID, userData); err != nil {
                            b.Fatal(err)
                        }
                    }
                    
                    _, err := tree.CommitBatch(batch)
                    if err != nil {
                        b.Fatal(err)
                    }
                }
                
                b.StopTimer()
                
                // Логируем статистику
                if i == 0 { // Только для первой итерации
                    b.Logf("Width=%d: nodes=%d, root=%x", 
                        width, tree.GetNodeCount(), tree.GetRoot()[:8])
                }
            }
        })
    }
}

// BenchmarkNodeWidthOperations детальные операции для разных ширин
func BenchmarkNodeWidthOperations(b *testing.B) {
    widths := []int{8, 16, 32, 64, 128, 256}
    userCount := 100000
    
    // Подготавливаем деревья для каждой ширины
    trees := make(map[int]*VerkleTree)
    userIDs := make([]string, userCount)
    
    b.Log("Подготовка тестовых деревьев...")
    for _, width := range widths {
        tree, _ := New(6, width, testSRS, nil)
        
        // Заполняем дерево
        for batchStart := 0; batchStart < userCount; batchStart += 5000 {
            batch := tree.BeginBatch()
            
            batchEnd := batchStart + 5000
            if batchEnd > userCount {
                batchEnd = userCount
            }
            
            for j := batchStart; j < batchEnd; j++ {
                userID := fmt.Sprintf("user_%d", j)
                if batchStart == 0 {
                    userIDs[j] = userID
                }
                
                userData := &UserData{
                    Balances: map[string]float64{
                        "USD": rand.Float64() * 1000.0,
                    },
                }
                batch.AddUserData(userID, userData)
            }
            
            tree.CommitBatch(batch)
        }
        
        trees[width] = tree
        b.Logf("Width=%d подготовлено: %d узлов", width, tree.GetNodeCount())
    }
    
    // Бенчмарк 1: Чтение одного пользователя
    for _, width := range widths {
        tree := trees[width]
        b.Run(fmt.Sprintf("read_single_w%d", width), func(b *testing.B) {
            for i := 0; i < b.N; i++ {
                userID := userIDs[rand.Intn(userCount)]
                _, err := tree.GetUserData(userID)
                if err != nil {
                    b.Fatal(err)
                }
            }
        })
    }
    
    // Бенчмарк 2: Обновление одного пользователя
    for _, width := range widths {
        tree := trees[width]
        b.Run(fmt.Sprintf("update_single_w%d", width), func(b *testing.B) {
            for i := 0; i < b.N; i++ {
                batch := tree.BeginBatch()
                
                userID := userIDs[rand.Intn(userCount)]
                userData := &UserData{
                    Balances: map[string]float64{
                        "USD": rand.Float64() * 1000.0,
                    },
                }
                
                batch.AddUserData(userID, userData)
                tree.CommitBatch(batch)
            }
        })
    }
    
    // Бенчмарк 3: Генерация пруфа
    for _, width := range widths {
        tree := trees[width]
        b.Run(fmt.Sprintf("proof_w%d", width), func(b *testing.B) {
            for i := 0; i < b.N; i++ {
                userID := userIDs[rand.Intn(userCount)]
                _, err := tree.GenerateProof(userID)
                if err != nil {
                    b.Fatal(err)
                }
            }
        })
    }
    
    // Бенчмарк 4: Мульти-чтение (10 пользователей)
    for _, width := range widths {
        tree := trees[width]
        b.Run(fmt.Sprintf("read_10_w%d", width), func(b *testing.B) {
            for i := 0; i < b.N; i++ {
                selectedUsers := make([]string, 10)
                for j := 0; j < 10; j++ {
                    selectedUsers[j] = userIDs[rand.Intn(userCount)]
                }
                
                _, err := tree.GetMultipleUserData(selectedUsers)
                if err != nil {
                    b.Fatal(err)
                }
            }
        })
    }
    
    // Бенчмарк 5: Has проверка
    for _, width := range widths {
        tree := trees[width]
        b.Run(fmt.Sprintf("has_w%d", width), func(b *testing.B) {
            for i := 0; i < b.N; i++ {
                userID := userIDs[rand.Intn(userCount)]
                _ = tree.Has(userID)
            }
        })
    }
}

// BenchmarkNodeWidthMemory измеряет использование памяти для разных ширин
func BenchmarkNodeWidthMemory(b *testing.B) {
    widths := []int{8, 16, 32, 64, 128, 256}
    userCount := 50000 // Меньше для измерения памяти
    
    for _, width := range widths {
        b.Run(fmt.Sprintf("memory_w%d", width), func(b *testing.B) {
            b.ReportAllocs()
            
            for i := 0; i < b.N; i++ {
                tree, _ := New(6, width, nil, nil) // Без KZG для чистого измерения
                
                for batchStart := 0; batchStart < userCount; batchStart += 5000 {
                    batch := tree.BeginBatch()
                    
                    batchEnd := batchStart + 5000
                    if batchEnd > userCount {
                        batchEnd = userCount
                    }
                    
                    for j := batchStart; j < batchEnd; j++ {
                        userID := fmt.Sprintf("user_%d", j)
                        userData := &UserData{
                            Balances: map[string]float64{
                                "USD": float64(j),
                            },
                        }
                        batch.AddUserData(userID, userData)
                    }
                    
                    tree.CommitBatch(batch)
                }
            }
        })
    }
}

// BenchmarkNodeWidthDepth сравнивает влияние глубины дерева
func BenchmarkNodeWidthDepth(b *testing.B) {
    type config struct {
        width  int
        levels int
        name   string
    }
    
    configs := []config{
        {width: 16, levels: 8, name: "shallow_wide"},   // Узкие и глубокие
        {width: 256, levels: 4, name: "wide_shallow"},  // Широкие и мелкие
        {width: 64, levels: 6, name: "balanced"},       // Сбалансированные
    }
    
    userCount := 10000
    
    for _, cfg := range configs {
        b.Run(cfg.name, func(b *testing.B) {
            for i := 0; i < b.N; i++ {
                b.StopTimer()
                tree, _ := New(cfg.levels, cfg.width, testSRS, nil)
                b.StartTimer()
                
                for batchStart := 0; batchStart < userCount; batchStart += 1000 {
                    batch := tree.BeginBatch()
                    
                    batchEnd := batchStart + 1000
                    if batchEnd > userCount {
                        batchEnd = userCount
                    }
                    
                    for j := batchStart; j < batchEnd; j++ {
                        userID := fmt.Sprintf("user_%d", j)
                        userData := &UserData{
                            Balances: map[string]float64{"USD": float64(j)},
                        }
                        batch.AddUserData(userID, userData)
                    }
                    
                    tree.CommitBatch(batch)
                }
            }
        })
    }
}
