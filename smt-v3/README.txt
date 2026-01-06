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
