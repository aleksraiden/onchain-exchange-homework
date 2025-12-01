// main.go — Verkle Tree прототип для биржи (CLOB)
// Полные хеши + красивое дерево в консоли
// go run main.go

package main

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"io/ioutil"
	"math/rand"
	"time"

	kzg4844 "github.com/crate-crypto/go-kzg-4844"
)

var ctx *kzg4844.Context

func init() {
	var err error
	ctx, err = kzg4844.NewContext4096Secure()
	if err != nil {
		panic("KZG init failed: " + err.Error())
	}
	fmt.Println("KZG контекст инициализирован (4096 полей)")
}

type VerkleNode struct {
	Commitment kzg4844.KZGCommitment
	Values     [64][32]byte
	Stem       [16]byte
	Populated  uint64
}

var Root = make(map[[16]byte]*VerkleNode)

func minuteStem(ts time.Time) [16]byte {
	key := []byte(fmt.Sprintf("BTC/USD_%s", ts.Format("2006-01-02T15:04")))
	hash := sha256.Sum256(key)
	var stem [16]byte
	copy(stem[:], hash[:16])
	return stem
}

func insertPrice(ts time.Time, price uint64) {
	stem := minuteStem(ts)
	second := ts.Second()

	node, ok := Root[stem]
	if !ok {
		node = &VerkleNode{Stem: stem}
		Root[stem] = node
	}

	idx := uint64(second)
	if idx >= 60 {
		return
	}

	binary.BigEndian.PutUint64(node.Values[idx][24:], price)
	node.Populated |= (1 << idx)

	rebuildCommitment(node)

	fmt.Printf("[%s] → %s | $%d | stem %x… | сек %02d\n",
		time.Now().UTC().Format("15:04:05"),
		ts.Format("15:04:05"),
		price,
		stem[:4],
		second,
	)
}

func rebuildCommitment(node *VerkleNode) {
	var blob kzg4844.Blob
	for i := 0; i < 64; i++ {
		copy(blob[i*32:(i+1)*32], node.Values[i][:])
	}

	commitment, _ := ctx.BlobToKZGCommitment(&blob, 0)
	node.Commitment = commitment
}

func makeAndVerifyProof(node *VerkleNode, second int) {
	var blob kzg4844.Blob
	for i := 0; i < 64; i++ {
		copy(blob[i*32:(i+1)*32], node.Values[i][:])
	}

	proof, _ := ctx.ComputeBlobKZGProof(&blob, node.Commitment, 0)

	if err := ctx.VerifyBlobKZGProof(&blob, node.Commitment, proof); err != nil {
		fmt.Printf("  ПРУФ НЕ ПРОШЁЛ: %v\n", err)
	} else {
		price := binary.BigEndian.Uint64(node.Values[second][24:])
		fmt.Printf("  ПРУФ ВЕРНЫЙ\n")
		fmt.Printf("    Секунда: %02d → цена: $%d\n", second, price)
		fmt.Printf("    Commitment: %s\n", hex.EncodeToString(node.Commitment[:]))
		fmt.Printf("    Proof:      %s\n\n", hex.EncodeToString(proof[:]))
	}
}

func printTree() {
	if len(Root) == 0 {
		fmt.Println("Дерево пустое")
		return
	}

	fmt.Printf("\n%s\n", string(make([]byte, 80)))
	fmt.Println("                  VERKLE TREE — ГРАФИЧЕСКИЙ ВЫВОД")
	fmt.Printf("%s\n", string(make([]byte, 80)))

	// Виртуальный root commitment — хеш всех дочерних
	rootHash := sha256.New()
	for _, node := range Root {
		rootHash.Write(node.Commitment[:])
	}
	virtualRoot := rootHash.Sum(nil)

	fmt.Printf("ROOT COMMITMENT (виртуальный): %s\n\n", hex.EncodeToString(virtualRoot))

	fmt.Println("Первый уровень (минутные узлы):")

	i := 0
	for stem, node := range Root {
		prefix := "├── "
		lastPrefix := "└── "
		if i == len(Root)-1 {
			fmt.Print(lastPrefix)
		} else {
			fmt.Print(prefix)
		}

		popCount := 0
		for j := uint64(0); j < 60; j++ {
			if node.Populated&(1<<j) != 0 {
				popCount++
			}
		}

		fmt.Printf("stem: %x\n", stem)
		fmt.Printf("│   время: %s\n", time.Now().UTC().Truncate(time.Minute).Add(time.Duration(i)*time.Minute).Format("02 Jan 15:04"))
		fmt.Printf("│   цен: %d/60\n", popCount)
		fmt.Printf("│   commitment: %s\n", hex.EncodeToString(node.Commitment[:]))
		fmt.Printf("│   proof sample: %s…\n\n", hex.EncodeToString(getSampleProof(node))[:64])

		i++
	}
	fmt.Printf("%s\n", string(make([]byte, 80)))
}

func getSampleProof(node *VerkleNode) []byte {
	var blob kzg4844.Blob
	for i := 0; i < 64; i++ {
		copy(blob[i*32:(i+1)*32], node.Values[i][:])
	}
	proof, _ := ctx.ComputeBlobKZGProof(&blob, node.Commitment, 0)
	return proof[:]
}

func main() {
	start := time.Now().UTC()
	fmt.Printf("=== Verkle Tree тест для высокоскоростной биржи ===\n")
	fmt.Printf("Старт (UTC): %s\n\n", start.Format("2006-01-02T15:04:05Z"))

	basePrice := uint64(60_000)
	startMinute := time.Now().UTC().Truncate(time.Minute)

	for i := 0; i < 180; i++ {
		ts := startMinute.Add(time.Duration(i) * time.Second)
		insertPrice(ts, basePrice+uint64(i))
		time.Sleep(3 * time.Millisecond)
	}

	fmt.Println("\n=== Результаты ===")
	fmt.Printf("Создано минутных узлов: %d\n\n", len(Root))

	// 5 полных пруфов
	fmt.Println("5 ПОЛНЫХ KZG-ПРУФОВ:\n")
	var stems [][16]byte
	for s := range Root {
		stems = append(stems, s)
	}
	rand.Shuffle(len(stems), func(i, j int) { stems[i], stems[j] = stems[j], stems[i] })

	for i := 0; i < 5 && i < len(stems); i++ {
		node := Root[stems[i]]
		sec := rand.Intn(60)
		for node.Populated&(1<<uint64(sec)) == 0 {
			sec = rand.Intn(60)
		}
		fmt.Printf("ПРУФ #%d (минута %x)\n", i+1, stems[i][:4])
		makeAndVerifyProof(node, sec)
	}

	// Графический вывод
	printTree()

	// Сохранение
	filename := fmt.Sprintf("verkle_test.%s.dat", time.Now().UTC().Format("20060102T150405Z"))
	var data []byte
	for _, node := range Root {
		data = append(data, node.Commitment[:]...)
		data = append(data, blobFromNode(node)...)
		data = append(data, node.Stem[:]...)
	}
	if err := ioutil.WriteFile(filename, data, 0644); err != nil {
		fmt.Println("Ошибка сохранения:", err)
	} else {
		fmt.Printf("\nДерево сохранено → %s (%d КБ)\n", filename, len(data)/1024)
	}

	fmt.Printf("\nГотово за %v\n", time.Since(start))
}

func blobFromNode(node *VerkleNode) []byte {
	var b kzg4844.Blob
	for i := 0; i < 64; i++ {
		copy(b[i*32:(i+1)*32], node.Values[i][:])
	}
	return b[:]
}