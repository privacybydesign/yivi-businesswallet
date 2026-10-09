// Package verify checks the authenticity of a DUO diploma PDF.
//
// The checks, in order:
//
//  1. The PDF carries exactly one signature and its ByteRange covers the whole
//     file. Anything outside the ByteRange is unsigned, so a file that has been
//     "updated" after signing is rejected.
//  2. The CMS SignedData in /Contents is a valid detached signature over the
//     signed bytes (message digest and signature check), and its ESS
//     signing-certificate-v2 attribute names the signer certificate.
//  3. The signing time is taken from the RFC 3161 timestamp token (its hash
//     must match the signature value, and its signer chain to a qualified
//     TSA). Without one the check fails; the signer-claimed time only lets
//     the remaining checks run.
//  4. The signer certificate chains to a trust anchor, evaluated at the
//     signing time. Anchors come from a TrustSource: by default the EU
//     Trusted Lists (eIDAS), or the pinned PKIoverheid root when offline.
//  5. The signer's subject matches the expected issuer (DUO's KvK number).
//  6. No certificate of the chain below the anchor is revoked: checked online
//     (OCSP, or the CRL where a certificate names no responder) with
//     Options.OCSP, otherwise against the revocation information embedded in
//     the signature (PAdES LTV). Without either the check fails.
package verify

import (
	"bytes"
	"context"
	"crypto"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/asn1"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/digitorus/pkcs7"
	"github.com/digitorus/timestamp"
	"golang.org/x/crypto/ocsp"

	"github.com/privacybydesign/yivi-businesswallet/backend/internal/diploma/pdfsig"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/diploma/trust"
)

var (
	oidTimeStampToken     = asn1.ObjectIdentifier{1, 2, 840, 113549, 1, 9, 16, 2, 14}
	oidOrganizationIdent  = asn1.ObjectIdentifier{2, 5, 4, 97}
	oidSigningCertV2      = asn1.ObjectIdentifier{1, 2, 840, 113549, 1, 9, 16, 2, 47}
	oidAdobeRevocationInf = asn1.ObjectIdentifier{1, 2, 840, 113583, 1, 1, 8}
	oidQCStatements       = asn1.ObjectIdentifier{1, 3, 6, 1, 5, 5, 7, 1, 3}
	oidQcCompliance       = asn1.ObjectIdentifier{0, 4, 0, 1862, 1, 1}
	oidQcType             = asn1.ObjectIdentifier{0, 4, 0, 1862, 1, 6}
	oidQcTypeESeal        = asn1.ObjectIdentifier{0, 4, 0, 1862, 1, 6, 2}
)

// essHashes are the digests an ESSCertIDv2 may name the signer certificate
// with; SHA-256 is the default when it names none (RFC 5035).
var essHashes = map[string]crypto.Hash{
	"2.16.840.1.101.3.4.2.1": crypto.SHA256,
	"2.16.840.1.101.3.4.2.2": crypto.SHA384,
	"2.16.840.1.101.3.4.2.3": crypto.SHA512,
}

// The PAdES /SubFilter values (ETSI EN 319 142-1) and the PDF date prefix.
const (
	subFilterCAdES = "ETSI.CAdES.detached"
	subFilterPKCS7 = "adbe.pkcs7.detached"
	pdfDatePrefix  = "D:"
)

const (
	// OCSPTimeout bounds one online revocation request: the default client's
	// timeout.
	OCSPTimeout = 10 * time.Second
	// maxResponseBytes caps an OCSP response or a CRL read online.
	maxResponseBytes = 1 << 20
	// maxRevocationAge is how old revocation information without a next
	// update may be, and how far from the signing time embedded information
	// may have been produced.
	maxRevocationAge = 7 * 24 * time.Hour
	// clockSkew is how far in the future a responder's clock may run.
	clockSkew = 5 * time.Minute
)

// TrustSource supplies the trust anchors. Anchors need not be self-signed
// roots: under eIDAS the CA certificate listed on a trusted list is the
// anchor, and Go's x509.Verify accepts any certificate in the Roots pool.
type TrustSource interface {
	// SignerRoots are the anchors accepted for the diploma signer.
	SignerRoots() *x509.CertPool
	// TSARoots are the anchors accepted for RFC 3161 timestamp tokens.
	TSARoots() *x509.CertPool
	// Describe returns a human readable provenance for an anchor, or "".
	Describe(anchor *x509.Certificate) string
}

