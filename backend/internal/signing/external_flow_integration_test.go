//go:build integration

package signing

import (
	"bytes"
	"context"
	"errors"
	"net/url"
	"os"
	"strings"
	"testing"

	"github.com/digitorus/pdfsign/verify"
	"github.com/google/uuid"

	"github.com/privacybydesign/yivi-businesswallet/backend/internal/signingprovider"
)

// This drives the whole mixed-signer ceremony against a real database and the
// in-process stub QTSP: a sequential request signed first by an org member and then by
// an external signee who has no membership and no session, only the link that was
// mailed to them. It is the acceptance case for external signees — the finished PDF
// must carry one valid PAdES signature per signer regardless of kind.

type stubConnection struct{}

func (stubConnection) ResolveConnection(context.Context, uuid.UUID) (string, string, string, error) {
	return "https://stub-qtsp.local", "stub-client", "stub-secret", nil
}
func (stubConnection) Available(context.Context, uuid.UUID) (bool, error) { return true, nil }

type stubMembers struct{ members []OrgMember }

func (s stubMembers) ListMembers(context.Context, uuid.UUID) ([]OrgMember, error) {
	return s.members, nil
}

type stubOrgs struct{}

func (stubOrgs) OrgName(context.Context, uuid.UUID) (string, error) { return "Acme", nil }

// capturingNotifier stands in for the mail: it records the member addresses notified
// and the raw invitation tokens handed to external signees, which is how the test gets
// hold of the link a real signee would click.
type capturingNotifier struct {
	members []string
	tokens  []string
}

func (c *capturingNotifier) NotifySignatureRequested(_ context.Context, _ uuid.UUID, email, _, _ string) error {
	c.members = append(c.members, email)
	return nil
}

func (c *capturingNotifier) NotifyExternalSignatureRequested(_ context.Context, _ uuid.UUID, _, _, token string) error {
	c.tokens = append(c.tokens, token)
	return nil
}

// stateFrom pulls the OAuth state out of an authorize URL, standing in for the
// browser+wallet round trip: the real authorization server redirects back with it.
func stateFrom(t *testing.T, authorizeURL string) string {
	t.Helper()
	u, err := url.Parse(authorizeURL)
	if err != nil {
		t.Fatalf("parse authorize url %q: %v", authorizeURL, err)
	}
	state := u.Query().Get("state")
	if state == "" {
		t.Fatalf("authorize url %q carries no state", authorizeURL)
	}
	return state
}

