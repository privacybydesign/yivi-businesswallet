//go:build integration

package providerlisting

import (
	"context"
	"errors"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/privacybydesign/yivi-businesswallet/backend/internal/audit"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/organization"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/testdb"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/user"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/wallet"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/walletunit"
)

type sentMail struct {
	kind       string
	orgID      uuid.UUID
	recipients []string
}

type captureMailer struct {
	mu   sync.Mutex
	sent []sentMail
}

func (m *captureMailer) record(kind string, orgID uuid.UUID, recipients []string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.sent = append(m.sent, sentMail{kind: kind, orgID: orgID, recipients: recipients})
	return nil
}

func (m *captureMailer) SendProviderDelisted(_ context.Context, orgID uuid.UUID, recipients []string, _ string) error {
	return m.record("delisted", orgID, recipients)
}

func (m *captureMailer) SendProviderRelisted(_ context.Context, orgID uuid.UUID, recipients []string, _ string) error {
	return m.record("relisted", orgID, recipients)
}

// mailed returns the orgs mailed kind, in order.
func (m *captureMailer) mailed(kind string) []uuid.UUID {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []uuid.UUID
	for _, s := range m.sent {
		if s.kind == kind {
			out = append(out, s.orgID)
		}
	}
	return out
}

type unreadableList struct{}

func (unreadableList) Ping(context.Context) error { return nil }

func (unreadableList) ProviderListed(context.Context) (bool, error) {
	return false, errors.New("trusted list unreachable")
}

type fixture struct {
	pool    *pgxpool.Pool
	list    *StubList
	mail    *captureMailer
	wallets *wallet.Service
	orgs    *organization.Store
}

func newFixture(t *testing.T) fixture {
	t.Helper()
	pool, _ := testdb.Fresh(t)
	units := walletunit.NewStore(pool, audit.NopRecorder{})
	store := wallet.NewStore(pool, audit.NewDBRecorder(), units)
	return fixture{
		pool:    pool,
		list:    NewStubList(pool),
		mail:    &captureMailer{},
		wallets: wallet.NewService(store, nil, wallet.OwnerID{}, nil, nil, nil, ""),
		orgs:    organization.NewStore(pool, audit.NopRecorder{}),
	}
}

func (f fixture) watcher(list List, grace time.Duration) *Watcher {
	return NewWatcher(f.pool, list, f.wallets, NewMailNotifier(f.mail, f.orgs), grace)
}

// seedOrg creates an org with an active wallet unit and one admin.
func (f fixture) seedOrg(t *testing.T, slug string) uuid.UUID {
	t.Helper()
	ctx := context.Background()
	var orgID uuid.UUID
	if err := f.pool.QueryRow(ctx, `INSERT INTO organizations (name, slug, kvk_number, euid, digital_address)
		VALUES ($1, $2, $3, $4, $5) RETURNING id`, slug, slug, "kvk-"+slug, "NLNHR."+slug, slug+"@qerds.localhost").Scan(&orgID); err != nil {
		t.Fatalf("create org %q: %v", slug, err)
	}
	if _, err := f.pool.Exec(ctx, `INSERT INTO wallet_units (organization_id) VALUES ($1)`, orgID); err != nil {
		t.Fatalf("wallet unit %q: %v", slug, err)
	}
	admin, err := user.NewStore(f.pool).Create(ctx, user.User{Email: user.Email("admin@" + slug + ".example"), GivenNames: "Admin", LastName: slug})
	if err != nil {
		t.Fatalf("admin %q: %v", slug, err)
	}
	if _, err := f.orgs.AddMembership(ctx, orgID, admin.ID, organization.RoleAdmin, nil, nil); err != nil {
		t.Fatalf("admin membership %q: %v", slug, err)
	}
	return orgID
}

// status returns the org's wallet status and its latest unit's status and reason,
// with how many units it had.
func (f fixture) status(t *testing.T, orgID uuid.UUID) (wallet, unit, reason string, units int) {
	t.Helper()
	var r *string
	if err := f.pool.QueryRow(context.Background(), `SELECT o.status, u.status, u.revoked_reason,
			(SELECT count(*) FROM wallet_units WHERE organization_id = o.id)
		FROM organizations o JOIN wallet_units u ON u.organization_id = o.id WHERE o.id = $1
		ORDER BY (u.status = 'active') DESC, u.created_at DESC LIMIT 1`, orgID).
		Scan(&wallet, &unit, &r, &units); err != nil {
		t.Fatalf("status of %s: %v", orgID, err)
	}
	if r != nil {
		reason = *r
	}
	return wallet, unit, reason, units
}

// wantActive fails unless the org's wallet and its latest unit are active, and it
// had units units.
func (f fixture) wantActive(t *testing.T, orgID uuid.UUID, units int) {
	t.Helper()
	if wallet, unit, _, n := f.status(t, orgID); wallet != organization.StatusActive || unit != walletunit.UnitActive || n != units {
		t.Fatalf("org %s = wallet %q, unit %q, %d units; want active, active, %d units", orgID, wallet, unit, n, units)
	}
}

// wantDelisted fails unless the org's unit is revoked as delisted and its wallet
// is in wallet status.
func (f fixture) wantDelisted(t *testing.T, orgID uuid.UUID, wallet string) {
	t.Helper()
	if w, unit, reason, _ := f.status(t, orgID); w != wallet || unit != walletunit.UnitRevoked || reason != walletunit.ReasonProviderDelisted {
		t.Fatalf("org %s = wallet %q, unit %q for %q; want wallet %q, unit revoked for %q",
			orgID, w, unit, reason, wallet, walletunit.ReasonProviderDelisted)
	}
}

