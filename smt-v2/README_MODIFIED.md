# In-Memory Sparse Merkle Trie

Это модифицированная версия оптимизированного sparse merkle trie без зависимостей от aergoio.
Все данные хранятся только в памяти (RAM).

## Что изменено

### Удалены зависимости:
- `github.com/aergoio/aergo-lib/db`
- `github.com/aergoio/aergo/v2/types/dbkey`
- `github.com/aergoio/aergo/v2/internal/common`

### Добавлено:
- `MemoryStore` - простое хранилище в памяти (заменяет db.DB)
- Упрощенный API без внешних зависимостей

### Изменения в API:
**Было:**
```go
store := db.NewDB(db.BadgerImpl, "/path/to/db")
trie := NewTrie(nil, common.Hasher, store)
```

**Стало:**
```go
trie := NewTrie(nil, Hasher)
```

## Установка

```bash
# Скопируйте все файлы в вашу директорию проекта
cp *.go /path/to/your/project/trie/

# Или используйте как модуль
go mod init github.com/yourusername/trie
```

## Использование

```go
package main

import (
    "crypto/sha256"
    "fmt"
    "github.com/yourusername/trie"
)

// Хэш функция
func Hasher(data ...[]byte) []byte {
    hasher := sha256.New()
    for _, b := range data {
        hasher.Write(b)
    }
    return hasher.Sum(nil)
}

func main() {
    // Создаем дерево
    tr := trie.NewTrie(nil, Hasher)

    // Добавляем данные
    keys := [][]byte{
        Hasher([]byte("key1")),
        Hasher([]byte("key2")),
    }
    values := [][]byte{
        []byte("value1"),
        []byte("value2"),
    }

    // Обновляем
    root, _ := tr.Update(keys, values)
    tr.Commit()

    // Читаем
    val, _ := tr.Get(keys[0])
    fmt.Printf("Value: %s\n", val)

    // Получаем Merkle proof
    proof, included, _, _, _ := tr.MerkleProof(keys[0])
    if included && tr.VerifyInclusion(proof, keys[0], values[0]) {
        fmt.Println("Merkle proof verified!")
    }
}
```

## Запуск тестов

```bash
go test -v
```

## Структура файлов

- `util.go` - утилиты и константы
- `trie_cache.go` - хранилище в памяти (MemoryStore и CacheDB)
- `trie.go` - основная структура и логика дерева
- `trie_tools.go` - вспомогательные функции (Get, Commit, Stash, LoadCache)
- `trie_revert.go` - функции отката состояния
- `trie_merkle_proof.go` - функции Merkle proof (копируется из оригинала БЕЗ ИЗМЕНЕНИЙ)
- `example_test.go` - примеры тестов

## API Reference

### Создание дерева

```go
func NewTrie(root []byte, hash func(data ...[]byte) []byte) *Trie
```

### Основные операции

```go
// Обновить дерево (заменяет предыдущие незакоммиченные изменения)
func (s *Trie) Update(keys, values [][]byte) ([]byte, error)

// Атомарное обновление (сохраняет все промежуточные состояния)
func (s *Trie) AtomicUpdate(keys, values [][]byte) ([]byte, error)

// Получить значение по ключу
func (s *Trie) Get(key []byte) ([]byte, error)

// Закоммитить изменения в память
func (s *Trie) Commit()

// Откатить незакоммиченные изменения
func (s *Trie) Stash(rollbackCache bool) error

// Вернуться к предыдущему состоянию
func (s *Trie) Revert(toOldRoot []byte) error
```

### Merkle Proof

```go
// Создать Merkle proof
func (s *Trie) MerkleProof(key []byte) ([][]byte, bool, []byte, []byte, error)

// Создать сжатый Merkle proof
func (s *Trie) MerkleProofCompressed(key []byte) ([]byte, [][]byte, uint64, bool, []byte, []byte, error)

// Проверить proof включения
func (s *Trie) VerifyInclusion(ap [][]byte, key, value []byte) bool

// Проверить proof невключения
func (s *Trie) VerifyNonInclusion(ap [][]byte, key, value, proofKey []byte) bool

// Проверить сжатый proof включения
func (s *Trie) VerifyInclusionC(bitmap, key, value []byte, ap [][]byte, length int) bool

// Проверить сжатый proof невключения
func (s *Trie) VerifyNonInclusionC(ap [][]byte, length int, bitmap, key, value, proofKey []byte) bool
```

## Преимущества

✅ Нет внешних зависимостей - только стандартная библиотека Go  
✅ Проще в использовании (меньше параметров)  
✅ Быстрее (нет дисковых операций)  
✅ Легче тестировать  
✅ Подходит для in-memory кэшей и временных структур  
✅ Полная поддержка Merkle proofs  
✅ Параллельные обновления с goroutines  

## Недостатки

⚠️ Данные не сохраняются между перезапусками  
⚠️ Ограничено размером доступной памяти  
⚠️ Нет персистентности на диск  

## Оригинальная документация

Для получения более подробной информации о алгоритме и структуре дерева,
смотрите оригинальный README из проекта aergoio.

## Лицензия

MIT License
