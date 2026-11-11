package crypto

import (
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"encoding/pem"
	"testing"
)

func TestParseECPrivateKeyPEM(t *testing.T) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	sec1, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	pkcs8, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	for name, block := range map[string]*pem.Block{
		"SEC 1":   {Type: "EC PRIVATE KEY", Bytes: sec1},
		"PKCS #8": {Type: "PRIVATE KEY", Bytes: pkcs8},
	} {
		got, err := ParseECPrivateKeyPEM(pem.EncodeToMemory(block))
		if err != nil || !got.Equal(key) {
			t.Errorf("%s: got %v, %v", name, got, err)
		}
	}

	_, edKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	edDER, err := x509.MarshalPKCS8PrivateKey(edKey)
	if err != nil {
		t.Fatal(err)
	}
	for name, in := range map[string][]byte{
		"no PEM":    []byte("nothing"),
		"garbage":   pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: []byte("x")}),
		"not ECDSA": pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: edDER}),
	} {
		if _, err := ParseECPrivateKeyPEM(in); err == nil {
			t.Errorf("%s was accepted", name)
		}
	}
}
