package postguard

import (
	"bytes"
	"testing"
)

// a valid hex-encoded 32-byte key.
const testKey = "000102030405060708090a0b0c0d0e0f101112131415161718191a1b1c1d1e1f"

func TestNewCipherEmptyKeyIsNotConfigured(t *testing.T) {
	c, err := NewCipher("")
	if err != nil {
		t.Fatalf("empty key: unexpected error: %v", err)
	}
	if c != nil {
		t.Fatalf("empty key: expected nil cipher, got %v", c)
	}
}

func TestNewCipherRejectsMalformedKey(t *testing.T) {
	for _, k := range []string{"zzzz", "abcd" /* too short */} {
		if _, err := NewCipher(k); err == nil {
			t.Errorf("key %q: expected error, got nil", k)
		}
	}
}

func TestCipherRoundTrip(t *testing.T) {
	c, err := NewCipher(testKey)
	if err != nil {
		t.Fatalf("new cipher: %v", err)
	}
	plaintext := []byte("PG-super-secret-business-key")

	blob, err := c.Encrypt(plaintext)
	if err != nil {
		t.Fatalf("encrypt: %v", err)
	}
	if bytes.Contains(blob, plaintext) {
		t.Fatal("ciphertext contains plaintext")
	}

	got, err := c.Decrypt(blob)
	if err != nil {
		t.Fatalf("decrypt: %v", err)
	}
	if !bytes.Equal(got, plaintext) {
		t.Fatalf("round trip mismatch: got %q want %q", got, plaintext)
	}
}

func TestEncryptUsesFreshNonce(t *testing.T) {
	c, _ := NewCipher(testKey)
	a, _ := c.Encrypt([]byte("same"))
	b, _ := c.Encrypt([]byte("same"))
	if bytes.Equal(a, b) {
		t.Fatal("two encryptions of the same plaintext produced identical ciphertext (nonce reuse)")
	}
}

func TestEnvelopeRoundTrip(t *testing.T) {
	master, err := NewCipher(testKey)
	if err != nil {
		t.Fatalf("master cipher: %v", err)
	}
	// Owner sets an arbitrary secret -> DEK -> wrapped by the master key.
	dek := deriveDEK("owner's chosen passphrase")
	wrapped, err := master.Encrypt(dek)
	if err != nil {
		t.Fatalf("wrap dek: %v", err)
	}

	// Later: unwrap the DEK and use it to seal/open the API key.
	unwrapped, err := master.Decrypt(wrapped)
	if err != nil {
		t.Fatalf("unwrap dek: %v", err)
	}
	dekCipher, err := newCipherFromKey(unwrapped)
	if err != nil {
		t.Fatalf("dek cipher: %v", err)
	}
	sealed, err := dekCipher.Encrypt([]byte("PG-the-api-key"))
	if err != nil {
		t.Fatalf("seal api key: %v", err)
	}
	got, err := dekCipher.Decrypt(sealed)
	if err != nil {
		t.Fatalf("open api key: %v", err)
	}
	if string(got) != "PG-the-api-key" {
		t.Fatalf("round trip mismatch: %q", got)
	}
}

func TestDeriveDEKIsDeterministicAndSized(t *testing.T) {
	a, b := deriveDEK("same"), deriveDEK("same")
	if !bytes.Equal(a, b) {
		t.Fatal("deriveDEK not deterministic")
	}
	if len(a) != keyBytes {
		t.Fatalf("DEK length = %d, want %d", len(a), keyBytes)
	}
	if bytes.Equal(a, deriveDEK("different")) {
		t.Fatal("different secrets produced the same DEK")
	}
}

func TestDecryptRejectsTampered(t *testing.T) {
	c, _ := NewCipher(testKey)
	blob, _ := c.Encrypt([]byte("payload"))
	blob[len(blob)-1] ^= 0xFF // flip a bit in the tag
	if _, err := c.Decrypt(blob); err == nil {
		t.Fatal("expected decrypt of tampered ciphertext to fail")
	}
}
