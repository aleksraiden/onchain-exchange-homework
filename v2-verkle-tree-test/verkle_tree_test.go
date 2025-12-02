// verkle_tree_test.go
package verkletree

import (
	"fmt"
	"math/rand" 
	"testing"
	"time"
	"sync"
	"strings"
	"runtime"
	
	kzg_bls12381 "github.com/consensys/gnark-crypto/ecc/bls12-381/kzg"
)

// Глобальный SRS для тестов
//var testSRS *kzg_bls12381.SRS = InitSRS(256) // 256 достаточно для наших тестов 

var testSRS, _ = InitSRS(256)

// Глобальные SRS для разных размеров
var (
    testSRS256  *kzg_bls12381.SRS
    testSRS512  *kzg_bls12381.SRS
    testSRS1024 *kzg_bls12381.SRS
    srsCache    map[int]*kzg_bls12381.SRS
    srsMutex    sync.RWMutex
)

func init() {
    var err error
    
    // Инициализируем базовые SRS
    testSRS256, err = InitSRS(256)
    if err != nil {
        panic(fmt.Sprintf("Не удалось инициализировать SRS256: %v", err))
    }
    
    testSRS512, err = InitSRS(512)
    if err != nil {
        panic(fmt.Sprintf("Не удалось инициализировать SRS512: %v", err))
    }
    
    testSRS1024, err = InitSRS(1024)
    if err != nil {
        panic(fmt.Sprintf("Не удалось инициализировать SRS1024: %v", err))
    }    
    
    // Кэш для динамического получения SRS
    srsCache = map[int]*kzg_bls12381.SRS{
        256:  testSRS256,
        512:  testSRS512,
        1024: testSRS1024,
    }
}

