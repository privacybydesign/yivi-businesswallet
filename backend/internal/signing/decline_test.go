package signing

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
)

// decline's guards mirror startSign's (minus the turn check, which decline
// deliberately skips — see the package doc on decline.go), and every rejecting
// branch returns before ever touching s.store, so they are testable without a
// database exactly like TestCheckTurn covers checkTurn.
func TestDeclineGuards(t *testing.T) {
	pending := func(kind string) []Signer {
		if kind == KindExternal {
			return []Signer{externalSignerRow("outsider@example.org")}
		}
		return []Signer{memberSigner(uuid.New())}
	}

	tests := []struct {
		name       string
		req        Request
		actingSelf bool // decline as the one signer in req.Signers; false = a stranger
		active     bool // a ceremony currently holds the per-request in-flight lock
		want       error
	}{
		{
			name:       "not a signer",
			req:        Request{ID: uuid.New(), Status: StatusAwaitingSignatures, Signers: pending(KindInternal)},
			actingSelf: false,
			want:       ErrNotSigner,
		},
		{
			name: "already signed",
			req: Request{ID: uuid.New(), Status: StatusAwaitingSignatures, Signers: []Signer{
				{ID: uuid.New(), Kind: KindInternal, Status: SignerSigned},
			}},
			actingSelf: true,
			want:       ErrAlreadySigned,
		},
		{
			name:       "request already completed",
			req:        Request{ID: uuid.New(), Status: StatusCompleted, Signers: pending(KindInternal)},
			actingSelf: true,
			want:       ErrInvalidRequest,
		},
		{
			name:       "request already declined by someone else",
			req:        Request{ID: uuid.New(), Status: StatusDeclined, Signers: pending(KindExternal)},
			actingSelf: true,
			want:       ErrInvalidRequest,
		},
		{
			name:       "a ceremony is in flight for this request",
			req:        Request{ID: uuid.New(), Status: StatusAwaitingSignatures, Signers: pending(KindInternal)},
			actingSelf: true,
			active:     true,
			want:       ErrSignInProgress,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			s := newTestService()
			signerID := uuid.New()
			if tc.actingSelf {
				signerID = tc.req.Signers[0].ID
			}
			if tc.active {
				s.active[tc.req.ID] = &ceremony{}
			}
			// Every case above returns before reaching s.store, so a nil store never
			// gets called — if it did, this would panic rather than silently pass.
			got := s.decline(context.Background(), uuid.New(), tc.req, signerID, "reason")
			if !errors.Is(got, tc.want) {
				t.Fatalf("decline = %v, want %v", got, tc.want)
			}
		})
	}
}

// fakeMemberDirectory stands in for the org member list notifyRequesterDeclined
// resolves both the creator's address and an internal decliner's display name from.
type fakeMemberDirectory struct{ members []OrgMember }

func (f fakeMemberDirectory) ListMembers(context.Context, uuid.UUID) ([]OrgMember, error) {
	return f.members, nil
}

// notifyRequesterDeclined resolves the creator's address and, for an internal
// signer, their display name, from the same member-directory call — mirroring how
// notifySigner resolves a member's address from an already-fetched list.
func TestNotifyRequesterDeclinedResolvesInternalSignerName(t *testing.T) {
	fn := &fakeNotifier{}
	creator, decliner := uuid.New(), uuid.New()
	s := &Service{
		notifier: fn,
		members: fakeMemberDirectory{members: []OrgMember{
			{UserID: creator, Name: "Requester Rita", Email: "rita@acme.example"},
			{UserID: decliner, Name: "Decliner Dan", Email: "dan@acme.example"},
		}},
	}
	req := Request{CreatedBy: creator, Filename: "Contract.pdf"}
	by := Signer{ID: uuid.New(), Kind: KindInternal, UserID: &decliner}

	s.notifyRequesterDeclined(context.Background(), uuid.New(), req, by, "not agreed")

	if len(fn.declined) != 1 || fn.declined[0] != "rita@acme.example" {
		t.Fatalf("notified requester = %v, want [rita@acme.example]", fn.declined)
	}
}

// An external signee already carries their own name/e-mail on the row (the store
// never looks them up in the member directory), so notifyRequesterDeclined must
// leave that alone rather than losing it to a directory lookup that finds no match.
func TestNotifyRequesterDeclinedKeepsExternalSignerIdentity(t *testing.T) {
	fn := &fakeNotifier{}
	creator := uuid.New()
	s := &Service{
		notifier: fn,
		members: fakeMemberDirectory{members: []OrgMember{
			{UserID: creator, Name: "Requester Rita", Email: "rita@acme.example"},
		}},
	}
	req := Request{CreatedBy: creator, Filename: "Contract.pdf"}
	by := Signer{ID: uuid.New(), Kind: KindExternal, Name: "Outside Olga", Email: "olga@example.org"}

	s.notifyRequesterDeclined(context.Background(), uuid.New(), req, by, "")

	if len(fn.declined) != 1 || fn.declined[0] != "rita@acme.example" {
		t.Fatalf("notified requester = %v, want [rita@acme.example]", fn.declined)
	}
}

// The creator's address cannot be resolved (an empty/no directory), so there is
// nowhere to send the notice — it must be skipped, not sent with a blank "to".
func TestNotifyRequesterDeclinedSkipsWhenCreatorUnresolved(t *testing.T) {
	fn := &fakeNotifier{}
	s := &Service{notifier: fn, members: fakeMemberDirectory{}}
	req := Request{CreatedBy: uuid.New(), Filename: "Contract.pdf"}
	by := Signer{ID: uuid.New(), Kind: KindExternal, Email: "olga@example.org"}

	s.notifyRequesterDeclined(context.Background(), uuid.New(), req, by, "")

	if len(fn.declined) != 0 {
		t.Fatalf("notified requester = %v, want none", fn.declined)
	}
}

func TestNotifyRequesterDeclinedNilNotifierIsNoop(t *testing.T) {
	s := &Service{}
	// Must not panic despite a nil notifier and a nil members directory.
	s.notifyRequesterDeclined(context.Background(), uuid.New(), Request{}, Signer{}, "")
}
