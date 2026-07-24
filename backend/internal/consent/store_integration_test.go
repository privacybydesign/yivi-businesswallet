//go:build integration

package consent_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/privacybydesign/yivi-businesswallet/backend/internal/audit"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/consent"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/testdb"
)

func createOrg(t *testing.T, pool *pgxpool.Pool, slug string) uuid.UUID {
	t.Helper()
	var id uuid.UUID
	if err := pool.QueryRow(context.Background(),
		`INSERT INTO organizations (name, slug, kvk_number, euid, digital_address)
		 VALUES ($1, $2, $3, $4, $5) RETURNING id`,
		slug, slug, "kvk-"+slug, "NL.KVK."+slug, slug+"@qerds.localhost").Scan(&id); err != nil {
		t.Fatalf("create org %q: %v", slug, err)
	}
	return id
}

func createUser(t *testing.T, pool *pgxpool.Pool, email string) uuid.UUID {
	t.Helper()
	var id uuid.UUID
	if err := pool.QueryRow(context.Background(),
		"INSERT INTO users (email, given_names, last_name) VALUES ($1, $2, $3) RETURNING id",
		email, "Test", "User").Scan(&id); err != nil {
		t.Fatalf("create user: %v", err)
	}
	return id
}

func countEvents(t *testing.T, pool *pgxpool.Pool, action string, orgID uuid.UUID) int {
	t.Helper()
	var n int
	if err := pool.QueryRow(context.Background(),
		"SELECT count(*) FROM audit_events WHERE action = $1 AND organization_id = $2", action, orgID).Scan(&n); err != nil {
		t.Fatalf("count audit events %q: %v", action, err)
	}
	return n
}

func enqueueParams(kind consent.Kind, counterparty string, attrs ...string) consent.EnqueueParams {
	return consent.EnqueueParams{
		Kind:         kind,
		Counterparty: counterparty,
		Requested:    attrs,
		ExpiresAt:    time.Now().Add(time.Hour),
	}
}

func TestEnqueueNoPolicyStaysPending(t *testing.T) {
	pool, _ := testdb.Fresh(t)
	store := consent.NewStore(pool, audit.NewDBRecorder())
	ctx := context.Background()
	orgID := createOrg(t, pool, "acme")

	req, err := store.Enqueue(ctx, orgID, enqueueParams(consent.KindPresentation, "verifier.example", "email", "name"))
	if err != nil {
		t.Fatalf("Enqueue: %v", err)
	}
	if req.Status != consent.StatusPending || req.Mode != consent.ModeHumanApproved {
		t.Errorf("status/mode = %s/%s, want pending/human_approved", req.Status, req.Mode)
	}
	if req.PolicyID != nil {
		t.Errorf("policyId = %v, want nil (no policy matched)", req.PolicyID)
	}
	if got := countEvents(t, pool, audit.ApprovalRequested, orgID); got != 1 {
		t.Errorf("approval.requested events = %d, want 1", got)
	}
}

func TestEnqueueAutoApprovePolicy(t *testing.T) {
	pool, _ := testdb.Fresh(t)
	store := consent.NewStore(pool, audit.NewDBRecorder())
	ctx := context.Background()
	orgID := createOrg(t, pool, "acme")
	admin := createUser(t, pool, "admin@example.test")

	p, err := store.CreatePolicy(ctx, orgID, admin, consent.PolicySpec{
		Kind:                consent.KindIssuance,
		CounterpartyPattern: "trusted.*",
		Effect:              consent.EffectAutoApprove,
		ApproveSubset:       []string{"diploma"},
	})
	if err != nil {
		t.Fatalf("CreatePolicy: %v", err)
	}

	req, err := store.Enqueue(ctx, orgID, enqueueParams(consent.KindIssuance, "trusted.university", "diploma", "grade"))
	if err != nil {
		t.Fatalf("Enqueue: %v", err)
	}
	if req.Status != consent.StatusApproved || req.Mode != consent.ModePolicyAuto {
		t.Errorf("status/mode = %s/%s, want approved/policy_auto", req.Status, req.Mode)
	}
	if req.PolicyID == nil || *req.PolicyID != p.ID {
		t.Errorf("policyId = %v, want %s", req.PolicyID, p.ID)
	}
	// The policy narrowed to {diploma}; grade must not be auto-approved.
	if len(req.DecidedSubset) != 1 || req.DecidedSubset[0] != "diploma" {
		t.Errorf("decidedSubset = %v, want [diploma]", req.DecidedSubset)
	}
	if got := countEvents(t, pool, audit.ApprovalAutoApproved, orgID); got != 1 {
		t.Errorf("approval.auto_approved events = %d, want 1", got)
	}
}

