package main

import (
	"fmt"
	"github.com/zeebo/blake3"
)

func main() {
	// Создаём новый хешер BLAKE3
	hasher := blake3.New()

	// Ничего не пишем — хеш пустой строки
	// hasher.Write([]byte("")) — делать не обязательно

	// Получаем результат хеширования (32 байта по умолчанию)
	sum := hasher.Sum(nil)

	// Выводим побайтово в hex
	fmt.Printf("BLAKE3(\"\" (пустая строка)) = ")
	for i, b := range sum {
		fmt.Printf("%02x", b)
		if i%8 == 7 {
			fmt.Print(" ")
		}
	}
	fmt.Println()

	// Альтернативный более компактный вариант вывода:
	fmt.Printf("\nОдной строкой: %x\n", sum)
	
	fmt.Println("Вариант 3 - с запятыми (удобно для массивов):")
	fmt.Print("[]byte{")
	for i, b := range sum {
		if i > 0 {
			fmt.Print(", ")
		}
		fmt.Printf("0x%02x", b)
	}
	fmt.Println("}")
}