package verify

import (
	"bytes"
	"context"
	"crypto"
	"crypto/ecdsa"
	"crypto/sha256"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/asn1"
	"encoding/hex"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/digitorus/pkcs7"
	"golang.org/x/crypto/ocsp"

	"github.com/privacybydesign/yivi-businesswallet/backend/internal/diploma/trust"
)

// contentsHexWidth is the /Contents space reserved before signing.
const contentsHexWidth = 16384

// signAttempts is how often signCMS retries when the second signing pass
// lands in another second than the first (the signing-time attribute moved).
const signAttempts = 3

// testIssuer stands in for DUO.
var testIssuer = trust.Issuer{Name: "Test DUO", OrganizationIdentifier: "NTRNL-12345678", Organization: "Test DUO"}

// testTrust is a TrustSource over fixed pools.
type testTrust struct{ signer, tsa *x509.CertPool }

func (s testTrust) SignerRoots() *x509.CertPool     { return s.signer }
func (s testTrust) TSARoots() *x509.CertPool        { return s.tsa }
func (testTrust) Describe(*x509.Certificate) string { return "" }

// pki is a signer chain (root, optional intermediate, seal), a TSA and the
// OCSP responder that answers for every certificate below the root.
type pki struct {
	root, intermediate, seal *x509.Certificate
	sealKey                  *ecdsa.PrivateKey
	tsa                      *x509.Certificate
	tsaKey                   *ecdsa.PrivateKey
	trust                    testTrust
	responder                *ocspResponder
}

// pkiOptions shapes newPKI's chain.
type pkiOptions struct {
	intermediate bool
	revoked      bool
	// intermediateNoRevocation leaves the intermediate without OCSP or CRL.
	intermediateNoRevocation bool
}

func newPKI(t *testing.T, opts pkiOptions) *pki {
	t.Helper()
	responder := &ocspResponder{t: t, issuers: map[string]issuerOf{}, revoked: map[string]bool{}}
	srv := httptest.NewServer(responder)
	t.Cleanup(srv.Close)

	p := &pki{responder: responder}
	var rootKey *ecdsa.PrivateKey
	p.root, rootKey = testCA(t, "test root", nil, nil)
	parent, parentKey := p.root, rootKey
	if opts.intermediate {
		tmpl := &x509.Certificate{
			Subject:  pkix.Name{CommonName: "test intermediate"},
			IsCA:     true,
			KeyUsage: x509.KeyUsageCertSign | x509.KeyUsageCRLSign | x509.KeyUsageDigitalSignature,
		}
		if !opts.intermediateNoRevocation {
			tmpl.OCSPServer = []string{srv.URL}
		}
		var key *ecdsa.PrivateKey
		p.intermediate, key = issue(t, tmpl, p.root, rootKey)
		responder.add(p.intermediate, p.root, rootKey)
		parent, parentKey = p.intermediate, key
	}
	p.seal, p.sealKey = issue(t, sealTemplate(t, srv.URL), parent, parentKey)
	responder.add(p.seal, parent, parentKey)
	if opts.revoked {
		responder.revoked[p.seal.SerialNumber.String()] = true
	}

	tsaRoot, tsaRootKey := testCA(t, "test TSA root", nil, nil)
	p.tsa, p.tsaKey = testTSA(t, "test TSA", tsaRoot, tsaRootKey)
	signer, tsa := x509.NewCertPool(), x509.NewCertPool()
	signer.AddCert(p.root)
	tsa.AddCert(tsaRoot)
	p.trust = testTrust{signer: signer, tsa: tsa}
	return p
}

// sealTemplate is a qualified electronic seal certificate for testIssuer.
func sealTemplate(t *testing.T, ocspURL string) *x509.Certificate {
	t.Helper()
	qcType, err := asn1.Marshal([]asn1.ObjectIdentifier{oidQcTypeESeal})
	if err != nil {
		t.Fatal(err)
	}
	type qcStatement struct {
		ID   asn1.ObjectIdentifier
		Info asn1.RawValue `asn1:"optional"`
	}
	statements, err := asn1.Marshal([]qcStatement{{ID: oidQcCompliance}, {ID: oidQcType, Info: asn1.RawValue{FullBytes: qcType}}})
	if err != nil {
		t.Fatal(err)
	}
	return &x509.Certificate{
		Subject: pkix.Name{
			CommonName:   "Test DUO seal",
			Organization: []string{testIssuer.Organization},
			ExtraNames:   []pkix.AttributeTypeAndValue{{Type: oidOrganizationIdent, Value: testIssuer.OrganizationIdentifier}},
		},
		KeyUsage:        x509.KeyUsageDigitalSignature | x509.KeyUsageContentCommitment,
		OCSPServer:      []string{ocspURL},
		ExtraExtensions: []pkix.Extension{{Id: oidQCStatements, Value: statements}},
	}
}