// getSRSForWidth возвращает подходящий SRS для заданной ширины узла
func getSRSForWidth(width int) *kzg_bls12381.SRS {
    srsMutex.RLock()
    defer srsMutex.RUnlock()
    
    // Находим ближайший подходящий SRS
    requiredSize := GetRequiredSRSSize(width)
    
    for size := requiredSize; size <= 1024; size *= 2 {
        if srs, exists := srsCache[size]; exists {
            return srs
        }
    }
    
    // Fallback на самый большой
    return testSRS1024
}

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
    widths := []int{8, 16, 32, 64, 128, 256}
    userCount := 100000
    
    for _, width := range widths {
        b.Run(fmt.Sprintf("width_%d", width), func(b *testing.B) {
            for i := 0; i < b.N; i++ {
                b.StopTimer()
                
                // Начинаем с малой глубины, дерево само расширится
                initialLevels := 3
                if width <= 16 {
                    initialLevels = 4  // Для узких узлов нужно больше уровней
                }
                
                tree, err := New(initialLevels, width, testSRS, nil)
                if err != nil {
                    b.Fatal(err)
                }
                
                b.StartTimer()
                
                // Вставляем 100k пользователей
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
                
                if i == 0 {
                    stats := tree.GetTreeStats()
                    b.Logf("Width=%d: depth=%d, nodes=%d", 
                        width, stats["depth"], stats["node_count"])
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

func TestAutoDepthExpansion(t *testing.T) {
    // Создаем дерево с малой начальной глубиной
    tree, err := New(2, 8, testSRS, nil)  // Только 2 уровня, ширина 8
    if err != nil {
        t.Fatalf("Ошибка создания дерева: %v", err)
    }
    
    initialDepth := tree.GetCurrentDepth()
    t.Logf("Начальная глубина: %d", initialDepth)
    
    // Вставляем много данных, чтобы вызвать расширение
    for i := 0; i < 1000; i++ {
        batch := tree.BeginBatch()
        
        for j := 0; j < 10; j++ {
            userID := fmt.Sprintf("user_%d_%d", i, j)
            userData := &UserData{
                Balances: map[string]float64{
                    "USD": float64(i * j),
                },
            }
            
            if err := batch.AddUserData(userID, userData); err != nil {
                t.Fatalf("Ошибка добавления: %v", err)
            }
        }
        
        _, err := tree.CommitBatch(batch)
        if err != nil {
            t.Fatalf("Ошибка коммита на итерации %d: %v", i, err)
        }
        
        // Проверяем расширение каждые 100 итераций
        if i%100 == 0 {
            stats := tree.GetTreeStats()
            t.Logf("Итерация %d: глубина=%d, узлов=%d", 
                i, stats["depth"], stats["node_count"])
        }
    }
    
    finalDepth := tree.GetCurrentDepth()
    t.Logf("Финальная глубина: %d (было %d)", finalDepth, initialDepth)
    
    if finalDepth <= initialDepth {
        t.Error("Дерево не расширилось автоматически")
    }
    
    // Проверяем что данные доступны
    userData, err := tree.GetUserData("user_0_0")
    if err != nil {
        t.Fatalf("Ошибка получения данных: %v", err)
    }
    
    if userData.Balances["USD"] != 0 {
        t.Errorf("Неверные данные после расширения")
    }
    
    t.Logf("✓ Автоматическое расширение работает корректно")
}

func BenchmarkOptimalWidth(b *testing.B) {
    widths := []int{64, 128, 256}
    operations := []struct {
        name string
        fn   func(*VerkleTree, []string)
    }{
        {"insert_10k", func(tree *VerkleTree, ids []string) {
            for i := 0; i < 10000; i++ {
                batch := tree.BeginBatch()
                userData := &UserData{
                    Balances: map[string]float64{"USD": float64(i)},
                }
                batch.AddUserData(fmt.Sprintf("user_%d", i), userData)
                tree.CommitBatch(batch)
            }
        }},
        {"read_random", func(tree *VerkleTree, ids []string) {
            for i := 0; i < 1000; i++ {
                tree.GetUserData(ids[rand.Intn(len(ids))])
            }
        }},
        {"generate_proof", func(tree *VerkleTree, ids []string) {
            for i := 0; i < 100; i++ {
                tree.GenerateProof(ids[rand.Intn(len(ids))])
            }
        }},
    }
    
    for _, width := range widths {
        for _, op := range operations {
            b.Run(fmt.Sprintf("%s_w%d", op.name, width), func(b *testing.B) {
                // Подготовка
                srs := getSRSForWidth(width)
                tree, _ := New(6, width, srs, nil)
                
                // Заполняем тестовыми данными
                userIDs := make([]string, 10000)
                for i := 0; i < 10000; i++ {
                    batch := tree.BeginBatch()
                    userID := fmt.Sprintf("prep_user_%d", i)
                    userIDs[i] = userID
                    userData := &UserData{
                        Balances: map[string]float64{"USD": float64(i)},
                    }
                    batch.AddUserData(userID, userData)
                    tree.CommitBatch(batch)
                }
                
                b.ResetTimer()
                
                // Измеряем операцию
                for i := 0; i < b.N; i++ {
                    op.fn(tree, userIDs)
                }
            })
        }
    }
}

// Сравнение характеристик
// TestWidthCharacteristics - упрощенная версия с прогрессом
func TestWidthCharacteristics(t *testing.T) {
    widths := []int{64, 128, 256}  // Убрали 32, оставили главные
    userCount := 10000  // УМЕНЬШИЛИ с 50000 до 10000
    
    results := make(map[int]map[string]interface{})
    
    for _, width := range widths {
        t.Logf("\n>>> Тестирование width=%d", width)
        
        srs := getSRSForWidth(width)
        tree, _ := New(6, width, srs, nil)
        
        // Вставляем данные
        t.Logf("  Вставка %d элементов...", userCount)
        startTime := time.Now()
        
        batchSize := 1000
        for i := 0; i < userCount; i += batchSize {
            if i%(batchSize*5) == 0 {
                t.Logf("    Прогресс: %d/%d", i, userCount)
            }
            
            batch := tree.BeginBatch()
            
            end := i + batchSize
            if end > userCount {
                end = userCount
            }
            
            for j := i; j < end; j++ {
                userID := fmt.Sprintf("user_%d", j)
                userData := &UserData{
                    Balances: map[string]float64{"USD": float64(j)},
                }
                
                if err := batch.AddUserData(userID, userData); err != nil {
                    t.Fatalf("Ошибка добавления: %v", err)
                }
            }
            
            if _, err := tree.CommitBatch(batch); err != nil {
                t.Fatalf("Ошибка коммита: %v", err)
            }
        }
        insertTime := time.Since(startTime)
        t.Logf("  ✓ Вставка завершена за %v", insertTime)
        
        // Измеряем чтение
        t.Logf("  Измерение чтения (100 операций)...")
        startTime = time.Now()
        for i := 0; i < 100; i++ {  // УМЕНЬШИЛИ с 1000 до 100
            _, err := tree.GetUserData(fmt.Sprintf("user_%d", rand.Intn(userCount)))
            if err != nil {
                t.Logf("  Предупреждение: ошибка чтения %v", err)
            }
        }
        readTime := time.Since(startTime)
        t.Logf("  ✓ Чтение завершено за %v", readTime)
        
        // Измеряем proof
        t.Logf("  Генерация proof (10 операций)...")
        startTime = time.Now()
        for i := 0; i < 10; i++ {  // УМЕНЬШИЛИ с 100 до 10
            _, err := tree.GenerateProof(fmt.Sprintf("user_%d", rand.Intn(userCount)))
            if err != nil {
                t.Logf("  Предупреждение: ошибка proof %v", err)
            }
        }
        proofTime := time.Since(startTime)
        t.Logf("  ✓ Proof завершен за %v", proofTime)
        
        stats := tree.GetTreeStats()
        
        results[width] = map[string]interface{}{
            "insert_time":   insertTime,
            "read_time":     readTime,
            "proof_time":    proofTime,
            "depth":         stats["depth"],
            "node_count":    stats["node_count"],
        }
        
        t.Logf("\n=== Результаты для Width %d ===", width)
        t.Logf("Вставка %d элементов: %v (%.3f мс/элемент)", 
            userCount, insertTime, 
            float64(insertTime.Milliseconds())/float64(userCount))
        t.Logf("Чтение 100 элементов: %v (%.1f мкс/элемент)", 
            readTime, float64(readTime.Microseconds())/100.0)
        t.Logf("Генерация 10 proof: %v (%.1f мс/proof)", 
            proofTime, float64(proofTime.Milliseconds())/10.0)
        t.Logf("Глубина дерева: %d", stats["depth"])
        t.Logf("Количество узлов: %d", stats["node_count"])
    }
    
    // Находим оптимальную ширину
    t.Log("\n" + strings.Repeat("=", 50))
    t.Log("ИТОГОВОЕ СРАВНЕНИЕ")
    t.Log(strings.Repeat("=", 50))
    
    var bestWidth int
    var bestScore float64 = 999999999
    
    for width, result := range results {
        // Веса для разных операций
        insertWeight := 0.5
        readWeight := 0.3
        proofWeight := 0.2
        
        insertMs := float64(result["insert_time"].(time.Duration).Milliseconds())
        readUs := float64(result["read_time"].(time.Duration).Microseconds())
        proofMs := float64(result["proof_time"].(time.Duration).Milliseconds())
        
        score := insertMs*insertWeight + 
                 (readUs/1000.0)*readWeight + 
                 proofMs*proofWeight
        
        t.Logf("Width %4d: score = %8.2f (insert: %6.0fms, read: %6.0fμs, proof: %6.0fms)", 
            width, score, insertMs, readUs, proofMs)
        
        if score < bestScore {
            bestScore = score
            bestWidth = width
        }
    }
    
    t.Log(strings.Repeat("=", 50))
    t.Logf("🏆 ОПТИМАЛЬНАЯ ШИРИНА: %d (score: %.2f)", bestWidth, bestScore)
    t.Log(strings.Repeat("=", 50))
}

// TestWidthCharacteristicsFast - быстрая версия без KZG
func TestWidthCharacteristicsFast(t *testing.T) {
    widths := []int{32, 64, 128, 256}
    userCount := 50000
    
    t.Log("Быстрый тест производительности (без KZG)")
    
    for _, width := range widths {
        t.Logf("\n>>> Width=%d", width)
        
        // БЕЗ SRS = только Blake3, очень быстро
        tree, _ := New(6, width, nil, nil)
        
        startTime := time.Now()
        
        // Вставка батчами
        for i := 0; i < userCount; i += 5000 {
            batch := tree.BeginBatch()
            
            end := i + 5000
            if end > userCount {
                end = userCount
            }
            
            for j := i; j < end; j++ {
                userID := fmt.Sprintf("user_%d", j)
                userData := &UserData{
                    Balances: map[string]float64{"USD": float64(j)},
                }
                batch.AddUserData(userID, userData)
            }
            
            tree.CommitBatch(batch)
            
            if i > 0 && i%10000 == 0 {
                elapsed := time.Since(startTime)
                rate := float64(i) / elapsed.Seconds()
                t.Logf("  %d/%d (%.0f items/sec)", i, userCount, rate)
            }
        }
        
        totalTime := time.Since(startTime)
        stats := tree.GetTreeStats()
        
        t.Logf("✓ Завершено за %v", totalTime)
        t.Logf("  Скорость: %.0f items/sec", float64(userCount)/totalTime.Seconds())
        t.Logf("  Глубина: %d, Узлов: %d", stats["depth"], stats["node_count"])
    }
}

// TestQuickComparison - очень быстрое сравнение
func TestQuickComparison(t *testing.T) {
    widths := []int{64, 128, 256}
    iterations := 1000
    
    t.Log("\nБыстрое сравнение (1000 вставок, без KZG)")
    t.Log(strings.Repeat("-", 60))
    t.Logf("%-10s | %-15s | %-10s | %s", "Width", "Time", "Rate", "Stats")
    t.Log(strings.Repeat("-", 60))
    
    for _, width := range widths {
        tree, _ := New(4, width, nil, nil)
        
        startTime := time.Now()
        
        batch := tree.BeginBatch()
        for i := 0; i < iterations; i++ {
            userData := &UserData{
                Balances: map[string]float64{"USD": float64(i)},
            }
            batch.AddUserData(fmt.Sprintf("user_%d", i), userData)
        }
        tree.CommitBatch(batch)
        
        elapsed := time.Since(startTime)
        rate := float64(iterations) / elapsed.Seconds()
        stats := tree.GetTreeStats()
        
        t.Logf("%-10d | %-15v | %8.0f/s | depth=%d nodes=%d", 
            width, elapsed, rate, stats["depth"], stats["node_count"])
    }
    t.Log(strings.Repeat("-", 60))
}

// TestBatchSizeImpact - влияние размера батча на производительность
func TestBatchSizeImpact(t *testing.T) {
	widths := []int{64, 128, 256}
	
	// Разные размеры батчей относительно ширины
	batchMultipliers := []float64{0.25, 0.5, 1.0, 2.0, 4.0, 8.0}
	
	totalItems := 10000
	
	t.Log("\n" + strings.Repeat("=", 80))
	t.Log("ТЕСТ ВЛИЯНИЯ РАЗМЕРА БАТЧА НА ПРОИЗВОДИТЕЛЬНОСТЬ")
	t.Log(strings.Repeat("=", 80))
	
	for _, width := range widths {
		t.Logf("\n>>> NodeWidth = %d", width)
		t.Log(strings.Repeat("-", 80))
		t.Logf("%-12s | %-15s | %-12s | %-15s | %s", 
			"BatchSize", "Time", "Rate", "Batches", "ms/batch")
		t.Log(strings.Repeat("-", 80))
		
		for _, multiplier := range batchMultipliers {
			batchSize := int(float64(width) * multiplier)
			if batchSize < 1 {
				batchSize = 1
			}
			
			// Создаем новое дерево для каждого теста
			tree, _ := New(6, width, nil, nil) // Без KZG для скорости
			
			startTime := time.Now()
			batchCount := 0
			
			for i := 0; i < totalItems; i += batchSize {
				batch := tree.BeginBatch()
				
				end := i + batchSize
				if end > totalItems {
					end = totalItems
				}
				
				for j := i; j < end; j++ {
					userID := fmt.Sprintf("user_%d_%d", width, j)
					userData := &UserData{
						Balances: map[string]float64{"USD": float64(j)},
					}
					batch.AddUserData(userID, userData)
				}
				
				tree.CommitBatch(batch)
				batchCount++
			}
			
			elapsed := time.Since(startTime)
			rate := float64(totalItems) / elapsed.Seconds()
			msPerBatch := float64(elapsed.Milliseconds()) / float64(batchCount)
			
			label := fmt.Sprintf("%d (%.2fx)", batchSize, multiplier)
			t.Logf("%-12s | %-15v | %8.0f/s | %6d       | %8.2f", 
				label, elapsed, rate, batchCount, msPerBatch)
		}
		t.Log(strings.Repeat("-", 80))
	}
}

// BenchmarkBatchSizeOptimization - бенчмарк для точных измерений
func BenchmarkBatchSizeOptimization(b *testing.B) {
	width := 128
	
	type testCase struct {
		batchSize int
		name      string
	}
	
	testCases := []testCase{
		{16, "tiny"},
		{32, "quarter"},
		{64, "half"},
		{128, "equal"},
		{256, "double"},
		{512, "quad"},
		{1024, "large"},
	}
	
	for _, tc := range testCases {
		b.Run(fmt.Sprintf("batch_%s_%d", tc.name, tc.batchSize), func(b *testing.B) {
			for i := 0; i < b.N; i++ {
				b.StopTimer()
				tree, _ := New(6, width, nil, nil)
				itemCount := 10000
				b.StartTimer()
				
				for j := 0; j < itemCount; j += tc.batchSize {
					batch := tree.BeginBatch()
					
					end := j + tc.batchSize
					if end > itemCount {
						end = itemCount
					}
					
					for k := j; k < end; k++ {
						userData := &UserData{
							Balances: map[string]float64{"USD": float64(k)},
						}
						batch.AddUserData(fmt.Sprintf("user_%d", k), userData)
					}
					
					tree.CommitBatch(batch)
				}
			}
		})
	}
}

// TestBatchSizeWithKZG - тест с реальным KZG
func TestBatchSizeWithKZG(t *testing.T) {
	if testing.Short() {
		t.Skip("Пропускаем медленный тест в short режиме")
	}
	
	width := 128
	srs := getSRSForWidth(width)
	
	batchSizes := []int{32, 64, 128, 256, 512}
	totalItems := 5000 // Меньше для KZG
	
	t.Log("\n" + strings.Repeat("=", 70))
	t.Log("ТЕСТ РАЗМЕРА БАТЧА С KZG (Width=128)")
	t.Log(strings.Repeat("=", 70))
	t.Logf("%-12s | %-15s | %-12s | %-12s", 
		"BatchSize", "Time", "Rate", "ms/item")
	t.Log(strings.Repeat("-", 70))
	
	for _, batchSize := range batchSizes {
		tree, _ := New(6, width, srs, nil)
		
		startTime := time.Now()
		
		for i := 0; i < totalItems; i += batchSize {
			batch := tree.BeginBatch()
			
			end := i + batchSize
			if end > totalItems {
				end = totalItems
			}
			
			for j := i; j < end; j++ {
				userData := &UserData{
					Balances: map[string]float64{"USD": float64(j)},
				}
				batch.AddUserData(fmt.Sprintf("user_%d", j), userData)
			}
			
			tree.CommitBatch(batch)
			
			// Показываем прогресс каждые 1000 элементов
			if i > 0 && i%1000 == 0 {
				elapsed := time.Since(startTime)
				currentRate := float64(i) / elapsed.Seconds()
				t.Logf("  [batch=%d] %d/%d (%.0f items/s)", 
					batchSize, i, totalItems, currentRate)
			}
		}
		
		elapsed := time.Since(startTime)
		rate := float64(totalItems) / elapsed.Seconds()
		msPerItem := float64(elapsed.Milliseconds()) / float64(totalItems)
		
		t.Logf("%-12d | %-15v | %8.0f/s | %8.2f", 
			batchSize, elapsed, rate, msPerItem)
	}
	t.Log(strings.Repeat("=", 70))
}

// TestOptimalBatchStrategy - поиск оптимальной стратегии
func TestOptimalBatchStrategy(t *testing.T) {
	width := 128
	totalItems := 10000
	
	strategies := []struct {
		name        string
		getBatchSize func(iteration, width int) int
	}{
		{
			name: "fixed_small",
			getBatchSize: func(i, w int) int { return w / 2 },
		},
		{
			name: "fixed_equal",
			getBatchSize: func(i, w int) int { return w },
		},
		{
			name: "fixed_double",
			getBatchSize: func(i, w int) int { return w * 2 },
		},
		{
			name: "adaptive_growing",
			getBatchSize: func(i, w int) int {
				// Начинаем с маленьких батчей, увеличиваем
				base := w / 4
				return base * (1 + i/1000)
			},
		},
		{
			name: "adaptive_shrinking",
			getBatchSize: func(i, w int) int {
				// Начинаем с больших батчей, уменьшаем
				maxSize := w * 4
				reduction := i / 1000
				size := maxSize - (reduction * w / 2)
				if size < w/2 {
					size = w / 2
				}
				return size
			},
		},
	}
	
	t.Log("\n" + strings.Repeat("=", 70))
	t.Log("ТЕСТ СТРАТЕГИЙ БАТЧИНГА")
	t.Log(strings.Repeat("=", 70))
	t.Logf("%-20s | %-15s | %-12s", "Strategy", "Time", "Rate")
	t.Log(strings.Repeat("-", 70))
	
	for _, strategy := range strategies {
		tree, _ := New(6, width, nil, nil)
		
		startTime := time.Now()
		i := 0
		
		for i < totalItems {
			batchSize := strategy.getBatchSize(i, width)
			batch := tree.BeginBatch()
			
			end := i + batchSize
			if end > totalItems {
				end = totalItems
			}
			
			for j := i; j < end; j++ {
				userData := &UserData{
					Balances: map[string]float64{"USD": float64(j)},
				}
				batch.AddUserData(fmt.Sprintf("user_%d", j), userData)
			}
			
			tree.CommitBatch(batch)
			i = end
		}
		
		elapsed := time.Since(startTime)
		rate := float64(totalItems) / elapsed.Seconds()
		
		t.Logf("%-20s | %-15v | %8.0f/s", strategy.name, elapsed, rate)
	}
	t.Log(strings.Repeat("=", 70))
}


// BenchmarkOptimizationLevels - сравнение всех уровней оптимизации
func BenchmarkOptimizationLevels(b *testing.B) {
	itemCount := 10000
	width := 128
	
	levels := []struct {
		level OptimizationLevel
		name  string
	}{
		{OptimizationNone, "none"},
		{OptimizationBasic, "lazy"},
		{OptimizationParallel, "parallel"},
		{OptimizationAsync, "async"},
		{OptimizationMax, "max"},
	}
	
	for _, lvl := range levels {
		b.Run(lvl.name, func(b *testing.B) {
			for i := 0; i < b.N; i++ {
				b.StopTimer()
				
				tree, _ := New(6, width, nil, nil)
				tree.SetOptimizationLevel(lvl.level)
				
				b.StartTimer()
				
				// Вставляем данные батчами
				batchSize := 1000
				for j := 0; j < itemCount; j += batchSize {
					batch := tree.BeginBatch()
					
					end := j + batchSize
					if end > itemCount {
						end = itemCount
					}
					
					for k := j; k < end; k++ {
						userData := &UserData{
							Balances: map[string]float64{"USD": float64(k)},
						}
						batch.AddUserData(fmt.Sprintf("user_%d", k), userData)
					}
					
					tree.CommitBatch(batch)
				}
				
				// Для async режимов - ждем завершения
				if lvl.level == OptimizationAsync || lvl.level == OptimizationMax {
					tree.WaitForCommit()
				}
			}
		})
	}
}

// TestAllOptimizations - тест что все оптимизации работают вместе
func TestAllOptimizations(t *testing.T) {
	itemCount := 5000
	width := 128
	
	levels := []OptimizationLevel{
		OptimizationNone,
		OptimizationBasic,
		OptimizationParallel,
		OptimizationAsync,
		OptimizationMax,
	}
	
	levelNames := map[OptimizationLevel]string{
		OptimizationNone:     "None",
		OptimizationBasic:    "Basic (lazy)",
		OptimizationParallel: "Parallel",
		OptimizationAsync:    "Async",
		OptimizationMax:      "Max (all)",
	}
	
	t.Log("\n" + strings.Repeat("=", 80))
	t.Log("ТЕСТ ВСЕХ УРОВНЕЙ ОПТИМИЗАЦИИ")
	t.Log(strings.Repeat("=", 80))
	t.Logf("%-20s | %-15s | %-12s | %s", "Level", "Time", "Rate", "Speedup")
	t.Log(strings.Repeat("-", 80))
	
	var baselineTime time.Duration
	
	for _, level := range levels {
		tree, _ := New(6, width, nil, nil)
		tree.SetOptimizationLevel(level)
		
		// Логируем включенные оптимизации
		info := tree.GetOptimizationInfo()
		t.Logf("\n%s:", levelNames[level])
		t.Logf("  lazy_commit: %v", info["lazy_commit"])
		t.Logf("  parallel: %v (workers: %v)", info["parallel_enabled"], info["parallel_workers"])
		t.Logf("  async: %v", info["async_mode"])
		
		startTime := time.Now()
		
		// Вставляем данные
		batchSize := 1000
		for i := 0; i < itemCount; i += batchSize {
			batch := tree.BeginBatch()
			
			end := i + batchSize
			if end > itemCount {
				end = itemCount
			}
			
			for j := i; j < end; j++ {
				userData := &UserData{
					Balances: map[string]float64{"USD": float64(j)},
				}
				if err := batch.AddUserData(fmt.Sprintf("user_%d", j), userData); err != nil {
					t.Fatal(err)
				}
			}
			
			if _, err := tree.CommitBatch(batch); err != nil {
				t.Fatal(err)
			}
		}
		
		// Для async - ждем завершения
		if level == OptimizationAsync || level == OptimizationMax {
			tree.WaitForCommit()
		}
		
		elapsed := time.Since(startTime)
		rate := float64(itemCount) / elapsed.Seconds()
		
		if level == OptimizationNone {
			baselineTime = elapsed
		}
		
		speedup := float64(baselineTime) / float64(elapsed)
		
		t.Logf("%-20s | %-15v | %8.0f/s | %.2fx", 
			levelNames[level], elapsed, rate, speedup)
		
		// Проверяем что данные доступны
		userData, err := tree.GetUserData("user_0")
		if err != nil {
			t.Errorf("Ошибка получения данных для %s: %v", levelNames[level], err)
		}
		if userData.Balances["USD"] != 0 {
			t.Errorf("Неверные данные для %s", levelNames[level])
		}
		
		// Cleanup для async режимов
		if level == OptimizationAsync || level == OptimizationMax {
			tree.DisableAsyncCommit()
		}
	}
	
	t.Log(strings.Repeat("=", 80))
}

// BenchmarkParallelCommits - сравнение последовательных и параллельных коммитментов
func BenchmarkParallelCommits(b *testing.B) {
	itemCount := 10000
	width := 128
	
	b.Run("sequential", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			b.StopTimer()
			tree, _ := New(6, width, nil, nil)
			tree.DisableParallelCommits() // ОТКЛЮЧАЕМ
			b.StartTimer()
			
			batch := tree.BeginBatch()
			for j := 0; j < itemCount; j++ {
				userData := &UserData{
					Balances: map[string]float64{"USD": float64(j)},
				}
				batch.AddUserData(fmt.Sprintf("user_%d", j), userData)
			}
			tree.CommitBatch(batch)
		}
	})
	
	b.Run("parallel", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			b.StopTimer()
			tree, _ := New(6, width, nil, nil)
			tree.EnableParallelCommits(runtime.NumCPU()) // ВКЛЮЧАЕМ
			b.StartTimer()
			
			batch := tree.BeginBatch()
			for j := 0; j < itemCount; j++ {
				userData := &UserData{
					Balances: map[string]float64{"USD": float64(j)},
				}
				batch.AddUserData(fmt.Sprintf("user_%d", j), userData)
			}
			tree.CommitBatch(batch)
		}
	})
}

// TestParallelScaling - тест масштабируемости
func TestParallelScaling(t *testing.T) {
	width := 128
	itemCount := 20000
	
	workerCounts := []int{1, 2, 4, 8, 16, 20}
	
	t.Log("\n" + strings.Repeat("=", 70))
	t.Log("ТЕСТ МАСШТАБИРУЕМОСТИ ПАРАЛЛЕЛИЗМА")
	t.Log(strings.Repeat("=", 70))
	t.Logf("%-10s | %-15s | %-12s | %s", "Workers", "Time", "Rate", "Speedup")
	t.Log(strings.Repeat("-", 70))
	
	var baselineTime time.Duration
	
	for _, workers := range workerCounts {
		tree, _ := New(6, width, nil, nil)
		tree.EnableParallelCommits(workers)
		
		startTime := time.Now()
		
		// Вставляем данные батчами
		batchSize := 1000
		for i := 0; i < itemCount; i += batchSize {
			batch := tree.BeginBatch()
			
			end := i + batchSize
			if end > itemCount {
				end = itemCount
			}
			
			for j := i; j < end; j++ {
				userData := &UserData{
					Balances: map[string]float64{"USD": float64(j)},
				}
				batch.AddUserData(fmt.Sprintf("user_%d", j), userData)
			}
			
			tree.CommitBatch(batch)
		}
		
		elapsed := time.Since(startTime)
		rate := float64(itemCount) / elapsed.Seconds()
		
		if workers == 1 {
			baselineTime = elapsed
		}
		
		speedup := float64(baselineTime) / float64(elapsed)
		
		t.Logf("%-10d | %-15v | %8.0f/s | %.2fx", 
			workers, elapsed, rate, speedup)
	}
	t.Log(strings.Repeat("=", 70))
}

// BenchmarkParallelProofs - бенчмарк параллельной генерации пруфов
func BenchmarkParallelProofs(b *testing.B) {
	tree, _ := New(6, 128, testSRS, nil)
	
	// Подготовка данных
	userIDs := make([]string, 1000)
	batch := tree.BeginBatch()
	for i := 0; i < 1000; i++ {
		userID := fmt.Sprintf("user_%d", i)
		userIDs[i] = userID
		userData := &UserData{
			Balances: map[string]float64{"USD": float64(i)},
		}
		batch.AddUserData(userID, userData)
	}
	tree.CommitBatch(batch)
	
	b.Run("sequential_proofs", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			for j := 0; j < 100; j++ {
				tree.GenerateProof(userIDs[j])
			}
		}
	})
	
	b.Run("parallel_proofs", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			tree.GenerateMultiProofParallel(userIDs[:100])
		}
	})
}
