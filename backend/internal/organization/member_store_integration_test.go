//go:build integration

package organization_test

import (
	"bytes"
	"context"
	"errors"
	"slices"
	"testing"

	"github.com/google/uuid"

	"github.com/privacybydesign/yivi-businesswallet/backend/internal/audit"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/organization"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/testdb"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/user"
)

func strptr(s string) *string { return &s }

// memberFixture seeds one org with two active members and one pending
// invitation, across two departments, so the list query has both branches,
// both departments, and distinct job titles to filter and sort on.
func memberFixture(t *testing.T) (*organization.Store, uuid.UUID) {
	t.Helper()
	pool, _ := testdb.Fresh(t)
	store := organization.NewStore(pool, audit.NopRecorder{})
	ctx := context.Background()

	org := makeOrg(t, pool, "Acme", "acme")

	eng, err := store.CreateDepartment(ctx, org.ID, "Engineering")
	if err != nil {
		t.Fatalf("CreateDepartment Engineering: %v", err)
	}
	sales, err := store.CreateDepartment(ctx, org.ID, "Sales")
	if err != nil {
		t.Fatalf("CreateDepartment Sales: %v", err)
	}

	newUser := func(email, given, last string) uuid.UUID {
		var id uuid.UUID
		if err := pool.QueryRow(ctx,
			"INSERT INTO users (email, given_names, last_name) VALUES ($1, $2, $3) RETURNING id",
			email, given, last,
		).Scan(&id); err != nil {
			t.Fatalf("create user %s: %v", email, err)
		}
		return id
	}

	alice := newUser("alice@example.test", "Alice", "Anderson")
	bob := newUser("bob@example.test", "Bob", "Brown")
	if _, err := store.AddMembership(ctx, org.ID, alice, organization.RoleAdmin, strptr("CTO"), &eng.ID); err != nil {
		t.Fatalf("AddMembership alice: %v", err)
	}
	if _, err := store.AddMembership(ctx, org.ID, bob, organization.RoleMember, strptr("Sales Rep"), &sales.ID); err != nil {
		t.Fatalf("AddMembership bob: %v", err)
	}

	if _, err := store.CreateInvitation(ctx, organization.Invitation{
		OrganizationID: org.ID,
		Email:          "carol@example.test",
		Role:           organization.RoleMember,
		GivenNames:     "Carol",
		LastName:       "Clark",
		JobTitle:       strptr("Designer"),
		DepartmentID:   &eng.ID,
		InvitedBy:      &alice,
	}); err != nil {
		t.Fatalf("CreateInvitation carol: %v", err)
	}

	return store, org.ID
}

func TestGetMember(t *testing.T) {
	pool, _ := testdb.Fresh(t)
	store := organization.NewStore(pool, audit.NopRecorder{})
	ctx := context.Background()

	org := makeOrg(t, pool, "Acme", "acme")

	var userID uuid.UUID
	if err := pool.QueryRow(ctx,
		"INSERT INTO users (email, given_names, last_name) VALUES ($1, $2, $3) RETURNING id",
		"alice@example.test", "Alice", "Anderson",
	).Scan(&userID); err != nil {
		t.Fatalf("create user: %v", err)
	}
	if _, err := store.AddMembership(ctx, org.ID, userID, organization.RoleAdmin, strptr("CTO"), nil); err != nil {
		t.Fatalf("AddMembership: %v", err)
	}

	got, err := store.GetMember(ctx, org.ID, userID)
	if err != nil {
		t.Fatalf("GetMember: %v", err)
	}
	if got.UserID != userID || got.Email != "alice@example.test" || got.Role != organization.RoleAdmin {
		t.Errorf("GetMember = %+v", got)
	}
	if got.JobTitle == nil || *got.JobTitle != "CTO" {
		t.Errorf("JobTitle = %v, want CTO", got.JobTitle)
	}

	if _, err := store.GetMember(ctx, org.ID, uuid.New()); !errors.Is(err, organization.ErrNotMember) {
		t.Errorf("GetMember unknown = %v, want ErrNotMember", err)
	}
}

