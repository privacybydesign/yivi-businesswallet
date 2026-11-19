package signing

import (
	"bytes"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/asn1"
	"math/big"
	"os"
	"testing"
	"time"

	"github.com/digitorus/pdfsign/verify"

	"github.com/privacybydesign/yivi-businesswallet/backend/internal/signingprovider"
)

// A credential from the wallet's own signing service (internal/yivicsc) is not
// the stub's lone self-signed certificate: it is a leaf with a non-repudiation
// key usage and the person's name parts in its subject, under a certificate
// authority, and the chain has both. This puts a credential of that shape through
// the real signing pass, so a profile pdfsign cannot embed, or one it would go
// to the network for, fails here rather than at the first real signature.
func TestPAdESSignsWithAnAuthorityIssuedCredential(t *testing.T) {
	now := time.Now()
	caKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("authority key: %v", err)
	}
	caTemplate := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "Test Signing CA"},
		NotBefore:             now.Add(-time.Hour),
		NotAfter:              now.Add(24 * time.Hour),
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageCRLSign,
		BasicConstraintsValid: true,
		IsCA:                  true,
		MaxPathLenZero:        true,
	}
	caDER, err := x509.CreateCertificate(rand.Reader, caTemplate, caTemplate, &caKey.PublicKey, caKey)
	if err != nil {
		t.Fatalf("authority certificate: %v", err)
	}
	ca, err := x509.ParseCertificate(caDER)
	if err != nil {
		t.Fatalf("parse authority certificate: %v", err)
	}

	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("signing key: %v", err)
	}
	leafTemplate := &x509.Certificate{
		SerialNumber: big.NewInt(2),
		Subject: pkix.Name{
			CommonName: "Petra van der Berg",
			ExtraNames: []pkix.AttributeTypeAndValue{
				{Type: asn1.ObjectIdentifier{2, 5, 4, 42}, Value: "Petra"},
				{Type: asn1.ObjectIdentifier{2, 5, 4, 4}, Value: "van der Berg"},
			},
		},
		NotBefore:             now.Add(-time.Hour),
		NotAfter:              now.Add(24 * time.Hour),
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageContentCommitment,
		BasicConstraintsValid: true,
	}
	leafDER, err := x509.CreateCertificate(rand.Reader, leafTemplate, ca, &key.PublicKey, caKey)
	if err != nil {
		t.Fatalf("signing certificate: %v", err)
	}
	leaf, err := x509.ParseCertificate(leafDER)
	if err != nil {
		t.Fatalf("parse signing certificate: %v", err)
	}
	cred := signingprovider.Credential{
		ID: "credential", Certificate: leaf, Chain: []*x509.Certificate{leaf, ca},
		KeyAlgo: []string{"1.2.840.10045.2.1", signingprovider.SignAlgoECDSASHA256OID},
	}

	pdf, err := os.ReadFile("testdata/sample.pdf")
	if err != nil {
		t.Fatalf("read sample pdf: %v", err)
	}
	sess, digest, err := startPAdES(pdf, cred, nil)
	if err != nil {
		t.Fatalf("startPAdES: %v", err)
	}
	// What signHash does: a raw ECDSA signature over the digest.
	sig, err := ecdsa.SignASN1(rand.Reader, key, digest)
	if err != nil {
		t.Fatalf("sign digest: %v", err)
	}
	signed, err := sess.finish(sig)
	if err != nil {
		t.Fatalf("finish PAdES: %v", err)
	}

	result, err := verify.Verify(bytes.NewReader(signed), int64(len(signed)))
	if err != nil {
		t.Fatalf("verify signed PDF: %v", err)
	}
	if len(result.Signers) != 1 || !result.Signers[0].ValidSignature {
		t.Fatalf("signers = %+v, want one valid signature", result.Signers)
	}
	if got := result.Signers[0].Name; got != "Petra van der Berg" {
		t.Errorf("signature names %q, want the certificate's common name", got)
	}
}
