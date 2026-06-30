//go:build integration

package integration

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/google/uuid"
)

type myInviteBody struct {
	ID               uuid.UUID `json:"id"`
	OrganizationSlug string    `json:"organizationSlug"`
	GivenNames       string    `json:"givenNames"`
	Email            string    `json:"email"`
	ReviewStatus     string    `json:"reviewStatus"`
}

func (e *testEnv) myInvitations() []myInviteBody {
	e.t.Helper()
	resp := e.do(http.MethodGet, "/api/v1/me/invitations", nil)
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		e.t.Fatalf("my invitations = %d, want 200", resp.StatusCode)
	}
	var out []myInviteBody
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		e.t.Fatalf("decode my invitations: %v", err)
	}
	return out
}

func (e *testEnv) invitationID(orgID uuid.UUID, email string) uuid.UUID {
	e.t.Helper()
	var id uuid.UUID
	if err := e.pool.QueryRow(context.Background(),
		`SELECT id FROM invitations WHERE organization_id = $1 AND email = $2`, orgID, email,
	).Scan(&id); err != nil {
		e.t.Fatalf("invitation id: %v", err)
	}
	return id
}

func TestMyInvitationsListAndAccept(t *testing.T) {
	env := setup(t)
	env.login("member@example.test") // mints the user as "Test User"
	orgID := env.createOrg("Acme", "acme")
	env.createInvitation(orgID, "member@example.test", "Test", "User")
	env.createInvitation(orgID, "someone-else@example.test", "Other", "Person")

	list := env.myInvitations()
	if len(list) != 1 {
		t.Fatalf("invitations = %d, want 1 (only mine)", len(list))
	}
	if list[0].OrganizationSlug != "acme" || list[0].Email != "member@example.test" {
		t.Errorf("invitation = %+v, want acme/member", list[0])
	}

	env.discloses("member@example.test", "Test", "User")
	resp := env.do(http.MethodPost, "/api/v1/invitations/"+list[0].ID.String()+"/accept",
		jsonBody(`{"disclosureToken":"test-token"}`))
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("accept = %d, want 200", resp.StatusCode)
	}
	var body struct{ Status string }
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body.Status != "accepted" {
		t.Errorf("status = %q, want accepted (same person -> proceed, not review)", body.Status)
	}
	if n := env.membershipCount(orgID, "member@example.test"); n != 1 {
		t.Errorf("membership = %d, want 1", n)
	}
}

func TestDeclineMyInvitation(t *testing.T) {
	env := setup(t)
	env.login("member@example.test")
	orgID := env.createOrg("Acme", "acme")
	env.createInvitation(orgID, "member@example.test", "Test", "User")
	id := env.myInvitations()[0].ID

	resp := env.do(http.MethodPost, "/api/v1/me/invitations/"+id.String()+"/decline", nil)
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("decline = %d, want 204", resp.StatusCode)
	}
	if n := env.invitationCount(orgID, "member@example.test"); n != 0 {
		t.Errorf("invitation after decline = %d, want 0", n)
	}
}

// Accept-by-id is public, so the disclosure's email-match is the gate: you can
// only accept an invitation whose e-mail you can disclose.
func TestAcceptByIDRejectsWrongEmail(t *testing.T) {
	env := setup(t)
	orgID := env.createOrg("Acme", "acme")
	env.createInvitation(orgID, "someone-else@example.test", "Other", "Person")
	othersID := env.invitationID(orgID, "someone-else@example.test")

	env.discloses("intruder@example.test", "In", "Truder")
	resp := env.do(http.MethodPost, "/api/v1/invitations/"+othersID.String()+"/accept",
		jsonBody(`{"disclosureToken":"test-token"}`))
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusUnprocessableEntity {
		t.Errorf("accept with non-matching disclosure = %d, want 422", resp.StatusCode)
	}
	if n := env.membershipCount(orgID, "someone-else@example.test"); n != 0 {
		t.Errorf("membership = %d, want 0", n)
	}
}

func TestMyInvitationsFlagsUnderReview(t *testing.T) {
	env := setup(t)
	orgID := env.createOrg("Acme", "acme")
	env.seedPendingReview(orgID, "changed@example.test")
	env.login("changed@example.test")

	list := env.myInvitations()
	if len(list) != 1 {
		t.Fatalf("invitations = %d, want 1", len(list))
	}
	if list[0].ReviewStatus != "pending" {
		t.Errorf("reviewStatus = %q, want pending", list[0].ReviewStatus)
	}
}

func TestMyInvitationsRequiresAuth(t *testing.T) {
	env := setup(t)
	resp := env.do(http.MethodGet, "/api/v1/me/invitations", nil)
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("my invitations unauthenticated = %d, want 401", resp.StatusCode)
	}
}
