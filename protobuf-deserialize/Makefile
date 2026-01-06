.PHONY: generate test bench

# Установка зависимостей
install:
	go install google.golang.org/protobuf/cmd/protoc-gen-go@latest
	go install github.com/planetscale/vtprotobuf/cmd/protoc-gen-go-vtproto@latest

# Генерация Go кода из proto (упрощенная версия)
generate:
	protoc --go_out=. \
		--plugin=protoc-gen-go=$(shell go env GOPATH)/bin/protoc-gen-go \
		--go-vtproto_out=. \
		--plugin=protoc-gen-go-vtproto=$(shell go env GOPATH)/bin/protoc-gen-go-vtproto \
		--go-vtproto_opt=features=marshal+unmarshal+size \
		order.proto


# Запуск теста
test:
	go test -v -run TestOrderDeserialization

# Запуск бенчмарков
bench:
	go test -bench=. -benchmem -benchtime=5s
