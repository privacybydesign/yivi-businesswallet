//go:build integration

package organization_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/privacybydesign/yivi-businesswallet/backend/internal/audit"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/organization"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/testdb"
)

// claimRepresentation records the register-backed legal representative: a
// `bestuurder` row in wallet_representations claimed by userID. Bootstrap writes
// this from the KVK registration attestation; the mandate layer reads it as Axis
// A's root of authority.
func claimRepresentation(t *testing.T, pool *pgxpool.Pool, orgID, userID uuid.UUID, kind string) {
	t.Helper()
	if _, err := pool.Exec(context.Background(),
		`INSERT INTO wallet_representations
			(organization_id, kind, given_names, family_name, claimed_by_user_id, claimed_at)
		 VALUES ($1, $2, 'Test', 'Representative', $3, now())`,
		orgID, kind, userID,
	); err != nil {
		t.Fatalf("claim representation: %v", err)
	}
}

func mandateAudit(t *testing.T, pool *pgxpool.Pool, orgID uuid.UUID, action string) []map[string]any {
	t.Helper()
	rows, err := pool.Query(context.Background(),
		`SELECT metadata FROM audit_events
		 WHERE organization_id = $1 AND action = $2 ORDER BY occurred_at, id`, orgID, action)
	if err != nil {
		t.Fatalf("read audit events: %v", err)
	}
	defer rows.Close()

	var out []map[string]any
	for rows.Next() {
		var meta map[string]any
		if err := rows.Scan(&meta); err != nil {
			t.Fatalf("scan audit metadata: %v", err)
		}
		out = append(out, meta)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("audit event rows: %v", err)
	}
	return out
}

