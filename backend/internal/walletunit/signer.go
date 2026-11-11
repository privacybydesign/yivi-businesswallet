package walletunit

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"encoding/pem"
	"errors"
	"fmt"
	"math/big"
	"time"

	"github.com/golang-jwt/jwt/v5"

	"github.com/privacybydesign/yivi-businesswallet/backend/internal/crypto"
)

const (
	ephemeralLifetime = 365 * 24 * time.Hour
	serialBits        = 128
	clockSkew         = time.Minute
)

// Signer signs as the Business Wallet Provider: its key and certificate chain,
// leaf first, for the x5c header a relying party checks against the Wallet
// Provider Trust List.
type Signer struct {
	key   *ecdsa.PrivateKey
	chain []*x509.Certificate
}

// LoadSigner reads the provider certificate chain (leaf first) and its P-256 key
// (PKCS#8 or SEC 1) from PEM.
func LoadSigner(chainPEM, keyPEM []byte) (*Signer, error) {
	var chain []*x509.Certificate
	for rest := chainPEM; ; {
		var block *pem.Block
		block, rest = pem.Decode(rest)
		if block == nil {
			break
		}
		if block.Type != "CERTIFICATE" {
			continue
		}
		cert, err := x509.ParseCertificate(block.Bytes)
		if err != nil {
			return nil, fmt.Errorf("walletunit: provider certificate: %w", err)
		}
		chain = append(chain, cert)
	}
	if len(chain) == 0 {
		return nil, errors.New("walletunit: provider certificate PEM holds no CERTIFICATE block")
	}
	key, err := crypto.ParseECPrivateKeyPEM(keyPEM)
	if err != nil {
		return nil, fmt.Errorf("walletunit: provider key: %w", err)
	}
	if key.Curve != elliptic.P256() {
		return nil, errors.New("walletunit: provider key must be P-256 for ES256")
	}
	if !key.PublicKey.Equal(chain[0].PublicKey) {
		return nil, errors.New("walletunit: provider key does not match the leaf certificate")
	}
	if chain[0].KeyUsage&x509.KeyUsageDigitalSignature == 0 {
		return nil, errors.New("walletunit: provider leaf certificate lacks the digitalSignature key usage")
	}
	return &Signer{key: key, chain: chain}, nil
}

// NewEphemeralSigner mints a self-signed provider certificate. Only for a
// deployment that has not configured one: nobody else trusts it, and it changes
// on every start.
func NewEphemeralSigner(commonName string) (*Signer, error) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, err
	}
	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), serialBits))
	if err != nil {
		return nil, err
	}
	now := time.Now()
	tmpl := &x509.Certificate{
		SerialNumber:          serial,
		Subject:               pkix.Name{CommonName: commonName},
		NotBefore:             now.Add(-clockSkew),
		NotAfter:              now.Add(ephemeralLifetime),
		IsCA:                  true,
		BasicConstraintsValid: true,
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageCertSign,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		return nil, err
	}
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		return nil, err
	}
	return &Signer{key: key, chain: []*x509.Certificate{cert}}, nil
}

// RootPEM is the last certificate of the chain in PEM: what a relying party adds
// to its trust anchors while the provider is not on a trusted list.
func (s *Signer) RootPEM() []byte {
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: s.chain[len(s.chain)-1].Raw})
}

// Leaf is the certificate the provider signs under.
func (s *Signer) Leaf() *x509.Certificate { return s.chain[0] }

func (s *Signer) sign(typ string, claims jwt.MapClaims) (string, error) {
	x5c := make([]string, len(s.chain))
	for i, c := range s.chain {
		x5c[i] = base64.StdEncoding.EncodeToString(c.Raw)
	}
	token := jwt.NewWithClaims(jwt.SigningMethodES256, claims)
	token.Header["typ"] = typ
	token.Header["x5c"] = x5c
	signed, err := token.SignedString(s.key)
	if err != nil {
		return "", fmt.Errorf("walletunit: sign %s: %w", typ, err)
	}
	return signed, nil
}
