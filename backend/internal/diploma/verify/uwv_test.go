package verify

import (
	"bytes"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/asn1"
	"math/big"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/digitorus/pkcs7"

	"github.com/privacybydesign/yivi-businesswallet/backend/internal/diploma/trust"
)

// testUWV stands in for UWV: its signatures lack what UWV's seal lacks.
var testUWV = trust.Issuer{
	Name: "Test UWV", OrganizationIdentifier: "NTRNL-87654321", Organization: "Test UWV",
	Lacks: trust.UWV.Lacks,
}

// rsaKeyBits is the test seal's RSA key size.
const rsaKeyBits = 2048

// uwvPDF is a PDF sealed the way UWV seals a Verzekeringsbericht: an RSA
// seal under p's root, a bare adbe.pkcs7.detached signature without signed
// attributes, DocMDP or timestamp, whose SignerInfo names
// sha256WithRSAEncryption as its digest algorithm. The seal names ocspURL as
// its responder. It returns the seal too.
func uwvPDF(t *testing.T, p *pki, ocspURL string) ([]byte, *x509.Certificate) {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, rsaKeyBits)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := sealTemplate(t, ocspURL)
	tmpl.Subject = pkix.Name{
		CommonName:   "UWV",
		Organization: []string{testUWV.Organization},
		ExtraNames:   []pkix.AttributeTypeAndValue{{Type: oidOrganizationIdent, Value: testUWV.OrganizationIdentifier}},
	}
	tmpl.SerialNumber = big.NewInt(time.Now().UnixNano())
	tmpl.NotBefore, tmpl.NotAfter = time.Now().Add(-time.Hour), time.Now().Add(time.Hour)
	rootKey := p.responder.issuers[p.seal.SerialNumber.String()].key
	der, err := x509.CreateCertificate(rand.Reader, tmpl, p.root, &key.PublicKey, rootKey)
	if err != nil {
		t.Fatal(err)
	}
	seal, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatal(err)
	}
	p.responder.add(seal, p.root, rootKey)

	pdf := layoutPDF(t, "/Filter /Adobe.PPKMS /SubFilter /adbe.pkcs7.detached", func(content []byte) []byte {
		sd, err := pkcs7.NewSignedData(content)
		if err != nil {
			t.Fatal(err)
		}
		sd.SetDigestAlgorithm(pkcs7.OIDDigestAlgorithmSHA256)
		if err := sd.SignWithoutAttr(seal, key, pkcs7.SignerInfoConfig{}); err != nil {
			t.Fatal(err)
		}
		sd.Detach()
		cms, err := sd.Finish()
		if err != nil {
			t.Fatal(err)
		}
		// Both encodings are 11 bytes long, so every length stays right.
		return bytes.ReplaceAll(cms, oidDER(t, pkcs7.OIDDigestAlgorithmSHA256), oidDER(t, pkcs7.OIDEncryptionAlgorithmRSASHA256))
	})
	return pdf, seal
}

func oidDER(t *testing.T, oid asn1.ObjectIdentifier) []byte {
	t.Helper()
	der, err := asn1.Marshal(oid)
	if err != nil {
		t.Fatal(err)
	}
	return der
}

func TestPDFUWV(t *testing.T) {
	p := newPKI(t, pkiOptions{})
	pdf, seal := uwvPDF(t, p, p.seal.OCSPServer[0])

	t.Run("verifies under its issuer", func(t *testing.T) { testUWVVerifies(t, p, pdf, seal) })
	t.Run("fails under an issuer that requires everything", func(t *testing.T) { testUWVStrictIssuer(t, p, pdf) })

	t.Run("fails under DUO", func(t *testing.T) {
		if res := verifyPDF(t, pdf, Options{Issuer: &trust.DUO, Trust: p.trust}); res.Valid {
			t.Error("a UWV-shaped seal verified as DUO's")
		}
	})

	t.Run("changed after signing", func(t *testing.T) {
		// Without signed attributes only the RSA signature over the content
		// guards it, so one changed byte must fail that check.
		tampered := append([]byte{}, pdf...)
		tampered[len("%PDF-1.")] = '4'
		res := verifyPDF(t, tampered, Options{Issuer: &testUWV, Trust: p.trust})
		if res.Valid || res.FirstFailure() == nil || res.FirstFailure().ID != CheckCmsSignature {
			t.Errorf("FirstFailure = %+v, want %s", res.FirstFailure(), CheckCmsSignature)
		}
	})

	t.Run("revoked seal, offline asked", func(t *testing.T) {
		// Nothing is embedded, so revocation is asked online even when
		// Options.OCSP is off, and a revoked seal fails.
		p.responder.mu.Lock()
		p.responder.revoked[seal.SerialNumber.String()] = true
		p.responder.mu.Unlock()
		res := verifyPDF(t, pdf, Options{Issuer: &testUWV, Trust: p.trust})
		if checkOK(t, res, CheckRevocation) || res.Valid {
			t.Errorf("a revoked UWV seal verified: %+v", res.Checks)
		}
	})

	t.Run("unreachable responder", func(t *testing.T) {
		closed := httptest.NewServer(http.NotFoundHandler())
		closed.Close()
		doc, _ := uwvPDF(t, p, closed.URL)
		res := verifyPDF(t, doc, Options{Issuer: &testUWV, Trust: p.trust})
		if checkOK(t, res, CheckRevocation) || res.Valid {
			t.Errorf("a seal whose responder cannot answer verified: %+v", res.Checks)
		}
	})
}

func testUWVVerifies(t *testing.T, p *pki, pdf []byte, seal *x509.Certificate) {
	t.Helper()
	res := verifyPDF(t, pdf, Options{Issuer: &testUWV, Trust: p.trust})
	if !res.Valid {
		t.Fatalf("a UWV-shaped seal did not verify: %+v", res.FirstFailure())
	}
	if res.TimestampTrusted || !res.Signer.Equal(seal) {
		t.Errorf("TimestampTrusted = %v, Signer = %v; want no timestamp and the seal", res.TimestampTrusted, res.Signer.Subject)
	}
	for _, c := range res.Checks {
		switch c.ID {
		case CheckCertification, CheckSigningCertificate, CheckTimestampToken, CheckTimestampAuthority:
			t.Errorf("check %s ran for an issuer that lacks it", c.ID)
		}
	}
}

func testUWVStrictIssuer(t *testing.T, p *pki, pdf []byte) {
	t.Helper()
	strict := testUWV
	strict.Lacks = trust.Lacks{}
	res := verifyPDF(t, pdf, Options{Issuer: &strict, Trust: p.trust, OCSP: true})
	for _, id := range []string{CheckCertification, CheckSigningCertificate, CheckTimestampToken} {
		if checkOK(t, res, id) {
			t.Errorf("check %s passed on a signature without it", id)
		}
	}
	if res.Valid {
		t.Error("a bare signature verified under an issuer that requires a long-term one")
	}
}