func TestResolveAuthority(t *testing.T) {
	pool, _ := testdb.Fresh(t)
	store := organization.NewStore(pool, audit.NopRecorder{})
	ctx := context.Background()

	org := makeOrg(t, pool, "Acme", "acme")
	dept, err := store.CreateDepartment(ctx, org.ID, "Finance")
	if err != nil {
		t.Fatalf("CreateDepartment: %v", err)
	}

	boss := createUser(t, pool, "boss@acme.example")
	addMembership(t, pool, boss, org.ID, nil)
	claimRepresentation(t, pool, org.ID, boss, "bestuurder")

	t.Run("a plain member holds nothing", func(t *testing.T) {
		u := createUser(t, pool, "plain@acme.example")
		addMembership(t, pool, u, org.ID, nil)

		got, err := store.ResolveAuthority(ctx, org.ID, u)
		if err != nil {
			t.Fatalf("ResolveAuthority: %v", err)
		}
		if got != (organization.Authority{}) {
			t.Errorf("authority = %+v, want the zero authority", got)
		}
		if got.Withdrawn() {
			t.Error("Withdrawn() = true for a member who was never granted anything")
		}
	})

	t.Run("a claimed bestuurder is the legal representative", func(t *testing.T) {
		got, err := store.ResolveAuthority(ctx, org.ID, boss)
		if err != nil {
			t.Fatalf("ResolveAuthority: %v", err)
		}
		if !got.LegalRepresentative {
			t.Error("LegalRepresentative = false, want true")
		}
		if !got.MayGrantMandate() {
			t.Error("MayGrantMandate() = false for the legal representative")
		}
	})

	t.Run("a claimed gevolmachtigde is not", func(t *testing.T) {
		u := createUser(t, pool, "proxy@acme.example")
		addMembership(t, pool, u, org.ID, nil)
		claimRepresentation(t, pool, org.ID, u, "gevolmachtigde")

		got, err := store.ResolveAuthority(ctx, org.ID, u)
		if err != nil {
			t.Fatalf("ResolveAuthority: %v", err)
		}
		if got.LegalRepresentative {
			t.Error("LegalRepresentative = true for a gevolmachtigde")
		}
	})

	t.Run("an expired mandate is not active, and its holder is withdrawn", func(t *testing.T) {
		u := createUser(t, pool, "expired@acme.example")
		addMembership(t, pool, u, org.ID, nil)
		if _, err := store.GrantMandate(ctx, org.ID, boss, organization.MandateGrant{
			Type:          organization.MandateAdministrative,
			GranteeUserID: u,
			Scope:         organization.MandateScopeOrganization,
			ValidFrom:     time.Now().Add(-2 * time.Hour),
			ValidUntil:    ptrTime(time.Now().Add(-time.Hour)),
		}); err != nil {
			t.Fatalf("GrantMandate: %v", err)
		}

		got, err := store.ResolveAuthority(ctx, org.ID, u)
		if err != nil {
			t.Fatalf("ResolveAuthority: %v", err)
		}
		if got.Granted != 1 || got.Mandated {
			t.Errorf("authority = %+v, want granted 1 and not mandated", got)
		}
		if !got.Withdrawn() {
			t.Error("Withdrawn() = false for a holder whose only mandate expired")
		}
	})

	t.Run("a mandate whose window has not opened is not active either", func(t *testing.T) {
		u := createUser(t, pool, "pending@acme.example")
		addMembership(t, pool, u, org.ID, nil)
		if _, err := store.GrantMandate(ctx, org.ID, boss, organization.MandateGrant{
			Type:          organization.MandateAdministrative,
			GranteeUserID: u,
			Scope:         organization.MandateScopeOrganization,
			ValidFrom:     time.Now().Add(time.Hour),
		}); err != nil {
			t.Fatalf("GrantMandate: %v", err)
		}

		got, err := store.ResolveAuthority(ctx, org.ID, u)
		if err != nil {
			t.Fatalf("ResolveAuthority: %v", err)
		}
		if got.Mandated {
			t.Errorf("authority = %+v, want not mandated", got)
		}
	})

	t.Run("a department-scoped mandate carries no org-wide authority", func(t *testing.T) {
		u := createUser(t, pool, "dept@acme.example")
		addMembership(t, pool, u, org.ID, nil)
		if _, err := store.GrantMandate(ctx, org.ID, boss, organization.MandateGrant{
			Type:              organization.MandateAdministrative,
			GranteeUserID:     u,
			Scope:             organization.MandateScopeDepartment,
			ScopeDepartmentID: &dept.ID,
		}); err != nil {
			t.Fatalf("GrantMandate: %v", err)
		}

		got, err := store.ResolveAuthority(ctx, org.ID, u)
		if err != nil {
			t.Fatalf("ResolveAuthority: %v", err)
		}
		if got.Granted != 1 {
			t.Errorf("Granted = %d, want 1", got.Granted)
		}
		if got.Mandated {
			t.Error("Mandated = true for a department-scoped mandate")
		}
		if !got.Withdrawn() {
			t.Error("Withdrawn() = false: a department-scoped mandate must not carry org-wide admin")
		}
	})

	t.Run("an active full mandate makes its holder a mandate authority", func(t *testing.T) {
		u := createUser(t, pool, "deputy@acme.example")
		addMembership(t, pool, u, org.ID, nil)
		if _, err := store.GrantMandate(ctx, org.ID, boss, organization.MandateGrant{
			Type:          organization.MandateFull,
			GranteeUserID: u,
			Scope:         organization.MandateScopeOrganization,
		}); err != nil {
			t.Fatalf("GrantMandate: %v", err)
		}

		got, err := store.ResolveAuthority(ctx, org.ID, u)
		if err != nil {
			t.Fatalf("ResolveAuthority: %v", err)
		}
		if !got.FullMandate || !got.Mandated || got.Withdrawn() {
			t.Errorf("authority = %+v, want an active full mandate", got)
		}
		if !got.MayGrantMandate() {
			t.Error("MayGrantMandate() = false for a full-mandate holder")
		}
	})
}

