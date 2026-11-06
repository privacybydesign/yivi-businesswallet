//go:build integration

package proofing

import (
	"context"
	"encoding/json"
	"slices"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/privacybydesign/yivi-businesswallet/backend/internal/audit"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/proofingprovider"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/testdb"
)

// stepsFixture is an org with an endpoint for every event, and an approved
// external request that asks for the address and diplomas.
type stepsFixture struct {
	pool     *pgxpool.Pool
	requests *RequestStore
	diplomas *DiplomaStore
	orgID    uuid.UUID
	reviewer uuid.UUID
	req      Request
}

func newStepsFixture(t *testing.T) stepsFixture {
	t.Helper()
	pool, _ := testdb.Fresh(t)
	cipher := newTestCipher(t)
	f := stepsFixture{
		pool:     pool,
		requests: NewRequestStore(pool, audit.NewDBRecorder(), cipher),
		diplomas: NewDiplomaStore(pool, audit.NewDBRecorder()),
		orgID:    makeOrg(t, pool, "acme"),
		reviewer: makeUser(t, pool, "rita@example.org"),
	}
	ctx := context.Background()
	if _, _, err := NewWebhookStore(pool, audit.NopRecorder{}, cipher).Save(ctx, f.orgID,
		"https://hooks.example.org/x", WebhookEvents); err != nil {
		t.Fatalf("Save webhook: %v", err)
	}
	in := newStoredRequest(f.orgID, makeUser(t, pool, "sam@example.org"), externalSubject("anna@example.org"))
	in.Address, in.Diplomas = AddressRequired, DiplomasRequired
	req := createStarted(t, f.requests, in, "s1")
	if err := f.requests.RecordOutcome(ctx, req, "s1", StatusApproved,
		proofingprovider.Result{Status: proofingprovider.StatusApproved, Name: "Anna Jansen"}); err != nil {
		t.Fatalf("RecordOutcome: %v", err)
	}
	f.req = req
	f.req = f.get(t)
	return f
}

