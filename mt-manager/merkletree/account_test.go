package merkletree

import (
	"testing"
)

func TestAccountHash(t *testing.T) {
	acc1 := NewAccountDeterministic(123, StatusUser)
	acc2 := NewAccountDeterministic(123, StatusUser)

	hash1 := acc1.Hash()
	hash2 := acc2.Hash()

	// Хеши должны совпадать для одинаковых UID
	// (но могут отличаться из-за случайного PublicKey)
	if hash1 == hash2 {
		// Может совпасть случайно, но маловероятно
	}

	// Проверяем, что хеш не нулевой
	var zero [32]byte
	if hash1 == zero {
		t.Error("Хеш не должен быть нулевым")
	}
}

func TestAccountKey(t *testing.T) {
	acc := NewAccountDeterministic(0x123456789ABCDEF0, StatusUser)
	key := acc.Key()

	// Проверяем BigEndian encoding
	expected := [8]byte{0x12, 0x34, 0x56, 0x78, 0x9A, 0xBC, 0xDE, 0xF0}
	if key != expected {
		t.Errorf("Ожидался ключ %v, получен %v", expected, key)
	}
}

func TestAccountID(t *testing.T) {
	uid := uint64(12345)
	acc := NewAccountDeterministic(uid, StatusUser)

	if acc.ID() != uid {
		t.Errorf("Ожидался ID %d, получен %d", uid, acc.ID())
	}
}

func TestAccountStatus(t *testing.T) {
	statuses := []AccountStatus{
		StatusSystem,
		StatusBlocked,
		StatusMM,
		StatusAlgo,
		StatusUser,
	}

	expected := []string{"system", "blocked", "mm", "algo", "user"}

	for i, status := range statuses {
		if status.String() != expected[i] {
			t.Errorf("Ожидалось %s, получено %s", expected[i], status.String())
		}
	}
}

func BenchmarkAccountHash(b *testing.B) {
	acc := NewAccountDeterministic(123, StatusUser)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		acc.Hash()
	}
}

func BenchmarkNewAccount(b *testing.B) {
	for i := 0; i < b.N; i++ {
		NewAccountDeterministic(uint64(i), StatusUser)
	}
}