func TestGrantMandate(t *testing.T) {
	pool, _ := testdb.Fresh(t)
	store := organization.NewStore(pool, audit.NewDBRecorder())
	ctx := context.Background()

	org := makeOrg(t, pool, "Acme", "acme")
	boss := createUser(t, pool, "boss@acme.example")
	addMembership(t, pool, boss, org.ID, nil)
	claimRepresentation(t, pool, org.ID, boss, "bestuurder")

	t.Run("the legal representative grants a full mandate and it is audited", func(t *testing.T) {
		grantee := createUser(t, pool, "deputy@acme.example")
		addMembership(t, pool, grantee, org.ID, nil)

		m, err := store.GrantMandate(ctx, org.ID, boss, organization.MandateGrant{
			Type:          organization.MandateFull,
			GranteeUserID: grantee,
			Scope:         organization.MandateScopeOrganization,
		})
		if err != nil {
			t.Fatalf("GrantMandate: %v", err)
		}
		if m.Status != organization.MandateStatusActive {
			t.Errorf("status = %q, want active", m.Status)
		}
		if m.GranteeName != "Test User" {
			t.Errorf("granteeName = %q, want the resolved name", m.GranteeName)
		}
		if m.ParentMandateID != nil {
			t.Errorf("parentMandateId = %v, want nil for a root grant", m.ParentMandateID)
		}

		events := mandateAudit(t, pool, org.ID, audit.MandateGranted)
		if len(events) != 1 {
			t.Fatalf("audit events = %d, want 1", len(events))
		}
		after, _ := events[0]["after"].(map[string]any)
		if after["mandateType"] != organization.MandateFull {
			t.Errorf("audited mandateType = %v, want full", after["mandateType"])
		}
		if after["basis"] != "legal_representative" {
			t.Errorf("audited basis = %v, want legal_representative", after["basis"])
		}
		if after["grantee"] != "Test User" {
			t.Errorf("audited grantee = %v, want a readable name", after["grantee"])
		}
	})

	t.Run("a non-member cannot be granted a mandate", func(t *testing.T) {
		outsider := createUser(t, pool, "outsider@example.com")
		_, err := store.GrantMandate(ctx, org.ID, boss, organization.MandateGrant{
			Type:          organization.MandateAdministrative,
			GranteeUserID: outsider,
			Scope:         organization.MandateScopeOrganization,
		})
		if !errors.Is(err, organization.ErrGranteeNotMember) {
			t.Errorf("err = %v, want ErrGranteeNotMember", err)
		}
	})

	t.Run("an admin who holds nothing cannot mint a mandate", func(t *testing.T) {
		// The route is gated on Axis A too, but the store is the decision of
		// record: an admin who reaches it anyway still gets nothing.
		plainAdmin := createUser(t, pool, "admin@acme.example")
		if _, err := pool.Exec(ctx,
			"INSERT INTO memberships (user_id, organization_id, role) VALUES ($1, $2, 'admin')",
			plainAdmin, org.ID); err != nil {
			t.Fatalf("add admin: %v", err)
		}

		_, err := store.GrantMandate(ctx, org.ID, plainAdmin, organization.MandateGrant{
			Type:          organization.MandateAdministrative,
			GranteeUserID: plainAdmin,
			Scope:         organization.MandateScopeOrganization,
		})
		if !errors.Is(err, organization.ErrNotMandateAuthority) {
			t.Errorf("err = %v, want ErrNotMandateAuthority", err)
		}
	})

	t.Run("a full-mandate holder delegates from their own mandate", func(t *testing.T) {
		holder := createUser(t, pool, "holder@acme.example")
		addMembership(t, pool, holder, org.ID, nil)
		parentEnd := time.Now().Add(24 * time.Hour)
		parent, err := store.GrantMandate(ctx, org.ID, boss, organization.MandateGrant{
			Type:          organization.MandateFull,
			GranteeUserID: holder,
			Scope:         organization.MandateScopeOrganization,
			ValidUntil:    &parentEnd,
		})
		if err != nil {
			t.Fatalf("GrantMandate parent: %v", err)
		}

		sub := createUser(t, pool, "sub@acme.example")
		addMembership(t, pool, sub, org.ID, nil)
		// Asks to outlive the parent by a week and names no parent at all.
		child, err := store.GrantMandate(ctx, org.ID, holder, organization.MandateGrant{
			Type:          organization.MandateAdministrative,
			GranteeUserID: sub,
			Scope:         organization.MandateScopeOrganization,
			ValidUntil:    ptrTime(parentEnd.Add(7 * 24 * time.Hour)),
		})
		if err != nil {
			t.Fatalf("GrantMandate child: %v", err)
		}
		if child.ParentMandateID == nil || *child.ParentMandateID != parent.ID {
			t.Errorf("parentMandateId = %v, want the holder's own mandate %s", child.ParentMandateID, parent.ID)
		}
		if child.ValidUntil == nil || child.ValidUntil.After(parentEnd) {
			t.Errorf("validUntil = %v, want clamped to the parent's %v", child.ValidUntil, parentEnd)
		}
	})

	t.Run("an administrative holder cannot delegate a full mandate", func(t *testing.T) {
		holder := createUser(t, pool, "admin-holder@acme.example")
		addMembership(t, pool, holder, org.ID, nil)
		parent, err := store.GrantMandate(ctx, org.ID, boss, organization.MandateGrant{
			Type:          organization.MandateAdministrative,
			GranteeUserID: holder,
			Scope:         organization.MandateScopeOrganization,
		})
		if err != nil {
			t.Fatalf("GrantMandate parent: %v", err)
		}

		sub := createUser(t, pool, "admin-sub@acme.example")
		addMembership(t, pool, sub, org.ID, nil)
		_, err = store.GrantMandate(ctx, org.ID, holder, organization.MandateGrant{
			Type:            organization.MandateFull,
			GranteeUserID:   sub,
			Scope:           organization.MandateScopeOrganization,
			ParentMandateID: &parent.ID,
		})
		if !errors.Is(err, organization.ErrOverDelegation) {
			t.Errorf("err = %v, want ErrOverDelegation", err)
		}
	})

	t.Run("a holder cannot cut from someone else's mandate", func(t *testing.T) {
		owner := createUser(t, pool, "owner-of-parent@acme.example")
		addMembership(t, pool, owner, org.ID, nil)
		parent, err := store.GrantMandate(ctx, org.ID, boss, organization.MandateGrant{
			Type:          organization.MandateFull,
			GranteeUserID: owner,
			Scope:         organization.MandateScopeOrganization,
		})
		if err != nil {
			t.Fatalf("GrantMandate parent: %v", err)
		}

		stranger := createUser(t, pool, "stranger@acme.example")
		addMembership(t, pool, stranger, org.ID, nil)
		_, err = store.GrantMandate(ctx, org.ID, stranger, organization.MandateGrant{
			Type:            organization.MandateAdministrative,
			GranteeUserID:   stranger,
			Scope:           organization.MandateScopeOrganization,
			ParentMandateID: &parent.ID,
		})
		if !errors.Is(err, organization.ErrNotMandateAuthority) {
			t.Errorf("err = %v, want ErrNotMandateAuthority", err)
		}
	})

	t.Run("a department from another organization is rejected", func(t *testing.T) {
		other := makeOrg(t, pool, "Other", "other")
		otherDept, err := store.CreateDepartment(ctx, other.ID, "Elsewhere")
		if err != nil {
			t.Fatalf("CreateDepartment: %v", err)
		}

		grantee := createUser(t, pool, "cross-org@acme.example")
		addMembership(t, pool, grantee, org.ID, nil)
		_, err = store.GrantMandate(ctx, org.ID, boss, organization.MandateGrant{
			Type:              organization.MandateAdministrative,
			GranteeUserID:     grantee,
			Scope:             organization.MandateScopeDepartment,
			ScopeDepartmentID: &otherDept.ID,
		})
		if !errors.Is(err, organization.ErrDepartmentNotFound) {
			t.Errorf("err = %v, want ErrDepartmentNotFound", err)
		}
	})
}

