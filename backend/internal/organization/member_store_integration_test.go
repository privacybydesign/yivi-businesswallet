//go:build integration

package organization_test

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"

	"github.com/privacybydesign/yivi-businesswallet/backend/internal/audit"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/organization"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/testdb"
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

	org, err := store.Create(ctx, "Acme", "acme")
	if err != nil {
		t.Fatalf("Create org: %v", err)
	}

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

	org, err := store.Create(ctx, "Acme", "acme")
	if err != nil {
		t.Fatalf("Create org: %v", err)
	}

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
