//benchmark
go test -v -run TestBenchmarkReport -timeout=10m

//Или быстро все
go test -bench=. -benchmem -benchtime=2s -count=3

//Только тесты (что ничего не сломалось)
go test -run "Test(Basic|Batch|Bundled)" -v