func (f stepsFixture) get(t *testing.T) Request {
	t.Helper()
	got, err := f.requests.Get(context.Background(), f.orgID, f.req.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	return got
}

// closeWindows moves the identity check past every step's window.
func (f stepsFixture) closeWindows(t *testing.T) {
	t.Helper()
	if _, err := f.pool.Exec(context.Background(), `UPDATE identity_proofing_requests
		SET checked_at = now() - interval '2 hours' WHERE id = $1`, f.req.ID); err != nil {
		t.Fatalf("close windows: %v", err)
	}
}

// assertStep checks that the session kept the identity's approval and that
// step kind reads state, with code.
func (f stepsFixture) assertStep(t *testing.T, kind StepKind, state StepState, code string, reviewed bool) {
	t.Helper()
	got := f.get(t)
	if got.Status != StatusApproved || got.ErrorCode != "" {
		t.Errorf("session = %s %q; want approved: a step never changes it", got.Status, got.ErrorCode)
	}
	i := slices.IndexFunc(got.Steps(time.Now()), func(s StepOutcome) bool { return s.Kind == kind })
	if i < 0 {
		t.Fatalf("no %s step", kind)
	}
	step := got.Steps(time.Now())[i]
	if step.State != state || step.Code != code || step.Reviewed != reviewed {
		t.Errorf("%s step = %+v; want %s %q reviewed %v", kind, step, state, code, reviewed)
	}
}

// sentEvent is a webhook delivery's event, with what its steps said of kind.
type sentEvent struct {
	event string
	state StepState
}

// assertSent checks every webhook sent for the request, oldest first, each
// once, with the state of step kind it carried.
func (f stepsFixture) assertSent(t *testing.T, kind StepKind, want []sentEvent) {
	t.Helper()
	rows, err := f.pool.Query(context.Background(), `SELECT event, COALESCE(payload->'steps', '[]')
		FROM identity_proofing_webhook_deliveries WHERE request_id = $1 ORDER BY created_at, id`, f.req.ID)
	if err != nil {
		t.Fatalf("query deliveries: %v", err)
	}
	defer rows.Close()
	var got []sentEvent
	for rows.Next() {
		var event string
		var raw []byte
		if err := rows.Scan(&event, &raw); err != nil {
			t.Fatalf("scan delivery: %v", err)
		}
		var steps []StepOutcome
		if err := json.Unmarshal(raw, &steps); err != nil {
			t.Fatalf("delivery steps: %v", err)
		}
		sent := sentEvent{event: event}
		if i := slices.IndexFunc(steps, func(s StepOutcome) bool { return s.Kind == kind }); i >= 0 {
			sent.state = steps[i].State
		}
		got = append(got, sent)
	}
	if !slices.Equal(got, want) {
		t.Errorf("webhooks = %+v; want %+v", got, want)
	}
}

// The webhooks every path starts with: the session created, its steps not
// started, then verified, its steps open.
var approvedEvents = []sentEvent{
	{EventSessionCreated, StepNotStarted},
	{EventSessionVerified, StepOpen},
}

func TestAddressStepPaths(t *testing.T) {
	registered := Address{Street: "Kerkstraat", HouseNumber: "12 A", PostalCode: "6511AB", City: "Nijmegen", Country: "Nederland"}
	brp := Address{Street: "Kerkstraat", HouseNumber: "12 A", PostalCode: "6511AB", City: "Nijmegen", Country: "Nederland", Verification: AddressRegistered}
	selfDeclared := Address{Street: "Molenstraat", HouseNumber: "3", PostalCode: "6511 AB", City: "Nijmegen", Country: "Nederland", Verification: AddressVerified}
	mismatch := selfDeclared
	mismatch.Verification, mismatch.Registered = AddressRegisteredMismatch, &registered

	for _, tc := range []struct {
		name string
		addr Address
		// decide is the reviewer's decision; nil for none.
		decide   *AddressReview
		state    StepState
		code     string
		reviewed bool
	}{
		{"BRP match", brp, nil, StepDone, "", false},
		{"self-declared, confirmed", selfDeclared, new(AddressReviewConfirmed), StepDone, "", true},
		{"self-declared, rejected", selfDeclared, new(AddressReviewRejected), StepRejected, ErrorAddressRejected, false},
		{"mismatch, confirmed", mismatch, new(AddressReviewConfirmed), StepDone, "", true},
		{"mismatch, rejected", mismatch, new(AddressReviewRejected), StepRejected, ErrorAddressRejected, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newStepsFixture(t)
			ctx := context.Background()
			if added, err := f.requests.AddAddress(ctx, f.req, tc.addr); err != nil || !added {
				t.Fatalf("AddAddress = %v, %v; want added", added, err)
			}
			want := append(slices.Clone(approvedEvents), sentEvent{EventSessionAddressAdded, StepDone})
			if tc.decide != nil {
				f.assertStep(t, StepAddress, StepInReview, "", false)
				want[len(want)-1].state = StepInReview
				if at, err := f.requests.DecideStepReview(ctx, f.get(t), ReviewKindAddress, f.reviewer, *tc.decide, "Checked"); err != nil || at == nil {
					t.Fatalf("DecideStepReview = %v, %v; want decided", at, err)
				}
				want = append(want, sentEvent{EventSessionAddressReviewed, tc.state})
			}
			f.closeWindows(t)
			f.assertStep(t, StepAddress, tc.state, tc.code, tc.reviewed)
			f.assertSent(t, StepAddress, want)
		})
	}

	t.Run("none in the window", func(t *testing.T) {
		f := newStepsFixture(t)
		f.assertStep(t, StepAddress, StepOpen, "", false)
		f.closeWindows(t)
		f.assertStep(t, StepAddress, StepNotProvided, ErrorAddressNotProvided, false)
		f.assertSent(t, StepAddress, approvedEvents)
	})
}

func TestDiplomaStepPaths(t *testing.T) {
	extract := func(number string) Diploma {
		return Diploma{
			DocumentType: "Diploma", Qualification: "HBO Bachelor Verpleegkunde", Profiles: []string{},
			Institution: "Hogeschool Utrecht", PlaceOfIssue: "Utrecht", DateAwarded: time.Date(2015, 7, 1, 0, 0, 0, 0, time.UTC),
			DocumentNumber: number,
		}
	}
	for _, tc := range []struct {
		name     string
		accepted int
		refused  int
		state    StepState
		code     string
	}{
		{"one accepted", 1, 0, StepDone, ""},
		{"several accepted", 2, 1, StepDone, ""},
		{"only refused uploads", 0, 2, StepNotProvided, ErrorDiplomaNotProvided},
		{"none in the window", 0, 0, StepNotProvided, ErrorDiplomaNotProvided},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newStepsFixture(t)
			ctx := context.Background()
			want := slices.Clone(approvedEvents)
			for range tc.refused {
				if err := f.diplomas.RecordRejected(ctx, f.req, "holder_mismatch", ""); err != nil {
					t.Fatalf("RecordRejected: %v", err)
				}
			}
			// A refused upload is no outcome while the window is open.
			f.assertStep(t, StepDiplomas, StepOpen, "", false)
			for i := range tc.accepted {
				if _, added, err := f.diplomas.Add(ctx, f.get(t), extract(string(rune('1'+i)))); err != nil || !added {
					t.Fatalf("Add = %v, %v; want added", added, err)
				}
				want = append(want, sentEvent{EventSessionDiplomaAdded, StepDone})
			}
			f.closeWindows(t)
			f.assertStep(t, StepDiplomas, tc.state, tc.code, false)
			f.assertSent(t, StepDiplomas, want)
		})
	}
}