type issuerOf struct {
	cert *x509.Certificate
	key  *ecdsa.PrivateKey
}

// ocspResponder answers OCSP requests for the certificates added to it.
type ocspResponder struct {
	t       *testing.T
	mu      sync.Mutex
	issuers map[string]issuerOf
	revoked map[string]bool
	// thisUpdate overrides the responses' this update (zero: a minute ago).
	thisUpdate time.Time
	nextUpdate time.Time
}

func (r *ocspResponder) add(cert, issuer *x509.Certificate, key *ecdsa.PrivateKey) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.issuers[cert.SerialNumber.String()] = issuerOf{issuer, key}
}

// response is a signed OCSP response about serial.
func (r *ocspResponder) response(serial *big.Int) ([]byte, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	issuer, ok := r.issuers[serial.String()]
	if !ok {
		return nil, fmt.Errorf("unknown serial %s", serial)
	}
	this, next := r.thisUpdate, r.nextUpdate
	if this.IsZero() {
		this, next = time.Now().Add(-time.Minute), time.Now().Add(time.Hour)
	}
	tmpl := ocsp.Response{Status: ocsp.Good, SerialNumber: serial, ThisUpdate: this, NextUpdate: next}
	if r.revoked[serial.String()] {
		tmpl.Status, tmpl.RevokedAt, tmpl.RevocationReason = ocsp.Revoked, time.Now().Add(-time.Hour), ocsp.KeyCompromise
	}
	return ocsp.CreateResponse(issuer.cert, issuer.cert, tmpl, issuer.key)
}

func (r *ocspResponder) ServeHTTP(w http.ResponseWriter, req *http.Request) {
	body, err := io.ReadAll(req.Body)
	if err != nil {
		r.t.Errorf("read OCSP request: %v", err)
		return
	}
	parsed, err := ocsp.ParseRequest(body)
	if err != nil {
		r.t.Errorf("parse OCSP request: %v", err)
		return
	}
	resp, err := r.response(parsed.SerialNumber)
	if err != nil {
		r.t.Errorf("OCSP response: %v", err)
		return
	}
	_, _ = w.Write(resp)
}

// cachingSigner signs each digest once and answers the same signature for
// it again, so signCMS's second pass reproduces the first pass's signature
// value, which the timestamp token is over.
type cachingSigner struct {
	key        *ecdsa.PrivateKey
	signatures map[string][]byte
}

func (s *cachingSigner) Public() crypto.PublicKey { return &s.key.PublicKey }

func (s *cachingSigner) Sign(rand io.Reader, digest []byte, opts crypto.SignerOpts) ([]byte, error) {
	if sig, ok := s.signatures[string(digest)]; ok {
		return sig, nil
	}
	sig, err := s.key.Sign(rand, digest, opts)
	if err != nil {
		return nil, err
	}
	s.signatures[string(digest)] = sig
	return sig, nil
}

// signOptions is what signCMS puts in the signature.
type signOptions struct {
	timestamp bool
	ess       bool
	// essCert is the certificate the ESS attribute names; nil names the seal.
	essCert *x509.Certificate
	// embedded are OCSP responses for the adbe-revocationInfoArchival attribute.
	embedded [][]byte
}

// essAttribute names cert by its SHA-256 hash, the default algorithm.
func essAttribute(cert *x509.Certificate) signingCertificateV2 {
	sum := sha256.Sum256(cert.Raw)
	return signingCertificateV2{Certs: []essCertIDv2{{CertHash: sum[:]}}}
}

