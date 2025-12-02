// example_usage.go
package verkletree

import (
	"fmt"
	"time"
	
	"github.com/consensys/gnark-crypto/ecc/bls12-381/kzg"
)

func ExampleUsageWithUserBalances() {
	// 1. Создание нового дерева с хранилищем (или nil для in-memory)
	srs := &kzg.SRS{} // В реальном приложении инициализируйте правильно
	
	// Можно использовать nil вместо dataStore для работы только в памяти
	tree, err := New(4, 256, srs, nil)
	if err != nil {
		panic(err)
	}
	
	// 2. Батч-обновление: добавление данных пользователей
	batch := tree.BeginBatch()
	
	// Пример 1: Добавление данных через структуру UserData
	user1Data := &UserData{
		Balances: map[string]float64{
			"USD": 1500.50,
			"BTC": 0.025,
			"ETH": 1.5,
		},
		Metadata: map[string]interface{}{
			"tier":       "gold",
			"verified":   true,
			"country":    "US",
		},
		Timestamp: time.Now().Unix(),
	}
	
	if err := batch.AddUserData("user123", user1Data); err != nil {
		panic(err)
	}
	
	// Пример 2: Добавление данных через сырой JSON
	user2JSON := `{
		"balances": {"USD": 500.0, "EUR": 450.0},
		"metadata": {"tier": "silver", "verified": false},
		"timestamp": 1701432000
	}`
	
	if err := batch.Add("user456", []byte(user2JSON)); err != nil {
		panic(err)
	}
	
	// Пример 3: Массовое добавление пользователей
	for i := 1000; i < 1100; i++ {
		userID := fmt.Sprintf("user%d", i)
		userData := &UserData{
			Balances: map[string]float64{
				"USD": float64(i * 10),
				"BTC": float64(i) * 0.001,
			},
			Timestamp: time.Now().Unix(),
		}
		batch.AddUserData(userID, userData)
	}
	
	// 3. Коммит батча и получение нового корня
	root, err := tree.CommitBatch(batch)
	if err != nil {
		panic(err)
	}
	
	fmt.Printf("✓ Новый корень дерева: %x\n", root)
	fmt.Printf("✓ Всего узлов в дереве: %d\n", tree.GetNodeCount())
	
	// 4. Получение данных одного пользователя
	userData, err := tree.GetUserData("user123")
	if err != nil {
		panic(err)
	}
	
	fmt.Printf("\nДанные пользователя user123:\n")
	fmt.Printf("  USD баланс: %.2f\n", userData.Balances["USD"])
	fmt.Printf("  BTC баланс: %.6f\n", userData.Balances["BTC"])
	fmt.Printf("  Tier: %v\n", userData.Metadata["tier"])
	
	// 5. Получение нескольких пользователей одним запросом
	userIDs := []string{"user123", "user456", "user1050"}
	usersData, err := tree.GetMultipleUserData(userIDs)
	if err != nil {
		panic(err)
	}
	
	fmt.Printf("\nПолучено %d пользователей:\n", len(usersData))
	for i, ud := range usersData {
		if ud != nil {
			fmt.Printf("  %s: USD = %.2f\n", userIDs[i], ud.Balances["USD"])
		} else {
			fmt.Printf("  %s: не найден\n", userIDs[i])
		}
	}
	
	// 6. Быстрая проверка наличия пользователя (O(1))
	exists := tree.Has("user123")
	fmt.Printf("\nПользователь user123 существует: %v\n", exists)
	
	// 7. Проверка по хешу ID
	userHash := HashUserID("user123")
	existsByHash := tree.HasByHash(userHash)
	fmt.Printf("Проверка по хешу: %v\n", existsByHash)
	
	// 8. Генерация доказательства для одного пользователя
	proof, err := tree.GenerateProof("user123")
	if err != nil {
		panic(err)
	}
	fmt.Printf("\n✓ Proof создан для пользователя user123\n")
	fmt.Printf("  Путь коммитментов: %d элементов\n", len(proof.Path))
	fmt.Printf("  Data key: %s\n", proof.DataKeys[0])
	
	// 9. Генерация мульти-доказательства (эффективнее для нескольких пользователей)
	multiProof, err := tree.GenerateMultiProof([]string{"user123", "user456", "user1050"})
	if err != nil {
		panic(err)
	}
	fmt.Printf("\n✓ Multi-proof создан для %d пользователей\n", len(multiProof.UserIDs))
	
	// 10. Сериализация в JSON (человеко-читаемый формат)
	jsonData, err := tree.SerializeJSON()
	if err != nil {
		panic(err)
	}
	fmt.Printf("\n✓ JSON сериализация: %d байт\n", len(jsonData))
	
	// 11. Сериализация в бинарный формат (компактный)
	binaryData, err := tree.Serialize()
	if err != nil {
		panic(err)
	}
	fmt.Printf("✓ Бинарная сериализация: %d байт\n", len(binaryData))
	fmt.Printf("  Сжатие: %.1f%%\n", (1.0-float64(len(binaryData))/float64(len(jsonData)))*100)
	
	// 12. Обновление балансов пользователей (новый батч)
	previousRoot := root
	
	batch2 := tree.BeginBatch()
	
	// Обновляем баланс существующего пользователя
	updatedUser1 := &UserData{
		Balances: map[string]float64{
			"USD": 2000.00, // Увеличили баланс
			"BTC": 0.030,   // Увеличили BTC
			"ETH": 1.5,
		},
		Metadata: userData.Metadata, // Сохраняем метаданные
		Timestamp: time.Now().Unix(),
	}
	batch2.AddUserData("user123", updatedUser1)
	
	// Добавляем нового пользователя
	newUser := &UserData{
		Balances: map[string]float64{
			"USD": 100.0,
			"BTC": 0.001,
		},
		Timestamp: time.Now().Unix(),
	}
	batch2.AddUserData("user_new_999", newUser)
	
	newRoot, err := tree.CommitBatch(batch2)
	if err != nil {
		panic(err)
	}
	
	fmt.Printf("\n✓ Обновленный корень: %x\n", newRoot)
	
	// 13. Получение diff между версиями дерева
	diff, err := tree.GetDiff(previousRoot)
	if err != nil {
		panic(err)
	}
	
	fmt.Printf("\n✓ Diff между версиями:\n")
	fmt.Printf("  Предыдущий root: %x\n", diff.PreviousRoot)
	fmt.Printf("  Текущий root: %x\n", diff.CurrentRoot)
	fmt.Printf("  Добавлено: %d\n", len(diff.Added))
	fmt.Printf("  Изменено: %d\n", len(diff.Modified))
	fmt.Printf("  Удалено: %d\n", len(diff.Deleted))
	
	// 14. Работа с Pebble (когда dataStore настроен)
	// if tree.dataStore != nil {
	//     err = tree.SaveToDisk()
	//     if err != nil {
	//         panic(err)
	//     }
	//     fmt.Printf("\n✓ Дерево сохранено в Pebble\n")
	// }
}

