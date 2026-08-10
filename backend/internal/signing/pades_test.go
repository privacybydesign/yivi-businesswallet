package signing

import (
	"bytes"
	"context"
	"encoding/base64"
	"os"
	"testing"

	"github.com/digitorus/pdfsign/verify"

	"github.com/privacybydesign/yivi-businesswallet/backend/internal/signingprovider"
)

// TestPAdESExternalSigningProducesVerifiablePDF drives the whole external-signing
// mechanism DB-free: prepare a PDF (pass 1) to capture the digest, sign that
// digest with the stub QTSP (as the real signHash does), embed it, and confirm
// the resulting PAdES signature verifies. This is the Phase C crux — that a
// signature produced across the interactive gap still validates.
func TestPAdESExternalSigningProducesVerifiablePDF(t *testing.T) {
	stub, err := signingprovider.NewStubProvider()
	if err != nil {
		t.Fatalf("stub provider: %v", err)
	}
	ctx := context.Background()
	cred, err := stub.CredentialInfo(ctx, "", "", "stub-credential")
	if err != nil {
		t.Fatalf("credential info: %v", err)
	}

	pdf, err := os.ReadFile("testdata/sample.pdf")
	if err != nil {
		t.Fatalf("read sample pdf: %v", err)
	}

	sess, digest, err := startPAdES(pdf, cred, nil)
	if err != nil {
		t.Fatalf("startPAdES: %v", err)
	}
	if len(digest) != 32 {
		t.Fatalf("expected a 32-byte SHA-256 digest, got %d", len(digest))
	}

	// The QTSP signs the captured digest (raw ECDSA over the hash), exactly the
	// signHash contract.
	sigs, err := stub.SignHash(ctx, "", "", cred.ID,
		[]string{base64.StdEncoding.EncodeToString(digest)},
		signingprovider.SignAlgoECDSASHA256OID, signingprovider.HashAlgoSHA256OID)
	if err != nil {
		t.Fatalf("stub signHash: %v", err)
	}
	sig, err := base64.StdEncoding.DecodeString(sigs[0])
	if err != nil {
		t.Fatalf("decode signature: %v", err)
	}

	signed, err := sess.finish(sig)
	if err != nil {
		t.Fatalf("finish PAdES: %v", err)
	}
	if len(signed) <= len(pdf) {
		t.Fatalf("signed PDF (%d) should be larger than the input (%d)", len(signed), len(pdf))
	}

	resp, err := verify.Verify(bytes.NewReader(signed), int64(len(signed)))
	if err != nil {
		t.Fatalf("verify signed PDF: %v", err)
	}
	if len(resp.Signers) != 1 {
		t.Fatalf("expected exactly 1 signer, got %d", len(resp.Signers))
	}
	if !resp.Signers[0].ValidSignature {
		t.Fatalf("signature did not validate: %+v", resp.Signers[0])
	}
}

// TestPAdESAbandonUnblocks ensures an abandoned session does not leak its parked
// goroutine (the signer returns an error and the pass unwinds).
func TestPAdESAbandonUnblocks(t *testing.T) {
	stub, err := signingprovider.NewStubProvider()
	if err != nil {
		t.Fatalf("stub provider: %v", err)
	}
	cred, err := stub.CredentialInfo(context.Background(), "", "", "stub-credential")
	if err != nil {
		t.Fatalf("credential info: %v", err)
	}
	pdf, err := os.ReadFile("testdata/sample.pdf")
	if err != nil {
		t.Fatalf("read sample pdf: %v", err)
	}
	sess, _, err := startPAdES(pdf, cred, nil)
	if err != nil {
		t.Fatalf("startPAdES: %v", err)
	}
	sess.abandon(ErrSessionExpired)
	if _, err := sess.finish(nil); err == nil {
		t.Fatal("expected finish to report the abandoned pass failed")
	}
}