func lastNames(entries []organization.MemberEntry) []string {
	names := make([]string, len(entries))
	for i, e := range entries {
		names[i] = e.LastName
	}
	return names
}

func TestListMemberEntriesStatusFilter(t *testing.T) {
	store, orgID := memberFixture(t)
	ctx := context.Background()

	tests := []struct {
		status    string
		wantTotal int
	}{
		{"", 3},
		{organization.StatusActive, 2},
		{organization.StatusInvited, 1},
	}
	for _, tc := range tests {
		entries, total, err := store.ListMemberEntries(ctx, orgID, organization.MemberListParams{
			Status: tc.status, Limit: 50,
		})
		if err != nil {
			t.Fatalf("status %q: %v", tc.status, err)
		}
		if total != tc.wantTotal || len(entries) != tc.wantTotal {
			t.Errorf("status %q: got %d entries / total %d, want %d", tc.status, len(entries), total, tc.wantTotal)
		}
		for _, e := range entries {
			if tc.status != "" && e.Status != tc.status {
				t.Errorf("status %q: entry has status %q", tc.status, e.Status)
			}
		}
	}
}

func TestListMemberEntriesSearch(t *testing.T) {
	store, orgID := memberFixture(t)
	ctx := context.Background()

	tests := []struct {
		name      string
		q         string
		wantTotal int
	}{
		{"last name", "anderson", 1},
		{"email", "bob@example", 1},
		{"job title on invitation branch", "designer", 1},
		{"department across both branches", "engineering", 2},
		{"no match", "zzz", 0},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			entries, total, err := store.ListMemberEntries(ctx, orgID, organization.MemberListParams{
				Search: tc.q, Limit: 50,
			})
			if err != nil {
				t.Fatalf("search %q: %v", tc.q, err)
			}
			if total != tc.wantTotal || len(entries) != tc.wantTotal {
				t.Errorf("search %q: got %d entries / total %d, want %d", tc.q, len(entries), total, tc.wantTotal)
			}
		})
	}
}

func TestListMemberEntriesSort(t *testing.T) {
	store, orgID := memberFixture(t)
	ctx := context.Background()

	asc, _, err := store.ListMemberEntries(ctx, orgID, organization.MemberListParams{
		Sort: "name", Limit: 50,
	})
	if err != nil {
		t.Fatalf("sort asc: %v", err)
	}
	if got := lastNames(asc); !equalStrings(got, []string{"Anderson", "Brown", "Clark"}) {
		t.Errorf("name asc = %v, want [Anderson Brown Clark]", got)
	}

	desc, _, err := store.ListMemberEntries(ctx, orgID, organization.MemberListParams{
		Sort: "name", Desc: true, Limit: 50,
	})
	if err != nil {
		t.Fatalf("sort desc: %v", err)
	}
	if got := lastNames(desc); !equalStrings(got, []string{"Clark", "Brown", "Anderson"}) {
		t.Errorf("name desc = %v, want [Clark Brown Anderson]", got)
	}

	// Job titles: CTO (Anderson) < Designer (Clark) < Sales Rep (Brown).
	byJob, _, err := store.ListMemberEntries(ctx, orgID, organization.MemberListParams{
		Sort: "jobtitle", Limit: 50,
	})
	if err != nil {
		t.Fatalf("sort jobtitle: %v", err)
	}
	if got := lastNames(byJob); !equalStrings(got, []string{"Anderson", "Clark", "Brown"}) {
		t.Errorf("jobtitle asc = %v, want [Anderson Clark Brown]", got)
	}
}

