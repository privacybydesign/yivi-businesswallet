// Package relyingparty is the verifier side of OpenID4VP: it signs
// Authorization Request Objects (JAR) and verifies the vp_token a wallet posts
// back. Two callers share it: internal/openid4vprequester, where an
// organization asks another organization's business wallet for credentials, and
// internal/devverifier, the local relying party for development and tests.
package relyingparty

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"encoding/pem"
	"errors"
	"fmt"
	"math/big"
	"time"

	"github.com/privacybydesign/irmago/eudi/openid4vp"
)

const (
	caLifetime = 10 * 365 * 24 * time.Hour
	serialBits = 128
	// clockSkew backdates NotBefore so a receiver whose clock runs slightly
	// behind still accepts a certificate minted a moment ago.
	clockSkew = time.Minute
)

// Signer is what a Request Object is signed with: the relying party's key, its
// certificate chain (leaf first) for the x5c header, and the client_id that
// chain authenticates.
type Signer struct {
	Key      *ecdsa.PrivateKey
	Chain    []*x509.Certificate
	ClientID string
}

// x5c is the chain in the JWS x5c header form: base64 (standard, not URL) DER,
// leaf first.
func (s Signer) x5c() []string {
	out := make([]string, len(s.Chain))
	for i, c := range s.Chain {
		out[i] = base64.StdEncoding.EncodeToString(c.Raw)
	}
	return out
}

// X509HashClientID is the x509_hash client identifier for leaf (OpenID4VP 1.0
// §5.9.3): the base64url SHA-256 of its DER encoding. Unlike x509_san_dns it
// binds no DNS name, so a relying party can have one without owning a host.
func X509HashClientID(leaf *x509.Certificate) string {
	sum := sha256.Sum256(leaf.Raw)
	return string(openid4vp.ClientIdentifierPrefix_X509Hash) + base64.RawURLEncoding.EncodeToString(sum[:])
}

// CA issues organization relying-party certificates. A receiving wallet that
// trusts its root trusts what the leaves certify: the organization's name and
// the QERDS address the request was sent from.
type CA struct {
	cert *x509.Certificate
	key  *ecdsa.PrivateKey
}

// NewCA mints a self-signed CA. For a deployment that has not configured one:
// its root changes on every start, so no other deployment can trust it.
func NewCA(commonName string) (*CA, error) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, err
	}
	tmpl, err := certTemplate(pkix.Name{CommonName: commonName}, time.Now(), caLifetime)
	if err != nil {
		return nil, err
	}
	tmpl.IsCA = true
	tmpl.BasicConstraintsValid = true
	tmpl.KeyUsage = x509.KeyUsageCertSign | x509.KeyUsageCRLSign
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		return nil, err
	}
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		return nil, err
	}
	return &CA{cert: cert, key: key}, nil
}

// LoadCA reads a CA certificate and its ECDSA key (PKCS#8 or SEC 1) from PEM.
func LoadCA(certPEM, keyPEM []byte) (*CA, error) {
	block, _ := pem.Decode(certPEM)
	if block == nil || block.Type != "CERTIFICATE" {
		return nil, errors.New("relyingparty: CA certificate PEM holds no CERTIFICATE block")
	}
	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("relyingparty: CA certificate: %w", err)
	}
	if !cert.IsCA {
		return nil, errors.New("relyingparty: CA certificate is not a CA")
	}
	key, err := parseECKey(keyPEM)
	if err != nil {
		return nil, err
	}
	if !key.PublicKey.Equal(cert.PublicKey) {
		return nil, errors.New("relyingparty: CA key does not match the CA certificate")
	}
	return &CA{cert: cert, key: key}, nil
}

func parseECKey(keyPEM []byte) (*ecdsa.PrivateKey, error) {
	block, _ := pem.Decode(keyPEM)
	if block == nil {
		return nil, errors.New("relyingparty: CA key PEM holds no block")
	}
	if key, err := x509.ParseECPrivateKey(block.Bytes); err == nil {
		return key, nil
	}
	keyAny, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("relyingparty: CA key: %w", err)
	}
	key, ok := keyAny.(*ecdsa.PrivateKey)
	if !ok {
		return nil, fmt.Errorf("relyingparty: CA key is %T, want ECDSA", keyAny)
	}
	return key, nil
}

// RootPEM is the CA certificate in PEM, what a receiving deployment adds to
// OPENID4VP_VERIFIER_TRUST_CHAIN.
func (ca *CA) RootPEM() []byte {
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: ca.cert.Raw})
}

// IssueOrganization mints a fresh relying-party identity for one request from
// organization orgName, sent from the QERDS address address: a leaf whose
// subject names the organization and whose only SAN is that address (rfc822Name)
// — the binding a receiver checks against the QERDS originalSender, so a request
// cannot be relayed from another sender. The leaf carries the key usages
// irmago's relying-party validation expects. The key signs one Request Object
// and is then dropped by the caller; nothing needs to outlive it.
func (ca *CA) IssueOrganization(orgName, address string, lifetime time.Duration) (Signer, error) {
	if orgName == "" || address == "" {
		return Signer{}, errors.New("relyingparty: organization name and address are required")
	}
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return Signer{}, err
	}
	tmpl, err := certTemplate(pkix.Name{CommonName: orgName, Organization: []string{orgName}}, time.Now(), lifetime)
	if err != nil {
		return Signer{}, err
	}
	tmpl.EmailAddresses = []string{address}
	tmpl.KeyUsage = x509.KeyUsageDigitalSignature
	tmpl.ExtKeyUsage = []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, ca.cert, &key.PublicKey, ca.key)
	if err != nil {
		return Signer{}, fmt.Errorf("relyingparty: issue organization certificate: %w", err)
	}
	leaf, err := x509.ParseCertificate(der)
	if err != nil {
		return Signer{}, err
	}
	return Signer{Key: key, Chain: []*x509.Certificate{leaf, ca.cert}, ClientID: X509HashClientID(leaf)}, nil
}

func certTemplate(subject pkix.Name, now time.Time, lifetime time.Duration) (*x509.Certificate, error) {
	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), serialBits))
	if err != nil {
		return nil, err
	}
	return &x509.Certificate{
		SerialNumber: serial,
		Subject:      subject,
		NotBefore:    now.Add(-clockSkew),
		NotAfter:     now.Add(lifetime),
	}, nil
}