func TestEnqueueAutoApproveEmptySubsetStaysPending(t *testing.T) {
	pool, _ := testdb.Fresh(t)
	store := consent.NewStore(pool, audit.NewDBRecorder())
	ctx := context.Background()
	orgID := createOrg(t, pool, "acme")
	admin := createUser(t, pool, "admin@example.test")

	// The policy matches on counterparty but narrows approval to an attribute this
	// request never asked for, so resolving its subset against the request yields
	// nothing: the policy would auto-approve an empty disclosure.
	if _, err := store.CreatePolicy(ctx, orgID, admin, consent.PolicySpec{
		Kind:                consent.KindPresentation,
		CounterpartyPattern: "*",
		Effect:              consent.EffectAutoApprove,
		ApproveSubset:       []string{"bsn"},
	}); err != nil {
		t.Fatalf("CreatePolicy: %v", err)
	}

	req, err := store.Enqueue(ctx, orgID, enqueueParams(consent.KindPresentation, "verifier.example", "email", "name"))
	if err != nil {
		t.Fatalf("Enqueue: %v", err)
	}
	// An "approved nothing" record is exactly what the human path forbids; the item
	// must fall to a human instead of being auto-approved with an empty subset.
	if req.Status != consent.StatusPending || req.Mode != consent.ModeHumanApproved {
		t.Errorf("status/mode = %s/%s, want pending/human_approved", req.Status, req.Mode)
	}
	if len(req.DecidedSubset) != 0 {
		t.Errorf("decidedSubset = %v, want empty", req.DecidedSubset)
	}
	if req.PolicyID != nil {
		t.Errorf("policyId = %v, want nil (no effective auto-approve)", req.PolicyID)
	}
	if got := countEvents(t, pool, audit.ApprovalAutoApproved, orgID); got != 0 {
		t.Errorf("approval.auto_approved events = %d, want 0", got)
	}
	if got := countEvents(t, pool, audit.ApprovalRequested, orgID); got != 1 {
		t.Errorf("approval.requested events = %d, want 1", got)
	}
}

func TestEnqueueAutoDeclinePolicy(t *testing.T) {
	pool, _ := testdb.Fresh(t)
	store := consent.NewStore(pool, audit.NewDBRecorder())
	ctx := context.Background()
	orgID := createOrg(t, pool, "acme")
	admin := createUser(t, pool, "admin@example.test")

	if _, err := store.CreatePolicy(ctx, orgID, admin, consent.PolicySpec{
		Kind:                consent.KindPresentation,
		CounterpartyPattern: "*",
		RequiredAttributes:  []string{"bsn"},
		Effect:              consent.EffectAutoDecline,
	}); err != nil {
		t.Fatalf("CreatePolicy: %v", err)
	}

	req, err := store.Enqueue(ctx, orgID, enqueueParams(consent.KindPresentation, "shady.example", "bsn", "name"))
	if err != nil {
		t.Fatalf("Enqueue: %v", err)
	}
	if req.Status != consent.StatusDeclined || req.Mode != consent.ModePolicyAuto {
		t.Errorf("status/mode = %s/%s, want declined/policy_auto", req.Status, req.Mode)
	}
	if got := countEvents(t, pool, audit.ApprovalAutoDeclined, orgID); got != 1 {
		t.Errorf("approval.auto_declined events = %d, want 1", got)
	}
}

