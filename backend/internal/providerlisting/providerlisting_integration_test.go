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
	orgID      uuid.UUID
	recipients []string
	orgName    string
}

type captureMailer struct {
	mu   sync.Mutex
	sent []sentMail
}

func (m *captureMailer) SendProviderDelisted(_ context.Context, orgID uuid.UUID, recipients []string, orgName string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.sent = append(m.sent, sentMail{orgID: orgID, recipients: recipients, orgName: orgName})
	return nil
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

func (f fixture) unit(t *testing.T, orgID uuid.UUID) (status, orgStatus, reason string) {
	t.Helper()
	var r *string
	if err := f.pool.QueryRow(context.Background(), `SELECT u.status, o.status, u.revoked_reason
		FROM wallet_units u JOIN organizations o ON o.id = u.organization_id WHERE u.organization_id = $1`, orgID).
		Scan(&status, &orgStatus, &r); err != nil {
		t.Fatalf("wallet unit of %s: %v", orgID, err)
	}
	if r != nil {
		reason = *r
	}
	return status, orgStatus, reason
}

// Taking us off the stub list revokes every org's wallet and wallet unit, and
// mails each org's admins once.
func TestOffTheListRevokesEveryWallet(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	acme, globex := f.seedOrg(t, "acme"), f.seedOrg(t, "globex")
	w := f.watcher(f.list, 0)

	if n, err := w.Check(ctx); err != nil || n != 0 {
		t.Fatalf("Check while listed = %d, %v; want 0, nil", n, err)
	}
	if status, _, _ := f.unit(t, acme); status != walletunit.UnitActive {
		t.Fatalf("unit while listed = %q, want active", status)
	}

	if err := f.list.SetListed(ctx, false); err != nil {
		t.Fatalf("SetListed: %v", err)
	}
	if n, err := w.Check(ctx); err != nil || n != 2 {
		t.Fatalf("Check off the list = %d, %v; want 2, nil", n, err)
	}
	for _, org := range []uuid.UUID{acme, globex} {
		status, orgStatus, reason := f.unit(t, org)
		if status != walletunit.UnitRevoked || orgStatus != organization.StatusRevoked || reason != walletunit.ReasonProviderDelisted {
			t.Fatalf("org %s = unit %q, wallet %q, reason %q; want revoked for %q", org, status, orgStatus, reason, walletunit.ReasonProviderDelisted)
		}
	}
	if len(f.mail.sent) != 2 {
		t.Fatalf("mails = %d, want one per org", len(f.mail.sent))
	}
	for _, m := range f.mail.sent {
		if len(m.recipients) != 1 || !slices.Contains([]string{"admin@acme.example", "admin@globex.example"}, m.recipients[0]) {
			t.Fatalf("mail for %s went to %v, want its admin", m.orgName, m.recipients)
		}
	}

	if n, err := w.Check(ctx); err != nil || n != 0 {
		t.Fatalf("second Check = %d, %v; want 0, nil", n, err)
	}
	if len(f.mail.sent) != 2 {
		t.Fatalf("mails after a second check = %d, want no new ones", len(f.mail.sent))
	}

	// Back on the list, the revoked wallets stay revoked.
	if err := f.list.SetListed(ctx, true); err != nil {
		t.Fatalf("SetListed: %v", err)
	}
	if _, err := w.Check(ctx); err != nil {
		t.Fatalf("Check back on the list: %v", err)
	}
	if status, _, _ := f.unit(t, acme); status != walletunit.UnitRevoked {
		t.Fatalf("unit back on the list = %q, want still revoked", status)
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
	if status, _, _ := f.unit(t, acme); status != walletunit.UnitActive {
		t.Fatalf("unit within the grace = %q, want active", status)
	}

	at(3 * time.Hour)
	if n, err := w.Check(ctx); err != nil || n != 1 {
		t.Fatalf("Check after the grace = %d, %v; want 1, nil", n, err)
	}
}

// A list that cannot be read revokes nothing and does not start the grace.
func TestUnreadableListChangesNothing(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	acme := f.seedOrg(t, "acme")

	if n, err := f.watcher(unreadableList{}, 0).Check(ctx); err == nil || n != 0 {
		t.Fatalf("Check on an unreadable list = %d, %v; want 0 and an error", n, err)
	}
	if status, _, _ := f.unit(t, acme); status != walletunit.UnitActive {
		t.Fatalf("unit = %q, want active", status)
	}
	var rows int
	if err := f.pool.QueryRow(ctx, `SELECT count(*) FROM provider_listing_state WHERE missing_since IS NOT NULL`).Scan(&rows); err != nil || rows != 0 {
		t.Fatalf("missing_since rows = %d, %v; want 0", rows, err)
	}
	if len(f.mail.sent) != 0 {
		t.Fatalf("mails = %d, want none", len(f.mail.sent))
	}
}
