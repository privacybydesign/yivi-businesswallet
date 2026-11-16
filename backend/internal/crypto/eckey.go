package crypto

import (
	"crypto/ecdsa"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"fmt"
)

// ParseECPrivateKeyPEM reads an ECDSA private key from PEM, as SEC 1 or PKCS #8.
func ParseECPrivateKeyPEM(keyPEM []byte) (*ecdsa.PrivateKey, error) {
	block, _ := pem.Decode(keyPEM)
	if block == nil {
		return nil, errors.New("key PEM holds no block")
	}
	if key, err := x509.ParseECPrivateKey(block.Bytes); err == nil {
		return key, nil
	}
	keyAny, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		return nil, err
	}
	key, ok := keyAny.(*ecdsa.PrivateKey)
	if !ok {
		return nil, fmt.Errorf("key is %T, want ECDSA", keyAny)
	}
	return key, nil
}
