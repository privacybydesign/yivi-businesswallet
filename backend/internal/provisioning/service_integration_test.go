//go:build integration

package provisioning_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/privacybydesign/yivi-businesswallet/backend/internal/audit"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/identity"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/organization"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/provisioner"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/provisioning"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/testdb"
)

// graphUser is one account as the fake Microsoft Graph serves it.
type graphUser struct {
	ID             string `json:"id"`
	Mail           string `json:"mail"`
	GivenName      string `json:"givenName"`
	Surname        string `json:"surname"`
	JobTitle       string `json:"jobTitle,omitempty"`
	Department     string `json:"department,omitempty"`
	AccountEnabled bool   `json:"accountEnabled"`
}

// fakeGraph is the tenant's directory: the token endpoint, the scoped user
// collection and one admin group. The driver talks to it exactly as it would to
// Microsoft, so this test covers the real HTTP mapping as well as the SQL.
type fakeGraph struct {
	users  []graphUser
	admins []string
}

func (g *fakeGraph) start(t *testing.T) *provisioner.Entra {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case strings.HasSuffix(r.URL.Path, "/oauth2/v2.0/token"):
			_ = json.NewEncoder(w).Encode(map[string]any{"access_token": "t", "expires_in": 3600})
		case strings.HasPrefix(r.URL.Path, "/graph/groups/staff/"):
			_ = json.NewEncoder(w).Encode(map[string]any{"value": g.users})
		case strings.HasPrefix(r.URL.Path, "/graph/groups/leads/"):
			members := make([]map[string]string, 0, len(g.admins))
			for _, id := range g.admins {
				members = append(members, map[string]string{"id": id})
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"value": members})
		default:
			t.Errorf("unexpected graph request %s", r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(server.Close)
	return provisioner.NewEntra(server.Client()).WithEndpoints(server.URL+"/graph", server.URL+"/login")
}

// endToEnd wires the real stores, the real Entra driver and a fake directory over
// a real database, so a sync exercises every statement the reconciler runs.
type endToEnd struct {
	orgID   uuid.UUID
	pool    *pgxpool.Pool
	orgs    *organization.Store
	service *provisioning.Service
	graph   *fakeGraph
}

func newEndToEnd(t *testing.T, users []graphUser, admins []string) *endToEnd {
	t.Helper()
	pool, _ := testdb.Fresh(t)
	orgs := organization.NewStore(pool, audit.NopRecorder{})
	store := newStore(t, pool)
	e := &endToEnd{
		orgID: makeOrg(t, pool, "acme"),
		pool:  pool,
		orgs:  orgs,
		graph: &fakeGraph{users: users, admins: admins},
	}
	e.service = provisioning.NewService(store, orgs, orgs, nil, "https://wallet.example")
	e.service.Register(e.graph.start(t))

	secret := "s3cret"
	if _, err := store.Save(context.Background(), e.orgID, provisioning.SettingsInput{
		Enabled: true, Source: provisioner.SourceEntra,
		TenantID: "tenant", ClientID: "client", ClientSecret: &secret,
		GroupID: "staff", AdminGroupIDs: []string{"leads"},
	}); err != nil {
		t.Fatalf("Save settings: %v", err)
	}
	return e
}

func (e *endToEnd) sync(t *testing.T) provisioning.Result {
	t.Helper()
	result, err := e.service.Sync(context.Background(), e.orgID)
	if err != nil {
		t.Fatalf("Sync: %v", err)
	}
	return result
}

func (e *endToEnd) entry(t *testing.T, email string) organization.MemberEntry {
	t.Helper()
	entry, err := e.orgs.MemberEntryByEmail(context.Background(), e.orgID, email)
	if err != nil {
		t.Fatalf("MemberEntryByEmail %s: %v", email, err)
	}
	return entry
}

