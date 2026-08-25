package eudiholder

import (
	"bytes"
	"crypto/x509"
	"fmt"

	eudijwt "github.com/privacybydesign/irmago/eudi/jwt"
	"github.com/privacybydesign/irmago/eudi/utils"
)

// mergeTrustChain returns a verification context that trusts everything base
// trusts *plus* every CA in pemChain. It is additive on purpose: a partner's
// trust anchor (ver.iD's dev root, say) has to coexist with irmago's built-in
// trust model and its staging anchors, because one deployment receives
// credentials from both. Replacing the built-in model with the configured PEM —
// which is what irmago's own CreateX509VerifyOptionsFromCertChain amounts to
// here — would silently stop every other issuer from verifying.
//
// Certificates are classified by signature, not by position: a self-signed cert
// is a root, anything else an intermediate. irmago's helper instead takes
// certs[0] as the root and files certs[1:] as intermediates, so a PEM carrying
// two independent roots misplaces the second one into the intermediate pool,
// where nothing can ever chain to it. Classifying lets one PEM carry several
// partners' anchors, in any order, each with its own intermediates.
//
// The base template also carries the KeyUsages the built-in path verifies under
// (ExtKeyUsageAny — irmago checks the digitalSignature key usage itself), which
// is inherited here rather than narrowed to ExtKeyUsageClientAuth.
func mergeTrustChain(base eudijwt.X509VerificationContext, pemChain []byte) (eudijwt.X509VerificationContext, error) {
	certs, err := utils.ParsePemCertificateChain(pemChain)
	if err != nil {
		return nil, fmt.Errorf("parse trust chain: %w", err)
	}
	if len(certs) == 0 {
		return nil, fmt.Errorf("trust chain contains no CERTIFICATE blocks")
	}

	opts := base.GetVerificationOptionsTemplate()
	roots := clonePool(opts.Roots)
	intermediates := clonePool(opts.Intermediates)
	for _, cert := range certs {
		if isSelfSigned(cert) {
			roots.AddCert(cert)
		} else {
			intermediates.AddCert(cert)
		}
	}
	opts.Roots = roots
	opts.Intermediates = intermediates

	return &eudijwt.StaticVerificationContext{
		VerifyOpts:      opts,
		RevocationLists: base.GetRevocationLists(),
	}, nil
}

// clonePool copies pool so adding the configured anchors cannot mutate the
// trust model's own pools, which are shared across redemptions.
func clonePool(pool *x509.CertPool) *x509.CertPool {
	if pool == nil {
		return x509.NewCertPool()
	}
	return pool.Clone()
}

// isSelfSigned reports whether cert is its own issuer. It verifies the signature
// directly rather than going through CheckSignatureFrom, which additionally
// rejects a parent without CA basic constraints — true of plenty of self-signed
// certificates, and a distinction that belongs to chain building, not to sorting
// the configured material into the right pool.
func isSelfSigned(cert *x509.Certificate) bool {
	if !bytes.Equal(cert.RawIssuer, cert.RawSubject) {
		return false
	}
	return cert.CheckSignature(cert.SignatureAlgorithm, cert.RawTBSCertificate, cert.Signature) == nil
}
