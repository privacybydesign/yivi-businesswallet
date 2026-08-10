package signing

import (
	"testing"

	"github.com/google/uuid"
)

func TestParseSignersKeepsFormOrderAcrossKinds(t *testing.T) {
	alice := uuid.New()
	got, err := parseSigners([]string{
		`{"kind":"internal","userId":"` + alice.String() + `"}`,
		"   ",
		`{"kind":"external","email":"out@example.org","name":"Outsider"}`,
	})
	if err != nil {
		t.Fatalf("parseSigners: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("got %d signers, want 2 (the blank value is skipped)", len(got))
	}
	if got[0].Kind != KindInternal || got[0].UserID != alice {
		t.Errorf("first signer = %+v, want alice as an internal signer", got[0])
	}
	if got[1].Kind != KindExternal || got[1].Email != "out@example.org" || got[1].Name != "Outsider" {
		t.Errorf("second signer = %+v, want the external signee", got[1])
	}
}

func TestParseSignersRejectsMalformedValues(t *testing.T) {
	tests := []struct {
		name   string
		values []string
	}{
		{"no values", nil},
		{"only blanks", []string{"", "  "}},
		{"not json", []string{"just-a-uuid"}},
		{"unknown kind", []string{`{"kind":"robot"}`}},
		{"internal without a usable id", []string{`{"kind":"internal","userId":"nope"}`}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := parseSigners(tc.values); err == nil {
				t.Fatal("parseSigners should have refused this input")
			}
		})
	}
}

// An empty filename must still produce a usable attachment name in both directions.
func TestDocumentNames(t *testing.T) {
	if got := documentName(""); got != "document.pdf" {
		t.Errorf("documentName(\"\") = %q", got)
	}
	if got := documentName("Contract.pdf"); got != "Contract.pdf" {
		t.Errorf("documentName = %q, want the upload's own name", got)
	}
	if got := signedName(""); got != "signed.pdf" {
		t.Errorf("signedName(\"\") = %q", got)
	}
}