func (f fixture) setListed(t *testing.T, listed bool) {
	t.Helper()
	if err := f.list.SetListed(context.Background(), listed); err != nil {
		t.Fatalf("SetListed(%v): %v", listed, err)
	}
}

// Off the list revokes every wallet unit, suspends every active wallet and mails
// the admins; back on it the wallets we suspended are active again with a new
// unit. A wallet an admin suspended stays suspended, and one revoked in between
// stays revoked.
func TestOffTheListRevokesUnitsUntilBackOnIt(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	acme, globex, initech := f.seedOrg(t, "acme"), f.seedOrg(t, "globex"), f.seedOrg(t, "initech")
	if _, err := f.wallets.Suspend(ctx, initech); err != nil {
		t.Fatalf("admin suspends initech: %v", err)
	}
	w := f.watcher(f.list, 0)

	if n, err := w.Check(ctx); err != nil || n != 0 {
		t.Fatalf("Check while listed = %d, %v; want 0, nil", n, err)
	}
	f.wantActive(t, acme, 1)

	f.setListed(t, false)
	if n, err := w.Check(ctx); err != nil || n != 3 {
		t.Fatalf("Check off the list = %d, %v; want 3, nil", n, err)
	}
	for _, org := range []uuid.UUID{acme, globex, initech} {
		f.wantDelisted(t, org, organization.StatusSuspended)
	}
	if got := f.mail.mailed("delisted"); !slices.Equal(got, []uuid.UUID{acme, globex, initech}) {
		t.Fatalf("delisted mails to %v, want every org", got)
	}
	for _, m := range f.mail.sent {
		if len(m.recipients) != 1 || !slices.Contains([]string{"admin@acme.example", "admin@globex.example", "admin@initech.example"}, m.recipients[0]) {
			t.Fatalf("mail for %s went to %v, want its admin", m.orgID, m.recipients)
		}
	}

	if n, err := w.Check(ctx); err != nil || n != 0 {
		t.Fatalf("second Check off the list = %d, %v; want 0, nil", n, err)
	}
	if err := f.wallets.OwnerIDRevoked(ctx, globex); err != nil {
		t.Fatalf("globex ceases: %v", err)
	}

	f.setListed(t, true)
	if n, err := w.Check(ctx); err != nil || n != 1 {
		t.Fatalf("Check back on the list = %d, %v; want 1, nil", n, err)
	}
	f.wantActive(t, acme, 2)
	f.wantDelisted(t, initech, organization.StatusSuspended)
	f.wantDelisted(t, globex, organization.StatusRevoked)
	if got := f.mail.mailed("relisted"); !slices.Equal(got, []uuid.UUID{acme}) {
		t.Fatalf("relisted mails to %v, want acme", got)
	}

	if n, err := w.Check(ctx); err != nil || n != 0 {
		t.Fatalf("next Check while listed = %d, %v; want 0, nil", n, err)
	}
	if len(f.mail.sent) != 4 {
		t.Fatalf("mails = %d, want no new ones", len(f.mail.sent))
	}
}

// The wallets stop only once we are off the list for the whole grace; being
// listed again in between starts it over.
func TestGraceKeepsWalletsUntilItRunsOut(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	acme := f.seedOrg(t, "acme")
	w := f.watcher(f.list, time.Hour)
	start := time.Now()
	at := func(d time.Duration) { w.now = func() time.Time { return start.Add(d) } }

	if err := f.list.SetListed(ctx, false); err != nil {
		t.Fatalf("SetListed: %v", err)
	}
	for _, d := range []time.Duration{0, 59 * time.Minute} {
		at(d)
		if n, err := w.Check(ctx); err != nil || n != 0 {
			t.Fatalf("Check %v off the list = %d, %v; want 0, nil", d, n, err)
		}
	}

	if err := f.list.SetListed(ctx, true); err != nil {
		t.Fatalf("SetListed: %v", err)
	}
	at(90 * time.Minute)
	if _, err := w.Check(ctx); err != nil {
		t.Fatalf("Check listed: %v", err)
	}
	if err := f.list.SetListed(ctx, false); err != nil {
		t.Fatalf("SetListed: %v", err)
	}
	at(2 * time.Hour)
	if n, err := w.Check(ctx); err != nil || n != 0 {
		t.Fatalf("Check off again = %d, %v; want 0, the grace starts over", n, err)
	}
	f.wantActive(t, acme, 1)

	at(3 * time.Hour)
	if n, err := w.Check(ctx); err != nil || n != 1 {
		t.Fatalf("Check after the grace = %d, %v; want 1, nil", n, err)
	}
}

// A list that cannot be read suspends nothing and does not start the grace.
func TestUnreadableListChangesNothing(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	acme := f.seedOrg(t, "acme")

	if n, err := f.watcher(unreadableList{}, 0).Check(ctx); err == nil || n != 0 {
		t.Fatalf("Check on an unreadable list = %d, %v; want 0 and an error", n, err)
	}
	f.wantActive(t, acme, 1)
	var rows int
	if err := f.pool.QueryRow(ctx, `SELECT count(*) FROM provider_listing_state WHERE missing_since IS NOT NULL`).Scan(&rows); err != nil || rows != 0 {
		t.Fatalf("missing_since rows = %d, %v; want 0", rows, err)
	}
	if len(f.mail.sent) != 0 {
		t.Fatalf("mails = %d, want none", len(f.mail.sent))
	}
}
