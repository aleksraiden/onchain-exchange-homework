# Установка зависимостей
go get github.com/zeebo/blake3

# Запуск основных тестов
go run main.go

# Запуск бенчмарков
go test -bench=. -benchmem -benchtime=10s



=== Sparse Variable-Arity Merkle-Patricia Tree Benchmark ===

Before fill: Alloc = 0 MB, HeapAlloc = 0 MB, HeapObjects = 289
1. Генерация 1M системных аккаунтов...
   Время: 5.757610602s
After 1M system: Alloc = 151 MB, HeapAlloc = 151 MB, HeapObjects = 1105139
2. Генерация 10M случайных пользователей...
   Время: 52.118546388s
   Всего аккаунтов: 11000000
After 11M total: Alloc = 1471 MB, HeapAlloc = 1471 MB, HeapObjects = 11133827
   Оценка памяти по структурам: 1177.98 MB

3. Вычисление root hash...
   Root Hash (первые 16 байт): 41b38cc656615181e42f7d98dd2b0fb4
   Время: 48.895µs

4. Обновление 1000 случайных аккаунтов...
   Новый Root Hash (первые 16 байт): ca13de3b8421b9d5f56fa239f7d37e56
   Время (обновление + пересчет): 5.538985ms

5. Обновление 1000 + добавление 5000 новых аккаунтов...
   Новый Root Hash (первые 16 байт): 0d9e4ac80a12962a5b2542835b2662e0
   Время (обновление + вставка + пересчет): 32.660398ms
   Всего аккаунтов: 11005000
After updates: Alloc = 1472 MB, HeapAlloc = 1472 MB, HeapObjects = 11138830

6. Тест получения случайного аккаунта (1M операций)...
   Время: 755.757211ms
   Скорость: 1323176 ops/sec
   Cache hits: 1000000/1000000

7. Тест батч чтения...
   Batch 16: 112.538335ms (1421738 ops/sec)
   Batch 32: 221.368648ms (1445552 ops/sec)
   Batch 64: 442.370959ms (1446750 ops/sec)
   Batch 128: 742.68173ms (1723484 ops/sec)
=== Тест разных конфигураций дерева ===

--- Тестирование LeafArity = 16 ---
  Вставка 1M: 5.259014341s (190150 ops/sec)
  Root hash: 44.371µs
  Память: 109.86 MB
  MaxDepth: 4

--- Тестирование LeafArity = 64 ---
  Вставка 1M: 5.551534927s (180130 ops/sec)
  Root hash: 33.052µs
  Память: 109.86 MB
  MaxDepth: 3

--- Тестирование LeafArity = 128 ---
  Вставка 1M: 5.634604957s (177475 ops/sec)
  Root hash: 33.94µs
  Память: 109.86 MB
  MaxDepth: 3

--- Тестирование LeafArity = 256 ---
  Вставка 1M: 5.524882376s (180999 ops/sec)
  Root hash: 34.67µs
  Память: 109.86 MB
  MaxDepth: 2


=== Тесты завершены ===
