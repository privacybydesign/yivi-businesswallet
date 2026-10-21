//go:build integration

package proofing

import (
	"context"
	"errors"
	"maps"
	"slices"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/privacybydesign/yivi-businesswallet/backend/internal/audit"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/proofingengine"
	proofingflow "github.com/privacybydesign/yivi-businesswallet/backend/internal/proofingengine/flow"
	proofingsession "github.com/privacybydesign/yivi-businesswallet/backend/internal/proofingengine/session"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/proofingprovider"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/testdb"
)

// checkViolation is the SQLSTATE of a failed CHECK constraint.
const checkViolation = "23514"

// sameIDs reports whether a and b hold the same ids, in any order.
func sameIDs(a, b []uuid.UUID) bool {
	less := func(x, y uuid.UUID) int { return slices.Compare(x[:], y[:]) }
	return slices.Equal(slices.SortedFunc(slices.Values(a), less), slices.SortedFunc(slices.Values(b), less))
}

// A legal retention is the least an external identity check is kept: the purge
// time follows the later of it and the flow's or org's retention, moves when it
// is saved or removed, and leaves a data request, a member's request and
// another org alone.
func TestLegalRetentionFloorsPurge(t *testing.T) {
	f := newPurgeFixture(t)
	ctx := context.Background()
	other := makeOrg(t, f.pool, "globex")
	week := int((7 * 24 * time.Hour).Seconds())
	external := func(email string) NewStoredRequest {
		return newStoredRequest(f.orgID, f.sam, externalSubject(email))
	}
	identity := f.settledAgo(t, external("a@example.org"), "s-identity", "40 days")
	short := external("b@example.org")
	short.Flow.RetentionOverrideSeconds = week
	shortFlow := f.settledAgo(t, short, "s-short", "40 days")
	erasure := external("c@example.org")
	erasure.FlowKind = FlowDataErasure
	dataRequest := f.settledAgo(t, erasure, "s-erasure", "40 days")
	elsewhere := f.settledAgo(t, newStoredRequest(other, f.sam, externalSubject("d@example.org")), "s-other", "40 days")

	all := []uuid.UUID{identity.ID, shortFlow.ID, dataRequest.ID, elsewhere.ID}
	if due := f.purgeDue(t); !sameIDs(due, all) {
		t.Fatalf("due before any legal retention = %v; want all four", due)
	}

	legal := LegalRetention{Months: 12, ReasonEN: "Kept by law.", ReasonNL: "Bewaard volgens de wet."}
	if _, err := f.settings.SaveLegalRetention(ctx, f.orgID, legal); err != nil {
		t.Fatalf("SaveLegalRetention: %v", err)
	}
	// The identity checks of the org wait out the year, whatever their flow's
	// week; its data request and the other org's check do not.
	if due := f.purgeDue(t); !sameIDs(due, []uuid.UUID{dataRequest.ID, elsewhere.ID}) {
		t.Errorf("due under a year's legal retention = %v; want the data request and the other org's check", due)
	}
	var floored bool
	if err := f.pool.QueryRow(ctx, `SELECT purge_at = completed_at + interval '12 months'
		FROM identity_proofing_requests WHERE id = $1`, shortFlow.ID).Scan(&floored); err != nil || !floored {
		t.Errorf("purge_at of the check on a week's flow = a year after it ended: %v, %v", floored, err)
	}
	// The name read off the document is kept as long, at most a month's 31 days over.
	got, err := f.requests.Get(ctx, f.orgID, shortFlow.ID)
	if err != nil || got.NameRetention != legal.longest() {
		t.Errorf("name retention = %v, %v; want the legal period's %v", got.NameRetention, err, legal.longest())
	}

	// Past the year they go.
	if _, err := f.pool.Exec(ctx, `UPDATE identity_proofing_requests SET completed_at = now() - interval '13 months'
		WHERE id = ANY($1)`, []uuid.UUID{identity.ID, shortFlow.ID}); err != nil {
		t.Fatalf("age the checks: %v", err)
	}
	if err := refreshPurgeAt(ctx, f.pool, identity.ID, shortFlow.ID); err != nil {
		t.Fatalf("refresh purge time: %v", err)
	}
	if due := f.purgeDue(t); !sameIDs(due, all) {
		t.Errorf("due 13 months on = %v; want all four", due)
	}

	// Removing it moves the purge time back under the org's retention.
	if _, err := f.pool.Exec(ctx, `UPDATE identity_proofing_requests SET completed_at = now() - interval '40 days'
		WHERE id = ANY($1)`, []uuid.UUID{identity.ID, shortFlow.ID}); err != nil {
		t.Fatalf("age the checks back: %v", err)
	}
	if err := refreshPurgeAt(ctx, f.pool, identity.ID, shortFlow.ID); err != nil {
		t.Fatalf("refresh purge time: %v", err)
	}
	if _, err := f.settings.SaveLegalRetention(ctx, f.orgID, legal); err != nil {
		t.Fatalf("SaveLegalRetention again: %v", err)
	}
	if due := f.purgeDue(t); !sameIDs(due, []uuid.UUID{dataRequest.ID, elsewhere.ID}) {
		t.Fatalf("due under the legal retention again = %v", due)
	}
	if _, err := f.settings.SaveLegalRetention(ctx, f.orgID, LegalRetention{}); err != nil {
		t.Fatalf("remove the legal retention: %v", err)
	}
	if due := f.purgeDue(t); !sameIDs(due, all) {
		t.Errorf("due after removing it = %v; want all four", due)
	}
}