func TestRevokeMandate(t *testing.T) {
	pool, _ := testdb.Fresh(t)
	store := organization.NewStore(pool, audit.NewDBRecorder())
	ctx := context.Background()

	org := makeOrg(t, pool, "Acme", "acme")
	boss := createUser(t, pool, "boss@acme.example")
	addMembership(t, pool, boss, org.ID, nil)
	claimRepresentation(t, pool, org.ID, boss, "bestuurder")

	// grantChain builds boss -> holder (full) -> sub (administrative).
	grantChain := func(t *testing.T, holderEmail, subEmail string) (holder, sub uuid.UUID, parent, child organization.Mandate) {
		t.Helper()
		holder = createUser(t, pool, holderEmail)
		addMembership(t, pool, holder, org.ID, nil)
		var err error
		parent, err = store.GrantMandate(ctx, org.ID, boss, organization.MandateGrant{
			Type:          organization.MandateFull,
			GranteeUserID: holder,
			Scope:         organization.MandateScopeOrganization,
		})
		if err != nil {
			t.Fatalf("GrantMandate parent: %v", err)
		}
		sub = createUser(t, pool, subEmail)
		addMembership(t, pool, sub, org.ID, nil)
		child, err = store.GrantMandate(ctx, org.ID, holder, organization.MandateGrant{
			Type:          organization.MandateAdministrative,
			GranteeUserID: sub,
			Scope:         organization.MandateScopeOrganization,
		})
		if err != nil {
			t.Fatalf("GrantMandate child: %v", err)
		}
		return holder, sub, parent, child
	}

	t.Run("revoking a mandate cascades down its delegation chain", func(t *testing.T) {
		_, sub, parent, _ := grantChain(t, "h1@acme.example", "s1@acme.example")

		touched, err := store.RevokeMandate(ctx, org.ID, parent.ID, boss, nil, "left the company")
		if err != nil {
			t.Fatalf("RevokeMandate: %v", err)
		}
		if len(touched) != 2 {
			t.Fatalf("touched = %d mandates, want 2 (the target and its delegation)", len(touched))
		}
		if touched[0].ID != parent.ID {
			t.Errorf("touched[0] = %s, want the target %s", touched[0].ID, parent.ID)
		}
		for _, m := range touched {
			if m.Status != organization.MandateStatusRevoked {
				t.Errorf("mandate %s status = %q, want revoked", m.ID, m.Status)
			}
		}

		// The delegate loses its authority in the same breath, on the next request.
		got, err := store.ResolveAuthority(ctx, org.ID, sub)
		if err != nil {
			t.Fatalf("ResolveAuthority: %v", err)
		}
		if got.Mandated || !got.Withdrawn() {
			t.Errorf("delegate authority = %+v, want withdrawn", got)
		}

		events := mandateAudit(t, pool, org.ID, audit.MandateRevoked)
		if len(events) != 2 {
			t.Fatalf("audit events = %d, want one per revoked mandate", len(events))
		}
		var cascaded int
		for _, e := range events {
			before, _ := e["before"].(map[string]any)
			if before["reason"] != "left the company" {
				t.Errorf("audited reason = %v, want the given reason", before["reason"])
			}
			if before["revokedBy"] != "legal_representative" {
				t.Errorf("audited revokedBy = %v, want legal_representative", before["revokedBy"])
			}
			if before["cascadedFrom"] == parent.ID.String() {
				cascaded++
			}
		}
		if cascaded != 1 {
			t.Errorf("events naming a cascade = %d, want 1 (the delegation, not the target)", cascaded)
		}
	})

	t.Run("an effective-dated revocation leaves the mandate active until then", func(t *testing.T) {
		holder, _, parent, _ := grantChain(t, "h2@acme.example", "s2@acme.example")
		effective := time.Now().Add(48 * time.Hour)

		touched, err := store.RevokeMandate(ctx, org.ID, parent.ID, boss, &effective, "")
		if err != nil {
			t.Fatalf("RevokeMandate: %v", err)
		}
		for _, m := range touched {
			if m.Status != organization.MandateStatusActive {
				t.Errorf("mandate %s status = %q, want still active", m.ID, m.Status)
			}
			if m.ValidUntil == nil || m.ValidUntil.Sub(effective).Abs() > time.Second {
				t.Errorf("mandate %s validUntil = %v, want the effective date %v", m.ID, m.ValidUntil, effective)
			}
		}

		got, err := store.ResolveAuthority(ctx, org.ID, holder)
		if err != nil {
			t.Fatalf("ResolveAuthority: %v", err)
		}
		if !got.Mandated {
			t.Error("Mandated = false, want the mandate to run until the effective date")
		}
	})

	t.Run("an effective date in the past is rejected", func(t *testing.T) {
		_, _, parent, _ := grantChain(t, "h3@acme.example", "s3@acme.example")
		past := time.Now().Add(-time.Hour)
		if _, err := store.RevokeMandate(ctx, org.ID, parent.ID, boss, &past, ""); !errors.Is(err, organization.ErrMandateEffectiveInPast) {
			t.Errorf("err = %v, want ErrMandateEffectiveInPast", err)
		}
	})

	t.Run("an effective date after a delegation starts revokes that delegation outright", func(t *testing.T) {
		holder, _, parent, _ := grantChain(t, "h4@acme.example", "s4@acme.example")
		later := createUser(t, pool, "later@acme.example")
		addMembership(t, pool, later, org.ID, nil)
		pending, err := store.GrantMandate(ctx, org.ID, holder, organization.MandateGrant{
			Type:          organization.MandateAdministrative,
			GranteeUserID: later,
			Scope:         organization.MandateScopeOrganization,
			ValidFrom:     time.Now().Add(72 * time.Hour),
		})
		if err != nil {
			t.Fatalf("GrantMandate pending: %v", err)
		}

		effective := time.Now().Add(24 * time.Hour)
		touched, err := store.RevokeMandate(ctx, org.ID, parent.ID, boss, &effective, "")
		if err != nil {
			t.Fatalf("RevokeMandate: %v", err)
		}
		for _, m := range touched {
			if m.ID != pending.ID {
				continue
			}
			// Its window would never have opened inside the parent's remaining life,
			// so there is nothing to trim — it goes.
			if m.Status != organization.MandateStatusRevoked {
				t.Errorf("pending delegation status = %q, want revoked", m.Status)
			}
		}
	})

	t.Run("only the legal representative or the grantor may revoke", func(t *testing.T) {
		holder, sub, parent, child := grantChain(t, "h5@acme.example", "s5@acme.example")

		if _, err := store.RevokeMandate(ctx, org.ID, parent.ID, sub, nil, ""); !errors.Is(err, organization.ErrNotMandateAuthority) {
			t.Errorf("delegate revoking its own grantor: err = %v, want ErrNotMandateAuthority", err)
		}
		// The holder granted the child, so they may take it back.
		if _, err := store.RevokeMandate(ctx, org.ID, child.ID, holder, nil, ""); err != nil {
			t.Errorf("grantor revoking their own grant: %v", err)
		}
	})

	t.Run("revoking twice is a conflict", func(t *testing.T) {
		_, _, parent, _ := grantChain(t, "h6@acme.example", "s6@acme.example")
		if _, err := store.RevokeMandate(ctx, org.ID, parent.ID, boss, nil, ""); err != nil {
			t.Fatalf("RevokeMandate: %v", err)
		}
		if _, err := store.RevokeMandate(ctx, org.ID, parent.ID, boss, nil, ""); !errors.Is(err, organization.ErrMandateInactive) {
			t.Errorf("err = %v, want ErrMandateInactive", err)
		}
	})

	t.Run("a mandate of another organization is not found", func(t *testing.T) {
		_, _, parent, _ := grantChain(t, "h7@acme.example", "s7@acme.example")
		other := makeOrg(t, pool, "Other", "other")
		if _, err := store.RevokeMandate(ctx, other.ID, parent.ID, boss, nil, ""); !errors.Is(err, organization.ErrMandateNotFound) {
			t.Errorf("err = %v, want ErrMandateNotFound", err)
		}
	})
}