func signCMS(t *testing.T, p *pki, content []byte, opts signOptions) []byte {
	t.Helper()
	var signedAttrs []pkcs7.Attribute
	if opts.ess {
		named := p.seal
		if opts.essCert != nil {
			named = opts.essCert
		}
		signedAttrs = append(signedAttrs, pkcs7.Attribute{Type: oidSigningCertV2, Value: essAttribute(named)})
	}
	if len(opts.embedded) > 0 {
		archive := revocationArchive{}
		for _, resp := range opts.embedded {
			archive.OCSP = append(archive.OCSP, asn1.RawValue{FullBytes: resp})
		}
		signedAttrs = append(signedAttrs, pkcs7.Attribute{Type: oidAdobeRevocationInf, Value: archive})
	}
	var parents []*x509.Certificate
	if p.intermediate != nil {
		parents = append(parents, p.intermediate)
	}
	signer := &cachingSigner{key: p.sealKey, signatures: map[string][]byte{}}

	pass := func(unsigned []pkcs7.Attribute) *pkcs7.SignedData {
		sd, err := pkcs7.NewSignedData(content)
		if err != nil {
			t.Fatalf("signed data: %v", err)
		}
		sd.SetDigestAlgorithm(pkcs7.OIDDigestAlgorithmSHA256)
		if err := sd.AddSignerChain(p.seal, signer, parents, pkcs7.SignerInfoConfig{
			ExtraSignedAttributes: signedAttrs, ExtraUnsignedAttributes: unsigned,
		}); err != nil {
			t.Fatalf("add signer: %v", err)
		}
		return sd
	}
	finish := func(sd *pkcs7.SignedData) []byte {
		sd.Detach()
		cms, err := sd.Finish()
		if err != nil {
			t.Fatalf("finish: %v", err)
		}
		return cms
	}

	if !opts.timestamp {
		return finish(pass(nil))
	}
	for range signAttempts {
		value := pass(nil).GetSignedData().SignerInfos[0].EncryptedDigest
		digest := sha256.Sum256(value)
		token := timestampToken(t, digest[:], p.tsa, p.tsaKey)
		sd := pass([]pkcs7.Attribute{{Type: oidTimeStampToken, Value: asn1.RawValue{FullBytes: token}}})
		if bytes.Equal(sd.GetSignedData().SignerInfos[0].EncryptedDigest, value) {
			return finish(sd)
		}
	}
	t.Fatalf("the signature value changed between passes %d times", signAttempts)
	return nil
}