// Options configures a verification run.
type Options struct {
	// Issuer is the party whose signature is required. Defaults to trust.DUO.
	Issuer *trust.Issuer
	// Trust supplies the anchors. Defaults to the pinned roots in package
	// trust; pass an *eutl.Store to use the EU Trusted Lists.
	Trust TrustSource
	// OCSP checks revocation online instead of against the revocation
	// information embedded in the signature.
	OCSP bool
	// HTTPClient is used for OCSP and CRLs. Defaults to a client with an
	// OCSPTimeout timeout.
	HTTPClient *http.Client
	// Now overrides the wall clock (for tests).
	Now func() time.Time
}

// Result is the outcome of a verification. Valid is only true when every
// check passed; Checks lists each individual check so a UI can show detail.
type Result struct {
	Valid  bool
	Checks []Check

	Document    pdfsig.Info
	Signature   *pdfsig.Signature
	Signer      *x509.Certificate
	Chain       []*x509.Certificate
	SigningTime time.Time
	// TimestampTrusted is true when SigningTime comes from an RFC 3161 token
	// signed by a trusted TSA.
	TimestampTrusted bool
	TSA              *x509.Certificate
	// Anchor describes where the trust anchor for the signer chain came from.
	Anchor string
}

// Check is one verification step. ID is a stable machine readable key (used
// by the API and translated by the frontend), Name a short English label.
type Check struct {
	ID     string `json:"id"`
	Name   string `json:"name"`
	OK     bool   `json:"ok"`
	Detail string `json:"detail"`
}

// The header and media type of an OCSP request (RFC 6960 appendix A).
const (
	headerContentType = "Content-Type"
	ocspRequestMime   = "application/ocsp-request"
)

// Stable check identifiers, in the order the checks run.
const (
	CheckSignaturePresent   = "signature_present"
	CheckSingleSignature    = "single_signature"
	CheckCoversWholeFile    = "covers_whole_file"
	CheckCertification      = "certification_signature"
	CheckPadesSubfilter     = "pades_subfilter"
	CheckSignedByteRange    = "signed_byte_range"
	CheckCmsParse           = "cms_parse"
	CheckCmsSignature       = "cms_signature"
	CheckSigningCertificate = "signing_certificate_v2"
	CheckTimestampToken     = "timestamp_token"
	CheckTimestampAuthority = "timestamp_authority_trusted"
	CheckCertificateChain   = "certificate_chain"
	CheckQualifiedSeal      = "qualified_eseal"
	CheckKeyUsage           = "key_usage_non_repudiation"
	CheckSignerIdentity     = "signer_identity"
	CheckRevocation         = "revocation"
)

// add records c, marking the result invalid when it failed, and reports
// whether it passed.
func (r *Result) add(c Check) bool {
	r.Checks = append(r.Checks, c)
	if !c.OK {
		r.Valid = false
	}
	return c.OK
}

// FirstFailure returns the first check that failed, or nil when all passed.
func (r *Result) FirstFailure() *Check {
	for i := range r.Checks {
		if !r.Checks[i].OK {
			return &r.Checks[i]
		}
	}
	return nil
}

