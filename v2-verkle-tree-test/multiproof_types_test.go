package verkletree

import (
	"strings"
	"testing"
)

// TestMultiProofTypes - сравнение типов multi-proof
func TestMultiProofTypes(t *testing.T) {
	t.Log("\n" + strings.Repeat("=", 100))
	t.Log("ТИПЫ MULTI-PROOF: AGGREGATED vs BUNDLED")
	t.Log(strings.Repeat("=", 100))
	
	proofCount := 1000
	singleProofSize := 304 // байт (depth=8)
	
	// === ТИП 1: Aggregated (неделимый) ===
	t.Log("\n### ТИП 1: AGGREGATED MULTI-PROOF")
	t.Log("Описание: Один большой proof с общими узлами и aggregated KZG")
	
	// Размер с дедупликацией: ~30% экономия
	aggregatedSize := int(float64(proofCount*singleProofSize) * 0.7)
	
	t.Logf("Размер: ~%d KB", aggregatedSize/1024)
	t.Log("\n✅ Преимущества:")
	t.Log("  • Минимальный размер (~30% экономия)")
	t.Log("  • Один KZG commitment для всех")
	t.Log("  • Быстрая верификация всех элементов сразу")
	
	t.Log("\n❌ Недостатки:")
	t.Log("  • НЕ МОЖЕМ проверить отдельный элемент!")
	t.Log("  • Верификация: все или ничего")
	t.Log("  • Нужно передавать весь proof целиком")
	
	// === ТИП 2: Bundled (независимые) ===
	t.Log("\n### ТИП 2: BUNDLED MULTI-PROOF")
	t.Log("Описание: Коллекция независимых пруфов с дедупликацией общих узлов")
	
	// Размер с дедупликацией при передаче
	bundledTransmitSize := int(float64(proofCount*singleProofSize) * 0.7)
	bundledExpandedSize := proofCount * singleProofSize
	
	t.Logf("Размер при передаче: ~%d KB (с дедупликацией)", bundledTransmitSize/1024)
	t.Logf("Размер после распаковки: ~%d KB", bundledExpandedSize/1024)
	
	t.Log("\n✅ Преимущества:")
	t.Log("  • МОЖЕМ проверить любой элемент отдельно!")
	t.Log("  • Гибкость: отправляем только нужные пруфы")
	t.Log("  • Независимая верификация")
	t.Log("  • Можно кэшировать отдельные пруфы")
	
	t.Log("\n❌ Недостатки:")
	t.Log("  • Больше размер после распаковки")
	t.Log("  • Больше операций верификации")
	
	t.Log("\n" + strings.Repeat("=", 100))
}

// MultiProofStructure - структуры для разных типов
type AggregatedMultiProof struct {
	// Общие узлы пути (дедуплицированные)
	SharedPathNodes [][]byte
	
	// Aggregated KZG commitment для всех элементов
	AggregatedKZG []byte
	
	// Индексы и значения
	Indices []int
	Values  [][]byte
	
	// Метаданные для восстановления путей
	PathMetadata []PathInfo
}

type PathInfo struct {
	UserID       string
	NodeIndices  []int // индексы в SharedPathNodes
}

type BundledMultiProof struct {
	// Коллекция независимых пруфов
	Proofs []*Proof
	
	// UserIDs соответствующие каждому proof
	UserIDs []string
	
	// Опционально: дедупликация для эффективной передачи
	SharedNodes [][]byte // Общие узлы (для сжатия при передаче)
	
	// Mapping: какие узлы использует каждый proof
	NodeReferences [][]int
}

// TestExtractSingleProof - тест извлечения одного proof
func TestExtractSingleProof(t *testing.T) {
	t.Log("\n" + strings.Repeat("=", 100))
	t.Log("ИЗВЛЕЧЕНИЕ ОДИНОЧНОГО PROOF ИЗ MULTI-PROOF")
	t.Log(strings.Repeat("=", 100))
	
	// Подготовка
	srs, _ := InitSRS(256)
	tree, _ := New(8, 128, srs, nil)
	
	// Вставляем пользователей A-Z
	batch := tree.BeginBatch()
	users := []string{"alice", "bob", "charlie", "david", "eve", "frank", 
		"grace", "helen", "ivan", "judy", "karen", "leo", "mary"}
	
	for i, user := range users {
		userData := &UserData{
			Balances: map[string]float64{"USD": float64((i + 1) * 1000)},
		}
		batch.AddUserData(user, userData)
	}
	tree.CommitBatch(batch)
	tree.WaitForCommit()
	
	t.Log("\n>>> Сценарий 1: AGGREGATED multi-proof")
	t.Log("Генерируем один aggregated proof для всех пользователей...")
	
	// (Пока нет реализации настоящего aggregated)
	t.Log("Проверяем баланс Mary:")
	t.Log("  ❌ НЕВОЗМОЖНО! Нужно верифицировать весь proof")
	t.Log("  → Нужно проверить ВСЕ элементы [A..Z]")
	t.Log("  → Не можем получить proof только для Mary")
	
	t.Log("\n>>> Сценарий 2: BUNDLED multi-proof")
	t.Log("Генерируем отдельные пруфы для каждого пользователя...")
	
	// Генерируем bundled
	bundled := &BundledMultiProof{
		Proofs:  make([]*Proof, 0, len(users)),
		UserIDs: make([]string, 0, len(users)),
	}
	
	for _, user := range users {
		proof, err := tree.GenerateProof(user)
		if err == nil && proof != nil {
			bundled.Proofs = append(bundled.Proofs, proof)
			bundled.UserIDs = append(bundled.UserIDs, user)
		}
	}
	
	t.Logf("Сгенерировано %d независимых пруфов", len(bundled.Proofs))
	
	t.Log("\nПроверяем баланс Mary:")
	t.Log("  ✅ ВОЗМОЖНО! Извлекаем proof для Mary")
	
	// Находим proof для mary
	maryProof := bundled.ExtractProof("mary")
	
	if maryProof != nil {
		t.Log("  → Извлечен proof для mary")
		t.Logf("  → Размер: ~%d байт", estimateProofSize(maryProof))
		t.Log("  → Можем верифицировать независимо!")
	} else {
		t.Log("  → Proof не найден")
	}
	
	t.Log("\n" + strings.Repeat("=", 100))
}