func TestListMemberEntriesPaging(t *testing.T) {
	store, orgID := memberFixture(t)
	ctx := context.Background()

	page1, total, err := store.ListMemberEntries(ctx, orgID, organization.MemberListParams{
		Sort: "name", Limit: 2, Offset: 0,
	})
	if err != nil {
		t.Fatalf("page 1: %v", err)
	}
	if total != 3 || !equalStrings(lastNames(page1), []string{"Anderson", "Brown"}) {
		t.Errorf("page 1 = %v / total %d, want [Anderson Brown] / 3", lastNames(page1), total)
	}

	page2, total, err := store.ListMemberEntries(ctx, orgID, organization.MemberListParams{
		Sort: "name", Limit: 2, Offset: 2,
	})
	if err != nil {
		t.Fatalf("page 2: %v", err)
	}
	if total != 3 || !equalStrings(lastNames(page2), []string{"Clark"}) {
		t.Errorf("page 2 = %v / total %d, want [Clark] / 3", lastNames(page2), total)
	}

	// Offset past the end: no rows, but the total must still be reported so the
	// pager can recover. This is the case count(*) OVER() would get wrong.
	empty, total, err := store.ListMemberEntries(ctx, orgID, organization.MemberListParams{
		Sort: "name", Limit: 2, Offset: 10,
	})
	if err != nil {
		t.Fatalf("past end: %v", err)
	}
	if total != 3 || len(empty) != 0 {
		t.Errorf("past end = %d entries / total %d, want 0 / 3", len(empty), total)
	}
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// The notification channels mail an org's admins, so this query has to hold to
// exactly that: admins of this organization, not its plain members, not someone
// who is only invited, and nobody from another organization.
func TestListAdminEmails(t *testing.T) {
	pool, _ := testdb.Fresh(t)
	store := organization.NewStore(pool, audit.NopRecorder{})
	ctx := context.Background()

	org := makeOrg(t, pool, "Acme", "acme")
	other := makeOrg(t, pool, "Globex", "globex")

	newUser := func(email string) uuid.UUID {
		var id uuid.UUID
		if err := pool.QueryRow(ctx,
			"INSERT INTO users (email, given_names, last_name) VALUES ($1, $2, $3) RETURNING id",
			email, "Test", "User",
		).Scan(&id); err != nil {
			t.Fatalf("create user %s: %v", email, err)
		}
		return id
	}

	memberships := []struct {
		email string
		orgID uuid.UUID
		role  string
	}{
		{"zoe@example.test", org.ID, organization.RoleAdmin},
		{"alice@example.test", org.ID, organization.RoleAdmin},
		{"bob@example.test", org.ID, organization.RoleMember},
		{"dana@example.test", other.ID, organization.RoleAdmin},
	}
	for _, m := range memberships {
		if _, err := store.AddMembership(ctx, m.orgID, newUser(m.email), m.role, nil, nil); err != nil {
			t.Fatalf("AddMembership %s: %v", m.email, err)
		}
	}
	if _, err := store.CreateInvitation(ctx, organization.Invitation{
		OrganizationID: org.ID,
		Email:          "carol@example.test",
		Role:           organization.RoleAdmin,
		GivenNames:     "Carol",
		LastName:       "Clark",
	}); err != nil {
		t.Fatalf("CreateInvitation carol: %v", err)
	}

	got, err := store.ListAdminEmails(ctx, org.ID)
	if err != nil {
		t.Fatalf("ListAdminEmails: %v", err)
	}
	want := []string{"alice@example.test", "zoe@example.test"}
	if !slices.Equal(got, want) {
		t.Errorf("ListAdminEmails = %v, want %v", got, want)
	}

	empty, err := store.ListAdminEmails(ctx, uuid.New())
	if err != nil {
		t.Fatalf("ListAdminEmails for an unknown org: %v", err)
	}
	if len(empty) != 0 {
		t.Errorf("ListAdminEmails for an unknown org = %v, want none", empty)
	}
}

// TestGetMemberAvatarIsScopedToTheOrganization is the access boundary: an avatar
// is personal data, so it must only be readable through an org the person is a
// member of — a second org's admin gets the same answer as "no photo set".
func TestGetMemberAvatarIsScopedToTheOrganization(t *testing.T) {
	pool, _ := testdb.Fresh(t)
	store := organization.NewStore(pool, audit.NopRecorder{})
	users := user.NewStore(pool)
	ctx := context.Background()

	acme := makeOrg(t, pool, "Acme", "acme")
	other := makeOrg(t, pool, "Other", "other")

	var userID uuid.UUID
	if err := pool.QueryRow(ctx,
		"INSERT INTO users (email, given_names, last_name) VALUES ($1, $2, $3) RETURNING id",
		"alice@example.test", "Alice", "Anderson",
	).Scan(&userID); err != nil {
		t.Fatalf("create user: %v", err)
	}
	if _, err := store.AddMembership(ctx, acme.ID, userID, organization.RoleMember, nil, nil); err != nil {
		t.Fatalf("AddMembership: %v", err)
	}

	if _, err := store.GetMemberAvatar(ctx, acme.ID, userID); !errors.Is(err, user.ErrNoAvatar) {
		t.Errorf("GetMemberAvatar before upload = %v, want user.ErrNoAvatar", err)
	}

	avatar := user.Avatar{Bytes: []byte{0xFF, 0xD8, 0xFF, 0xE0}, ContentType: user.AvatarContentType}
	if _, err := users.SetAvatar(ctx, userID, avatar); err != nil {
		t.Fatalf("SetAvatar: %v", err)
	}

	got, err := store.GetMemberAvatar(ctx, acme.ID, userID)
	if err != nil {
		t.Fatalf("GetMemberAvatar: %v", err)
	}
	if !bytes.Equal(got.Bytes, avatar.Bytes) || got.ContentType != avatar.ContentType {
		t.Errorf("GetMemberAvatar = %+v, want %+v", got, avatar)
	}

	if _, err := store.GetMemberAvatar(ctx, other.ID, userID); !errors.Is(err, user.ErrNoAvatar) {
		t.Errorf("GetMemberAvatar from a foreign org = %v, want user.ErrNoAvatar", err)
	}
}

// TestListMemberEntriesReportsAvatars keeps the list query's has_avatar column
// honest: an active member's photo shows, an invited entry has no user row and so
// never claims one.
func TestListMemberEntriesReportsAvatars(t *testing.T) {
	pool, _ := testdb.Fresh(t)
	store := organization.NewStore(pool, audit.NopRecorder{})
	users := user.NewStore(pool)
	ctx := context.Background()

	org := makeOrg(t, pool, "Acme", "acme")

	var userID uuid.UUID
	if err := pool.QueryRow(ctx,
		"INSERT INTO users (email, given_names, last_name) VALUES ($1, $2, $3) RETURNING id",
		"alice@example.test", "Alice", "Anderson",
	).Scan(&userID); err != nil {
		t.Fatalf("create user: %v", err)
	}
	if _, err := store.AddMembership(ctx, org.ID, userID, organization.RoleMember, nil, nil); err != nil {
		t.Fatalf("AddMembership: %v", err)
	}
	if _, err := store.CreateInvitation(ctx, organization.Invitation{
		OrganizationID: org.ID,
		Email:          "dave@example.test",
		GivenNames:     "Dave",
		LastName:       "Dijk",
		Role:           organization.RoleMember,
	}); err != nil {
		t.Fatalf("CreateInvitation: %v", err)
	}
	if _, err := users.SetAvatar(ctx, userID, user.Avatar{Bytes: []byte{1, 2, 3}, ContentType: user.AvatarContentType}); err != nil {
		t.Fatalf("SetAvatar: %v", err)
	}

	entries, _, err := store.ListMemberEntries(ctx, org.ID, organization.MemberListParams{Limit: 10})
	if err != nil {
		t.Fatalf("ListMemberEntries: %v", err)
	}
	for _, e := range entries {
		switch e.Status {
		case organization.StatusActive:
			if !e.HasAvatar || e.AvatarUpdatedAt == nil {
				t.Errorf("active entry %s: hasAvatar = %v, updatedAt = %v; want true and a timestamp", e.Email, e.HasAvatar, e.AvatarUpdatedAt)
			}
		case organization.StatusInvited:
			if e.HasAvatar {
				t.Errorf("invited entry %s claims an avatar", e.Email)
			}
		}
	}
}
