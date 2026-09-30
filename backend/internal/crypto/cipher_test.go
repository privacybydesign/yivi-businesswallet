package crypto

import (
	"bytes"
	"strings"
	"testing"
)

func TestDeriveSecretIsStablePerPurpose(t *testing.T) {
	key := strings.Repeat("ab", keyBytes)
	a, _ := NewCipher(key)
	b, _ := NewCipher(key)
	first, err := a.DeriveSecret("one")
	if err != nil || len(first) != keyBytes {
		t.Fatalf("DeriveSecret = %x, %v", first, err)
	}
	again, _ := b.DeriveSecret("one")
	other, _ := a.DeriveSecret("two")
	if !bytes.Equal(first, again) || bytes.Equal(first, other) {
		t.Errorf("same key and purpose must match, another purpose must not: %x %x %x", first, again, other)
	}
}