func TestExternalSigneeSignsAlongsideAMember(t *testing.T) {
	store, pool := newStore(t)
	ctx := context.Background()
	orgID := makeOrg(t, pool, "acme")
	alice := makeUser(t, pool, "alice@acme.example")

	stub, err := signingprovider.NewStubProvider()
	if err != nil {
		t.Fatalf("stub provider: %v", err)
	}
	notifier := &capturingNotifier{}
	svc := NewService(store, stub, stubConnection{},
		stubMembers{members: []OrgMember{{UserID: alice, Name: "Alice", Email: "alice@acme.example"}}},
		stubOrgs{}, nil, notifier,
		"https://wallet.example.org/api/v1/signing/callback", "https://wallet.example.org", "")

	pdf, err := os.ReadFile("testdata/sample.pdf")
	if err != nil {
		t.Fatalf("read sample pdf: %v", err)
	}

	// Sequential: Alice first, then the external signee.
	requestID, err := svc.CreateRequest(ctx, orgID, alice, "acme", "Contract.pdf", pdf, []SignerInput{
		{Kind: KindInternal, UserID: alice},
		{Kind: KindExternal, Email: "outsider@example.org", Name: "Outsider"},
	}, ModeSequential, RecipientInput{Channel: ChannelNone})
	if err != nil {
		t.Fatalf("CreateRequest: %v", err)
	}
	// Only the first signer is asked in sequential mode, so the external signee has no
	// live link yet.
	if len(notifier.members) != 1 || notifier.members[0] != "alice@acme.example" {
		t.Fatalf("notified members = %v, want only alice", notifier.members)
	}
	if len(notifier.tokens) != 0 {
		t.Fatalf("the external signee was invited before their turn (tokens = %d)", len(notifier.tokens))
	}

	// Alice links her credential and signs.
	linkStart, err := svc.StartLink(ctx, orgID, alice, "acme")
	if err != nil {
		t.Fatalf("StartLink: %v", err)
	}
	if dest := svc.HandleCallback(ctx, "code", stateFrom(t, linkStart.AuthorizeURL)); !strings.HasSuffix(dest, "link=ok") {
		t.Fatalf("link callback redirected to %q, want link=ok", dest)
	}
	signStart, err := svc.StartSign(ctx, orgID, alice, "acme", requestID)
	if err != nil {
		t.Fatalf("StartSign: %v", err)
	}
	if dest := svc.HandleCallback(ctx, "code", stateFrom(t, signStart.AuthorizeURL)); !strings.Contains(dest, requestID.String()) {
		t.Fatalf("sign callback redirected to %q, want the request id", dest)
	}

	// Her signature advancing the request is what invites the external signee.
	if len(notifier.tokens) != 1 {
		t.Fatalf("the external signee was not invited when their turn came (tokens = %d)", len(notifier.tokens))
	}
	token := notifier.tokens[0]

	// Everything the signee can do is keyed by that token alone.
	view, err := svc.ExternalView(ctx, token)
	if err != nil {
		t.Fatalf("ExternalView: %v", err)
	}
	if view.OrgName != "Acme" || view.Filename != "Contract.pdf" {
		t.Errorf("view = %+v, want Acme / Contract.pdf", view)
	}
	if view.SignerCount != 2 || view.SignedCount != 1 {
		t.Errorf("view progress = %d/%d, want 1/2", view.SignedCount, view.SignerCount)
	}
	if view.HasCredential || view.CanSign {
		t.Errorf("a signee with no credential yet = %+v, want hasCredential/canSign false", view)
	}

	// They can read what they are about to sign: Alice's signature is already on it.
	afterAlice, filename, err := svc.ExternalDocument(ctx, token)
	if err != nil {
		t.Fatalf("ExternalDocument: %v", err)
	}
	if filename != "Contract.pdf" || len(afterAlice) <= len(pdf) {
		t.Errorf("document = %q, %d bytes; want the once-signed Contract.pdf", filename, len(afterAlice))
	}

	// Link their own credential, then sign — the same two ceremonies a member runs.
	extLink, err := svc.StartExternalLink(ctx, token)
	if err != nil {
		t.Fatalf("StartExternalLink: %v", err)
	}
	dest := svc.HandleCallback(ctx, "code", stateFrom(t, extLink.AuthorizeURL))
	if dest != "https://wallet.example.org"+ExternalSignPath(token)+"?link=ok" {
		t.Fatalf("external link callback redirected to %q, want back to the signing link", dest)
	}

	view, err = svc.ExternalView(ctx, token)
	if err != nil {
		t.Fatalf("ExternalView after linking: %v", err)
	}
	if !view.HasCredential || !view.CanSign {
		t.Fatalf("after linking, view = %+v, want hasCredential and canSign", view)
	}

	extSign, err := svc.StartExternalSign(ctx, token)
	if err != nil {
		t.Fatalf("StartExternalSign: %v", err)
	}
	if extSign.RequestID == nil || *extSign.RequestID != requestID {
		t.Errorf("StartExternalSign returned request %v, want %s", extSign.RequestID, requestID)
	}
	if dest := svc.HandleCallback(ctx, "code", stateFrom(t, extSign.AuthorizeURL)); !strings.HasPrefix(dest, "https://wallet.example.org"+ExternalSignPath(token)) {
		t.Fatalf("external sign callback redirected to %q, want back to the signing link", dest)
	}

	// The request is complete and the PDF carries both signatures.
	req, err := svc.GetRequest(ctx, orgID, alice, requestID, false)
	if err != nil {
		t.Fatalf("GetRequest: %v", err)
	}
	if req.Status != StatusCompleted {
		t.Fatalf("request status = %q, want completed (error: %q)", req.Status, req.Error)
	}
	for _, sg := range req.Signers {
		if sg.Status != SignerSigned {
			t.Errorf("signer %+v did not end signed", sg)
		}
	}
	signed, _, err := svc.GetSignedDocument(ctx, orgID, alice, requestID, false)
	if err != nil {
		t.Fatalf("GetSignedDocument: %v", err)
	}
	resp, err := verify.Verify(bytes.NewReader(signed), int64(len(signed)))
	if err != nil {
		t.Fatalf("verify the co-signed PDF: %v", err)
	}
	if len(resp.Signers) != 2 {
		t.Fatalf("the finished PDF carries %d signatures, want 2 (one per signer)", len(resp.Signers))
	}
	for i, s := range resp.Signers {
		if !s.ValidSignature {
			t.Errorf("signature %d did not validate: %+v", i, s)
		}
	}

	// The link stays readable so the signee sees their signature landed, but it cannot
	// start another signature and cannot re-link a credential.
	view, err = svc.ExternalView(ctx, token)
	if err != nil {
		t.Fatalf("ExternalView after signing: %v", err)
	}
	if view.SignerStatus != SignerSigned || view.CanSign {
		t.Errorf("after signing, view = %+v, want signed and canSign false", view)
	}
	if _, err := svc.StartExternalSign(ctx, token); !errors.Is(err, ErrAlreadySigned) {
		t.Errorf("a second signature from the same link = %v, want ErrAlreadySigned", err)
	}
	if _, err := svc.StartExternalLink(ctx, token); !errors.Is(err, ErrAlreadySigned) {
		t.Errorf("re-linking after signing = %v, want ErrAlreadySigned", err)
	}
}

