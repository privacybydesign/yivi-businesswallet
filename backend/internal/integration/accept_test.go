//go:build integration

package integration

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/privacybydesign/yivi-businesswallet/backend/internal/audit"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/organization"
)

func (e *testEnv) createInvitation(orgID uuid.UUID, email, givenNames, lastName string) string {
	e.t.Helper()
	store := organization.NewStore(e.pool, audit.NewDBRecorder())
	inv, err := store.CreateInvitation(context.Background(), organization.Invitation{
		OrganizationID: orgID,
		Email:          email,
		Role:           organization.RoleMember,
		GivenNames:     givenNames,
		LastName:       lastName,
	})
	if err != nil {
		e.t.Fatalf("create invitation %q: %v", email, err)
	}
	return inv.Token
}

func (e *testEnv) createUserNamed(email, givenNames, lastName string) uuid.UUID {
	e.t.Helper()
	var id uuid.UUID
	if err := e.pool.QueryRow(context.Background(),
		`INSERT INTO users (email, given_names, last_name) VALUES ($1, $2, $3) RETURNING id`,
		email, givenNames, lastName,
	).Scan(&id); err != nil {
		e.t.Fatalf("create user %q: %v", email, err)
	}
	return id
}

func (e *testEnv) discloses(email, givenNames, lastName string) {
	e.fake.email = email
	e.fake.givenNames = givenNames
	e.fake.familyName = lastName
}

func (e *testEnv) acceptInvite(token string) *http.Response {
	e.t.Helper()
	return e.do(http.MethodPost, "/api/v1/invite/"+token+"/accept", strings.NewReader(`{"disclosureToken":"test-token"}`))
}

func (e *testEnv) membershipCount(orgID uuid.UUID, email string) int {
	e.t.Helper()
	var n int
	if err := e.pool.QueryRow(context.Background(),
		`SELECT count(*) FROM memberships m JOIN users u ON u.id = m.user_id WHERE m.organization_id = $1 AND u.email = $2`,
		orgID, email,
	).Scan(&n); err != nil {
		e.t.Fatalf("membership count: %v", err)
	}
	return n
}

func (e *testEnv) invitationCount(orgID uuid.UUID, email string) int {
	e.t.Helper()
	var n int
	if err := e.pool.QueryRow(context.Background(),
		`SELECT count(*) FROM invitations WHERE organization_id = $1 AND email = $2`, orgID, email,
	).Scan(&n); err != nil {
		e.t.Fatalf("invitation count: %v", err)
	}
	return n
}

func (e *testEnv) userName(email string) (string, string) {
	e.t.Helper()
	var given, last string
	if err := e.pool.QueryRow(context.Background(),
		`SELECT given_names, last_name FROM users WHERE email = $1`, email,
	).Scan(&given, &last); err != nil {
		e.t.Fatalf("user name %q: %v", email, err)
	}
	return given, last
}

func TestAcceptInvitationMintsNewUser(t *testing.T) {
	env := setup(t)
	orgID := env.createOrg("Acme", "acme")
	token := env.createInvitation(orgID, "newbie@example.test", "José", "van der Berg")
	env.discloses("newbie@example.test", "JOSE", "VAN DER BERG")

	resp := env.acceptInvite(token)
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("accept = %d, want 200", resp.StatusCode)
	}
	var body struct{ OrganizationSlug string }
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body.OrganizationSlug != "acme" {
		t.Errorf("slug = %q, want acme", body.OrganizationSlug)
	}

	if n := env.membershipCount(orgID, "newbie@example.test"); n != 1 {
		t.Errorf("membership = %d, want 1", n)
	}
	if n := env.invitationCount(orgID, "newbie@example.test"); n != 0 {
		t.Errorf("invitation after accept = %d, want 0", n)
	}
	// MRZ all-caps disclosure is cleaned to readable casing on the minted profile.
	if given, last := env.userName("newbie@example.test"); given != "Jose" || last != "van der Berg" {
		t.Errorf("minted name = %q %q, want Jose / van der Berg", given, last)
	}
	if n := env.auditCount(orgID, audit.MembershipAccepted); n != 1 {
		t.Errorf("accepted events = %d, want 1", n)
	}
}