func TestSyncEndToEndThroughTheRealStores(t *testing.T) {
	e := newEndToEnd(t,
		[]graphUser{
			{
				ID: "u1", Mail: "ada@example.org", GivenName: "Ada", Surname: "Lovelace",
				JobTitle: "Engineer", Department: "Research", AccountEnabled: true,
			},
			{
				ID: "u2", Mail: "bob@example.org", GivenName: "Bob", Surname: "Baker",
				Department: "Sales", AccountEnabled: true,
			},
		},
		[]string{"u1"})
	ctx := context.Background()

	// 1. First run: the departments and the two invitations are created.
	first := e.sync(t)
	if first.DepartmentsCreated != 2 || first.MembersInvited != 2 {
		t.Fatalf("first run = %+v, want two departments and two invitations", first)
	}
	departments, err := e.orgs.ListDepartments(ctx, e.orgID)
	if err != nil {
		t.Fatalf("ListDepartments: %v", err)
	}
	if len(departments) != 2 {
		t.Fatalf("departments = %+v, want Research and Sales", departments)
	}
	ada := e.entry(t, "ada@example.org")
	if ada.Status != organization.StatusInvited || ada.Role != organization.RoleAdmin {
		t.Errorf("ada = %+v, want an admin invitation", ada)
	}
	if ada.DepartmentName == nil || *ada.DepartmentName != "Research" {
		t.Errorf("ada department = %v, want Research", ada.DepartmentName)
	}
	if ada.JobTitle == nil || *ada.JobTitle != "Engineer" {
		t.Errorf("ada job title = %v, want Engineer", ada.JobTitle)
	}

	// 2. Second run over the same directory changes nothing.
	second := e.sync(t)
	if second.DepartmentsCreated != 0 || second.MembersInvited != 0 || second.MembersUpdated != 0 {
		t.Errorf("second run = %+v, want no changes", second)
	}

	// 3. Ada accepts with her wallet, so her invitation becomes a membership. This
	//    is the flow provisioning deliberately does not shortcut.
	invitationID := *ada.InvitationID
	invitation, err := e.orgs.InvitationByID(ctx, invitationID)
	if err != nil {
		t.Fatalf("InvitationByID: %v", err)
	}
	var userID uuid.UUID
	if err := e.pool.QueryRow(ctx,
		"INSERT INTO users (email, given_names, last_name) VALUES ($1, $2, $3) RETURNING id",
		"ada@example.org", "Ada", "Lovelace").Scan(&userID); err != nil {
		t.Fatalf("create user: %v", err)
	}
	if err := e.orgs.AcceptInvitation(ctx, invitation, userID,
		identity.Name{GivenNames: "Ada", LastName: "Lovelace"}, "+31600000000"); err != nil {
		t.Fatalf("AcceptInvitation: %v", err)
	}

	bobInvitationID := *e.entry(t, "bob@example.org").InvitationID

	// 4. A change in the source reaches the accepted membership...
	e.graph.users[0].JobTitle = "Principal Engineer"
	// ...and a pending invitation is rewritten in place, keeping its accept link.
	e.graph.users[1].Department = "Research"
	third := e.sync(t)
	if third.MembersUpdated != 2 {
		t.Fatalf("third run = %+v, want both people updated", third)
	}
	ada = e.entry(t, "ada@example.org")
	if ada.Status != organization.StatusActive {
		t.Fatalf("ada = %+v, want her accepted membership", ada)
	}
	if ada.JobTitle == nil || *ada.JobTitle != "Principal Engineer" {
		t.Errorf("ada job title = %v, want the new one", ada.JobTitle)
	}
	bob := e.entry(t, "bob@example.org")
	if bob.DepartmentName == nil || *bob.DepartmentName != "Research" {
		t.Errorf("bob department = %v, want Research", bob.DepartmentName)
	}
	if *bob.InvitationID != bobInvitationID {
		t.Error("bob's invitation was replaced rather than updated; the accept link he was sent is dead")
	}

	// 5. Bob is disabled in the source: his pending invitation goes.
	e.graph.users[1].AccountEnabled = false
	fourth := e.sync(t)
	if fourth.MembersRemoved != 1 {
		t.Fatalf("fourth run = %+v, want bob deprovisioned", fourth)
	}
	if _, err := e.orgs.MemberEntryByEmail(ctx, e.orgID, "bob@example.org"); err == nil {
		t.Error("bob is disabled in the source but still has an invitation")
	}

	// 6. Ada leaves the source altogether: her accepted membership goes with her.
	//    A second admin has to exist first, because the store refuses to remove the
	//    organisation's last one.
	var otherID uuid.UUID
	if err := e.pool.QueryRow(ctx,
		"INSERT INTO users (email, given_names, last_name) VALUES ($1, $2, $3) RETURNING id",
		"carol@example.org", "Carol", "Clark").Scan(&otherID); err != nil {
		t.Fatalf("create user: %v", err)
	}
	if _, err := e.orgs.AddMembership(ctx, e.orgID, otherID, organization.RoleAdmin, nil, nil); err != nil {
		t.Fatalf("AddMembership: %v", err)
	}
	e.graph.users = []graphUser{{
		ID: "u3", Mail: "dave@example.org", GivenName: "Dave",
		Surname: "Dijk", AccountEnabled: true,
	}}
	fifth := e.sync(t)
	if fifth.MembersRemoved != 1 || fifth.MembersInvited != 1 {
		t.Fatalf("fifth run = %+v, want ada removed and dave invited", fifth)
	}
	if _, err := e.orgs.MemberEntryByEmail(ctx, e.orgID, "ada@example.org"); err == nil {
		t.Error("ada left the source but keeps her membership")
	}
	// Carol was never provisioned, so the sync must not have touched her.
	if _, err := e.orgs.MemberEntryByEmail(ctx, e.orgID, "carol@example.org"); err != nil {
		t.Errorf("carol was added by hand and must survive the sync: %v", err)
	}
}

