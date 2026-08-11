package signing

import (
	"context"
	"errors"
	"os"
	"testing"

	"github.com/privacybydesign/yivi-businesswallet/backend/internal/signingprovider"
)

// TestStartPAdESSurvivesMalformedPDF sweeps single-byte mutations of the sample
// PDF through startPAdES. digitorus/pdf accepts some of these and then panics deep
// inside the signing goroutine (a non-request goroutine, so an unrecovered panic
// would kill the whole process — and with co-signing that goroutine runs on another
// member's Sign click over the stored upload). This test completing at all is the
// proof the recover is in place, and every failure must surface as ErrInvalidPDF,
// never a raw error that mapStartError would turn into a 500.
func TestStartPAdESSurvivesMalformedPDF(t *testing.T) {
	stub, err := signingprovider.NewStubProvider()
	if err != nil {
		t.Fatalf("stub provider: %v", err)
	}
	cred, err := stub.CredentialInfo(context.Background(), "", "", "stub-credential")
	if err != nil {
		t.Fatalf("credential info: %v", err)
	}
	original, err := os.ReadFile("testdata/sample.pdf")
	if err != nil {
		t.Fatalf("read sample pdf: %v", err)
	}

	// Flip one byte at a stride across the object/xref region. A completed sweep (no
	// panic escaping the goroutine) is the regression guard.
	for off := 0; off < len(original); off += 13 {
		mutated := append([]byte(nil), original...)
		mutated[off] ^= 0xFF

		sess, _, serr := startPAdES(mutated, cred, nil)
		if serr != nil {
			if !errors.Is(serr, ErrInvalidPDF) {
				t.Fatalf("offset %d: startPAdES error %v, want ErrInvalidPDF", off, serr)
			}
			continue
		}
		// Parsed far enough to publish a digest: a live parked goroutine holds the
		// pass, so abandon it to unwind cleanly rather than leak it.
		sess.abandon(ErrSessionExpired)
	}
}