// PDF verifies a diploma PDF given as raw bytes.
func PDF(ctx context.Context, pdf []byte, opts Options) (*Result, error) {
	if opts.Issuer == nil {
		opts.Issuer = &trust.DUO
	}
	if opts.Now == nil {
		opts.Now = time.Now
	}
	if opts.Trust == nil {
		pinned, err := trust.NewPinned()
		if err != nil {
			return nil, err
		}
		opts.Trust = pinned
	}
	if opts.HTTPClient == nil {
		opts.HTTPClient = &http.Client{Timeout: OCSPTimeout}
	}

	res := &Result{Valid: true, Document: pdfsig.ExtractInfo(pdf)}

	// 1. Signature presence and coverage.
	sigs, err := pdfsig.Extract(pdf)
	if err != nil {
		res.add(Check{ID: CheckSignaturePresent, Name: "signature present", Detail: err.Error()})
		return res, nil
	}
	if !res.add(Check{
		ID: CheckSingleSignature, Name: "single signature", OK: len(sigs) == 1,
		Detail: fmt.Sprintf("%d signature(s) found", len(sigs)),
	}) {
		return res, nil
	}
	sig := &sigs[0]
	res.Signature = sig
	res.add(Check{
		ID: CheckCoversWholeFile, Name: "signature covers whole file", OK: sig.CoversWholeFile,
		Detail: fmt.Sprintf("ByteRange %v, file size %d", sig.ByteRange, len(pdf)),
	})
	res.add(Check{
		ID: CheckCertification, Name: "certification signature (DocMDP)", OK: sig.DocMDPPermission == 1,
		Detail: fmt.Sprintf("DocMDP /P = %d (1 = no changes permitted)", sig.DocMDPPermission),
	})
	res.add(Check{
		ID: CheckPadesSubfilter, Name: "PAdES subfilter", OK: sig.SubFilter == subFilterCAdES || sig.SubFilter == subFilterPKCS7,
		Detail: "/SubFilter /" + sig.SubFilter,
	})

	signed, err := sig.SignedBytes(pdf)
	if err != nil {
		res.add(Check{ID: CheckSignedByteRange, Name: "signed byte range", Detail: err.Error()})
		return res, nil
	}

	// 2. CMS signature over the signed bytes.
	p7, err := pkcs7.Parse(sig.CMS)
	if err != nil {
		res.add(Check{ID: CheckCmsParse, Name: "CMS parse", Detail: err.Error()})
		return res, nil
	}
	p7.Content = signed
	// Verify() checks the messageDigest attribute against the content and
	// the signature against the signer certificate found in the CMS. It does
	// not establish trust; we do that ourselves below so we can control the
	// validation time and the key usage.
	if err := p7.Verify(); err != nil {
		res.add(Check{ID: CheckCmsSignature, Name: "CMS signature", Detail: err.Error()})
		return res, nil
	}
	signer := p7.GetOnlySigner()
	if signer == nil {
		res.add(Check{ID: CheckCmsSignature, Name: "CMS signature", Detail: "expected exactly one signer"})
		return res, nil
	}
	res.Signer = signer
	res.add(Check{ID: CheckCmsSignature, Name: "CMS signature", OK: true, Detail: "message digest and signature valid"})
	res.add(checkSigningCertificate(p7, signer))

	// 3. Signing time: the RFC 3161 timestamp token.
	res.SigningTime = opts.Now()
	if t, err := parsePDFDate(sig.SigningTimeClaimed); err == nil {
		res.SigningTime = t
	}
	checkTimestamp(res, p7, sig, opts.Trust)

	// 4. Chain to a trust anchor at signing time.
	checkChain(res, p7, signer, opts.Trust)
	qc := qcStatements(signer)
	res.add(Check{
		ID: CheckQualifiedSeal, Name: "qualified electronic seal certificate", OK: qc.compliance && qc.eseal,
		Detail: fmt.Sprintf("QcCompliance=%v QcType-eseal=%v (ETSI EN 319 412-5)", qc.compliance, qc.eseal),
	})
	res.add(Check{
		ID: CheckKeyUsage, Name: "signer key usage non-repudiation", OK: signer.KeyUsage&x509.KeyUsageContentCommitment != 0,
		Detail: fmt.Sprintf("KeyUsage=%d", signer.KeyUsage),
	})

	// 5. Issuer identity.
	orgID := subjectAttr(signer.Subject, oidOrganizationIdent)
	org := ""
	if len(signer.Subject.Organization) > 0 {
		org = signer.Subject.Organization[0]
	}
	res.add(Check{
		ID: CheckSignerIdentity, Name: "signer is " + opts.Issuer.Name,
		OK:     orgID == opts.Issuer.OrganizationIdentifier && org == opts.Issuer.Organization,
		Detail: fmt.Sprintf("subject O=%q organizationIdentifier=%q", org, orgID),
	})

	// 6. Revocation.
	res.add(checkRevocation(ctx, res, p7, opts))

	return res, nil
}

// essCertIDv2 and signingCertificateV2 are the ESS signing-certificate-v2
// attribute (RFC 5035).
type essCertIDv2 struct {
	HashAlgorithm pkix.AlgorithmIdentifier `asn1:"optional"`
	CertHash      []byte
	IssuerSerial  asn1.RawValue `asn1:"optional"`
}

type signingCertificateV2 struct {
	Certs    []essCertIDv2
	Policies asn1.RawValue `asn1:"optional"`
}

