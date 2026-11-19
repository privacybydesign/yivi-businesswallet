package eudiholder

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"testing"
	"time"

	eudijwt "github.com/privacybydesign/irmago/eudi/jwt"
)

// baseContext stands in for irmago's TrustModel: a template carrying the anchors
// already trusted, which mergeTrustChain must preserve.
type baseContext struct {
	opts x509.VerifyOptions
	crls []*x509.RevocationList
}

func (b *baseContext) GetVerificationOptionsTemplate() x509.VerifyOptions { return b.opts }
func (b *baseContext) GetRevocationLists() []*x509.RevocationList         { return b.crls }

func TestMergeTrustChainKeepsBaseAnchors(t *testing.T) {
	existing, existingKey := selfSignedCA(t, "Existing Root")
	added, addedKey := selfSignedCA(t, "Ver.iD Dev Root CA")

	basePool := x509.NewCertPool()
	basePool.AddCert(existing)
	base := &baseContext{opts: x509.VerifyOptions{
		Roots:     basePool,
		KeyUsages: []x509.ExtKeyUsage{x509.ExtKeyUsageAny},
	}}

	merged, err := mergeTrustChain(base, pemOf(added))
	if err != nil {
		t.Fatalf("mergeTrustChain: %v", err)
	}

	// Both anchors verify: adding a partner root must not unseat the ones the
	// built-in trust model already carries.
	for _, tc := range []struct {
		name   string
		issuer *x509.Certificate
		key    *ecdsa.PrivateKey
	}{
		{"existing anchor", existing, existingKey},
		{"added anchor", added, addedKey},
	} {
		t.Run(tc.name, func(t *testing.T) {
			leaf := leafSignedBy(t, tc.issuer, tc.key, "leaf")
			if _, err := leaf.Verify(merged.GetVerificationOptionsTemplate()); err != nil {
				t.Fatalf("leaf under %s did not verify: %v", tc.name, err)
			}
		})
	}

	// The template hands back the trust model's own pool pointers, so the merge
	// must have copied rather than added in place: the base must still reject.
	if _, err := leafSignedBy(t, added, addedKey, "leaf").Verify(base.GetVerificationOptionsTemplate()); err == nil {
		t.Fatal("merge mutated the base trust model's root pool")
	}
}

func TestMergeTrustChainAcceptsSeveralRoots(t *testing.T) {
	first, firstKey := selfSignedCA(t, "Partner A Root")
	second, secondKey := selfSignedCA(t, "Partner B Root")

	base := &baseContext{opts: x509.VerifyOptions{
		Roots:     x509.NewCertPool(),
		KeyUsages: []x509.ExtKeyUsage{x509.ExtKeyUsageAny},
	}}

	// irmago's own helper would file the second root as an intermediate, where
	// nothing can chain to it; classifying by signature keeps both usable.
	merged, err := mergeTrustChain(base, append(pemOf(first), pemOf(second)...))
	if err != nil {
		t.Fatalf("mergeTrustChain: %v", err)
	}

	for _, tc := range []struct {
		name   string
		issuer *x509.Certificate
		key    *ecdsa.PrivateKey
	}{
		{"first root", first, firstKey},
		{"second root", second, secondKey},
	} {
		t.Run(tc.name, func(t *testing.T) {
			leaf := leafSignedBy(t, tc.issuer, tc.key, "leaf")
			if _, err := leaf.Verify(merged.GetVerificationOptionsTemplate()); err != nil {
				t.Fatalf("leaf under %s did not verify: %v", tc.name, err)
			}
		})
	}
}

func TestMergeTrustChainFilesIntermediatesSeparately(t *testing.T) {
	root, rootKey := selfSignedCA(t, "Partner Root")
	intermediate, intermediateKey := intermediateCA(t, root, rootKey, "Partner Issuing CA")
	leaf := leafSignedBy(t, intermediate, intermediateKey, "leaf")

	base := &baseContext{opts: x509.VerifyOptions{
		Roots:     x509.NewCertPool(),
		KeyUsages: []x509.ExtKeyUsage{x509.ExtKeyUsageAny},
	}}

	// Intermediate first, root second: position must not decide the pool.
	merged, err := mergeTrustChain(base, append(pemOf(intermediate), pemOf(root)...))
	if err != nil {
		t.Fatalf("mergeTrustChain: %v", err)
	}
	if _, err := leaf.Verify(merged.GetVerificationOptionsTemplate()); err != nil {
		t.Fatalf("leaf under root-signed intermediate did not verify: %v", err)
	}
}

func TestMergeTrustChainRejectsUnusablePEM(t *testing.T) {
	base := &baseContext{opts: x509.VerifyOptions{Roots: x509.NewCertPool()}}

	for _, tc := range []struct {
		name string
		pem  []byte
	}{
		{"empty", nil},
		{"no certificate blocks", []byte("-----BEGIN PRIVATE KEY-----\nAAAA\n-----END PRIVATE KEY-----\n")},
		{"malformed certificate", []byte("-----BEGIN CERTIFICATE-----\nAAAA\n-----END CERTIFICATE-----\n")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := mergeTrustChain(base, tc.pem); err == nil {
				t.Fatal("expected an error, got none")
			}
		})
	}
}

func TestMergeTrustChainCarriesRevocationLists(t *testing.T) {
	root, _ := selfSignedCA(t, "Partner Root")
	base := &baseContext{
		opts: x509.VerifyOptions{Roots: x509.NewCertPool()},
		crls: []*x509.RevocationList{{Number: big.NewInt(7)}},
	}

	merged, err := mergeTrustChain(base, pemOf(root))
	if err != nil {
		t.Fatalf("mergeTrustChain: %v", err)
	}
	if got := merged.GetRevocationLists(); len(got) != 1 || got[0].Number.Int64() != 7 {
		t.Fatalf("revocation lists not carried over: %#v", got)
	}
}

