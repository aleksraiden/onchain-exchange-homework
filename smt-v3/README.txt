go test -bench=. -benchmem -benchtime=3s
goos: linux
goarch: amd64
pkg: smt-blake3
cpu: 13th Gen Intel(R) Core(TM) i5-13500
BenchmarkOperation_Insert_New-20                  255795             12792 ns/op            6519 B/op         42 allocs/op
BenchmarkOperation_Update_Existing-20             530418              6560 ns/op            1778 B/op         31 allocs/op
BenchmarkOperation_Get_Existing-20               2583848              1461 ns/op               0 B/op          0 allocs/op
BenchmarkOperation_Get_NonExisting-20            8282598               426.2 ns/op            16 B/op          1 allocs/op
BenchmarkOperation_Delete_Existing-20             409333              8951 ns/op            4681 B/op         23 allocs/op
BenchmarkFullBuild_100K-20                             3        1197582857 ns/op        509078544 B/op   3952497 allocs/op
BenchmarkBatchInsert_100K-20                          14         231244185 ns/op        291292981 B/op    906710 allocs/op
BenchmarkParallelGet-20                          9356667               423.5 ns/op             0 B/op          0 allocs/op
PASS
ok      smt-blake3      47.314s





cpu: Intel(R) Xeon(R) Gold 5412U
BenchmarkOperation_Insert_New-48                  170323             27087 ns/op            5420 B/op         41 allocs/op
BenchmarkOperation_Update_Existing-48             275737             13882 ns/op            1841 B/op         31 allocs/op
BenchmarkOperation_Get_Existing-48               1327813              2282 ns/op               0 B/op          0 allocs/op
BenchmarkOperation_Get_NonExisting-48            5967140               618.9 ns/op            16 B/op          1 allocs/op
BenchmarkOperation_Delete_Existing-48             196108             18326 ns/op            4586 B/op         23 allocs/op
BenchmarkFullBuild_100K-48                             2        2706972374 ns/op        508621704 B/op   3953373 allocs/op
BenchmarkBatchInsert_100K-48                           7         490566913 ns/op        291478068 B/op    905536 allocs/op
BenchmarkParallelGet-48                          3419766              1013 ns/op               0 B/op          0 allocs/op
PASS
ok      smt-blake3      55.695s


##### Arity-256


goos: linux
goarch: amd64
pkg: smt-blake3
cpu: 13th Gen Intel(R) Core(TM) i5-13500
BenchmarkOperation_Insert_New-20                  208464             21396 ns/op           47876 B/op         20 allocs/op
BenchmarkOperation_Update_Existing-20             228762             15875 ns/op           47167 B/op         19 allocs/op
BenchmarkOperation_Get_Existing-20              13155746               266.7 ns/op             0 B/op          0 allocs/op
BenchmarkOperation_Get_NonExisting-20           37488435                95.99 ns/op           16 B/op          1 allocs/op
BenchmarkOperation_Delete_Existing-20             219944             16186 ns/op           45169 B/op         17 allocs/op
BenchmarkFullBuild_100K-20                             2        2092136958 ns/op        4348497428 B/op  1985730 allocs/op
BenchmarkBatchInsert_100K-20                          15         205390465 ns/op        420033940 B/op    393401 allocs/op
BenchmarkParallelGet-20                          7055571               462.5 ns/op             1 B/op          0 allocs/op
PASS
ok      smt-blake3      62.417s


goos: linux
goarch: amd64
pkg: smt-blake3
cpu: Intel(R) Xeon(R) Gold 5412U
BenchmarkOperation_Insert_New-48                   97756             45498 ns/op           43349 B/op         19 allocs/op
BenchmarkOperation_Update_Existing-48             110811             32150 ns/op           47292 B/op         19 allocs/op
BenchmarkOperation_Get_Existing-48               7758982               435.9 ns/op             0 B/op          0 allocs/op
BenchmarkOperation_Get_NonExisting-48           28000296               131.3 ns/op            16 B/op          1 allocs/op
BenchmarkOperation_Delete_Existing-48             103058             33622 ns/op           45208 B/op         17 allocs/op
BenchmarkFullBuild_100K-48                             1        4474921193 ns/op        4348225296 B/op  1985682 allocs/op
BenchmarkBatchInsert_100K-48                           9         360151214 ns/op        421588829 B/op    393785 allocs/op
BenchmarkParallelGet-48                          4859954               621.7 ns/op             1 B/op          0 allocs/op
PASS
ok      smt-blake3      73.175s



#### Syntetic multi-arity test 

goos: linux
goarch: amd64
pkg: smt-blake3
cpu: 13th Gen Intel(R) Core(TM) i5-13500
BenchmarkArity_008_Bits3-20     39377294               183.4 ns/op               8.000 arity            85.00 depth           65 B/op          3 allocs/op
BenchmarkArity_016_Bits4-20     29663491               184.0 ns/op              16.00 arity             64.00 depth           65 B/op          3 allocs/op
BenchmarkArity_032_Bits5-20     30872430               186.2 ns/op              32.00 arity             51.00 depth           65 B/op          3 allocs/op
BenchmarkArity_064_Bits6-20     32643594               191.2 ns/op              64.00 arity             42.00 depth           65 B/op          3 allocs/op
BenchmarkArity_128_Bits7-20     39035148               179.5 ns/op             128.0 arity              36.00 depth           65 B/op          3 allocs/op
BenchmarkArity_256_Bits8-20     37902601               182.5 ns/op             256.0 arity              32.00 depth           65 B/op          3 allocs/op
PASS
ok      smt-blake3      43.977s

cpu: Intel(R) Xeon(R) Gold 5412U
BenchmarkArity_008_Bits3-48     18902665               314.2 ns/op               8.000 arity            85.00 depth           65 B/op          3 allocs/op
BenchmarkArity_016_Bits4-48     19362420               311.6 ns/op              16.00 arity             64.00 depth           65 B/op          3 allocs/op
BenchmarkArity_032_Bits5-48     18844144               313.4 ns/op              32.00 arity             51.00 depth           65 B/op          3 allocs/op
BenchmarkArity_064_Bits6-48     18863384               319.0 ns/op              64.00 arity             42.00 depth           65 B/op          3 allocs/op
BenchmarkArity_128_Bits7-48     18787236               313.6 ns/op             128.0 arity              36.00 depth           65 B/op          3 allocs/op
BenchmarkArity_256_Bits8-48     19802878               311.3 ns/op             256.0 arity              32.00 depth           65 B/op          3 allocs/op
PASS
ok      smt-blake3      37.924s