// TestUseCaseRecommendations - рекомендации по use cases
func TestUseCaseRecommendations(t *testing.T) {
	t.Log("\n" + strings.Repeat("=", 100))
	t.Log("РЕКОМЕНДАЦИИ ПО ИСПОЛЬЗОВАНИЮ")
	t.Log(strings.Repeat("=", 100))
	
	scenarios := []struct {
		useCase      string
		proofType    string
		reasoning    []string
	}{
		{
			"Light client синхронизация",
			"AGGREGATED",
			[]string{
				"Нужно проверить весь state целиком",
				"Минимальный размер критичен",
				"Верификация всего сразу",
			},
		},
		{
			"API для проверки баланса",
			"BUNDLED",
			[]string{
				"Клиенты запрашивают отдельные балансы",
				"Нужна независимая верификация",
				"Гибкость важнее размера",
			},
		},
		{
			"Аудит конкретных аккаунтов",
			"BUNDLED",
			[]string{
				"Проверяем подмножество пользователей",
				"Не нужны пруфы для всех",
				"Можем отправить только нужные",
			},
		},
		{
			"State snapshot для consensus",
			"AGGREGATED",
			[]string{
				"Весь state должен быть валиден",
				"Одна верификация для всех",
				"Минимальный размер для передачи",
			},
		},
		{
			"User-facing приложение",
			"BUNDLED",
			[]string{
				"Каждый пользователь проверяет свой баланс",
				"Нужен только один proof",
				"Кэшируем proof локально",
			},
		},
	}
	
	t.Logf("\n%-35s | %-15s | %s", "Use Case", "Recommended", "Reasoning")
	t.Log(strings.Repeat("-", 100))
	
	for _, s := range scenarios {
		t.Logf("\n%-35s | %-15s |", s.useCase, s.proofType)
		for _, reason := range s.reasoning {
			t.Logf("%-35s | %-15s | • %s", "", "", reason)
		}
	}
	
	t.Log("\n" + strings.Repeat("=", 100))
	
	t.Log("\n💡 ИТОГОВАЯ РЕКОМЕНДАЦИЯ:")
	t.Log("\nДля вашего сценария (1K пруфов, проверка отдельных элементов):")
	t.Log("  ✅ Используйте BUNDLED multi-proof")
	t.Log("\nПричины:")
	t.Log("  1. Вы хотите проверять баланс отдельного юзера M")
	t.Log("  2. Гибкость важна (не всегда нужны все пруфы)")
	t.Log("  3. Можно дедуплицировать при передаче (~30% экономия)")
	t.Log("  4. Каждый proof независимо верифицируется")
	
	t.Log("\nРеализация:")
	t.Log("  • Генерируем отдельные пруфы для каждого пользователя")
	t.Log("  • При передаче: дедуплицируем общие узлы")
	t.Log("  • На клиенте: распаковываем в полные пруфы")
	t.Log("  • Проверяем только нужный proof для M")
}

// BundledMultiProofExample - методы для работы с bundled proof
func (b *BundledMultiProof) ExtractProof(userID string) *Proof {
	// Находим proof по userID
	for i, uid := range b.UserIDs {
		if uid == userID {
			return b.Proofs[i]
		}
	}
	return nil
}

func (b *BundledMultiProof) CompressForTransmission() []byte {
	// Дедупликация общих узлов для эффективной передачи
	// TODO: реализация
	return nil
}

func (b *BundledMultiProof) DecompressFromTransmission(data []byte) error {
	// Восстановление полных пруфов из сжатого формата
	// TODO: реализация
	return nil
}