// checkSigningCertificate checks that the signed ESS signing-certificate-v2
// attribute names signer by its hash: it binds the signature to that
// certificate, so another certificate over the same key cannot stand in.
func checkSigningCertificate(p7 *pkcs7.PKCS7, signer *x509.Certificate) Check {
	c := Check{ID: CheckSigningCertificate, Name: "ESS signing-certificate-v2 attribute"}
	var attr signingCertificateV2
	if err := p7.UnmarshalSignedAttribute(oidSigningCertV2, &attr); err != nil {
		c.Detail = "missing or unreadable: " + err.Error()
		return c
	}
	if len(attr.Certs) == 0 {
		c.Detail = "names no certificate"
		return c
	}
	first := attr.Certs[0]
	hash := crypto.SHA256
	if len(first.HashAlgorithm.Algorithm) > 0 {
		h, ok := essHashes[first.HashAlgorithm.Algorithm.String()]
		if !ok {
			c.Detail = "unsupported hash algorithm " + first.HashAlgorithm.Algorithm.String()
			return c
		}
		hash = h
	}
	digest := hash.New()
	digest.Write(signer.Raw)
	c.OK = bytes.Equal(digest.Sum(nil), first.CertHash)
	c.Detail = fmt.Sprintf("%s hash of the signer certificate %s", hash, map[bool]string{true: "matches", false: "does NOT match"}[c.OK])
	return c
}

// checkTimestamp takes the signing time from the RFC 3161 token in p7's
// unsigned attributes. The time counts as trusted only once the token's hash
// matches the signature value and its signer chains to a qualified TSA.
func checkTimestamp(res *Result, p7 *pkcs7.PKCS7, sig *pdfsig.Signature, trustSource TrustSource) {
	tok := unsignedAttr(p7, oidTimeStampToken)
	if tok == nil {
		res.add(Check{
			ID: CheckTimestampToken, Name: "timestamp token",
			Detail: "no RFC 3161 timestamp; the remaining checks use the signer-claimed time /M=" + sig.SigningTimeClaimed,
		})
		return
	}
	ts, err := timestamp.Parse(tok)
	if err != nil {
		res.add(Check{ID: CheckTimestampToken, Name: "timestamp token", Detail: err.Error()})
		return
	}

	h := ts.HashAlgorithm.New()
	h.Write(p7.Signers[0].EncryptedDigest)
	match := bytes.Equal(h.Sum(nil), ts.HashedMessage)
	if !res.add(Check{
		ID: CheckTimestampToken, Name: "timestamp token", OK: match,
		Detail: fmt.Sprintf("RFC 3161 token from %s, hash of signature value %s",
			ts.Time.UTC().Format(time.RFC3339), map[bool]string{true: "matches", false: "MISMATCH"}[match]),
	}) {
		return
	}

	res.TSA = tsaSigner(tok)
	if !res.add(Check{
		ID: CheckTimestampAuthority, Name: "timestamp authority trusted",
		OK: verifyTSA(ts, res.TSA, trustSource.TSARoots(), p7.Certificates), Detail: tsaDetail(res.TSA, ts),
	}) {
		return
	}
	res.SigningTime = ts.Time
	res.TimestampTrusted = true
}

// checkChain verifies signer up to a trust anchor at the signing time.
func checkChain(res *Result, p7 *pkcs7.PKCS7, signer *x509.Certificate, trustSource TrustSource) {
	inter := x509.NewCertPool()
	for _, c := range p7.Certificates {
		if !c.Equal(signer) {
			inter.AddCert(c)
		}
	}
	chains, err := signer.Verify(x509.VerifyOptions{
		Roots:         trustSource.SignerRoots(),
		Intermediates: inter,
		CurrentTime:   res.SigningTime,
		// The signer cert carries the Microsoft "Document Signing" EKU, which
		// Go does not recognise; accept any EKU and check KeyUsage ourselves.
		KeyUsages: []x509.ExtKeyUsage{x509.ExtKeyUsageAny},
	})
	if err != nil {
		res.add(Check{ID: CheckCertificateChain, Name: "certificate chain to trust anchor", Detail: err.Error()})
		return
	}

	res.Chain = chains[0]
	anchor := chains[0][len(chains[0])-1]
	res.Anchor = trustSource.Describe(anchor)
	if res.Anchor == "" {
		res.Anchor = fmt.Sprintf("pinned %q", anchor.Subject.CommonName)
	}
	res.add(Check{
		ID: CheckCertificateChain, Name: "certificate chain to trust anchor", OK: true,
		Detail: fmt.Sprintf("%d certificates, anchor %q, valid at %s", len(chains[0]),
			anchor.Subject.CommonName, res.SigningTime.UTC().Format(time.RFC3339)),
	})
}

