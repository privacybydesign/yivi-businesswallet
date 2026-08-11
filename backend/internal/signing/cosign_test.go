package signing

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
)

type fakeNotifier struct {
	emails   []string
	external []string
}

func (f *fakeNotifier) NotifySignatureRequested(_ context.Context, _ uuid.UUID, email, _, _ string) error {
	f.emails = append(f.emails, email)
	return nil
}

func (f *fakeNotifier) NotifyExternalSignatureRequested(_ context.Context, _ uuid.UUID, email, _, _ string) error {
	f.external = append(f.external, email)
	return nil
}

// memberSigner is an internal signer row for a given member.
func memberSigner(userID uuid.UUID) Signer {
	return Signer{ID: uuid.New(), Kind: KindInternal, UserID: &userID, Status: SignerPending}
}

// externalSignerRow is an external signee's signer row.
func externalSignerRow(email string) Signer {
	return Signer{ID: uuid.New(), Kind: KindExternal, Email: email, Status: SignerPending}
}

func TestNotifySignerResolvesEmailAndSkipsUnknown(t *testing.T) {
	fn := &fakeNotifier{}
	s := &Service{notifier: fn}
	alice := uuid.New()
	members := []OrgMember{{UserID: alice, Email: "alice@example.org", Name: "Alice"}}
	req := uuid.New()

	s.notifySigner(context.Background(), uuid.New(), "acme", "Doc.pdf", req, memberSigner(alice), members)
	if len(fn.emails) != 1 || fn.emails[0] != "alice@example.org" {
		t.Fatalf("expected one notification to alice, got %v", fn.emails)
	}

	// A signer not in the member list is skipped, not an error.
	s.notifySigner(context.Background(), uuid.New(), "acme", "Doc.pdf", req, memberSigner(uuid.New()), members)
	if len(fn.emails) != 1 {
		t.Fatalf("an unknown signer should not be notified, got %v", fn.emails)
	}
}

func TestNotifySignerNilNotifierIsNoop(t *testing.T) {
	s := &Service{} // notifier nil
	s.notifySigner(context.Background(), uuid.New(), "acme", "Doc.pdf", uuid.New(), memberSigner(uuid.New()), nil)
}

func newTestService() *Service {
	return &Service{
		sessions: make(map[string]*ceremony),
		active:   make(map[uuid.UUID]*ceremony),
	}
}

func TestReserveSignLocksOutOtherSignersButLetsSameUserReclaim(t *testing.T) {
	s := newTestService()
	req := uuid.New()
	// Signer row ids, not user ids: the slot is keyed by the signer row so an external
	// signee (who has no user id) is serialised with the members.
	alice, bob := uuid.New(), uuid.New()

	if !s.reserveSign(req, alice) {
		t.Fatal("first reserve should succeed")
	}
	if s.reserveSign(req, bob) {
		t.Fatal("a different signer must be blocked while a ceremony is in flight")
	}
	if !s.reserveSign(req, alice) {
		t.Fatal("the same signer should reclaim their own in-flight slot")
	}

	s.release(req)
	if !s.reserveSign(req, bob) {
		t.Fatal("after release the slot is free for anyone")
	}
}

// Reclaiming an own slot must tear the stale ceremony down: drop its session entry
// and unblock its parked pdfsign pass, so it does not leak until SessionTTL.
func TestReserveSignDiscardsStaleCeremony(t *testing.T) {
	s := newTestService()
	req := uuid.New()
	alice := uuid.New()

	stale := &ceremony{
		flow:      flowSign,
		requestID: req,
		signer:    signerRef{signerID: alice},
		state:     "stale-state",
		pades:     &padesSession{signer: &padesSigner{errCh: make(chan error, 1)}, result: make(chan padesResult, 1)},
	}
	s.sessions[stale.state] = stale
	s.active[req] = stale

	if !s.reserveSign(req, alice) {
		t.Fatal("same user should reclaim the slot")
	}
	if _, ok := s.sessions["stale-state"]; ok {
		t.Fatal("the stale ceremony's session entry should be discarded")
	}
	select {
	case <-stale.pades.signer.errCh:
		// abandoned as expected
	default:
		t.Fatal("the stale parked pass should have been abandoned")
	}
}

