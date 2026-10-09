package verify

import (
	"crypto"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/asn1"
	"math/big"
	"testing"
	"time"

	"github.com/digitorus/pkcs7"
	"github.com/digitorus/timestamp"
)

// issue signs tmpl with parentKey as parent (self-signed when parent is nil)
// over a fresh key.
func issue(t *testing.T, tmpl, parent *x509.Certificate, parentKey *ecdsa.PrivateKey) (*x509.Certificate, *ecdsa.PrivateKey) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("key: %v", err)
	}
	serial, err := rand.Int(rand.Reader, big.NewInt(1<<62))
	if err != nil {
		t.Fatalf("serial: %v", err)
	}
	tmpl.SerialNumber = serial
	tmpl.NotBefore = time.Now().Add(-time.Hour)
	tmpl.NotAfter = time.Now().Add(time.Hour)
	tmpl.BasicConstraintsValid = true
	if parent == nil {
		parent, parentKey = tmpl, key
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, parent, &key.PublicKey, parentKey)
	if err != nil {
		t.Fatalf("certificate %s: %v", tmpl.Subject.CommonName, err)
	}
	c, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatalf("parse %s: %v", tmpl.Subject.CommonName, err)
	}
	return c, key
}

// testCA issues a CA certificate for cn, signed by parent (self-signed when
// parent is nil).
func testCA(t *testing.T, cn string, parent *x509.Certificate, parentKey *ecdsa.PrivateKey) (*x509.Certificate, *ecdsa.PrivateKey) {
	t.Helper()
	return issue(t, &x509.Certificate{
		Subject:  pkix.Name{CommonName: cn},
		IsCA:     true,
		KeyUsage: x509.KeyUsageCertSign | x509.KeyUsageCRLSign | x509.KeyUsageDigitalSignature,
	}, parent, parentKey)
}

// testTSA issues a timestamping certificate for cn under parent.
func testTSA(t *testing.T, cn string, parent *x509.Certificate, parentKey *ecdsa.PrivateKey) (*x509.Certificate, *ecdsa.PrivateKey) {
	t.Helper()
	return issue(t, &x509.Certificate{
		Subject:     pkix.Name{CommonName: cn},
		KeyUsage:    x509.KeyUsageDigitalSignature,
		ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageTimeStamping},
	}, parent, parentKey)
}

// timestampToken is an RFC 3161 token by tsa over digest.
func timestampToken(t *testing.T, digest []byte, tsa *x509.Certificate, key *ecdsa.PrivateKey) []byte {
	t.Helper()
	ts := timestamp.Timestamp{
		HashAlgorithm: crypto.SHA256, HashedMessage: digest, Time: time.Now(),
		Policy: asn1.ObjectIdentifier{1, 2, 3, 4}, AddTSACertificate: true,
	}
	resp, err := ts.CreateResponseWithOpts(tsa, key, crypto.SHA256)
	if err != nil {
		t.Fatalf("create token: %v", err)
	}
	parsed, err := timestamp.ParseResponse(resp)
	if err != nil {
		t.Fatalf("parse token: %v", err)
	}
	return parsed.RawToken
}

// The TSA that counts is the token's signer, never another certificate the
// token carries: the token sits outside the PDF's signed bytes, so anyone can
// add a trusted TSA's certificate to it next to their own signer.
func TestTSASignerIsTokenSigner(t *testing.T) {
	trustedRoot, trustedKey := testCA(t, "trusted TSA root", nil, nil)
	trustedTSA, _ := testTSA(t, "trusted TSA", trustedRoot, trustedKey)
	ownRoot, ownRootKey := testCA(t, "own root", nil, nil)
	ownSigner, ownKey := testTSA(t, "own signer", ownRoot, ownRootKey)

	// A genuine token by the untrusted signer, re-signed with the trusted
	// TSA's certificate placed first in its list.
	digest := sha256.Sum256([]byte("signature value"))
	inner, err := pkcs7.Parse(timestampToken(t, digest[:], ownSigner, ownKey))
	if err != nil {
		t.Fatalf("parse token CMS: %v", err)
	}
	sd, err := pkcs7.NewSignedData(inner.Content)
	if err != nil {
		t.Fatalf("signed data: %v", err)
	}
	sd.SetDigestAlgorithm(pkcs7.OIDDigestAlgorithmSHA256)
	sd.SetContentType(asn1.ObjectIdentifier{1, 2, 840, 113549, 1, 9, 16, 1, 4})
	sd.AddCertificate(trustedTSA)
	if err := sd.AddSigner(ownSigner, ownKey, pkcs7.SignerInfoConfig{}); err != nil {
		t.Fatalf("add signer: %v", err)
	}
	tok, err := sd.Finish()
	if err != nil {
		t.Fatalf("finish token: %v", err)
	}
	parsed, err := timestamp.Parse(tok)
	if err != nil {
		t.Fatalf("parse forged token: %v", err)
	}
	if len(parsed.Certificates) == 0 || !parsed.Certificates[0].Equal(trustedTSA) {
		t.Fatalf("token's first certificate = %s, want the trusted TSA", parsed.Certificates[0].Subject.CommonName)
	}

	signer := tsaSigner(tok)
	if signer == nil || !signer.Equal(ownSigner) {
		t.Fatalf("tsaSigner = %v, want the token's own signer", signer)
	}
	trusted := x509.NewCertPool()
	trusted.AddCert(trustedRoot)
	if verifyTSA(parsed, signer, trusted, nil) {
		t.Error("a token signed by an untrusted TSA passed because it carries a trusted TSA's certificate")
	}
	own := x509.NewCertPool()
	own.AddCert(ownRoot)
	if !verifyTSA(parsed, signer, own, nil) {
		t.Error("the token's signer does not chain to its own root")
	}
}
