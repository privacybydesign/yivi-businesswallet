// Package trust holds pinned trust anchors for offline verification. The
// preferred source is the EU Trusted Lists (package eutl); this package is
// the fallback when the lists cannot be fetched.
//
// DUO signs diploma extracts with a PKIoverheid "Organisatie Services"
// certificate. PKIoverheid is the Dutch government PKI; its root is NOT in the
// default browser/OS trust stores, so it must be pinned explicitly. The root
// below was taken from the certificate chain embedded in a DUO PDF and its
// SHA-256 fingerprint matches the value published by Logius:
//
//	3C:4F:B0:B9:5A:B8:B3:00:32:F4:32:B8:6F:53:5F:E1:72:C1:85:D0:FD:39:86:58:37:CF:36:18:7F:A6:F4:28
//
// The root expires 13 Nov 2028; the G4 root ("Staat der Nederlanden Root CA -
// G4") should be added here once DUO starts signing under it.
package trust

import (
	"crypto/x509"
	_ "embed"
	"errors"
)

//go:embed staat-der-nederlanden-root-ca-g3.pem
var rootG3PEM []byte

// DUO's PDFs are timestamped by certSIGN, a Romanian qualified trust service
// provider listed on the EU Trusted List. Its root is pinned separately: the
// signer trust (PKIoverheid) and the timestamp trust (EU qualified TSA) are
// different questions. A production verifier would load these anchors from
// the EU List of Trusted Lists (LOTL) instead of embedding them.
//
// SHA-256: B6:A8:0A:71:14:6B:C1:5F:8A:9C:CA:6B:57:A7:93:B0:C5:02:DD:C3:34:D6:24:82:66:9C:34:58:54:04:0B:E6
//
//go:embed certsign-root-ca-sign-2023-rsa.pem
var certSIGNRootPEM []byte

// Roots returns the pinned root pool.
func Roots() (*x509.CertPool, error) {
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(rootG3PEM) {
		return nil, errors.New("trust: embedded root PEM is invalid")
	}
	return pool, nil
}

// TSARoots returns the pinned roots accepted for RFC 3161 timestamp tokens.
// PKIoverheid roots are included as well, so a future switch to a Dutch
// qualified TSA keeps working.
func TSARoots() (*x509.CertPool, error) {
	pool, err := Roots()
	if err != nil {
		return nil, err
	}
	if !pool.AppendCertsFromPEM(certSIGNRootPEM) {
		return nil, errors.New("trust: embedded certSIGN root PEM is invalid")
	}
	return pool, nil
}

// Pinned is a verify.TrustSource backed by the embedded roots.
type Pinned struct{ signer, tsa *x509.CertPool }

// NewPinned builds the pinned trust source.
func NewPinned() (*Pinned, error) {
	signer, err := Roots()
	if err != nil {
		return nil, err
	}
	tsa, err := TSARoots()
	if err != nil {
		return nil, err
	}
	return &Pinned{signer: signer, tsa: tsa}, nil
}

func (p *Pinned) SignerRoots() *x509.CertPool { return p.signer }
func (p *Pinned) TSARoots() *x509.CertPool    { return p.tsa }
func (p *Pinned) Describe(anchor *x509.Certificate) string {
	return "pinned root " + anchor.Subject.CommonName
}

// Issuer describes who is allowed to sign a diploma.
type Issuer struct {
	// Name is a human readable label.
	Name string
	// OrganizationIdentifier is the ETSI EN 319 412-1 organisation identifier
	// (X.520 attribute 2.5.4.97) that must appear in the signer's subject.
	// For DUO this is the Dutch chamber of commerce (KvK) number 50973029.
	OrganizationIdentifier string
	// Organization must equal the subject O attribute.
	Organization string
}

// DUO is the Dutch Dienst Uitvoering Onderwijs, the government agency that
// keeps the national diploma register and signs the PDF extracts.
var DUO = Issuer{
	Name:                   "Dienst Uitvoering Onderwijs (DUO)",
	OrganizationIdentifier: "NTRNL-50973029",
	Organization:           "Dienst Uitvoering Onderwijs (DUO)",
}
