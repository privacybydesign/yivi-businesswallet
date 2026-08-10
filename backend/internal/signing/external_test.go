package signing

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/google/uuid"
)

func TestValidateSignersMixesMembersAndExternalSigneesInOrder(t *testing.T) {
	alice, bob := uuid.New(), uuid.New()
	members := []OrgMember{
		{UserID: alice, Email: "alice@acme.example", Name: "Alice"},
		{UserID: bob, Email: "bob@acme.example", Name: "Bob"},
	}

	got, err := validateSigners([]SignerInput{
		{Kind: KindExternal, Email: " Outsider@Example.ORG ", Name: "  Outsider  "},
		{Kind: KindInternal, UserID: alice},
		{Kind: KindExternal, Email: "second@example.org", Name: "Second"},
	}, members)
	if err != nil {
		t.Fatalf("validateSigners: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("got %d signers, want 3", len(got))
	}
	// The position in the list is the sequential signing order, across both kinds.
	for i, sg := range got {
		if sg.Order != i+1 {
			t.Errorf("signer %d order = %d, want %d", i, sg.Order, i+1)
		}
	}
	// An address is normalised, so the same person is one subject however it was typed.
	if got[0].Email != "outsider@example.org" || got[0].Name != "Outsider" {
		t.Errorf("external signee = %q / %q, want normalised address and trimmed name", got[0].Email, got[0].Name)
	}
	if got[1].Email != "" || got[1].UserID != alice {
		t.Errorf("member signer = %q / %s, want no address and alice's id", got[1].Email, got[1].UserID)
	}
	if got[2].UserID != uuid.Nil {
		t.Errorf("external signee carries user id %s, want none", got[2].UserID)
	}
}

// tooManySigners is one signer over the per-request bound, each with a distinct
// address so the only thing wrong with the list is its length.
func tooManySigners() []SignerInput {
	out := make([]SignerInput, 0, maxSigners+1)
	for i := 0; i <= maxSigners; i++ {
		out = append(out, SignerInput{
			Kind:  KindExternal,
			Email: fmt.Sprintf("signee%d@example.org", i),
		})
	}
	return out
}

func TestValidateSignersRejections(t *testing.T) {
	alice := uuid.New()
	members := []OrgMember{{UserID: alice, Email: "alice@acme.example", Name: "Alice"}}

	tests := []struct {
		name string
		in   []SignerInput
	}{
		{"no signers", nil},
		{"more signers than one request may carry", tooManySigners()},
		{"unknown kind", []SignerInput{{Kind: "guest", Email: "x@example.org"}}},
		{"member who is not in the org", []SignerInput{{Kind: KindInternal, UserID: uuid.New()}}},
		{"the same member twice", []SignerInput{
			{Kind: KindInternal, UserID: alice}, {Kind: KindInternal, UserID: alice},
		}},
		{"the same address twice", []SignerInput{
			{Kind: KindExternal, Email: "out@example.org"}, {Kind: KindExternal, Email: "OUT@example.org"},
		}},
		// A member invited as an external signee would be two signers for one person,
		// each with their own credential and turn.
		{"a member's own address as an external signee", []SignerInput{
			{Kind: KindExternal, Email: "Alice@ACME.example"},
		}},
		{"an empty address", []SignerInput{{Kind: KindExternal, Email: "   "}}},
		{"not an address at all", []SignerInput{{Kind: KindExternal, Email: "not-an-address"}}},
		{"a display-name address", []SignerInput{{Kind: KindExternal, Email: "Out <out@example.org>"}}},
		{"an over-long address", []SignerInput{
			{Kind: KindExternal, Email: strings.Repeat("a", maxEmailLength) + "@example.org"},
		}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := validateSigners(tc.in, members); !errors.Is(err, ErrInvalidRequest) {
				t.Fatalf("validateSigners = %v, want ErrInvalidRequest", err)
			}
		})
	}
}

func TestSignerSubjectSplitsByKind(t *testing.T) {
	alice := uuid.New()
	if subj := memberSigner(alice).subject(); subj.isExternal() || subj.userID != alice {
		t.Errorf("member subject = %+v, want alice's user subject", subj)
	}
	sg := externalSignerRow("Outsider@Example.ORG")
	subj := sg.subject()
	if !subj.isExternal() || subj.email != "outsider@example.org" {
		t.Errorf("external subject = %+v, want the lower-cased address", subj)
	}
	// The subject is what keys the credential row, so the two kinds must never collide.
	if subj.String() == memberSigner(alice).subject().String() {
		t.Error("an external and a member subject must not describe the same row")
	}
}

func TestSignerByUserIgnoresExternalRows(t *testing.T) {
	alice := uuid.New()
	member := memberSigner(alice)
	external := externalSignerRow("outsider@example.org")
	req := Request{Signers: []Signer{external, member}}

	if got := signerByUser(req, alice); got == nil || got.ID != member.ID {
		t.Fatalf("signerByUser(alice) = %+v, want the member row", got)
	}
	if got := signerByUser(req, uuid.New()); got != nil {
		t.Fatalf("signerByUser(stranger) = %+v, want nil", got)
	}
	if got := signerByID(req, external.ID); got == nil || got.Email != "outsider@example.org" {
		t.Fatalf("signerByID(external) = %+v, want the external row", got)
	}
}

// An external signee has no org page to return to, so their ceremony must come back
// to the invitation link they arrived on — and the token must survive path escaping.
func TestResultURLReturnsExternalSigneeToTheirLink(t *testing.T) {
	s := &Service{appBaseURL: "https://wallet.example.org"}

	member := &ceremony{slug: "acme"}
	if got := s.resultURL(member, "link=ok"); got != "https://wallet.example.org/acme/signing?link=ok" {
		t.Errorf("member resultURL = %q", got)
	}

	external := &ceremony{slug: "", signer: signerRef{token: "tok/en+value"}}
	want := "https://wallet.example.org/sign/tok%2Fen+value?link=ok"
	if got := s.resultURL(external, "link=ok"); got != want {
		t.Errorf("external resultURL = %q, want %q", got, want)
	}
}

// The invitation mail and the post-ceremony redirect must name the same page, so both
// go through ExternalSignPath.
func TestExternalSignPathEscapesTheToken(t *testing.T) {
	if got := ExternalSignPath("abc123"); got != "/sign/abc123" {
		t.Errorf("ExternalSignPath = %q", got)
	}
	if got := ExternalSignPath("a/b"); got != "/sign/a%2Fb" {
		t.Errorf("ExternalSignPath = %q, want the token escaped", got)
	}
}

func TestExternalSubjectNormalisesTheAddress(t *testing.T) {
	if got := externalSubject("  Sam@Example.ORG "); got.email != "sam@example.org" {
		t.Errorf("externalSubject = %q, want sam@example.org", got.email)
	}
	if got := externalSubject("sam@example.org"); !got.isExternal() {
		t.Error("an external subject should report itself external")
	}
	if got := internalSubject(uuid.New()); got.isExternal() {
		t.Error("a member subject should not report itself external")
	}
}