func TestListMandates(t *testing.T) {
	pool, _ := testdb.Fresh(t)
	store := organization.NewStore(pool, audit.NopRecorder{})
	ctx := context.Background()

	org := makeOrg(t, pool, "Acme", "acme")
	boss := createUser(t, pool, "boss@acme.example")
	addMembership(t, pool, boss, org.ID, nil)
	claimRepresentation(t, pool, org.ID, boss, "bestuurder")

	grantee := createUser(t, pool, "deputy@acme.example")
	addMembership(t, pool, grantee, org.ID, nil)
	active, err := store.GrantMandate(ctx, org.ID, boss, organization.MandateGrant{
		Type:          organization.MandateFull,
		GranteeUserID: grantee,
		Scope:         organization.MandateScopeOrganization,
	})
	if err != nil {
		t.Fatalf("GrantMandate: %v", err)
	}
	if _, err := store.RevokeMandate(ctx, org.ID, active.ID, boss, nil, "changed my mind"); err != nil {
		t.Fatalf("RevokeMandate: %v", err)
	}

	// A revoked mandate that vanished from the register would hide the revocation,
	// so the list is the whole history.
	mandates, err := store.ListMandates(ctx, org.ID)
	if err != nil {
		t.Fatalf("ListMandates: %v", err)
	}
	if len(mandates) != 1 {
		t.Fatalf("mandates = %d, want the revoked one to still be listed", len(mandates))
	}
	m := mandates[0]
	if m.Status != organization.MandateStatusRevoked {
		t.Errorf("status = %q, want revoked", m.Status)
	}
	if m.RevocationReason == nil || *m.RevocationReason != "changed my mind" {
		t.Errorf("revocationReason = %v, want the recorded reason", m.RevocationReason)
	}
	if m.GrantorName == nil || *m.GrantorName != "Test User" {
		t.Errorf("grantorName = %v, want a readable name", m.GrantorName)
	}
	if m.RevokedByUserID == nil || *m.RevokedByUserID != boss {
		t.Errorf("revokedByUserId = %v, want %s", m.RevokedByUserID, boss)
	}
}

func ptrTime(t time.Time) *time.Time { return &t }
