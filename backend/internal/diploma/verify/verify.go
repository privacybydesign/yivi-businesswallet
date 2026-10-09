// Package verify checks the authenticity of a DUO diploma PDF.
//
// The checks, in order:
//
//  1. The PDF carries exactly one signature and its ByteRange covers the whole
//     file. Anything outside the ByteRange is unsigned, so a file that has been
//     "updated" after signing is rejected.
//  2. The CMS SignedData in /Contents is a valid detached signature over the
//     signed bytes (message digest and signature check).
//  3. The signer certificate chains to a trust anchor, evaluated at the
//     signing time. Anchors come from a TrustSource: by default the EU
//     Trusted Lists (eIDAS), or the pinned PKIoverheid root when offline.
//  4. The signing time is taken from the RFC 3161 timestamp token if present
//     (and the token's hash must match the signature value); otherwise the
//     signer-claimed time is used and reported as untrusted.
//  5. The signer's subject matches the expected issuer (DUO's KvK number).
//  6. Optionally (DIPLOMA_OCSP), an online OCSP check of the signer
//     certificate.
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
	// OCSP enables an online revocation check of the signer certificate.
	OCSP bool
	// HTTPClient is used for OCSP. Defaults to a client with a 10s timeout.
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
	// TimestampTrusted is true when SigningTime comes from an RFC 3161 token.
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
	CheckRevocation         = "ocsp"
)

func (r *Result) add(id, name string, ok bool, detail string) bool {
	r.Checks = append(r.Checks, Check{ID: id, Name: name, OK: ok, Detail: detail})
	if !ok {
		r.Valid = false
	}
	return ok
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
		opts.HTTPClient = &http.Client{Timeout: 10 * time.Second}
	}

	res := &Result{Valid: true, Document: pdfsig.ExtractInfo(pdf)}

	// 1. Signature presence and coverage.
	sigs, err := pdfsig.Extract(pdf)
	if err != nil {
		res.add(CheckSignaturePresent, "signature present", false, err.Error())
		return res, nil
	}
	if !res.add(CheckSingleSignature, "single signature", len(sigs) == 1, fmt.Sprintf("%d signature(s) found", len(sigs))) {
		return res, nil
	}
	sig := &sigs[0]
	res.Signature = sig
	res.add(CheckCoversWholeFile, "signature covers whole file", sig.CoversWholeFile,
		fmt.Sprintf("ByteRange %v, file size %d", sig.ByteRange, len(pdf)))
	res.add(CheckCertification, "certification signature (DocMDP)", sig.DocMDPPermission == 1,
		fmt.Sprintf("DocMDP /P = %d (1 = no changes permitted)", sig.DocMDPPermission))
	res.add(CheckPadesSubfilter, "PAdES subfilter", sig.SubFilter == "ETSI.CAdES.detached" || sig.SubFilter == "adbe.pkcs7.detached",
		"/SubFilter /"+sig.SubFilter)

	signed, err := sig.SignedBytes(pdf)
	if err != nil {
		res.add(CheckSignedByteRange, "signed byte range", false, err.Error())
		return res, nil
	}

	// 2. CMS signature over the signed bytes.
	p7, err := pkcs7.Parse(sig.CMS)
	if err != nil {
		res.add(CheckCmsParse, "CMS parse", false, err.Error())
		return res, nil
	}
	p7.Content = signed
	// Verify() checks the messageDigest attribute against the content and
	// the signature against the signer certificate found in the CMS. It does
	// not establish trust; we do that ourselves below so we can control the
	// validation time and the key usage.
	if err := p7.Verify(); err != nil {
		res.add(CheckCmsSignature, "CMS signature", false, err.Error())
		return res, nil
	}
	signer := p7.GetOnlySigner()
	if signer == nil {
		res.add(CheckCmsSignature, "CMS signature", false, "expected exactly one signer")
		return res, nil
	}
	res.Signer = signer
	res.add(CheckCmsSignature, "CMS signature", true, "message digest and RSA signature valid")

	var sigCertV2 asn1.RawValue
	res.add(CheckSigningCertificate, "ESS signing-certificate-v2 attribute", p7.UnmarshalSignedAttribute(oidSigningCertV2, &sigCertV2) == nil,
		"binds the signature to the signer certificate (PAdES baseline requirement)")

	// 3./4. Signing time: prefer the RFC 3161 timestamp token.
	res.SigningTime = opts.Now()
	if tok := unsignedAttr(p7, oidTimeStampToken); tok != nil {
		ts, err := timestamp.Parse(tok)
		if err != nil {
			res.add(CheckTimestampToken, "timestamp token", false, err.Error())
		} else {
			h := ts.HashAlgorithm.New()
			h.Write(p7.Signers[0].EncryptedDigest)
			match := bytes.Equal(h.Sum(nil), ts.HashedMessage)
			res.add(CheckTimestampToken, "timestamp token", match,
				fmt.Sprintf("RFC 3161 token from %s, hash of signature value %s",
					ts.Time.UTC().Format(time.RFC3339), map[bool]string{true: "matches", false: "MISMATCH"}[match]))
			if match {
				res.SigningTime = ts.Time
				res.TimestampTrusted = true
				res.TSA = tsaSigner(tok)
				res.add(CheckTimestampAuthority, "timestamp authority trusted", verifyTSA(ts, res.TSA, opts.Trust.TSARoots(), p7.Certificates),
					tsaDetail(res.TSA, ts))
			}
		}
	} else {
		res.add(CheckTimestampToken, "timestamp token", false, "no RFC 3161 timestamp; falling back to signer-claimed time /M="+sig.SigningTimeClaimed)
		if t, err := parsePDFDate(sig.SigningTimeClaimed); err == nil {
			res.SigningTime = t
		}
	}

	// 3. Chain to pinned root at signing time.
	inter := x509.NewCertPool()
	for _, c := range p7.Certificates {
		if !c.Equal(signer) {
			inter.AddCert(c)
		}
	}
	chains, err := signer.Verify(x509.VerifyOptions{
		Roots:         opts.Trust.SignerRoots(),
		Intermediates: inter,
		CurrentTime:   res.SigningTime,
		// The signer cert carries the Microsoft "Document Signing" EKU, which
		// Go does not recognise; accept any EKU and check KeyUsage ourselves.
		KeyUsages: []x509.ExtKeyUsage{x509.ExtKeyUsageAny},
	})
	if err != nil {
		res.add(CheckCertificateChain, "certificate chain to trust anchor", false, err.Error())
	} else {
		res.Chain = chains[0]
		anchor := chains[0][len(chains[0])-1]
		res.Anchor = opts.Trust.Describe(anchor)
		if res.Anchor == "" {
			res.Anchor = fmt.Sprintf("pinned %q", anchor.Subject.CommonName)
		}
		res.add(CheckCertificateChain, "certificate chain to trust anchor", true,
			fmt.Sprintf("%d certificates, anchor %q, valid at %s", len(chains[0]),
				anchor.Subject.CommonName, res.SigningTime.UTC().Format(time.RFC3339)))
	}
	qc := qcStatements(signer)
	res.add(CheckQualifiedSeal, "qualified electronic seal certificate", qc.compliance && qc.eseal,
		fmt.Sprintf("QcCompliance=%v QcType-eseal=%v (ETSI EN 319 412-5)", qc.compliance, qc.eseal))
	res.add(CheckKeyUsage, "signer key usage non-repudiation", signer.KeyUsage&x509.KeyUsageContentCommitment != 0,
		fmt.Sprintf("KeyUsage=%d", signer.KeyUsage))

	// 5. Issuer identity.
	orgID := subjectAttr(signer.Subject, oidOrganizationIdent)
	org := ""
	if len(signer.Subject.Organization) > 0 {
		org = signer.Subject.Organization[0]
	}
	res.add(CheckSignerIdentity, "signer is "+opts.Issuer.Name,
		orgID == opts.Issuer.OrganizationIdentifier && org == opts.Issuer.Organization,
		fmt.Sprintf("subject O=%q organizationIdentifier=%q", org, orgID))

	// 6. Revocation (optional, online).
	if opts.OCSP {
		if len(res.Chain) < 2 {
			res.add(CheckRevocation, "OCSP", false, "no issuer certificate available")
		} else {
			status, err := checkOCSP(ctx, opts.HTTPClient, signer, res.Chain[1])
			if err != nil {
				status = err.Error()
			}
			res.add(CheckRevocation, "OCSP", err == nil, status)
		}
	} else {
		note := "skipped (online revocation check disabled)"
		var raw asn1.RawValue
		if p7.UnmarshalSignedAttribute(oidAdobeRevocationInf, &raw) == nil {
			note += "; signed Adobe revocation-info attribute present for offline (LTV) validation"
		}
		res.Checks = append(res.Checks, Check{ID: CheckRevocation, Name: "OCSP", OK: true, Detail: note})
	}

	return res, nil
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
// pinned TSA root, was valid at the asserted time, and is allowed to
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