// checkTurn is keyed by signer row id, so the same table covers a member and an
// external signee: the first signer is a member, the second an external signee.
func TestCheckTurn(t *testing.T) {
	signers := func(states ...string) []Signer {
		out := make([]Signer, len(states))
		for i, st := range states {
			if i == 0 {
				out[i] = memberSigner(uuid.New())
			} else {
				out[i] = externalSignerRow("outsider@example.org")
			}
			out[i].Order, out[i].Status = i+1, st
		}
		return out
	}

	tests := []struct {
		name    string
		mode    string
		signers []Signer
		// which signer of the list is acting; -1 means someone who is not a signer.
		acting int
		want   error
	}{
		{"parallel member can sign", ModeParallel, signers(SignerPending, SignerPending), 0, nil},
		{"parallel external can sign before member", ModeParallel, signers(SignerPending, SignerPending), 1, nil},
		{"parallel already signed", ModeParallel, signers(SignerSigned, SignerPending), 0, ErrAlreadySigned},
		{"not a signer", ModeParallel, signers(SignerPending, SignerPending), -1, ErrNotSigner},
		{"sequential first can sign", ModeSequential, signers(SignerPending, SignerPending), 0, nil},
		{"sequential external must wait for the member", ModeSequential, signers(SignerPending, SignerPending), 1, ErrNotYourTurn},
		{"sequential external after the member signed", ModeSequential, signers(SignerSigned, SignerPending), 1, nil},
		{"sequential already signed", ModeSequential, signers(SignerSigned, SignerPending), 0, ErrAlreadySigned},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			req := Request{Mode: tc.mode, Signers: tc.signers}
			acting := uuid.New()
			if tc.acting >= 0 {
				acting = tc.signers[tc.acting].ID
			}
			if got := checkTurn(req, acting); !errors.Is(got, tc.want) {
				t.Fatalf("checkTurn = %v, want %v", got, tc.want)
			}
		})
	}
}

// A member blocked behind a pending external signee is the mixed case sequential
// mode has to get right: the block comes from the sign_order, not the signer's kind.
func TestCheckTurnSequentialMemberWaitsForExternal(t *testing.T) {
	external := externalSignerRow("outsider@example.org")
	external.Order = 1
	member := memberSigner(uuid.New())
	member.Order = 2
	req := Request{Mode: ModeSequential, Signers: []Signer{external, member}}

	if err := checkTurn(req, member.ID); !errors.Is(err, ErrNotYourTurn) {
		t.Fatalf("member behind a pending external signee: checkTurn = %v, want ErrNotYourTurn", err)
	}
	req.Signers[0].Status = SignerSigned
	if err := checkTurn(req, member.ID); err != nil {
		t.Fatalf("member after the external signee signed: checkTurn = %v, want nil", err)
	}
}

func TestValidateRecipient(t *testing.T) {
	tests := []struct {
		name string
		rec  RecipientInput
		want error
	}{
		{"none", RecipientInput{Channel: ChannelNone}, nil},
		{"email with address", RecipientInput{Channel: ChannelEmail, Address: "a@example.org"}, nil},
		{"qerds with address", RecipientInput{Channel: ChannelQERDS, Address: "0208:nl:acme"}, nil},
		{"email without address", RecipientInput{Channel: ChannelEmail}, ErrInvalidRequest},
		{"qerds blank address", RecipientInput{Channel: ChannelQERDS, Address: "   "}, ErrInvalidRequest},
		{"unknown channel", RecipientInput{Channel: "sms", Address: "x"}, ErrInvalidRequest},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := validateRecipient(tc.rec); !errors.Is(got, tc.want) {
				t.Fatalf("validateRecipient = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestInvolves(t *testing.T) {
	creator, signer, stranger := uuid.New(), uuid.New(), uuid.New()
	// The external signee row carries no user id, so involves must skip it rather than
	// dereference it.
	req := Request{CreatedBy: creator, Signers: []Signer{
		externalSignerRow("outsider@example.org"),
		memberSigner(signer),
	}}
	if !involves(req, creator) {
		t.Fatal("creator should be involved")
	}
	if !involves(req, signer) {
		t.Fatal("signer should be involved")
	}
	if involves(req, stranger) {
		t.Fatal("stranger should not be involved")
	}
}

func TestCursorRoundTrip(t *testing.T) {
	id := uuid.New()
	cur := encodeCursor(time.Unix(1_700_000_000, 123).UTC(), id)
	gotTime, gotID, has, err := decodeCursor(cur)
	if err != nil || !has {
		t.Fatalf("decodeCursor(%q) = has %v, err %v", cur, has, err)
	}
	if gotID != id {
		t.Fatalf("cursor id round-trip: got %s want %s", gotID, id)
	}
	if gotTime.IsZero() {
		t.Fatal("cursor time round-trip lost the timestamp")
	}
	if _, _, has, _ := decodeCursor(""); has {
		t.Fatal("empty cursor should report no cursor")
	}
	if _, _, _, err := decodeCursor("!!!not-base64!!!"); !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("bad cursor should be ErrInvalidRequest, got %v", err)
	}
}
