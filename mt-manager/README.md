# Менеджер MerkleTree 

Текущая версия 



**Benchmark Intel Xeon Gold:**

**CGO_ENABLED=0 GOAMD64=v3 go test ./merkletree -ldflags="-s -w" -v -run "TestManagerStressTest/Trees_10" -timeout=10m -count=1**

=== RUN   TestManagerStressTest
=== RUN   TestManagerStressTest/Trees_10
    manager_benchmark_test.go:42: === Тест с 10 деревьями ===
    manager_benchmark_test.go:48: Фаза 1: Создание и заполнение деревьев...
    manager_benchmark_test.go:72:   Создано 10/10 деревьев, всего элементов: 5914638
    manager_benchmark_test.go:77: Фаза 1 завершена за 6.406931876s
    manager_benchmark_test.go:78:   Всего элементов: 5914638
    manager_benchmark_test.go:79:   Средний размер дерева: 591463
    manager_benchmark_test.go:82:
        Фаза 2: Вычисление хешей деревьев...
    manager_benchmark_test.go:97: Фаза 2 завершена за 10.045585ms
    manager_benchmark_test.go:98:   Среднее время на дерево: 1.004558ms
    manager_benchmark_test.go:104:   tree_1: 0b5d45440ee624a8738c0e74e217bd5e
    manager_benchmark_test.go:104:   tree_5: 79a6763e959c2d964a2a9d0df64449d5
    manager_benchmark_test.go:104:   tree_7: 3d71d87a0622a7c6fcb29281f4981390
    manager_benchmark_test.go:110:
        Фаза 3: Вычисление глобального корня...
    manager_benchmark_test.go:116: Фаза 3 завершена за 15.192µs
    manager_benchmark_test.go:117:   Глобальный корень: b6bb6c9c57cbd85f75e3527b35d185be
    manager_benchmark_test.go:120:
        Фаза 4: Выполнение случайных обновлений...
    manager_benchmark_test.go:148:   Выполнено 100000/100000 обновлений (391 батчей)
    manager_benchmark_test.go:153: Фаза 4 завершена за 279.780042ms
    manager_benchmark_test.go:154:   Батчей обновлений: 391
    manager_benchmark_test.go:155:   Средний размер батча: 255
    manager_benchmark_test.go:156:   Скорость обновлений: 357424 ops/sec
    manager_benchmark_test.go:159:
        Фаза 5: Выполнение случайных удалений...
    manager_benchmark_test.go:196:   Обработано 10/10 деревьев, удалено элементов: 52557
    manager_benchmark_test.go:201: Фаза 5 завершена за 141.052225ms
    manager_benchmark_test.go:202:   Всего удалено: 52557 элементов
    manager_benchmark_test.go:203:   Батчей удалений: 211
    manager_benchmark_test.go:205:   Скорость удалений: 372607 ops/sec
    manager_benchmark_test.go:209:
        Фаза 6: Пересчет хешей после удалений...
    manager_benchmark_test.go:223: Фаза 6 завершена за 956.129µs
    manager_benchmark_test.go:224:   Деревьев с измененным хешем: 10/10
    manager_benchmark_test.go:227:
        Фаза 7: Новый глобальный корень...
    manager_benchmark_test.go:233: Фаза 7 завершена за 12.16µs
    manager_benchmark_test.go:234:   Новый глобальный корень: 4236a0ca3a8f7903072f5e4443eaefcd
    manager_benchmark_test.go:244:
        === Итоговая статистика ===
    manager_benchmark_test.go:245: Общее время: 6.838793209s
    manager_benchmark_test.go:246:   Фаза 1 (заполнение): 6.406931876s
    manager_benchmark_test.go:247:   Фаза 2 (хеши): 10.045585ms
    manager_benchmark_test.go:248:   Фаза 3 (глобальный рут): 15.192µs
    manager_benchmark_test.go:249:   Фаза 4 (обновления): 279.780042ms
    manager_benchmark_test.go:250:   Фаза 5 (удаления): 141.052225ms
    manager_benchmark_test.go:251:   Фаза 6 (пересчет): 956.129µs
    manager_benchmark_test.go:252:   Фаза 7 (новый рут): 12.16µs
    manager_benchmark_test.go:255:
        Статистика менеджера:
    manager_benchmark_test.go:256:   Деревьев: 10
    manager_benchmark_test.go:257:   Элементов: 5962081
    manager_benchmark_test.go:258:   Узлов: 2590
    manager_benchmark_test.go:259:   Размер кеша: 984280
    manager_benchmark_test.go:262:
        Статистика удалений по деревьям:
    manager_benchmark_test.go:267:   tree_0:
    manager_benchmark_test.go:268:     Элементов: 896704
    manager_benchmark_test.go:269:     Удаленных узлов: 8522
    manager_benchmark_test.go:267:   tree_1:
    manager_benchmark_test.go:268:     Элементов: 703335
    manager_benchmark_test.go:269:     Удаленных узлов: 4461
    manager_benchmark_test.go:267:   tree_2:
    manager_benchmark_test.go:268:     Элементов: 893482
    manager_benchmark_test.go:269:     Удаленных узлов: 3241