func checkOCSP(ctx context.Context, hc *http.Client, cert, issuer *x509.Certificate) (string, error) {
	if len(cert.OCSPServer) == 0 {
		return "", errors.New("certificate has no OCSP responder URL")
	}
	req, err := ocsp.CreateRequest(cert, issuer, &ocsp.RequestOptions{Hash: crypto.SHA1})
	if err != nil {
		return "", err
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, cert.OCSPServer[0], bytes.NewReader(req))
	if err != nil {
		return "", err
	}
	httpReq.Header.Set(headerContentType, ocspRequestMime)
	httpResp, err := hc.Do(httpReq)
	if err != nil {
		return "", err
	}
	defer func() { _ = httpResp.Body.Close() }()
	body, err := io.ReadAll(io.LimitReader(httpResp.Body, 1<<20))
	if err != nil {
		return "", err
	}
	resp, err := ocsp.ParseResponseForCert(body, cert, issuer)
	if err != nil {
		return "", fmt.Errorf("ocsp: %w", err)
	}
	switch resp.Status {
	case ocsp.Good:
		return fmt.Sprintf("good (responder %s, this update %s)", cert.OCSPServer[0], resp.ThisUpdate.UTC().Format(time.RFC3339)), nil
	case ocsp.Revoked:
		return "", fmt.Errorf("certificate REVOKED at %s (reason %d)", resp.RevokedAt.UTC().Format(time.RFC3339), resp.RevocationReason)
	default:
		return "", errors.New("ocsp responder returned unknown status")
	}
}

// parsePDFDate parses "D:YYYYMMDDHHmmSS+HH'mm'" style dates.
func parsePDFDate(s string) (time.Time, error) {
	if len(s) < 16 || s[:2] != "D:" {
		return time.Time{}, errors.New("not a PDF date")
	}
	s = s[2:]
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