func TestEnqueueFourEyesBeatsAutoApprove(t *testing.T) {
	pool, _ := testdb.Fresh(t)
	store := consent.NewStore(pool, audit.NewDBRecorder())
	ctx := context.Background()
	orgID := createOrg(t, pool, "acme")
	admin := createUser(t, pool, "admin@example.test")

	if _, err := store.CreatePolicy(ctx, orgID, admin, consent.PolicySpec{
		Kind:                consent.KindPresentation,
		CounterpartyPattern: "*",
		Effect:              consent.EffectAutoApprove,
		FourEyes:            true,
	}); err != nil {
		t.Fatalf("CreatePolicy: %v", err)
	}

	req, err := store.Enqueue(ctx, orgID, enqueueParams(consent.KindPresentation, "verifier.example", "email"))
	if err != nil {
		t.Fatalf("Enqueue: %v", err)
	}
	// Dual control is a floor a policy cannot waive: the item is queued, not approved.
	if req.Status != consent.StatusPending || req.Mode != consent.ModeFourEyes {
		t.Errorf("status/mode = %s/%s, want pending/four_eyes", req.Status, req.Mode)
	}
}

func TestEnqueueRevokedPolicyIgnored(t *testing.T) {
	pool, _ := testdb.Fresh(t)
	store := consent.NewStore(pool, audit.NewDBRecorder())
	ctx := context.Background()
	orgID := createOrg(t, pool, "acme")
	admin := createUser(t, pool, "admin@example.test")

	p, err := store.CreatePolicy(ctx, orgID, admin, consent.PolicySpec{
		Kind:                consent.KindPresentation,
		CounterpartyPattern: "*",
		Effect:              consent.EffectAutoApprove,
	})
	if err != nil {
		t.Fatalf("CreatePolicy: %v", err)
	}
	if _, err := store.RevokePolicy(ctx, orgID, p.ID); err != nil {
		t.Fatalf("RevokePolicy: %v", err)
	}

	req, err := store.Enqueue(ctx, orgID, enqueueParams(consent.KindPresentation, "verifier.example", "email"))
	if err != nil {
		t.Fatalf("Enqueue: %v", err)
	}
	if req.Status != consent.StatusPending {
		t.Errorf("status = %s, want pending (revoked policy must not match)", req.Status)
	}
}

func TestEnqueueFirstMatchWins(t *testing.T) {
	pool, _ := testdb.Fresh(t)
	store := consent.NewStore(pool, audit.NewDBRecorder())
	ctx := context.Background()
	orgID := createOrg(t, pool, "acme")
	admin := createUser(t, pool, "admin@example.test")

	// Lower priority number is evaluated first: the decline wins over the approve.
	if _, err := store.CreatePolicy(ctx, orgID, admin, consent.PolicySpec{
		Kind: consent.KindPresentation, CounterpartyPattern: "*", Effect: consent.EffectAutoDecline, Priority: 1,
	}); err != nil {
		t.Fatalf("CreatePolicy decline: %v", err)
	}
	if _, err := store.CreatePolicy(ctx, orgID, admin, consent.PolicySpec{
		Kind: consent.KindPresentation, CounterpartyPattern: "*", Effect: consent.EffectAutoApprove, Priority: 2,
	}); err != nil {
		t.Fatalf("CreatePolicy approve: %v", err)
	}

	req, err := store.Enqueue(ctx, orgID, enqueueParams(consent.KindPresentation, "verifier.example", "email"))
	if err != nil {
		t.Fatalf("Enqueue: %v", err)
	}
	if req.Status != consent.StatusDeclined {
		t.Errorf("status = %s, want declined (first-match-wins by priority)", req.Status)
	}
}