// A flow that keeps its checks longer than the law says keeps them: the
// legal retention is a floor, not the retention.
func TestLegalYieldsToLongerFlow(t *testing.T) {
	f := newPurgeFixture(t)
	ctx := context.Background()
	if _, err := f.settings.SaveLegalRetention(ctx, f.orgID, LegalRetention{Months: 1, ReasonEN: "a", ReasonNL: "b"}); err != nil {
		t.Fatalf("SaveLegalRetention: %v", err)
	}
	in := newStoredRequest(f.orgID, f.sam, externalSubject("a@example.org"))
	in.Flow.RetentionOverrideSeconds = int((90 * 24 * time.Hour).Seconds())
	req := f.settledAgo(t, in, "s1", "60 days")
	if due := f.purgeDue(t); len(due) != 0 {
		t.Errorf("due after 60 days on a 90 days' flow under a month's law = %v; want none", due)
	}
	if _, err := f.pool.Exec(ctx, `UPDATE identity_proofing_requests SET completed_at = now() - interval '91 days'
		WHERE id = $1`, req.ID); err != nil {
		t.Fatalf("age the check: %v", err)
	}
	if err := refreshPurgeAt(ctx, f.pool, req.ID); err != nil {
		t.Fatalf("refresh purge time: %v", err)
	}
	if due := f.purgeDue(t); !slices.Equal(due, []uuid.UUID{req.ID}) {
		t.Errorf("due after 91 days = %v; want the check", due)
	}
}

// A member's request keeps the wallet's own retention, which a legal retention
// does not extend.
func TestLegalLeavesMembers(t *testing.T) {
	f := newPurgeFixture(t)
	ctx := context.Background()
	if _, err := f.settings.SaveLegalRetention(ctx, f.orgID, LegalRetention{Months: 12, ReasonEN: "a", ReasonNL: "b"}); err != nil {
		t.Fatalf("SaveLegalRetention: %v", err)
	}
	member := f.settledAgo(t, newStoredRequest(f.orgID, f.sam,
		memberSubject(Member{UserID: f.sam, Name: "Sam", Email: "sam@example.org"})), "s-member", "2 years")
	var purgeAt, completedAt *time.Time
	if err := f.pool.QueryRow(ctx, `SELECT purge_at, completed_at FROM identity_proofing_requests WHERE id = $1`, member.ID).
		Scan(&purgeAt, &completedAt); err != nil || purgeAt == nil || completedAt == nil || purgeAt.Sub(*completedAt) != proofedNameRetention {
		t.Errorf("purge_at of a member's request = %v after %v, %v; want the wallet's own retention", purgeAt, completedAt, err)
	}
}