// signPDF lays out a one-signature PDF and signs its two ByteRange segments
// with a detached CMS signature, as a PAdES signing tool does.
func signPDF(t *testing.T, p *pki, opts signOptions) []byte {
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

	contents := hex.EncodeToString(signCMS(t, p, []byte(prefix+suffix), opts))
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

func verifyPDF(t *testing.T, pdf []byte, opts Options) *Result {
	t.Helper()
	res, err := PDF(context.Background(), pdf, opts)
	if err != nil {
		t.Fatalf("PDF: %v", err)
	}
	return res
}

// complete is every element a genuine extract carries.
var complete = signOptions{timestamp: true, ess: true}

func TestPDFValid(t *testing.T) {
	p := newPKI(t, pkiOptions{intermediate: true})
	res := verifyPDF(t, signPDF(t, p, complete), Options{Issuer: &testIssuer, Trust: p.trust, OCSP: true})
	if !res.Valid {
		t.Fatalf("a complete, genuine signature did not verify: %+v", res.FirstFailure())
	}
	if !res.TimestampTrusted || res.TSA == nil || !res.TSA.Equal(p.tsa) {
		t.Errorf("TimestampTrusted = %v, TSA = %v; want the trusted token's", res.TimestampTrusted, res.TSA)
	}
}

func TestPDFEmbeddedRevocation(t *testing.T) {
	p := newPKI(t, pkiOptions{})
	opts := Options{Issuer: &testIssuer, Trust: p.trust}

	res := verifyPDF(t, signPDF(t, p, complete), opts)
	if checkOK(t, res, CheckRevocation) || res.Valid {
		t.Errorf("without OCSP or embedded information the revocation check passed: %+v", res.Checks)
	}

	good, err := p.responder.response(p.seal.SerialNumber)
	if err != nil {
		t.Fatal(err)
	}
	embedded := complete
	embedded.embedded = [][]byte{good}
	res = verifyPDF(t, signPDF(t, p, embedded), opts)
	if !res.Valid {
		t.Errorf("an embedded good OCSP response did not verify: %+v", res.FirstFailure())
	}
}

func TestPDFRevocation(t *testing.T) {
	t.Run("revoked signer", func(t *testing.T) {
		p := newPKI(t, pkiOptions{revoked: true})
		res := verifyPDF(t, signPDF(t, p, complete), Options{Issuer: &testIssuer, Trust: p.trust, OCSP: true})
		if checkOK(t, res, CheckRevocation) || res.Valid {
			t.Errorf("a revoked signer verified: %+v", res.Checks)
		}
	})

	t.Run("intermediate without revocation information", func(t *testing.T) {
		p := newPKI(t, pkiOptions{intermediate: true, intermediateNoRevocation: true})
		res := verifyPDF(t, signPDF(t, p, complete), Options{Issuer: &testIssuer, Trust: p.trust, OCSP: true})
		if checkOK(t, res, CheckRevocation) || res.Valid {
			t.Errorf("an unchecked intermediate passed: %+v", res.Checks)
		}
	})

	t.Run("expired response replayed", func(t *testing.T) {
		p := newPKI(t, pkiOptions{})
		p.responder.thisUpdate, p.responder.nextUpdate = time.Now().Add(-48*time.Hour), time.Now().Add(-24*time.Hour)
		res := verifyPDF(t, signPDF(t, p, complete), Options{Issuer: &testIssuer, Trust: p.trust, OCSP: true})
		if checkOK(t, res, CheckRevocation) || res.Valid {
			t.Errorf("an expired OCSP response passed: %+v", res.Checks)
		}
	})
}

func TestPDFSignedAttributes(t *testing.T) {
	p := newPKI(t, pkiOptions{})
	opts := Options{Issuer: &testIssuer, Trust: p.trust, OCSP: true}

	t.Run("no ESS attribute", func(t *testing.T) {
		res := verifyPDF(t, signPDF(t, p, signOptions{timestamp: true}), opts)
		if checkOK(t, res, CheckSigningCertificate) || res.Valid {
			t.Errorf("a signature without signing-certificate-v2 verified: %+v", res.Checks)
		}
	})

	t.Run("ESS attribute names another certificate", func(t *testing.T) {
		res := verifyPDF(t, signPDF(t, p, signOptions{timestamp: true, ess: true, essCert: p.tsa}), opts)
		if checkOK(t, res, CheckSigningCertificate) || res.Valid {
			t.Errorf("a signing-certificate-v2 naming another certificate verified: %+v", res.Checks)
		}
	})

	t.Run("no timestamp", func(t *testing.T) {
		res := verifyPDF(t, signPDF(t, p, signOptions{ess: true}), opts)
		if checkOK(t, res, CheckTimestampToken) || res.Valid || res.TimestampTrusted {
			t.Errorf("a signature without a timestamp verified: %+v", res.Checks)
		}
	})

	t.Run("untrusted TSA", func(t *testing.T) {
		untrusted := opts
		untrusted.Trust = testTrust{signer: p.trust.signer, tsa: x509.NewCertPool()}
		res := verifyPDF(t, signPDF(t, p, complete), untrusted)
		if checkOK(t, res, CheckTimestampAuthority) || res.TimestampTrusted || res.Valid {
			t.Errorf("TimestampTrusted = %v with an untrusted TSA: %+v", res.TimestampTrusted, res.Checks)
		}
	})
}

func TestPDF(t *testing.T) {
	p := newPKI(t, pkiOptions{})
	opts := Options{Trust: p.trust, OCSP: true}
	pdf := signPDF(t, p, complete)

	t.Run("signed by someone else than DUO", func(t *testing.T) {
		res := verifyPDF(t, pdf, opts)
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
		res := verifyPDF(t, pdf, Options{Trust: testTrust{x509.NewCertPool(), p.trust.tsa}})
		if !checkOK(t, res, CheckCmsSignature) || checkOK(t, res, CheckCertificateChain) {
			t.Errorf("want a valid CMS signature on an untrusted chain: %+v", res.Checks)
		}
	})

	t.Run("changed after signing", func(t *testing.T) {
		tampered := append([]byte{}, pdf...)
		tampered[len("%PDF-1.")] = '4'
		res := verifyPDF(t, tampered, opts)
		if res.Valid || res.FirstFailure() == nil || res.FirstFailure().ID != CheckCmsSignature {
			t.Errorf("FirstFailure = %+v, want %s", res.FirstFailure(), CheckCmsSignature)
		}
	})

	t.Run("bytes appended after signing", func(t *testing.T) {
		updated := append(append([]byte{}, pdf...), []byte("2 0 obj\n<< >>\nendobj\n")...)
		res := verifyPDF(t, updated, opts)
		if checkOK(t, res, CheckCoversWholeFile) || res.Valid {
			t.Errorf("an incrementally updated file passed: %+v", res.Checks)
		}
	})

	t.Run("unsigned", func(t *testing.T) {
		res := verifyPDF(t, []byte("%PDF-1.7\n1 0 obj\n<< /Type /Catalog >>\nendobj\n"), opts)
		if res.Valid || res.FirstFailure() == nil || res.FirstFailure().ID != CheckSignaturePresent {
			t.Errorf("FirstFailure = %+v, want %s", res.FirstFailure(), CheckSignaturePresent)
		}
	})
}