func TestAcceptInvitationPreview(t *testing.T) {
	env := setup(t)
	orgID := env.createOrg("Acme", "acme")
	token := env.createInvitation(orgID, "newbie@example.test", "Pen", "Ding")

	resp := env.do(http.MethodGet, "/api/v1/invite/"+token, nil)
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("preview = %d, want 200", resp.StatusCode)
	}
	var body struct {
		OrganizationName string `json:"organizationName"`
		GivenNames       string `json:"givenNames"`
		Email            string `json:"email"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body.OrganizationName != "Acme" || body.GivenNames != "Pen" || body.Email != "newbie@example.test" {
		t.Errorf("preview = %+v, want Acme/Pen/newbie", body)
	}
}

func TestDeclineInvitation(t *testing.T) {
	env := setup(t)
	orgID := env.createOrg("Acme", "acme")
	token := env.createInvitation(orgID, "newbie@example.test", "Pen", "Ding")

	resp := env.do(http.MethodPost, "/api/v1/invite/"+token+"/decline", nil)
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("decline = %d, want 204", resp.StatusCode)
	}
	if n := env.invitationCount(orgID, "newbie@example.test"); n != 0 {
		t.Errorf("invitation after decline = %d, want 0", n)
	}
	if n := env.auditCount(orgID, audit.MembershipDeclined); n != 1 {
		t.Errorf("declined events = %d, want 1", n)
	}
}

func TestAcceptUnknownToken(t *testing.T) {
	env := setup(t)
	resp := env.acceptInvite("does-not-exist")
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("accept unknown = %d, want 404", resp.StatusCode)
	}
}

func TestAcceptExpiredInvitation(t *testing.T) {
	env := setup(t)
	orgID := env.createOrg("Acme", "acme")
	token := env.createInvitation(orgID, "newbie@example.test", "Pen", "Ding")
	if _, err := env.pool.Exec(context.Background(),
		`UPDATE invitations SET expires_at = now() - interval '1 hour' WHERE organization_id = $1 AND email = $2`,
		orgID, "newbie@example.test",
	); err != nil {
		t.Fatalf("expire invitation: %v", err)
	}
	env.discloses("newbie@example.test", "Pen", "Ding")

	resp := env.acceptInvite(token)
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusGone {
		t.Errorf("accept expired = %d, want 410", resp.StatusCode)
	}
}

func TestAcceptEmailMismatch(t *testing.T) {
	env := setup(t)
	orgID := env.createOrg("Acme", "acme")
	token := env.createInvitation(orgID, "invited@example.test", "Pen", "Ding")
	env.discloses("someone-else@example.test", "Pen", "Ding")

	resp := env.acceptInvite(token)
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusUnprocessableEntity {
		t.Errorf("email mismatch = %d, want 422", resp.StatusCode)
	}
	if n := env.membershipCount(orgID, "invited@example.test"); n != 0 {
		t.Errorf("membership after mismatch = %d, want 0", n)
	}
}

func TestAcceptNameMismatch(t *testing.T) {
	env := setup(t)
	orgID := env.createOrg("Acme", "acme")
	token := env.createInvitation(orgID, "newbie@example.test", "Pen", "Ding")
	env.discloses("newbie@example.test", "Some", "Body")

	resp := env.acceptInvite(token)
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusUnprocessableEntity {
		t.Errorf("name mismatch = %d, want 422", resp.StatusCode)
	}
	if n := env.invitationCount(orgID, "newbie@example.test"); n != 1 {
		t.Errorf("invitation after mismatch = %d, want 1 (still pending)", n)
	}
}

func TestAcceptExistingUserProceeds(t *testing.T) {
	env := setup(t)
	orgID := env.createOrg("Acme", "acme")
	env.createUserNamed("member@example.test", "Anna", "Bakker")
	token := env.createInvitation(orgID, "member@example.test", "Anna", "Bakker")
	env.discloses("member@example.test", "Anna", "Bakker")

	resp := env.acceptInvite(token)
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("accept = %d, want 200", resp.StatusCode)
	}
	if n := env.membershipCount(orgID, "member@example.test"); n != 1 {
		t.Errorf("membership = %d, want 1", n)
	}
	if given, last := env.userName("member@example.test"); given != "Anna" || last != "Bakker" {
		t.Errorf("name = %q %q, want unchanged Anna / Bakker", given, last)
	}
}

func TestAcceptUpgradesProfileName(t *testing.T) {
	env := setup(t)
	orgID := env.createOrg("Acme", "acme")
	env.createUserNamed("jose@example.test", "Jose", "Berg")
	token := env.createInvitation(orgID, "jose@example.test", "José", "Berg")
	env.discloses("jose@example.test", "José", "Berg")

	resp := env.acceptInvite(token)
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("accept = %d, want 200", resp.StatusCode)
	}
	if given, _ := env.userName("jose@example.test"); given != "José" {
		t.Errorf("given = %q, want upgraded to José", given)
	}
}

func TestAcceptProfileMismatchNeedsReview(t *testing.T) {
	env := setup(t)
	orgID := env.createOrg("Acme", "acme")
	env.createUserNamed("changed@example.test", "Anna", "Berg")
	token := env.createInvitation(orgID, "changed@example.test", "José", "Berg")
	env.discloses("changed@example.test", "José", "Berg")

	resp := env.acceptInvite(token)
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("profile mismatch = %d, want 200", resp.StatusCode)
	}
	var body struct{ Status string }
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body.Status != "pending_review" {
		t.Errorf("status = %q, want pending_review", body.Status)
	}
	if n := env.membershipCount(orgID, "changed@example.test"); n != 0 {
		t.Errorf("membership after review hold = %d, want 0", n)
	}
	if n := env.invitationCount(orgID, "changed@example.test"); n != 1 {
		t.Errorf("invitation after review hold = %d, want 1 (still pending)", n)
	}
	if n := env.reviewCount("pending"); n != 1 {
		t.Errorf("pending reviews = %d, want 1", n)
	}
}