func TestDecideHumanApproved(t *testing.T) {
	pool, _ := testdb.Fresh(t)
	store := consent.NewStore(pool, audit.NewDBRecorder())
	ctx := context.Background()
	orgID := createOrg(t, pool, "acme")
	approver := createUser(t, pool, "approver@example.test")

	req, err := store.Enqueue(ctx, orgID, enqueueParams(consent.KindPresentation, "verifier.example", "email", "name", "age"))
	if err != nil {
		t.Fatalf("Enqueue: %v", err)
	}

	decided, err := store.Decide(ctx, orgID, req.ID, approver, true, []string{"email", "name"})
	if err != nil {
		t.Fatalf("Decide: %v", err)
	}
	if decided.Status != consent.StatusApproved {
		t.Errorf("status = %s, want approved", decided.Status)
	}
	if decided.DecidedBy == nil || *decided.DecidedBy != approver {
		t.Errorf("decidedBy = %v, want %s", decided.DecidedBy, approver)
	}
	if len(decided.DecidedSubset) != 2 {
		t.Errorf("decidedSubset = %v, want 2 attributes", decided.DecidedSubset)
	}
	if got := countEvents(t, pool, audit.ApprovalApproved, orgID); got != 1 {
		t.Errorf("approval.approved events = %d, want 1", got)
	}

	// A resolved item cannot be decided again.
	if _, err := store.Decide(ctx, orgID, req.ID, approver, true, []string{"email"}); !errors.Is(err, consent.ErrRequestResolved) {
		t.Errorf("second Decide err = %v, want ErrRequestResolved", err)
	}
}

func TestDecideSubsetValidation(t *testing.T) {
	pool, _ := testdb.Fresh(t)
	store := consent.NewStore(pool, audit.NewDBRecorder())
	ctx := context.Background()
	orgID := createOrg(t, pool, "acme")
	approver := createUser(t, pool, "approver@example.test")

	req, err := store.Enqueue(ctx, orgID, enqueueParams(consent.KindPresentation, "verifier.example", "email"))
	if err != nil {
		t.Fatalf("Enqueue: %v", err)
	}
	if _, err := store.Decide(ctx, orgID, req.ID, approver, true, nil); !errors.Is(err, consent.ErrEmptySubset) {
		t.Errorf("empty subset err = %v, want ErrEmptySubset", err)
	}
	if _, err := store.Decide(ctx, orgID, req.ID, approver, true, []string{"bsn"}); !errors.Is(err, consent.ErrSubsetNotSubset) {
		t.Errorf("out-of-set subset err = %v, want ErrSubsetNotSubset", err)
	}
}

func TestDecideDeclineHumanApproved(t *testing.T) {
	pool, _ := testdb.Fresh(t)
	store := consent.NewStore(pool, audit.NewDBRecorder())
	ctx := context.Background()
	orgID := createOrg(t, pool, "acme")
	approver := createUser(t, pool, "approver@example.test")

	req, err := store.Enqueue(ctx, orgID, enqueueParams(consent.KindIssuance, "issuer.example", "diploma"))
	if err != nil {
		t.Fatalf("Enqueue: %v", err)
	}
	decided, err := store.Decide(ctx, orgID, req.ID, approver, false, nil)
	if err != nil {
		t.Fatalf("Decide decline: %v", err)
	}
	if decided.Status != consent.StatusDeclined {
		t.Errorf("status = %s, want declined", decided.Status)
	}
	if got := countEvents(t, pool, audit.ApprovalDeclined, orgID); got != 1 {
		t.Errorf("approval.declined events = %d, want 1", got)
	}
}