// ExamplePebbleIntegration показывает интеграцию с Pebble
func ExamplePebbleIntegration() {
	// Когда будете готовы интегрировать Pebble, используйте так:
	
	/*
	import "github.com/cockroachdb/pebble"
	
	// Открываем Pebble базу данных
	db, err := pebble.Open("verkle_data", &pebble.Options{})
	if err != nil {
		panic(err)
	}
	defer db.Close()
	
	// Создаем адаптер для интерфейса Storage
	pebbleStore := &PebbleStorage{db: db}
	
	// Создаем дерево с Pebble хранилищем
	srs := &kzg.SRS{}
	tree, err := New(4, 256, srs, pebbleStore)
	
	// Работаем с деревом
	batch := tree.BeginBatch()
	userData := &UserData{
		Balances: map[string]float64{"USD": 1000.0},
	}
	batch.AddUserData("user123", userData)
	tree.CommitBatch(batch)
	
	// Сохраняем на диск
	tree.SaveToDisk()
	
	// Позже можно загрузить
	loadedTree, err := LoadFromDisk(pebbleStore, srs)
	*/
}

// PebbleStorage - адаптер для Pebble базы данных
// Раскомментируйте когда будете готовы использовать Pebble
/*
type PebbleStorage struct {
	db *pebble.DB
}

func (ps *PebbleStorage) Put(key []byte, value []byte) error {
	return ps.db.Set(key, value, pebble.Sync)
}

func (ps *PebbleStorage) Get(key []byte) ([]byte, error) {
	value, closer, err := ps.db.Get(key)
	if err != nil {
		return nil, err
	}
	defer closer.Close()
	
	// Копируем данные, так как они становятся недействительными после Close
	result := make([]byte, len(value))
	copy(result, value)
	return result, nil
}

func (ps *PebbleStorage) Delete(key []byte) error {
	return ps.db.Delete(key, pebble.Sync)
}

func (ps *PebbleStorage) Close() error {
	return ps.db.Close()
}
*/
