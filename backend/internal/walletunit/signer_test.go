package walletunit

import (
	"bytes"
	"compress/zlib"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"encoding/base64"
	"encoding/pem"
	"io"
	"testing"
)

func TestLoadSignerRoundTrip(t *testing.T) {
	t.Parallel()
	eph, err := NewEphemeralSigner("test")
	if err != nil {
		t.Fatal(err)
	}
	der, err := x509.MarshalPKCS8PrivateKey(eph.key)
	if err != nil {
		t.Fatal(err)
	}
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der})
	if _, err := LoadSigner(eph.RootPEM(), keyPEM); err != nil {
		t.Fatalf("load: %v", err)
	}

	other, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	otherDER, err := x509.MarshalECPrivateKey(other)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := LoadSigner(eph.RootPEM(), pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: otherDER})); err == nil {
		t.Error("a key that does not match the leaf was accepted")
	}
	if _, err := LoadSigner([]byte("nothing"), keyPEM); err == nil {
		t.Error("a chain without certificates was accepted")
	}
}

func TestEncodeStatusList(t *testing.T) {
	t.Parallel()
	lst, err := encodeStatusList(map[int64]byte{0: statusInvalid, 5: statusSuspended, statusListSize - 1: statusInvalid})
	if err != nil {
		t.Fatal(err)
	}
	compressed, err := base64.RawURLEncoding.DecodeString(lst)
	if err != nil {
		t.Fatal(err)
	}
	zr, err := zlib.NewReader(bytes.NewReader(compressed))
	if err != nil {
		t.Fatal(err)
	}
	bits, err := io.ReadAll(zr)
	if err != nil {
		t.Fatal(err)
	}
	if len(bits) != statusListSize*statusBits/8 {
		t.Fatalf("len = %d", len(bits))
	}
	// Two bits per entry, lowest bits first: entry 0 is bits 0-1 of byte 0, entry 5
	// bits 2-3 of byte 1, the last entry the top two bits of the last byte.
	if bits[0] != 0b01 || bits[1] != 0b1000 || bits[len(bits)-1] != 0b01000000 {
		t.Errorf("bytes = %08b %08b … %08b", bits[0], bits[1], bits[len(bits)-1])
	}
	if _, err := encodeStatusList(map[int64]byte{statusListSize: statusInvalid}); err == nil {
		t.Error("an index outside the list was accepted")
	}
}

func TestECJWKRejectsOtherCurves(t *testing.T) {
	t.Parallel()
	k, err := ecdsa.GenerateKey(elliptic.P384(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ecJWK(&k.PublicKey); err == nil {
		t.Error("a P-384 key was accepted")
	}
}