func TestFourEyesFlow(t *testing.T) {
	pool, _ := testdb.Fresh(t)
	store := consent.NewStore(pool, audit.NewDBRecorder())
	ctx := context.Background()
	orgID := createOrg(t, pool, "acme")
	first := createUser(t, pool, "first@example.test")
	second := createUser(t, pool, "second@example.test")

	req, err := store.Enqueue(ctx, orgID, consent.EnqueueParams{
		Kind: consent.KindPresentation, Counterparty: "verifier.example",
		Requested: []string{"email", "name"}, ExpiresAt: time.Now().Add(time.Hour), ForceDualControl: true,
	})
	if err != nil {
		t.Fatalf("Enqueue: %v", err)
	}
	if req.Mode != consent.ModeFourEyes {
		t.Fatalf("mode = %s, want four_eyes", req.Mode)
	}

	// First approval keeps the item pending; no terminal audit yet.
	afterFirst, err := store.Decide(ctx, orgID, req.ID, first, true, []string{"email"})
	if err != nil {
		t.Fatalf("first Decide: %v", err)
	}
	if afterFirst.Status != consent.StatusPending {
		t.Errorf("status after first approval = %s, want pending", afterFirst.Status)
	}
	if got := countEvents(t, pool, audit.ApprovalApproved, orgID); got != 0 {
		t.Errorf("approval.approved events after first approval = %d, want 0", got)
	}

	// The second approver must be a distinct subject.
	if _, err := store.DecideDual(ctx, orgID, req.ID, first, true); !errors.Is(err, consent.ErrSameApprover) {
		t.Errorf("same-approver dual err = %v, want ErrSameApprover", err)
	}

	done, err := store.DecideDual(ctx, orgID, req.ID, second, true)
	if err != nil {
		t.Fatalf("DecideDual: %v", err)
	}
	if done.Status != consent.StatusApproved {
		t.Errorf("status = %s, want approved", done.Status)
	}
	if done.DualDecidedBy == nil || *done.DualDecidedBy != second {
		t.Errorf("dualDecidedBy = %v, want %s", done.DualDecidedBy, second)
	}
	// The first approver's subset stands.
	if len(done.DecidedSubset) != 1 || done.DecidedSubset[0] != "email" {
		t.Errorf("decidedSubset = %v, want [email]", done.DecidedSubset)
	}
	if got := countEvents(t, pool, audit.ApprovalApproved, orgID); got != 1 {
		t.Errorf("approval.approved events = %d, want 1", got)
	}
}

func TestFourEyesDeclineAfterFirstApprovalAttributesDecliner(t *testing.T) {
	pool, _ := testdb.Fresh(t)
	store := consent.NewStore(pool, audit.NewDBRecorder())
	ctx := context.Background()
	orgID := createOrg(t, pool, "acme")
	first := createUser(t, pool, "first@example.test")
	second := createUser(t, pool, "second@example.test")

	req, err := store.Enqueue(ctx, orgID, consent.EnqueueParams{
		Kind: consent.KindPresentation, Counterparty: "verifier.example",
		Requested: []string{"email", "name"}, ExpiresAt: time.Now().Add(time.Hour), ForceDualControl: true,
	})
	if err != nil {
		t.Fatalf("Enqueue: %v", err)
	}
	if _, err := store.Decide(ctx, orgID, req.ID, first, true, []string{"email"}); err != nil {
		t.Fatalf("first Decide: %v", err)
	}

	// A distinct second actor declines through Decide (a decline never needs
	// DecideDual). The item resolves, and the decline must be attributed to the
	// decliner, not silently kept against the first approver.
	declined, err := store.Decide(ctx, orgID, req.ID, second, false, nil)
	if err != nil {
		t.Fatalf("second Decide decline: %v", err)
	}
	if declined.Status != consent.StatusDeclined {
		t.Errorf("status = %s, want declined", declined.Status)
	}
	if declined.DecidedBy == nil || *declined.DecidedBy != first {
		t.Errorf("decidedBy = %v, want the first approver %s", declined.DecidedBy, first)
	}
	if declined.DualDecidedBy == nil || *declined.DualDecidedBy != second {
		t.Errorf("dualDecidedBy = %v, want the decliner %s", declined.DualDecidedBy, second)
	}
	if got := countEvents(t, pool, audit.ApprovalDeclined, orgID); got != 1 {
		t.Errorf("approval.declined events = %d, want 1", got)
	}
}