// checkRevocation checks every certificate of the chain below its anchor,
// online with opts.OCSP and otherwise against the signature's embedded
// revocation information. It fails closed: a certificate it cannot check is
// a failure, never a pass.
func checkRevocation(ctx context.Context, res *Result, p7 *pkcs7.PKCS7, opts Options) Check {
	c := Check{ID: CheckRevocation, Name: "revocation"}
	if len(res.Chain) < 2 {
		c.Detail = "no verified chain to check"
		return c
	}

	var archive revocationArchive
	if !opts.OCSP {
		var err error
		if archive, err = embeddedRevocation(p7); err != nil {
			c.Detail = "online revocation check disabled and " + err.Error()
			return c
		}
	}

	var details []string
	for i := 0; i < len(res.Chain)-1; i++ {
		cert, issuer := res.Chain[i], res.Chain[i+1]
		var detail string
		var err error
		if opts.OCSP {
			detail, err = checkOnline(ctx, opts.HTTPClient, cert, issuer, opts.Now())
		} else {
			detail, err = archive.check(cert, issuer, res.SigningTime)
		}
		if err != nil {
			c.Detail = fmt.Sprintf("%q: %v", cert.Subject.CommonName, err)
			return c
		}
		details = append(details, fmt.Sprintf("%q: %s", cert.Subject.CommonName, detail))
	}
	c.OK = true
	c.Detail = fmt.Sprintf("%v", details)
	return c
}

// revocationArchive is the Adobe revocation-info archival attribute (PDF 1.7,
// 12.8.3.3.2): the CRLs and OCSP responses the signer embedded for long-term
// validation.
type revocationArchive struct {
	CRL   []asn1.RawValue `asn1:"explicit,optional,tag:0"`
	OCSP  []asn1.RawValue `asn1:"explicit,optional,tag:1"`
	Other []asn1.RawValue `asn1:"explicit,optional,tag:2"`
}

func embeddedRevocation(p7 *pkcs7.PKCS7) (revocationArchive, error) {
	var archive revocationArchive
	if err := p7.UnmarshalSignedAttribute(oidAdobeRevocationInf, &archive); err != nil {
		return archive, fmt.Errorf("no embedded revocation information: %w", err)
	}
	return archive, nil
}

// check finds cert's status in the archive: a good OCSP response or a CRL by
// issuer that does not list it, produced within maxRevocationAge of the
// signing time.
func (a revocationArchive) check(cert, issuer *x509.Certificate, signingTime time.Time) (string, error) {
	for _, raw := range a.OCSP {
		resp, err := ocsp.ParseResponseForCert(raw.FullBytes, cert, issuer)
		if err != nil {
			continue
		}
		if err := nearSigning(resp.ThisUpdate, signingTime); err != nil {
			return "", fmt.Errorf("embedded OCSP response: %w", err)
		}
		return ocspStatus(resp, "embedded OCSP response")
	}
	for _, raw := range a.CRL {
		crl, err := x509.ParseRevocationList(raw.FullBytes)
		if err != nil || crl.CheckSignatureFrom(issuer) != nil {
			continue
		}
		if err := nearSigning(crl.ThisUpdate, signingTime); err != nil {
			return "", fmt.Errorf("embedded CRL: %w", err)
		}
		return crlStatus(crl, cert, "embedded CRL")
	}
	return "", errors.New("no embedded OCSP response or CRL covers it")
}

// nearSigning requires revocation information produced around the signing
// time: information from long before or after says nothing about it.
func nearSigning(produced, signingTime time.Time) error {
	if d := produced.Sub(signingTime).Abs(); d > maxRevocationAge {
		return fmt.Errorf("produced %s, %s from the signing time", produced.UTC().Format(time.RFC3339), d.Round(time.Hour))
	}
	return nil
}

