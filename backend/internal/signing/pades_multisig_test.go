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

// signOnce runs one external-signing pass over doc with cred's key (as finishSign
// does): prepare, have the stub sign the digest, embed. It is the per-signer step
// the co-signing service repeats over the accumulating document.
func signOnce(t *testing.T, stub *signingprovider.StubProvider, cred signingprovider.Credential, doc []byte) []byte {
	t.Helper()
	sess, digest, err := startPAdES(doc, cred, nil)
	if err != nil {
		t.Fatalf("startPAdES: %v", err)
	}
	sigs, err := stub.SignHash(context.Background(), "", "", cred.ID,
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
	return signed
}

// TestIncrementalCoSigningProducesTwoSignatures is the crux of the co-signing
// model: two members sign the same document one after another, each with their own
// credential, and the result carries both signatures — the second, incremental pass
// must not invalidate the first. This is what lets StartSign run each signer's
// ceremony over the evolving bytes.
func TestIncrementalCoSigningProducesTwoSignatures(t *testing.T) {
	stubA, err := signingprovider.NewStubProvider()
	if err != nil {
		t.Fatalf("stub A: %v", err)
	}
	stubB, err := signingprovider.NewStubProvider()
	if err != nil {
		t.Fatalf("stub B: %v", err)
	}
	ctx := context.Background()
	credA, err := stubA.CredentialInfo(ctx, "", "", "stub-credential")
	if err != nil {
		t.Fatalf("credential A: %v", err)
	}
	credB, err := stubB.CredentialInfo(ctx, "", "", "stub-credential")
	if err != nil {
		t.Fatalf("credential B: %v", err)
	}

	original, err := os.ReadFile("testdata/sample.pdf")
	if err != nil {
		t.Fatalf("read sample pdf: %v", err)
	}

	afterA := signOnce(t, stubA, credA, original)
	afterB := signOnce(t, stubB, credB, afterA)

	if len(afterB) <= len(afterA) {
		t.Fatalf("second signature should grow the document: after A=%d, after B=%d", len(afterA), len(afterB))
	}

	resp, err := verify.Verify(bytes.NewReader(afterB), int64(len(afterB)))
	if err != nil {
		t.Fatalf("verify doubly-signed PDF: %v", err)
	}
	if len(resp.Signers) != 2 {
		t.Fatalf("expected 2 signers on the co-signed PDF, got %d", len(resp.Signers))
	}
	for i, s := range resp.Signers {
		if !s.ValidSignature {
			t.Fatalf("signer %d signature did not validate: %+v", i, s)
		}
	}
}