func TestDecideDualErrors(t *testing.T) {
	pool, _ := testdb.Fresh(t)
	store := consent.NewStore(pool, audit.NewDBRecorder())
	ctx := context.Background()
	orgID := createOrg(t, pool, "acme")
	approver := createUser(t, pool, "approver@example.test")

	// Non-four-eyes item rejects DecideDual.
	single, err := store.Enqueue(ctx, orgID, enqueueParams(consent.KindPresentation, "verifier.example", "email"))
	if err != nil {
		t.Fatalf("Enqueue: %v", err)
	}
	if _, err := store.DecideDual(ctx, orgID, single.ID, approver, true); !errors.Is(err, consent.ErrNotDualControl) {
		t.Errorf("DecideDual on single err = %v, want ErrNotDualControl", err)
	}

	// Four-eyes item with no first approval rejects DecideDual.
	dual, err := store.Enqueue(ctx, orgID, consent.EnqueueParams{
		Kind: consent.KindPresentation, Counterparty: "verifier.example",
		Requested: []string{"email"}, ExpiresAt: time.Now().Add(time.Hour), ForceDualControl: true,
	})
	if err != nil {
		t.Fatalf("Enqueue dual: %v", err)
	}
	if _, err := store.DecideDual(ctx, orgID, dual.ID, approver, true); !errors.Is(err, consent.ErrAwaitingFirstApproval) {
		t.Errorf("DecideDual before first err = %v, want ErrAwaitingFirstApproval", err)
	}
}

func TestListPending(t *testing.T) {
	pool, _ := testdb.Fresh(t)
	store := consent.NewStore(pool, audit.NewDBRecorder())
	ctx := context.Background()
	orgID := createOrg(t, pool, "acme")
	approver := createUser(t, pool, "approver@example.test")

	a, err := store.Enqueue(ctx, orgID, enqueueParams(consent.KindPresentation, "one.example", "email"))
	if err != nil {
		t.Fatalf("Enqueue a: %v", err)
	}
	if _, err := store.Enqueue(ctx, orgID, enqueueParams(consent.KindIssuance, "two.example", "diploma")); err != nil {
		t.Fatalf("Enqueue b: %v", err)
	}

	pending, err := store.ListPending(ctx, orgID)
	if err != nil {
		t.Fatalf("ListPending: %v", err)
	}
	if len(pending) != 2 {
		t.Fatalf("pending = %d, want 2", len(pending))
	}

	// Deciding one drops it from the pending list.
	if _, err := store.Decide(ctx, orgID, a.ID, approver, false, nil); err != nil {
		t.Fatalf("Decide: %v", err)
	}
	pending, err = store.ListPending(ctx, orgID)
	if err != nil {
		t.Fatalf("ListPending 2: %v", err)
	}
	if len(pending) != 1 {
		t.Errorf("pending after one decision = %d, want 1", len(pending))
	}
}

func TestSweepExpired(t *testing.T) {
	pool, _ := testdb.Fresh(t)
	store := consent.NewStore(pool, audit.NewDBRecorder())
	ctx := context.Background()
	orgID := createOrg(t, pool, "acme")

	// One already-expired, one still valid.
	if _, err := store.Enqueue(ctx, orgID, consent.EnqueueParams{
		Kind: consent.KindPresentation, Counterparty: "old.example",
		Requested: []string{"email"}, ExpiresAt: time.Now().Add(-time.Hour),
	}); err != nil {
		t.Fatalf("Enqueue expired: %v", err)
	}
	if _, err := store.Enqueue(ctx, orgID, enqueueParams(consent.KindPresentation, "fresh.example", "email")); err != nil {
		t.Fatalf("Enqueue fresh: %v", err)
	}

	n, err := store.SweepExpired(ctx, orgID)
	if err != nil {
		t.Fatalf("SweepExpired: %v", err)
	}
	if n != 1 {
		t.Errorf("swept = %d, want 1", n)
	}
	if got := countEvents(t, pool, audit.ApprovalExpired, orgID); got != 1 {
		t.Errorf("approval.expired events = %d, want 1", got)
	}
	pending, err := store.ListPending(ctx, orgID)
	if err != nil {
		t.Fatalf("ListPending: %v", err)
	}
	if len(pending) != 1 {
		t.Errorf("pending after sweep = %d, want 1 (the fresh one)", len(pending))
	}
}

