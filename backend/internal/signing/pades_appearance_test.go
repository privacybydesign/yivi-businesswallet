package signing

import (
	"bytes"
	"context"
	"encoding/base64"
	"fmt"
	"os"
	"testing"

	"github.com/digitorus/pdfsign/verify"

	"github.com/privacybydesign/yivi-businesswallet/backend/internal/signingprovider"
)

// stubCredential is a signing credential from a fresh stub QTSP (a local EC key and
// a self-signed certificate), the same one the ceremony tests sign with.
func stubCredential(t *testing.T) (*signingprovider.StubProvider, signingprovider.Credential) {
	t.Helper()
	stub, err := signingprovider.NewStubProvider()
	if err != nil {
		t.Fatalf("stub provider: %v", err)
	}
	cred, err := stub.CredentialInfo(context.Background(), "", "", "stub-credential")
	if err != nil {
		t.Fatalf("credential info: %v", err)
	}
	return stub, cred
}

// signDigest is the QTSP half of the ceremony: the stub signs the captured digest,
// exactly as the signHash call does.
func signDigest(t *testing.T, stub *signingprovider.StubProvider, cred signingprovider.Credential, digest []byte) []byte {
	t.Helper()
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
	return sig
}

// signWithPlacements runs one full external-signing pass over doc with the signer's
// placements, as finishSign does.
func signWithPlacements(t *testing.T, doc []byte, placements []Placement) []byte {
	t.Helper()
	stub, cred := stubCredential(t)
	sess, digest, err := startPAdES(doc, cred, placements)
	if err != nil {
		t.Fatalf("startPAdES: %v", err)
	}
	signed, err := sess.finish(signDigest(t, stub, cred, digest))
	if err != nil {
		t.Fatalf("finish PAdES: %v", err)
	}
	return signed
}

func mustVerify(t *testing.T, signed []byte, wantSigners int) {
	t.Helper()
	resp, err := verify.Verify(bytes.NewReader(signed), int64(len(signed)))
	if err != nil {
		t.Fatalf("verify signed PDF: %v", err)
	}
	if len(resp.Signers) != wantSigners {
		t.Fatalf("got %d signers, want %d", len(resp.Signers), wantSigners)
	}
	for i, s := range resp.Signers {
		if !s.ValidSignature {
			t.Fatalf("signer %d did not validate: %+v", i, s)
		}
	}
}

// TestSignaturePlacementBecomesTheVisibleAppearance is the signature half of the
// feature: the rectangle the requester placed is the rectangle the signature's own
// widget occupies, and binding it that way leaves the signature verifiable.
func TestSignaturePlacementBecomesTheVisibleAppearance(t *testing.T) {
	block := Placement{Kind: PlacementSignature, Page: 2, X: 72, Y: 96, Width: 200, Height: 64}
	signed := signWithPlacements(t, buildTestPDF(t, 2, false), []Placement{block})

	mustVerify(t, signed, 1)
	// The widget carries the placed rectangle: pdfsign writes /Rect with %f, so the
	// comparison is on that formatting.
	want := fmt.Sprintf("/Rect [%f %f %f %f]", block.X, block.Y, block.X+block.Width, block.Y+block.Height)
	if !bytes.Contains(signed, []byte(want)) {
		t.Fatalf("signed PDF does not carry the placed signature rectangle %q", want)
	}
	if !bytes.Contains(signed, []byte("/FT /Sig")) {
		t.Fatal("signed PDF has no signature field")
	}
}

// A signer who placed nothing keeps the invisible signature every signature had
// before placement existed — the zero rectangle, not a visible box at the origin.
func TestNoPlacementKeepsTheSignatureInvisible(t *testing.T) {
	signed := signWithPlacements(t, buildTestPDF(t, 1, false), nil)
	mustVerify(t, signed, 1)
	if !bytes.Contains(signed, []byte("/Rect [0 0 0 0]")) {
		t.Fatal("a signature with no placement should stay invisible")
	}
}

// TestParaphsAreStampedOnEveryPlacedPage is the paraph half: initials land on each
// page they were placed on, in the same revision chain, and the signature over them
// still verifies.
func TestParaphsAreStampedOnEveryPlacedPage(t *testing.T) {
	const pages = 3
	placements := []Placement{
		{Kind: PlacementSignature, Page: pages, X: 72, Y: 96, Width: 200, Height: 64},
	}
	for page := 1; page <= pages; page++ {
		placements = append(placements, Placement{
			Kind: PlacementParaph, Page: page, X: 500, Y: 40, Width: 48, Height: 24,
		})
	}
	signed := signWithPlacements(t, buildTestPDF(t, pages, false), placements)

	mustVerify(t, signed, 1)
	if got := bytes.Count(signed, []byte("/Subtype /Stamp")); got != pages {
		t.Fatalf("got %d paraph annotations, want %d", got, pages)
	}
	// One appearance rectangle per paraph, at the placed position.
	if got := bytes.Count(signed, []byte("/Rect [500.00 40.00 548.00 64.00]")); got != pages {
		t.Fatalf("got %d paraph rectangles, want %d", got, pages)
	}
}