// checkOnline asks cert's OCSP responder, or fetches its CRL when it names
// no responder.
func checkOnline(ctx context.Context, hc *http.Client, cert, issuer *x509.Certificate, now time.Time) (string, error) {
	if len(cert.OCSPServer) > 0 {
		resp, err := fetchOCSP(ctx, hc, cert, issuer)
		if err != nil {
			return "", err
		}
		if err := current(resp.ThisUpdate, resp.NextUpdate, now); err != nil {
			return "", fmt.Errorf("ocsp response: %w", err)
		}
		return ocspStatus(resp, "OCSP "+cert.OCSPServer[0])
	}
	if len(cert.CRLDistributionPoints) > 0 {
		crl, err := fetchCRL(ctx, hc, cert.CRLDistributionPoints[0], issuer)
		if err != nil {
			return "", err
		}
		if err := current(crl.ThisUpdate, crl.NextUpdate, now); err != nil {
			return "", fmt.Errorf("crl: %w", err)
		}
		return crlStatus(crl, cert, "CRL "+cert.CRLDistributionPoints[0])
	}
	return "", errors.New("certificate names neither an OCSP responder nor a CRL")
}

// current requires revocation information that is in force at now: not from
// the future, and not past its next update (or, without one, not older than
// maxRevocationAge). An old "good" answer replayed is no answer.
func current(thisUpdate, nextUpdate, now time.Time) error {
	switch {
	case thisUpdate.After(now.Add(clockSkew)):
		return fmt.Errorf("this update %s is in the future", thisUpdate.UTC().Format(time.RFC3339))
	case !nextUpdate.IsZero() && now.After(nextUpdate):
		return fmt.Errorf("expired at %s", nextUpdate.UTC().Format(time.RFC3339))
	case nextUpdate.IsZero() && now.Sub(thisUpdate) > maxRevocationAge:
		return fmt.Errorf("this update %s is too old", thisUpdate.UTC().Format(time.RFC3339))
	}
	return nil
}

func ocspStatus(resp *ocsp.Response, source string) (string, error) {
	switch resp.Status {
	case ocsp.Good:
		return fmt.Sprintf("good (%s, this update %s)", source, resp.ThisUpdate.UTC().Format(time.RFC3339)), nil
	case ocsp.Revoked:
		return "", fmt.Errorf("certificate REVOKED at %s (reason %d, %s)", resp.RevokedAt.UTC().Format(time.RFC3339), resp.RevocationReason, source)
	default:
		return "", fmt.Errorf("%s returned unknown status", source)
	}
}

func crlStatus(crl *x509.RevocationList, cert *x509.Certificate, source string) (string, error) {
	for _, entry := range crl.RevokedCertificateEntries {
		if entry.SerialNumber.Cmp(cert.SerialNumber) == 0 {
			return "", fmt.Errorf("certificate REVOKED at %s (%s)", entry.RevocationTime.UTC().Format(time.RFC3339), source)
		}
	}
	return fmt.Sprintf("not revoked (%s, this update %s)", source, crl.ThisUpdate.UTC().Format(time.RFC3339)), nil
}

func fetchOCSP(ctx context.Context, hc *http.Client, cert, issuer *x509.Certificate) (*ocsp.Response, error) {
	req, err := ocsp.CreateRequest(cert, issuer, &ocsp.RequestOptions{Hash: crypto.SHA1})
	if err != nil {
		return nil, err
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, cert.OCSPServer[0], bytes.NewReader(req))
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set(headerContentType, ocspRequestMime)
	body, err := fetch(hc, httpReq)
	if err != nil {
		return nil, err
	}
	resp, err := ocsp.ParseResponseForCert(body, cert, issuer)
	if err != nil {
		return nil, fmt.Errorf("ocsp: %w", err)
	}
	return resp, nil
}

func fetchCRL(ctx context.Context, hc *http.Client, url string, issuer *x509.Certificate) (*x509.RevocationList, error) {
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	body, err := fetch(hc, httpReq)
	if err != nil {
		return nil, err
	}
	crl, err := x509.ParseRevocationList(body)
	if err != nil {
		return nil, fmt.Errorf("crl: %w", err)
	}
	if err := crl.CheckSignatureFrom(issuer); err != nil {
		return nil, fmt.Errorf("crl signature: %w", err)
	}
	return crl, nil
}

func fetch(hc *http.Client, req *http.Request) ([]byte, error) {
	resp, err := hc.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("%s %s: %s", req.Method, req.URL.Redacted(), resp.Status)
	}
	return io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes))
}