func TestPolicyCRUD(t *testing.T) {
	pool, _ := testdb.Fresh(t)
	store := consent.NewStore(pool, audit.NewDBRecorder())
	ctx := context.Background()
	orgID := createOrg(t, pool, "acme")
	admin := createUser(t, pool, "admin@example.test")

	p, err := store.CreatePolicy(ctx, orgID, admin, consent.PolicySpec{
		Kind: consent.KindIssuance, CounterpartyPattern: "trusted.*", Effect: consent.EffectAutoApprove, Priority: 5,
	})
	if err != nil {
		t.Fatalf("CreatePolicy: %v", err)
	}
	if p.CreatedBy == nil || *p.CreatedBy != admin {
		t.Errorf("createdBy = %v, want %s", p.CreatedBy, admin)
	}
	if got := countEvents(t, pool, audit.PolicyCreated, orgID); got != 1 {
		t.Errorf("policy.created events = %d, want 1", got)
	}

	updated, err := store.UpdatePolicy(ctx, orgID, p.ID, consent.PolicySpec{
		Kind: consent.KindIssuance, CounterpartyPattern: "trusted.*", Effect: consent.EffectAutoDecline, Priority: 5,
	})
	if err != nil {
		t.Fatalf("UpdatePolicy: %v", err)
	}
	if updated.Effect != consent.EffectAutoDecline {
		t.Errorf("effect = %s, want auto_decline", updated.Effect)
	}
	if got := countEvents(t, pool, audit.PolicyUpdated, orgID); got != 1 {
		t.Errorf("policy.updated events = %d, want 1", got)
	}

	active, err := store.ListPolicies(ctx, orgID)
	if err != nil {
		t.Fatalf("ListPolicies: %v", err)
	}
	if len(active) != 1 {
		t.Fatalf("active policies = %d, want 1", len(active))
	}

	if _, err := store.RevokePolicy(ctx, orgID, p.ID); err != nil {
		t.Fatalf("RevokePolicy: %v", err)
	}
	if got := countEvents(t, pool, audit.PolicyRevoked, orgID); got != 1 {
		t.Errorf("policy.revoked events = %d, want 1", got)
	}
	active, err = store.ListPolicies(ctx, orgID)
	if err != nil {
		t.Fatalf("ListPolicies after revoke: %v", err)
	}
	if len(active) != 0 {
		t.Errorf("active policies after revoke = %d, want 0", len(active))
	}

	// A revoked policy cannot be edited or revoked again.
	if _, err := store.UpdatePolicy(ctx, orgID, p.ID, consent.PolicySpec{
		Kind: consent.KindIssuance, CounterpartyPattern: "trusted.*", Effect: consent.EffectAutoApprove,
	}); !errors.Is(err, consent.ErrPolicyRevoked) {
		t.Errorf("update revoked err = %v, want ErrPolicyRevoked", err)
	}
}

func TestPolicyNotFound(t *testing.T) {
	pool, _ := testdb.Fresh(t)
	store := consent.NewStore(pool, audit.NewDBRecorder())
	ctx := context.Background()
	orgID := createOrg(t, pool, "acme")

	if _, err := store.RevokePolicy(ctx, orgID, uuid.New()); !errors.Is(err, consent.ErrPolicyNotFound) {
		t.Errorf("revoke missing err = %v, want ErrPolicyNotFound", err)
	}
}

func TestEnqueueValidation(t *testing.T) {
	pool, _ := testdb.Fresh(t)
	store := consent.NewStore(pool, audit.NewDBRecorder())
	ctx := context.Background()
	orgID := createOrg(t, pool, "acme")

	if _, err := store.Enqueue(ctx, orgID, consent.EnqueueParams{
		Kind: "bogus", Counterparty: "x", Requested: []string{"email"}, ExpiresAt: time.Now().Add(time.Hour),
	}); !errors.Is(err, consent.ErrInvalidKind) {
		t.Errorf("bad-kind err = %v, want ErrInvalidKind", err)
	}
	if _, err := store.Enqueue(ctx, orgID, consent.EnqueueParams{
		Kind: consent.KindPresentation, Counterparty: "x", ExpiresAt: time.Now().Add(time.Hour),
	}); !errors.Is(err, consent.ErrNoAttributes) {
		t.Errorf("no-attrs err = %v, want ErrNoAttributes", err)
	}
}