// TestStampedParaphIsCoveredByTheSignature is what makes a paraph attributable: it
// is stamped in a revision the signer's own signature then covers, so changing the
// initials after the fact breaks that signature. Without that ordering a paraph would
// be an unsigned annotation anybody could edit.
func TestStampedParaphIsCoveredByTheSignature(t *testing.T) {
	stub, cred := stubCredential(t)
	// The paraph text comes from the certificate's common name, so the tamper below
	// has something known to look for.
	mark := []byte(pdfLiteral(winAnsi(paraphText(cred.Certificate.Subject.CommonName))) + " Tj")

	sess, digest, err := startPAdES(buildTestPDF(t, 1, false), cred, []Placement{
		{Kind: PlacementParaph, Page: 1, X: 500, Y: 40, Width: 48, Height: 24},
	})
	if err != nil {
		t.Fatalf("startPAdES: %v", err)
	}
	signed, err := sess.finish(signDigest(t, stub, cred, digest))
	if err != nil {
		t.Fatalf("finish PAdES: %v", err)
	}
	mustVerify(t, signed, 1)

	at := bytes.Index(signed, mark)
	if at < 0 {
		t.Fatalf("the paraph mark %q is not in the signed document", mark)
	}
	// Same length, so only the drawn glyphs change — nothing else about the file moves.
	tampered := bytes.Clone(signed)
	tampered[at+1] = 'Z'
	resp, err := verify.Verify(bytes.NewReader(tampered), int64(len(tampered)))
	if err == nil && len(resp.Signers) > 0 && resp.Signers[0].ValidSignature {
		t.Fatal("editing a stamped paraph left the signature valid; it is not covered")
	}
}

// A document indexed by a cross-reference stream needs the appended revision to be
// one too — a table would leave a reader that found it first unable to reach the
// object streams the previous section indexes. Both kinds go through the same path,
// so both are exercised.
func TestParaphStampMatchesTheDocumentsXrefKind(t *testing.T) {
	for _, xrefStream := range []bool{false, true} {
		name := "xref table"
		if xrefStream {
			name = "xref stream"
		}
		t.Run(name, func(t *testing.T) {
			signed := signWithPlacements(t, buildTestPDF(t, 2, xrefStream), []Placement{
				{Kind: PlacementSignature, Page: 1, X: 72, Y: 96, Width: 200, Height: 64},
				{Kind: PlacementParaph, Page: 2, X: 500, Y: 40, Width: 48, Height: 24},
			})
			mustVerify(t, signed, 1)
			geometry, err := readGeometry(signed)
			if err != nil {
				t.Fatalf("the stamped document no longer parses: %v", err)
			}
			if len(geometry.pages) != 2 {
				t.Fatalf("got %d pages after stamping, want 2", len(geometry.pages))
			}
		})
	}
}

// Co-signing with placements is the real shape of the feature: two signers, each
// with their own block and their own paraph, over the document as the previous signer
// left it. Both signatures must survive.
func TestCoSigningWithPlacementsKeepsBothSignaturesValid(t *testing.T) {
	doc := buildTestPDF(t, 2, false)
	afterA := signWithPlacements(t, doc, []Placement{
		{Kind: PlacementSignature, Page: 2, X: 72, Y: 300, Width: 180, Height: 60},
		{Kind: PlacementParaph, Page: 1, X: 480, Y: 40, Width: 48, Height: 24},
	})
	afterB := signWithPlacements(t, afterA, []Placement{
		{Kind: PlacementSignature, Page: 2, X: 300, Y: 300, Width: 180, Height: 60},
		{Kind: PlacementParaph, Page: 1, X: 530, Y: 40, Width: 48, Height: 24},
	})
	mustVerify(t, afterB, 2)
	if got := bytes.Count(afterB, []byte("/Subtype /Stamp")); got != 2 {
		t.Fatalf("got %d paraph annotations after two signers, want 2", got)
	}
}

// The repository's own sample document is a %PDF-2.0 file from another producer, so
// it is worth one pass of its own: placement must work on a document this code did
// not write.
func TestPlacementOnTheSampleDocument(t *testing.T) {
	doc, err := os.ReadFile("testdata/sample.pdf")
	if err != nil {
		t.Fatalf("read sample pdf: %v", err)
	}
	geometry, err := readGeometry(doc)
	if err != nil {
		t.Fatalf("readGeometry: %v", err)
	}
	if len(geometry.pages) != 1 {
		t.Fatalf("got %d pages in the sample, want 1", len(geometry.pages))
	}
	signed := signWithPlacements(t, doc, []Placement{
		{Kind: PlacementSignature, Page: 1, X: 60, Y: 60, Width: 180, Height: 50},
		{Kind: PlacementParaph, Page: 1, X: 520, Y: 30, Width: 48, Height: 24},
	})
	mustVerify(t, signed, 1)
}
