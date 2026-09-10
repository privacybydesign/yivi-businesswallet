package eudiholder

import (
	"crypto/x509"
	"fmt"

	"github.com/privacybydesign/irmago/eudi"
	eudijwt "github.com/privacybydesign/irmago/eudi/jwt"
)

// NewVerifierTrust builds the X.509 trust material a signed OpenID4VP
// Authorization Request's x5c chain is verified against: irmago's pinned Yivi
// relying-party anchors (production always, staging when stagingAnchors is set —
// the same switch the holder's issuer trust uses, since both are one Yivi PKI
// per environment), with extraPEM's CAs merged on top the way the holder's
// issuer trust merges a partner chain (see mergeTrustChain). Verifier trust is a
// deployment-level fact — a request is validated before any organization is
// known — so unlike the issuer trust it does not live in per-org storage.
//
// The template's KeyUsages is ExtKeyUsageAny, as irmago's own trust model sets
// it: x509.Verify would otherwise demand serverAuth, and relying-party leaf
// certificates carry clientAuth; the digitalSignature check is irmago's.
func NewVerifierTrust(extraPEM []byte, stagingAnchors bool) (eudijwt.X509VerificationContext, error) {
	anchors := [][]byte{[]byte(eudi.Production_Yivi_VerifierTrustAnchor)}
	if stagingAnchors {
		anchors = append(anchors, []byte(eudi.Staging_Yivi_VerifierTrustAnchor))
	}
	trust, err := staticTrust(anchors, extraPEM)
	if err != nil {
		return nil, fmt.Errorf("eudiholder: verifier trust: %w", err)
	}
	return trust, nil
}

// NewIssuerTrust is NewVerifierTrust's counterpart for credential issuers: the
// pinned Yivi attestation-provider anchors plus extraPEM, without per-org
// storage. The holder engine itself keeps using irmago's storage-backed trust
// model (Engine.trustContext); this static form is for a verifier-side check of
// a presented SD-JWT VC, such as the dev verifier's.
func NewIssuerTrust(extraPEM []byte, stagingAnchors bool) (eudijwt.X509VerificationContext, error) {
	anchors := [][]byte{[]byte(eudi.Production_Yivi_IssuerTrustAnchor)}
	if stagingAnchors {
		anchors = append(anchors, []byte(eudi.Staging_Yivi_IssuerTrustAnchor))
	}
	trust, err := staticTrust(anchors, extraPEM)
	if err != nil {
		return nil, fmt.Errorf("eudiholder: issuer trust: %w", err)
	}
	return trust, nil
}

func staticTrust(anchors [][]byte, extraPEM []byte) (eudijwt.X509VerificationContext, error) {
	var trust eudijwt.X509VerificationContext = &eudijwt.StaticVerificationContext{
		VerifyOpts: x509.VerifyOptions{
			Roots:         x509.NewCertPool(),
			Intermediates: x509.NewCertPool(),
			KeyUsages:     []x509.ExtKeyUsage{x509.ExtKeyUsageAny},
		},
	}
	if len(extraPEM) > 0 {
		anchors = append(anchors, extraPEM)
	}
	for _, pem := range anchors {
		merged, err := mergeTrustChain(trust, pem)
		if err != nil {
			return nil, err
		}
		trust = merged
	}
	return trust, nil
}