func TestSaveLegalRetention(t *testing.T) {
	pool, _ := testdb.Fresh(t)
	store := NewOrgSettingsStore(pool, audit.NewDBRecorder())
	orgID := makeOrg(t, pool, "acme")
	ctx := context.Background()
	legal := LegalRetention{Months: 60, ReasonEN: "The Wwft.", ReasonNL: "De Wwft."}

	if _, err := store.SaveLegalRetention(ctx, uuid.New(), legal); err == nil {
		t.Error("SaveLegalRetention of an unknown org succeeded")
	}
	saved, err := store.SaveLegalRetention(ctx, orgID, legal)
	if err != nil || saved.LegalRetention != legal {
		t.Fatalf("SaveLegalRetention = %+v, %v", saved.LegalRetention, err)
	}
	if saved.Session != defaultSettings(orgID).Session {
		t.Errorf("session settings = %+v; want the defaults kept", saved.Session)
	}
	for range 2 {
		again, err := store.SaveLegalRetention(ctx, orgID, legal)
		if err != nil || !again.UpdatedAt.Equal(saved.UpdatedAt) {
			t.Errorf("saving the same legal retention = %+v, %v; want its update time kept", again, err)
		}
	}
	// Another setting's save leaves it.
	if kept, err := store.SaveSession(ctx, orgID, sessionChange(SessionSettings{SessionTTL: SessionTTL, DataRetentionDays: 90})); err != nil || kept.LegalRetention != legal {
		t.Errorf("legal retention after the session settings = %+v, %v; want it kept", kept.LegalRetention, err)
	}
	removed, err := store.SaveLegalRetention(ctx, orgID, LegalRetention{})
	if err != nil || removed.LegalRetention != (LegalRetention{}) {
		t.Fatalf("removing it = %+v, %v", removed.LegalRetention, err)
	}

	audits := settingsAudits(t, pool, orgID)
	if len(audits) != 3 {
		t.Fatalf("settings_updated audits = %d, want 3 (set, session settings, removed; a repeat changes nothing)", len(audits))
	}
	if first := audits[0]; first.Before["legalRetentionMonths"] != nil || first.After["legalRetentionMonths"] != float64(60) ||
		first.After["legalRetentionReasonEn"] != legal.ReasonEN || first.After["legalRetentionReasonNl"] != legal.ReasonNL {
		t.Errorf("first audit = %+v; want none before and five years after, with the reasons", first)
	}
	if last := audits[2]; last.Before["legalRetentionMonths"] != float64(60) || last.After["legalRetentionMonths"] != nil {
		t.Errorf("last audit = %+v; want five years before and none after", last)
	}
}

// The database holds the months and both reasons together, in bounds.
func TestLegalRetentionColumns(t *testing.T) {
	pool, _ := testdb.Fresh(t)
	orgID := makeOrg(t, pool, "acme")
	ctx := context.Background()
	for name, query := range map[string]string{
		"months without reasons":  `INSERT INTO identity_proofing_settings (organization_id, legal_retention_months) VALUES ($1, 12)`,
		"a reason without months": `INSERT INTO identity_proofing_settings (organization_id, legal_retention_reason_en, legal_retention_reason_nl) VALUES ($1, 'a', 'b')`,
		"a blank reason":          `INSERT INTO identity_proofing_settings (organization_id, legal_retention_months, legal_retention_reason_en, legal_retention_reason_nl) VALUES ($1, 12, ' ', 'b')`,
		"no months":               `INSERT INTO identity_proofing_settings (organization_id, legal_retention_months, legal_retention_reason_en, legal_retention_reason_nl) VALUES ($1, 0, 'a', 'b')`,
		"past the longest period": `INSERT INTO identity_proofing_settings (organization_id, legal_retention_months, legal_retention_reason_en, legal_retention_reason_nl) VALUES ($1, 601, 'a', 'b')`,
	} {
		_, err := pool.Exec(ctx, query, orgID)
		var pgErr *pgconn.PgError
		if !errors.As(err, &pgErr) || pgErr.Code != checkViolation {
			t.Errorf("%s: %v; want a check violation", name, err)
		}
	}
}