// An external signee cannot sign out of turn, and a parallel request invites every
// external signee at create time.
func TestExternalSigneeTurnAndParallelInvitations(t *testing.T) {
	store, pool := newStore(t)
	ctx := context.Background()
	orgID := makeOrg(t, pool, "acme")
	alice := makeUser(t, pool, "alice@acme.example")

	stub, err := signingprovider.NewStubProvider()
	if err != nil {
		t.Fatalf("stub provider: %v", err)
	}
	notifier := &capturingNotifier{}
	svc := NewService(store, stub, stubConnection{},
		stubMembers{members: []OrgMember{{UserID: alice, Name: "Alice", Email: "alice@acme.example"}}},
		stubOrgs{}, nil, notifier,
		"https://wallet.example.org/api/v1/signing/callback", "https://wallet.example.org", "")

	pdf, err := os.ReadFile("testdata/sample.pdf")
	if err != nil {
		t.Fatalf("read sample pdf: %v", err)
	}

	// Sequential with the external signee second: their link exists only after Alice
	// signs, so there is nothing to sign out of turn with. Assert the turn rule on a
	// request whose external signee is invited up front instead — parallel invites all.
	if _, err := svc.CreateRequest(ctx, orgID, alice, "acme", "Parallel.pdf", pdf, []SignerInput{
		{Kind: KindExternal, Email: "one@example.org", Name: "One"},
		{Kind: KindExternal, Email: "two@example.org", Name: "Two"},
	}, ModeParallel, RecipientInput{Channel: ChannelNone}); err != nil {
		t.Fatalf("CreateRequest (parallel): %v", err)
	}
	if len(notifier.tokens) != 2 {
		t.Fatalf("parallel mode invited %d external signees, want 2", len(notifier.tokens))
	}
	// Either of them may go first in parallel mode.
	for i, token := range notifier.tokens {
		view, err := svc.ExternalView(ctx, token)
		if err != nil {
			t.Fatalf("ExternalView %d: %v", i, err)
		}
		if view.CanSign {
			t.Errorf("signee %d can sign before linking a credential", i)
		}
		if _, err := svc.StartExternalSign(ctx, token); !errors.Is(err, ErrNoCredential) {
			t.Errorf("signing without a credential = %v, want ErrNoCredential", err)
		}
	}

	// A sequential request whose second signee is invited early (by re-issuing their
	// token directly) still cannot sign before the first.
	requestID, created, err := store.CreateRequest(ctx, orgID, alice, "Sequential.pdf", pdf, ModeSequential,
		[]SignerInput{
			{Kind: KindExternal, Email: "first@example.org", Name: "First", Order: 1},
			{Kind: KindExternal, Email: "second@example.org", Name: "Second", Order: 2},
		}, RecipientInput{Channel: ChannelNone})
	if err != nil {
		t.Fatalf("CreateRequest (sequential): %v", err)
	}
	secondToken, err := store.IssueExternalToken(ctx, requestID, created[1].ID)
	if err != nil {
		t.Fatalf("IssueExternalToken: %v", err)
	}
	if err := store.UpsertCredential(ctx, orgID, externalSubject("second@example.org"),
		testCredential(t, "second-cred")); err != nil {
		t.Fatalf("UpsertCredential: %v", err)
	}
	view, err := svc.ExternalView(ctx, secondToken)
	if err != nil {
		t.Fatalf("ExternalView (second in line): %v", err)
	}
	if view.CanSign {
		t.Error("the second signee of a sequential request may not sign yet")
	}
	if _, err := svc.StartExternalSign(ctx, secondToken); !errors.Is(err, ErrNotYourTurn) {
		t.Errorf("signing out of turn = %v, want ErrNotYourTurn", err)
	}

	// An unknown link is refused everywhere.
	if _, err := svc.ExternalView(ctx, "not-a-token"); !errors.Is(err, ErrInvalidToken) {
		t.Errorf("ExternalView(unknown) = %v, want ErrInvalidToken", err)
	}
	if _, err := svc.StartExternalLink(ctx, "not-a-token"); !errors.Is(err, ErrInvalidToken) {
		t.Errorf("StartExternalLink(unknown) = %v, want ErrInvalidToken", err)
	}
	if _, _, err := svc.ExternalDocument(ctx, "not-a-token"); !errors.Is(err, ErrInvalidToken) {
		t.Errorf("ExternalDocument(unknown) = %v, want ErrInvalidToken", err)
	}
}
