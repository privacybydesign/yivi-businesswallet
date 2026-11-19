package yivicsc

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/asn1"
	"fmt"
	"math/big"
	"strings"
	"time"
)

const (
	// authorityCommonName and authorityOrganization name the deployment's own
	// certificate authority.
	authorityCommonName   = "Yivi Business Wallet Signing CA"
	authorityOrganization = "Yivi Business Wallet"
	// serialBits is the size of a certificate serial number (RFC 5280 allows
	// up to 20 octets; CA/B asks for at least 64 random bits).
	serialBits = 128
	// subjectKeyPurpose derives the key subject keys are made with.
	subjectKeyPurpose = "yivi-csc-subject-key"
	// clientSecretPurpose derives the built-in OAuth client's secret.
	clientSecretPurpose = "yivi-csc-client-secret"
)

// oidGivenName and oidSurname are the X.520 attributes a certificate names its
// subject with besides the common name.
var (
	oidGivenName = asn1.ObjectIdentifier{2, 5, 4, 42}
	oidSurname   = asn1.ObjectIdentifier{2, 5, 4, 4}
)

// authority is the deployment's certificate authority: the key that signs every
// signing certificate, and its self-signed certificate.
type authority struct {
	cert *x509.Certificate
	key  *ecdsa.PrivateKey
}

// newAuthority makes a certificate authority: a P-256 key and its self-signed
// certificate, which may sign end-entity certificates and nothing below them.
func newAuthority(now time.Time) (*authority, error) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, fmt.Errorf("yivicsc: generate authority key: %w", err)
	}
	serial, err := newSerial()
	if err != nil {
		return nil, err
	}
	tmpl := &x509.Certificate{
		SerialNumber:          serial,
		Subject:               pkix.Name{CommonName: authorityCommonName, Organization: []string{authorityOrganization}},
		NotBefore:             now.Add(-clockSkew),
		NotAfter:              now.Add(authorityValidity),
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageCRLSign,
		BasicConstraintsValid: true,
		IsCA:                  true,
		MaxPathLenZero:        true,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		return nil, fmt.Errorf("yivicsc: self-sign authority: %w", err)
	}
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		return nil, fmt.Errorf("yivicsc: parse authority certificate: %w", err)
	}
	return &authority{cert: cert, key: key}, nil
}

// issue makes a signing key for id and a certificate over it: the person's
// name as subject, good for signing documents and nothing else.
func (a *authority) issue(id Identity, now time.Time) (*ecdsa.PrivateKey, *x509.Certificate, error) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, nil, fmt.Errorf("yivicsc: generate signing key: %w", err)
	}
	serial, err := newSerial()
	if err != nil {
		return nil, nil, err
	}
	tmpl := &x509.Certificate{
		SerialNumber: serial,
		Subject: pkix.Name{
			CommonName: id.CommonName(),
			ExtraNames: []pkix.AttributeTypeAndValue{
				{Type: oidGivenName, Value: id.GivenNames},
				{Type: oidSurname, Value: id.FamilyName},
			},
		},
		NotBefore: now.Add(-clockSkew),
		NotAfter:  now.Add(CredentialValidity),
		// ContentCommitment is non-repudiation: what a document signature is.
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageContentCommitment,
		BasicConstraintsValid: true,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, a.cert, &key.PublicKey, a.key)
	if err != nil {
		return nil, nil, fmt.Errorf("yivicsc: issue signing certificate: %w", err)
	}
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		return nil, nil, fmt.Errorf("yivicsc: parse signing certificate: %w", err)
	}
	return key, cert, nil
}

func newSerial() (*big.Int, error) {
	limit := new(big.Int).Lsh(big.NewInt(1), serialBits)
	serial, err := rand.Int(rand.Reader, limit)
	if err != nil {
		return nil, fmt.Errorf("yivicsc: certificate serial: %w", err)
	}
	return serial, nil
}

// subjectKey names a person without holding who they are: a keyed hash of the
// family their name came from, the name and the date of birth. Keyed, because
// the name is in the certificate beside it and a plain hash would give the
// date of birth to anyone who tried every day of a century.
func subjectKey(secret []byte, id Identity) []byte {
	mac := hmac.New(sha256.New, secret)
	for _, part := range []string{id.Source, fold(id.GivenNames), fold(id.FamilyName), id.DateOfBirth} {
		mac.Write([]byte(part))
		mac.Write([]byte{0})
	}
	return mac.Sum(nil)
}

// fold makes a name compare however a credential cased it.
func fold(s string) string {
	return strings.ToLower(clean(s))
}