func TestIsSelfSignedRejectsForgedIssuerName(t *testing.T) {
	root, rootKey := selfSignedCA(t, "Real Root")
	// A certificate whose subject equals its issuer name but which is signed by
	// another key: a name comparison alone would file it as a root.
	impostor := leafSignedBy(t, root, rootKey, "Real Root")
	if isSelfSigned(impostor) {
		t.Fatal("cert signed by another key classified as self-signed")
	}
	if !isSelfSigned(root) {
		t.Fatal("genuine self-signed root not classified as self-signed")
	}
}

func TestTrustContextMergesConfiguredChain(t *testing.T) {
	builtIn, builtInKey := selfSignedCA(t, "Built-in Root")
	partner, partnerKey := selfSignedCA(t, "Ver.iD Dev Root CA")

	basePool := x509.NewCertPool()
	basePool.AddCert(builtIn)
	base := &baseContext{opts: x509.VerifyOptions{
		Roots:     basePool,
		KeyUsages: []x509.ExtKeyUsage{x509.ExtKeyUsageAny},
	}}

	e := &Engine{redeem: RedeemConfig{TrustChainPEM: pemOf(partner)}}
	merged, err := e.trustContext(base)
	if err != nil {
		t.Fatalf("trustContext: %v", err)
	}

	// Redeem derives every x5c consumer from this one context — the credential's
	// own issuer chain and the Status List Token its status.status_list reference
	// resolves to. Both are signed under the partner root, so a context that
	// carried only the built-in anchors would fail redemption on the status list
	// with "unauthorized: certificate validation" even though the credential
	// itself verified.
	for _, tc := range []struct {
		name   string
		issuer *x509.Certificate
		key    *ecdsa.PrivateKey
	}{
		{"built-in anchor", builtIn, builtInKey},
		{"configured partner anchor", partner, partnerKey},
	} {
		t.Run(tc.name, func(t *testing.T) {
			leaf := leafSignedBy(t, tc.issuer, tc.key, "leaf")
			if _, err := leaf.Verify(merged.GetVerificationOptionsTemplate()); err != nil {
				t.Fatalf("leaf under %s did not verify: %v", tc.name, err)
			}
		})
	}
}

func TestTrustContextWithoutChainReturnsBase(t *testing.T) {
	base := &baseContext{opts: x509.VerifyOptions{Roots: x509.NewCertPool()}}

	e := &Engine{}
	got, err := e.trustContext(base)
	if err != nil {
		t.Fatalf("trustContext: %v", err)
	}
	// Unconfigured, the built-in trust model must be handed through untouched
	// rather than copied into an equivalent-looking context.
	if got != eudijwt.X509VerificationContext(base) {
		t.Fatalf("base context not passed through: %#v", got)
	}
}

func TestTrustContextRejectsUnusableChain(t *testing.T) {
	base := &baseContext{opts: x509.VerifyOptions{Roots: x509.NewCertPool()}}

	e := &Engine{redeem: RedeemConfig{TrustChainPEM: []byte("not a pem")}}
	if _, err := e.trustContext(base); err == nil {
		t.Fatal("expected an error, got none")
	}
}

// --- helpers ---------------------------------------------------------------

func pemOf(cert *x509.Certificate) []byte {
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: cert.Raw})
}

func selfSignedCA(t *testing.T, cn string) (*x509.Certificate, *ecdsa.PrivateKey) {
	t.Helper()
	key := newKey(t)
	tmpl := caTemplate(t, cn)
	return signCert(t, tmpl, tmpl, &key.PublicKey, key), key
}

func intermediateCA(t *testing.T, parent *x509.Certificate, parentKey *ecdsa.PrivateKey, cn string) (*x509.Certificate, *ecdsa.PrivateKey) {
	t.Helper()
	key := newKey(t)
	return signCert(t, caTemplate(t, cn), parent, &key.PublicKey, parentKey), key
}

func leafSignedBy(t *testing.T, parent *x509.Certificate, parentKey *ecdsa.PrivateKey, cn string) *x509.Certificate {
	t.Helper()
	key := newKey(t)
	tmpl := &x509.Certificate{
		SerialNumber: serial(t),
		Subject:      pkix.Name{CommonName: cn},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
	}
	return signCert(t, tmpl, parent, &key.PublicKey, parentKey)
}

func caTemplate(t *testing.T, cn string) *x509.Certificate {
	t.Helper()
	return &x509.Certificate{
		SerialNumber:          serial(t),
		Subject:               pkix.Name{CommonName: cn},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(time.Hour),
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageCRLSign,
		BasicConstraintsValid: true,
		IsCA:                  true,
	}
}

func signCert(t *testing.T, tmpl, parent *x509.Certificate, pub *ecdsa.PublicKey, signer *ecdsa.PrivateKey) *x509.Certificate {
	t.Helper()
	der, err := x509.CreateCertificate(rand.Reader, tmpl, parent, pub, signer)
	if err != nil {
		t.Fatalf("create certificate: %v", err)
	}
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatalf("parse certificate: %v", err)
	}
	return cert
}

func newKey(t *testing.T) *ecdsa.PrivateKey {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	return key
}

func serial(t *testing.T) *big.Int {
	t.Helper()
	n, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if err != nil {
		t.Fatalf("serial: %v", err)
	}
	return n
}