// TestSyncReplacesASourceAccountOnAMailboxItOwnsAgainstTheRealIndex drives the
// one rule the in-memory doubles can only imitate: provisioned_members has a
// unique index on (organization, source, lower(email)), and LinkMember upserts on
// the primary key alone, so a second link on one address is an error that would
// abort the run.
func TestSyncReplacesASourceAccountOnAMailboxItOwnsAgainstTheRealIndex(t *testing.T) {
	e := newEndToEnd(t, []graphUser{{
		ID: "old-1", Mail: "ada@example.org", GivenName: "Ada", Surname: "Lovelace", AccountEnabled: true,
	}}, nil)
	ctx := context.Background()

	if first := e.sync(t); first.MembersInvited != 1 {
		t.Fatalf("first run = %+v, want the invitation", first)
	}

	// Ada declines, or an admin revokes it. The invitation goes and the ownership
	// link stays behind, because the sync does not re-invite somebody removed here.
	invitationID := *e.entry(t, "ada@example.org").InvitationID
	if err := e.orgs.RevokeInvitation(ctx, e.orgID, invitationID); err != nil {
		t.Fatalf("RevokeInvitation: %v", err)
	}

	// The tenant deletes the account and re-creates it on the same mailbox, which
	// gives it a new directory object id.
	e.graph.users[0].ID = "new-1"
	second := e.sync(t)

	if second.MembersInvited != 1 {
		t.Fatalf("second run = %+v, want the replacement account provisioned", second)
	}
	if entry := e.entry(t, "ada@example.org"); entry.Status != organization.StatusInvited {
		t.Errorf("entry = %+v, want an invitation for the replacement account", entry)
	}
	var externalID string
	if err := e.pool.QueryRow(ctx,
		"SELECT external_id FROM provisioned_members WHERE organization_id = $1", e.orgID,
	).Scan(&externalID); err != nil {
		t.Fatalf("read the ownership link: %v", err)
	}
	if externalID != "new-1" {
		t.Errorf("link = %q, want the mailbox owned by the new account alone", externalID)
	}
}
