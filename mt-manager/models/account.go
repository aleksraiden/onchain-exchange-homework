package models

import (
	"crypto/rand"
	"encoding/binary"
	"github.com/zeebo/blake3"
)

// AccountStatus статус аккаунта
type AccountStatus uint8

const (
	StatusSystem AccountStatus = iota
	StatusBlocked
	StatusMM
	StatusAlgo
	StatusUser
)

func (s AccountStatus) String() string {
	names := [...]string{"system", "blocked", "mm", "algo", "user"}
	if int(s) < len(names) {
		return names[s]
	}
	return "unknown"
}

// Account представляет аккаунт пользователя
type Account struct {
	PublicKey [32]byte
	UID       uint64
	key       [8]byte // Кешированный ключ
	EmailHash uint64
	Status    AccountStatus
	_         [7]byte // Padding
}

// ID реализует интерфейс Hashable
func (a *Account) ID() uint64 {
	return a.UID
}

// Key реализует интерфейс Hashable
func (a *Account) Key() [8]byte {
	return a.key
}

// Hash реализует интерфейс Hashable
func (a *Account) Hash() [32]byte {
	hasher := blake3.New()
	hasher.Write(a.key[:])
	binary.Write(hasher, binary.BigEndian, a.EmailHash)
	hasher.Write([]byte{byte(a.Status)})
	hasher.Write(a.PublicKey[:])
	var result [32]byte
	copy(result[:], hasher.Sum(nil))
	return result
}

// NewAccount создает новый аккаунт
func NewAccount(uid uint64, status AccountStatus) *Account {
	acc := &Account{
		UID:       uid,
		Status:    status,
		EmailHash: uid ^ 0xCAFEBABE,
	}
	binary.BigEndian.PutUint64(acc.key[:], uid)
	rand.Read(acc.PublicKey[:])
	return acc
}
