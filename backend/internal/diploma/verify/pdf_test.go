package verify

import (
	"context"
	"crypto/ecdsa"
	"crypto/x509"
	"encoding/hex"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/digitorus/pkcs7"
)

// contentsHexWidth is the /Contents space reserved before signing.
const contentsHexWidth = 8192

// testTrust is a TrustSource over a fixed pool.
type testTrust struct{ roots *x509.CertPool }

func (s testTrust) SignerRoots() *x509.CertPool     { return s.roots }
func (s testTrust) TSARoots() *x509.CertPool        { return s.roots }
func (testTrust) Describe(*x509.Certificate) string { return "" }

// signPDF lays out a one-signature PDF and signs its two ByteRange segments
// with a detached CMS signature, as a PAdES signing tool does.
func signPDF(t *testing.T, signer *x509.Certificate, key *ecdsa.PrivateKey) []byte {
	t.Helper()
	// The claimed signing time is the one the chain is checked at without a
	// timestamp, so it has to fall inside the test certificates' validity.
	claimed := "D:" + time.Now().UTC().Format("20060102150405") + "Z"
	prefix := "%PDF-1.7\n1 0 obj\n<< /Type /Sig /Filter /Adobe.PPKLite /SubFilter /ETSI.CAdES.detached " +
		"/M (" + claimed + ") /Reference [<< /TransformMethod /DocMDP /TransformParams << /P 1 >> >>] /Contents "
	suffixFormat := " /ByteRange [%010d %010d %010d %010d] >>\nendobj\n%%%%EOF\n"
	suffixLen := len(fmt.Sprintf(suffixFormat, 0, 0, 0, 0))
	secondStart := len(prefix) + contentsHexWidth + len("<>")
	suffix := fmt.Sprintf(suffixFormat, 0, len(prefix), secondStart, suffixLen)

	sd, err := pkcs7.NewSignedData([]byte(prefix + suffix))
	if err != nil {
		t.Fatalf("signed data: %v", err)
	}
	sd.SetDigestAlgorithm(pkcs7.OIDDigestAlgorithmSHA256)
	if err := sd.AddSigner(signer, key, pkcs7.SignerInfoConfig{}); err != nil {
		t.Fatalf("add signer: %v", err)
	}
	sd.Detach()
	cms, err := sd.Finish()
	if err != nil {
		t.Fatalf("finish: %v", err)
	}
	contents := hex.EncodeToString(cms)
	if len(contents) > contentsHexWidth {
		t.Fatalf("signature of %d hex chars does not fit the reserved %d", len(contents), contentsHexWidth)
	}
	contents += strings.Repeat("0", contentsHexWidth-len(contents))
	return []byte(prefix + "<" + contents + ">" + suffix)
}

func checkOK(t *testing.T, res *Result, id string) bool {
	t.Helper()
	for _, c := range res.Checks {
		if c.ID == id {
			return c.OK
		}
	}
	t.Fatalf("check %s did not run; checks: %+v", id, res.Checks)
	return false
}

func TestPDF(t *testing.T) {
	root, rootKey := testCert(t, "test root", true, nil, nil)
	signer, signerKey := testCert(t, "test signer", false, root, rootKey)
	trusted := x509.NewCertPool()
	trusted.AddCert(root)
	opts := Options{Trust: testTrust{trusted}, Now: time.Now}
	pdf := signPDF(t, signer, signerKey)

	t.Run("genuine signature on a trusted chain", func(t *testing.T) {
		res, err := PDF(context.Background(), pdf, opts)
		if err != nil {
			t.Fatalf("PDF: %v", err)
		}
		for _, id := range []string{CheckCoversWholeFile, CheckCertification, CheckPadesSubfilter, CheckCmsSignature, CheckCertificateChain} {
			if !checkOK(t, res, id) {
				t.Errorf("check %s failed: %+v", id, res.Checks)
			}
		}
		// Not DUO's seal: the issuer identity is the check that stops it.
		if checkOK(t, res, CheckSignerIdentity) || res.Valid {
			t.Error("a signature by someone other than DUO verified")
		}
	})

	t.Run("untrusted chain", func(t *testing.T) {
		res, err := PDF(context.Background(), pdf, Options{Trust: testTrust{x509.NewCertPool()}, Now: time.Now})
		if err != nil {
			t.Fatalf("PDF: %v", err)
		}
		if !checkOK(t, res, CheckCmsSignature) || checkOK(t, res, CheckCertificateChain) {
			t.Errorf("want a valid CMS signature on an untrusted chain: %+v", res.Checks)
		}
	})

	t.Run("changed after signing", func(t *testing.T) {
		tampered := append([]byte{}, pdf...)
		tampered[len("%PDF-1.")] = '4'
		res, err := PDF(context.Background(), tampered, opts)
		if err != nil {
			t.Fatalf("PDF: %v", err)
		}
		if res.Valid || res.FirstFailure() == nil || res.FirstFailure().ID != CheckCmsSignature {
			t.Errorf("FirstFailure = %+v, want %s", res.FirstFailure(), CheckCmsSignature)
		}
	})

	t.Run("bytes appended after signing", func(t *testing.T) {
		updated := append(append([]byte{}, pdf...), []byte("2 0 obj\n<< >>\nendobj\n")...)
		res, err := PDF(context.Background(), updated, opts)
		if err != nil {
			t.Fatalf("PDF: %v", err)
		}
		if checkOK(t, res, CheckCoversWholeFile) || res.Valid {
			t.Errorf("an incrementally updated file passed: %+v", res.Checks)
		}
	})

	t.Run("unsigned", func(t *testing.T) {
		res, err := PDF(context.Background(), []byte("%PDF-1.7\n1 0 obj\n<< /Type /Catalog >>\nendobj\n"), opts)
		if err != nil {
			t.Fatalf("PDF: %v", err)
		}
		if res.Valid || res.FirstFailure() == nil || res.FirstFailure().ID != CheckSignaturePresent {
			t.Errorf("FirstFailure = %+v, want %s", res.FirstFailure(), CheckSignaturePresent)
		}
	})
}