func unsignedAttr(p7 *pkcs7.PKCS7, oid asn1.ObjectIdentifier) []byte {
	if len(p7.Signers) == 0 {
		return nil
	}
	for _, a := range p7.Signers[0].UnauthenticatedAttributes {
		if a.Type.Equal(oid) {
			// Value is a SET containing one element; return that element's DER.
			var inner asn1.RawValue
			if _, err := asn1.Unmarshal(a.Value.Bytes, &inner); err == nil {
				return inner.FullBytes
			}
			return a.Value.FullBytes
		}
	}
	return nil
}

func subjectAttr(n pkix.Name, oid asn1.ObjectIdentifier) string {
	for _, atv := range n.Names {
		if atv.Type.Equal(oid) {
			if s, ok := atv.Value.(string); ok {
				return s
			}
		}
	}
	return ""
}

type qcInfo struct{ compliance, eseal bool }

// qcStatements parses the ETSI QcStatements extension (RFC 3739 / EN 319 412-5).
func qcStatements(c *x509.Certificate) qcInfo {
	var info qcInfo
	for _, ext := range c.Extensions {
		if !ext.Id.Equal(oidQCStatements) {
			continue
		}
		var stmts []struct {
			ID   asn1.ObjectIdentifier
			Info asn1.RawValue `asn1:"optional"`
		}
		if _, err := asn1.Unmarshal(ext.Value, &stmts); err != nil {
			return info
		}
		for _, st := range stmts {
			switch {
			case st.ID.Equal(oidQcCompliance):
				info.compliance = true
			case st.ID.Equal(oidQcType):
				var types []asn1.ObjectIdentifier
				if _, err := asn1.Unmarshal(st.Info.FullBytes, &types); err == nil {
					for _, t := range types {
						if t.Equal(oidQcTypeESeal) {
							info.eseal = true
						}
					}
				}
			}
		}
	}
	return info
}

// tsaSigner is the certificate that signed the timestamp token: the one its
// SignerInfo names by issuer and serial, which timestamp.Parse checked the
// token's signature against. Not any other certificate the token carries:
// the token sits outside the PDF's signed bytes, so its certificate list is
// the uploader's to fill. Nil when the token has no single signer.
func tsaSigner(tok []byte) *x509.Certificate {
	p7, err := pkcs7.Parse(tok)
	if err != nil {
		return nil
	}
	return p7.GetOnlySigner()
}

// verifyTSA checks that leaf, the token's signer (tsaSigner), chains to a
// qualified TSA anchor, was valid at the asserted time, and is allowed to
// timestamp. timestamp.Parse already checked the token signature itself.
func verifyTSA(ts *timestamp.Timestamp, leaf *x509.Certificate, roots *x509.CertPool, extra []*x509.Certificate) bool {
	if leaf == nil {
		return false
	}
	inter := x509.NewCertPool()
	for _, c := range ts.Certificates {
		if c != leaf {
			inter.AddCert(c)
		}
	}
	for _, c := range extra {
		inter.AddCert(c)
	}
	_, err := leaf.Verify(x509.VerifyOptions{
		Roots:         roots,
		Intermediates: inter,
		CurrentTime:   ts.Time,
		KeyUsages:     []x509.ExtKeyUsage{x509.ExtKeyUsageTimeStamping},
	})
	return err == nil
}

func tsaDetail(leaf *x509.Certificate, ts *timestamp.Timestamp) string {
	if leaf == nil {
		return "token carries no TSA certificate"
	}
	q := ""
	if ts.Qualified {
		q = ", token claims qualified status"
	}
	return fmt.Sprintf("TSA %q (%s)%s", leaf.Subject.CommonName, firstOr(leaf.Subject.Organization, "?"), q)
}

func firstOr(s []string, d string) string {
	if len(s) > 0 {
		return s[0]
	}
	return d
}

// parsePDFDate parses "D:YYYYMMDDHHmmSS+HH'mm'" style dates.
func parsePDFDate(s string) (time.Time, error) {
	if len(s) < 16 || s[:len(pdfDatePrefix)] != pdfDatePrefix {
		return time.Time{}, errors.New("not a PDF date")
	}
	s = s[len(pdfDatePrefix):]
	layout := "20060102150405"
	base := s[:14]
	rest := s[14:]
	if rest == "" || rest == "Z" {
		return time.Parse(layout, base)
	}
	// +HH'mm' -> +HHmm
	tz := make([]byte, 0, 5)
	for _, c := range rest {
		if c != '\'' {
			tz = append(tz, byte(c))
		}
	}
	return time.Parse(layout+"-0700", base+string(tz))
}