=== RUN   TestManagerStressTest/Trees_100
    manager_benchmark_test.go:42: === Тест с 100 деревьями ===
    manager_benchmark_test.go:48: Фаза 1: Создание и заполнение деревьев...
    manager_benchmark_test.go:72:   Создано 10/100 деревьев, всего элементов: 5478809
    manager_benchmark_test.go:72:   Создано 20/100 деревьев, всего элементов: 9901831
    manager_benchmark_test.go:72:   Создано 30/100 деревьев, всего элементов: 14695952
    manager_benchmark_test.go:72:   Создано 40/100 деревьев, всего элементов: 19338019
    manager_benchmark_test.go:72:   Создано 50/100 деревьев, всего элементов: 25461285
    manager_benchmark_test.go:72:   Создано 60/100 деревьев, всего элементов: 32129440
    manager_benchmark_test.go:72:   Создано 70/100 деревьев, всего элементов: 37365950
    manager_benchmark_test.go:72:   Создано 80/100 деревьев, всего элементов: 44152075
    manager_benchmark_test.go:72:   Создано 90/100 деревьев, всего элементов: 48890190
    manager_benchmark_test.go:72:   Создано 100/100 деревьев, всего элементов: 54109050
    manager_benchmark_test.go:77: Фаза 1 завершена за 55.091123857s
    manager_benchmark_test.go:78:   Всего элементов: 54109050
    manager_benchmark_test.go:79:   Средний размер дерева: 541090
    manager_benchmark_test.go:82:
        Фаза 2: Вычисление хешей деревьев...
    manager_benchmark_test.go:97: Фаза 2 завершена за 95.472906ms
    manager_benchmark_test.go:98:   Среднее время на дерево: 954.729µs
    manager_benchmark_test.go:104:   tree_57: 84e51aaf22092bce673ecb4b8012ed08
    manager_benchmark_test.go:104:   tree_64: 3b6bd0a088a21b9f1fecba4f712a4566
    manager_benchmark_test.go:104:   tree_18: 2ccdd78144aebb45a8bce66bc971f5d5
    manager_benchmark_test.go:110:
        Фаза 3: Вычисление глобального корня...
    manager_benchmark_test.go:116: Фаза 3 завершена за 116.985µs
    manager_benchmark_test.go:117:   Глобальный корень: 439b0066bdb4da938d4652cb9d354b65
    manager_benchmark_test.go:120:
        Фаза 4: Выполнение случайных обновлений...
    manager_benchmark_test.go:148:   Выполнено 100000/100000 обновлений (409 батчей)
    manager_benchmark_test.go:153: Фаза 4 завершена за 299.203195ms
    manager_benchmark_test.go:154:   Батчей обновлений: 409
    manager_benchmark_test.go:155:   Средний размер батча: 244
    manager_benchmark_test.go:156:   Скорость обновлений: 334221 ops/sec
    manager_benchmark_test.go:159:
        Фаза 5: Выполнение случайных удалений...
    manager_benchmark_test.go:196:   Обработано 10/100 деревьев, удалено элементов: 38894
    manager_benchmark_test.go:196:   Обработано 20/100 деревьев, удалено элементов: 95254
    manager_benchmark_test.go:196:   Обработано 30/100 деревьев, удалено элементов: 138827
    manager_benchmark_test.go:196:   Обработано 40/100 деревьев, удалено элементов: 190360
    manager_benchmark_test.go:196:   Обработано 50/100 деревьев, удалено элементов: 242564
    manager_benchmark_test.go:196:   Обработано 60/100 деревьев, удалено элементов: 307792
    manager_benchmark_test.go:196:   Обработано 70/100 деревьев, удалено элементов: 362517
    manager_benchmark_test.go:196:   Обработано 80/100 деревьев, удалено элементов: 391332
    manager_benchmark_test.go:196:   Обработано 90/100 деревьев, удалено элементов: 443105
    manager_benchmark_test.go:196:   Обработано 100/100 деревьев, удалено элементов: 493013
    manager_benchmark_test.go:201: Фаза 5 завершена за 901.125955ms
    manager_benchmark_test.go:202:   Всего удалено: 493013 элементов
    manager_benchmark_test.go:203:   Батчей удалений: 2130
    manager_benchmark_test.go:205:   Скорость удалений: 547108 ops/sec
    manager_benchmark_test.go:209:
        Фаза 6: Пересчет хешей после удалений...
    manager_benchmark_test.go:223: Фаза 6 завершена за 11.376121ms
    manager_benchmark_test.go:224:   Деревьев с измененным хешем: 100/100
    manager_benchmark_test.go:227:
        Фаза 7: Новый глобальный корень...
    manager_benchmark_test.go:233: Фаза 7 завершена за 85.444µs
    manager_benchmark_test.go:234:   Новый глобальный корень: 3f7ab0ddcc716e3cf6e8fd89acaad083
    manager_benchmark_test.go:244:
        === Итоговая статистика ===
    manager_benchmark_test.go:245: Общее время: 56.398504463s
    manager_benchmark_test.go:246:   Фаза 1 (заполнение): 55.091123857s
    manager_benchmark_test.go:247:   Фаза 2 (хеши): 95.472906ms
    manager_benchmark_test.go:248:   Фаза 3 (глобальный рут): 116.985µs
    manager_benchmark_test.go:249:   Фаза 4 (обновления): 299.203195ms
    manager_benchmark_test.go:250:   Фаза 5 (удаления): 901.125955ms
    manager_benchmark_test.go:251:   Фаза 6 (пересчет): 11.376121ms
    manager_benchmark_test.go:252:   Фаза 7 (новый рут): 85.444µs
    manager_benchmark_test.go:255:
        Статистика менеджера:
    manager_benchmark_test.go:256:   Деревьев: 100
    manager_benchmark_test.go:257:   Элементов: 53716037
    manager_benchmark_test.go:258:   Узлов: 25900
    manager_benchmark_test.go:259:   Размер кеша: 9851758
    manager_benchmark_test.go:262:
        Статистика удалений по деревьям:
    manager_benchmark_test.go:267:   tree_0:
    manager_benchmark_test.go:268:     Элементов: 748767
    manager_benchmark_test.go:269:     Удаленных узлов: 5343
    manager_benchmark_test.go:267:   tree_1:
    manager_benchmark_test.go:268:     Элементов: 357155
    manager_benchmark_test.go:269:     Удаленных узлов: 6402
    manager_benchmark_test.go:267:   tree_2:
    manager_benchmark_test.go:268:     Элементов: 174661
    manager_benchmark_test.go:269:     Удаленных узлов: 350
--- PASS: TestManagerStressTest (63.24s)
    --- PASS: TestManagerStressTest/Trees_10 (6.84s)
    --- PASS: TestManagerStressTest/Trees_100 (56.40s)
PASS
ok      mt-manager/merkletree   63.899s