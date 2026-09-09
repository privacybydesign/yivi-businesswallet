// Package devverifier is the relying-party side of OpenID4VP for local
// development and tests: it mints an x509_san_dns identity, signs Authorization
// Request Objects the business wallet's inbound flow fetches, decrypts and
// verifies the Authorization Response the wallet posts back, and reports what was
// disclosed. cmd/devverifier wraps it in a small HTTP server for the Compose dev
// stack; the wallet's own tests use the same helpers to drive its verifying
// validator against a chain they generated. Nothing here is for production: the
// hosted Yivi verifier is the real counterpart.
package devverifier

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
	"os"
	"time"
)

const (
	caLifetime   = 10 * 365 * 24 * time.Hour
	leafLifetime = 2 * 365 * 24 * time.Hour
	serialBits   = 128
)

// Identity is a relying party's signing identity: its leaf certificate and key
// and the chain up to the root, leaf first, plus the root PEM a wallet needs in
// its verifier trust to accept requests signed with it.
type Identity struct {
	Key   *ecdsa.PrivateKey
	Chain []*x509.Certificate
	// DNSName is the leaf's SAN and therefore the x509_san_dns client_id host.
	DNSName string
}

// ClientID is the OpenID4VP client identifier this identity signs as.
func (id Identity) ClientID() string { return "x509_san_dns:" + id.DNSName }

// RootPEM is the chain's root certificate in PEM, ready for
// OPENID4VP_VERIFIER_TRUST_CHAIN.
func (id Identity) RootPEM() []byte {
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: id.Chain[len(id.Chain)-1].Raw})
}

// ChainPEM is the whole chain in PEM, leaf first.
func (id Identity) ChainPEM() []byte {
	var out []byte
	for _, c := range id.Chain {
		out = append(out, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: c.Raw})...)
	}
	return out
}

// KeyPEM is the leaf private key as PKCS#8 PEM.
func (id Identity) KeyPEM() ([]byte, error) {
	der, err := x509.MarshalPKCS8PrivateKey(id.Key)
	if err != nil {
		return nil, err
	}
	return pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der}), nil
}

// x5c is the chain in the JWS x5c header form: base64 (standard, not URL) DER,
// leaf first.
func (id Identity) x5c() []string {
	out := make([]string, len(id.Chain))
	for i, c := range id.Chain {
		out[i] = base64.StdEncoding.EncodeToString(c.Raw)
	}
	return out
}

// NewIdentity mints a fresh CA and a relying-party leaf for dnsName under it.
// The leaf carries what irmago's relying-party validation demands: a DNS SAN
// equal to the client_id host, the digitalSignature key usage, and the
// clientAuth extended key usage the Yivi relying-party certificates carry.
func NewIdentity(dnsName string) (Identity, error) {
	caKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return Identity{}, err
	}
	caTemplate, err := template(pkix.Name{CommonName: "Dev Verifier CA", Organization: []string{"Yivi Business Wallet dev"}}, caLifetime)
	if err != nil {
		return Identity{}, err
	}
	caTemplate.IsCA = true
	caTemplate.BasicConstraintsValid = true
	caTemplate.KeyUsage = x509.KeyUsageCertSign | x509.KeyUsageCRLSign
	caDER, err := x509.CreateCertificate(rand.Reader, caTemplate, caTemplate, &caKey.PublicKey, caKey)
	if err != nil {
		return Identity{}, err
	}
	ca, err := x509.ParseCertificate(caDER)
	if err != nil {
		return Identity{}, err
	}

	leafKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return Identity{}, err
	}
	leafTemplate, err := template(pkix.Name{CommonName: dnsName, Organization: []string{"Dev Verifier"}}, leafLifetime)
	if err != nil {
		return Identity{}, err
	}
	leafTemplate.DNSNames = []string{dnsName}
	leafTemplate.KeyUsage = x509.KeyUsageDigitalSignature
	leafTemplate.ExtKeyUsage = []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth}
	leafDER, err := x509.CreateCertificate(rand.Reader, leafTemplate, ca, &leafKey.PublicKey, caKey)
	if err != nil {
		return Identity{}, err
	}
	leaf, err := x509.ParseCertificate(leafDER)
	if err != nil {
		return Identity{}, err
	}
	return Identity{Key: leafKey, Chain: []*x509.Certificate{leaf, ca}, DNSName: dnsName}, nil
}

func template(subject pkix.Name, lifetime time.Duration) (*x509.Certificate, error) {
	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), serialBits))
	if err != nil {
		return nil, err
	}
	now := time.Now()
	return &x509.Certificate{
		SerialNumber: serial,
		Subject:      subject,
		NotBefore:    now.Add(-time.Minute),
		NotAfter:     now.Add(lifetime),
	}, nil
}

// LoadIdentity reads a chain PEM (leaf first) and a PKCS#8 key PEM from disk —
// the checked-in dev certificates the Compose stack runs with, so the wallet
// can be configured with the root once.
func LoadIdentity(chainPath, keyPath string) (Identity, error) {
	chainPEM, err := os.ReadFile(chainPath)
	if err != nil {
		return Identity{}, err
	}
	keyPEM, err := os.ReadFile(keyPath)
	if err != nil {
		return Identity{}, err
	}
	var chain []*x509.Certificate
	rest := chainPEM
	for {
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
			return Identity{}, err
		}
		chain = append(chain, cert)
	}
	if len(chain) == 0 {
		return Identity{}, errors.New("devverifier: chain PEM holds no certificate")
	}
	block, _ := pem.Decode(keyPEM)
	if block == nil {
		return Identity{}, errors.New("devverifier: key PEM holds no block")
	}
	keyAny, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		return Identity{}, err
	}
	key, ok := keyAny.(*ecdsa.PrivateKey)
	if !ok {
		return Identity{}, fmt.Errorf("devverifier: key is %T, want ECDSA", keyAny)
	}
	if len(chain[0].DNSNames) == 0 {
		return Identity{}, errors.New("devverifier: leaf certificate has no DNS SAN")
	}
	return Identity{Key: key, Chain: chain, DNSName: chain[0].DNSNames[0]}, nil
}