// A match shows when the legal retention lets an erasure take it: that long
// after its check ended, and not for a data request, an erased session or an
// org without one.
func TestMatchesHeldUntil(t *testing.T) {
	f := newPurgeFixture(t)
	data := NewDataRequestStore(f.pool, audit.NewDBRecorder(), newTestCipher(t))
	ctx := context.Background()
	held := f.settledAgo(t, newStoredRequest(f.orgID, f.sam, externalSubject("a@example.org")), "s-held", "40 days")
	earlier := f.settledAgo(t, newStoredRequest(f.orgID, f.sam, externalSubject("a@example.org")), "s-earlier", "2 years")
	erasure := newDataRequest(t, f.requests, f.orgID, f.sam, FlowDataErasure, "s-erasure")
	if err := data.SaveMatches(ctx, erasure, []NewDataMatch{
		{RequestID: held.ID, Level: MatchStrong}, {RequestID: earlier.ID, Level: MatchProbable},
	}); err != nil {
		t.Fatalf("SaveMatches: %v", err)
	}
	heldUntil := func() map[uuid.UUID]*time.Time {
		t.Helper()
		matches, err := data.Matches(ctx, erasure)
		if err != nil {
			t.Fatalf("Matches: %v", err)
		}
		out := map[uuid.UUID]*time.Time{}
		for _, m := range matches {
			out[m.RequestID] = m.HeldUntil
		}
		return out
	}

	if got := heldUntil(); got[held.ID] != nil || got[earlier.ID] != nil {
		t.Errorf("held without a legal retention = %v; want none", got)
	}
	if _, err := f.settings.SaveLegalRetention(ctx, f.orgID, LegalRetention{Months: 60, ReasonEN: "a", ReasonNL: "b"}); err != nil {
		t.Fatalf("SaveLegalRetention: %v", err)
	}
	got := heldUntil()
	var wantHeld, wantEarlier time.Time
	if err := f.pool.QueryRow(ctx, `SELECT completed_at + interval '60 months' FROM identity_proofing_requests WHERE id = $1`, held.ID).Scan(&wantHeld); err != nil {
		t.Fatalf("read the end of the check: %v", err)
	}
	if err := f.pool.QueryRow(ctx, `SELECT completed_at + interval '60 months' FROM identity_proofing_requests WHERE id = $1`, earlier.ID).Scan(&wantEarlier); err != nil {
		t.Fatalf("read the end of the earlier check: %v", err)
	}
	if got[held.ID] == nil || !got[held.ID].Equal(wantHeld) || got[earlier.ID] == nil || !got[earlier.ID].Equal(wantEarlier) {
		t.Errorf("held until = %v, %v; want %v and %v", got[held.ID], got[earlier.ID], wantHeld, wantEarlier)
	}
	match := DataMatch{HeldUntil: got[held.ID]}
	if !match.held(time.Now()) || match.held(wantHeld.Add(time.Second)) {
		t.Errorf("held at %v = %v; want held now and free after", wantHeld, match.held(time.Now()))
	}

	// An erased session is gone, not held.
	current, err := f.requests.Get(ctx, f.orgID, held.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if err := f.requests.Purge(ctx, current, nil); err != nil {
		t.Fatalf("Purge: %v", err)
	}
	if got := heldUntil(); got[held.ID] != nil {
		t.Errorf("held after its purge = %v; want none", got[held.ID])
	}
}

// The legal period is counted in calendar months, not as the 30 days
// PostgreSQL compares a month to: a check that ended on 1 March 2027 is held to
// 1 March 2028, a day past a year's 365 days, whatever the org's retention.
func TestLegalCountsCalendarMonths(t *testing.T) {
	f := newPurgeFixture(t)
	ctx := context.Background()
	if _, err := f.settings.SaveSession(ctx, f.orgID, sessionChange(SessionSettings{SessionTTL: SessionTTL, DataRetentionDays: 365})); err != nil {
		t.Fatalf("SaveSession: %v", err)
	}
	if _, err := f.settings.SaveLegalRetention(ctx, f.orgID, LegalRetention{Months: 12, ReasonEN: "a", ReasonNL: "b"}); err != nil {
		t.Fatalf("SaveLegalRetention: %v", err)
	}
	req := f.settledAgo(t, newStoredRequest(f.orgID, f.sam, externalSubject("a@example.org")), "s1", "1 day")
	if _, err := f.pool.Exec(ctx, `UPDATE identity_proofing_requests SET completed_at = '2027-03-01T00:00:00Z' WHERE id = $1`, req.ID); err != nil {
		t.Fatalf("set the end of the check: %v", err)
	}
	if err := refreshPurgeAt(ctx, f.pool, req.ID); err != nil {
		t.Fatalf("refresh purge time: %v", err)
	}
	var purgeAt time.Time
	if err := f.pool.QueryRow(ctx, `SELECT purge_at FROM identity_proofing_requests WHERE id = $1`, req.ID).Scan(&purgeAt); err != nil {
		t.Fatalf("read purge_at: %v", err)
	}
	if want := time.Date(2028, time.March, 1, 0, 0, 0, 0, time.UTC); !purgeAt.Equal(want) {
		t.Errorf("purge_at = %v; want %v, the legal period's end and not the year's 365 days", purgeAt, want)
	}
}

// The engine sessions listed for a retention change are those of the org's
// unpurged identity checks, grouped by the retention override they were sent
// with: not a data request's, a member's, another org's, a purged check's, nor a
// request that has no session yet.
func TestIdentitySessions(t *testing.T) {
	f := newPurgeFixture(t)
	ctx := context.Background()
	week := 7 * 24 * time.Hour
	external := func(email string) NewStoredRequest {
		return newStoredRequest(f.orgID, f.sam, externalSubject(email))
	}
	short := func(email string) NewStoredRequest {
		in := external(email)
		in.Flow.RetentionOverrideSeconds = int(week / time.Second)
		return in
	}
	f.settledAgo(t, external("a@example.org"), "s-plain", "1 day")
	f.settledAgo(t, short("b@example.org"), "s-week-1", "1 day")
	f.settledAgo(t, short("c@example.org"), "s-week-2", "1 day")
	erasure := external("d@example.org")
	erasure.FlowKind = FlowDataErasure
	f.settledAgo(t, erasure, "s-erasure", "1 day")
	f.settledAgo(t, newStoredRequest(f.orgID, f.sam,
		memberSubject(Member{UserID: f.sam, Name: "Sam", Email: "sam@example.org"})), "s-member", "1 day")
	f.settledAgo(t, newStoredRequest(makeOrg(t, f.pool, "globex"), f.sam, externalSubject("e@example.org")), "s-other", "1 day")
	gone := f.settledAgo(t, external("f@example.org"), "s-purged", "1 day")
	purged, err := f.requests.Get(ctx, f.orgID, gone.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if err := f.requests.Purge(ctx, purged, nil); err != nil {
		t.Fatalf("Purge: %v", err)
	}
	if _, err := f.requests.Create(ctx, external("g@example.org")); err != nil {
		t.Fatalf("Create without a session: %v", err)
	}

	got, err := f.requests.IdentitySessions(ctx, f.orgID)
	if err != nil {
		t.Fatalf("IdentitySessions: %v", err)
	}
	for _, ids := range got {
		slices.Sort(ids)
	}
	want := map[time.Duration][]string{0: {"s-plain"}, week: {"s-week-1", "s-week-2"}}
	if !maps.EqualFunc(got, want, slices.Equal) {
		t.Errorf("IdentitySessions = %v; want %v", got, want)
	}
}

// engineOrgNames names every org for the engine's pages.
type engineOrgNames struct{}

func (engineOrgNames) DisplayName(context.Context, string) (string, error) { return "Acme", nil }

// The engine deletes a finished session at the retention it was created with,
// so a check sent before the legal retention was set keeps its session, and the
// evidence in it, only if saving the legal retention extends it. A data request
// is not an identity check and keeps what it had, and removing the legal
// retention takes the extension back.
func TestLegalKeepsEngineSessions(t *testing.T) {
	f := newPurgeFixture(t)
	ctx := context.Background()
	sessions := proofingsession.NewPostgresStore(f.pool, newTestCipher(t))
	engineConfig := proofingengine.DefaultConfig()
	engineConfig.PublicBaseURL = "https://wallet.example.test"
	engine := proofingengine.New(engineConfig, sessions, proofingflow.NewPostgresStore(f.pool), engineOrgNames{}, nil)
	svc := NewService(Stores{Requests: f.requests, OrgSettings: f.settings}, engine, nil, nil)
	tenant := orgTenant(f.orgID)
	settings, err := f.settings.Get(ctx, f.orgID)
	if err != nil {
		t.Fatalf("Get settings: %v", err)
	}

	flow, err := engine.CreateFlow(ctx, tenant, proofingprovider.FlowSpec{
		Name: "Passport", Steps: []string{"document_capture", "nfc_read"}, RequiredChecks: []string{"nfc.passive_auth"},
	})
	if err != nil {
		t.Fatalf("CreateFlow: %v", err)
	}

	// Two finished checks of the org, sent under its 30 days of retention.
	finished := func(kind FlowKind) string {
		t.Helper()
		in := newStoredRequest(f.orgID, f.sam, externalSubject(string(kind)+"@example.org"))
		in.FlowKind = kind
		req, err := f.requests.Create(ctx, in)
		if err != nil {
			t.Fatalf("Create %s: %v", kind, err)
		}
		sess, err := engine.CreateSession(ctx, tenant, proofingprovider.SessionInput{
			FlowID: flow.ID, ClientReference: req.ID.String(), Retention: engineRetention(&settings, kind, 0),
		})
		if err != nil {
			t.Fatalf("CreateSession %s: %v", kind, err)
		}
		if ok, err := f.requests.AttachSession(ctx, req, sess); err != nil || !ok {
			t.Fatalf("AttachSession %s = %v, %v", kind, ok, err)
		}
		if err := engine.CancelSession(ctx, tenant, sess.ID, sess.Token); err != nil {
			t.Fatalf("CancelSession %s: %v", kind, err)
		}
		return sess.ID
	}
	identity, dataRequest := finished(FlowIdentity), finished(FlowDataAccess)

	// purged is the sessions the engine's pruner removes that long from now.
	purged := func(after time.Duration) []string {
		t.Helper()
		removed, err := sessions.Purge(0, time.Now().Add(after))
		if err != nil {
			t.Fatalf("engine purge: %v", err)
		}
		var ids []string
		for _, sess := range removed {
			ids = append(ids, sess.ID)
		}
		return ids
	}
	const year = 365 * 24 * time.Hour

	legal := LegalRetention{Months: 60, ReasonEN: "Kept by law.", ReasonNL: "Bewaard volgens de wet."}
	if _, err := svc.UpdateSettings(ctx, f.orgID, SettingsChange{LegalRetention: &legal}); err != nil {
		t.Fatalf("save legal retention: %v", err)
	}
	if got := purged(2 * year); !slices.Equal(got, []string{dataRequest}) {
		t.Fatalf("engine purge after two years removed %v; want the data request's session alone, not the check %s the law holds", got, identity)
	}
	if _, err := svc.UpdateSettings(ctx, f.orgID, SettingsChange{LegalRetention: &LegalRetention{}}); err != nil {
		t.Fatalf("remove legal retention: %v", err)
	}
	if got := purged(2 * year); !slices.Equal(got, []string{identity}) {
		t.Errorf("engine purge after two years, no legal retention, removed %v; want the check's session", got)
	}
}
