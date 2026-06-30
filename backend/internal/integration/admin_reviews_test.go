//go:build integration

package integration

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/google/uuid"
)

type reviewBody struct {
	ID                  uuid.UUID `json:"id"`
	Email               string    `json:"email"`
	OrganizationSlug    string    `json:"organizationSlug"`
	StoredGivenNames    string    `json:"storedGivenNames"`
	DisclosedGivenNames string    `json:"disclosedGivenNames"`
}

func (e *testEnv) reviewCount(status string) int {
	e.t.Helper()
	var n int
	if err := e.pool.QueryRow(context.Background(),
		`SELECT count(*) FROM identity_reviews WHERE status = $1`, status,
	).Scan(&n); err != nil {
		e.t.Fatalf("review count: %v", err)
	}
	return n
}

func (e *testEnv) listReviews() []reviewBody {
	e.t.Helper()
	resp := e.do(http.MethodGet, "/api/v1/admin/identity-reviews", nil)
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		e.t.Fatalf("list reviews = %d, want 200", resp.StatusCode)
	}
	var reviews []reviewBody
	if err := json.NewDecoder(resp.Body).Decode(&reviews); err != nil {
		e.t.Fatalf("decode reviews: %v", err)
	}
	return reviews
}

// seedPendingReview makes the email an existing user with a different stored
// name, then accepts an invitation whose disclosed name matches the invite but
// not the profile — producing a pending identity review.
func (e *testEnv) seedPendingReview(orgID uuid.UUID, email string) {
	e.t.Helper()
	e.createUserNamed(email, "Anna", "Berg")
	token := e.createInvitation(orgID, email, "José", "Berg")
	e.discloses(email, "José", "Berg")
	resp := e.acceptInvite(token)
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		e.t.Fatalf("seed accept = %d, want 200", resp.StatusCode)
	}
}

func TestIdentityReviewListAndApprove(t *testing.T) {
	env := setup(t, "boss@example.test")
	orgID := env.createOrg("Acme", "acme")
	env.seedPendingReview(orgID, "changed@example.test")
	env.login("boss@example.test")

	reviews := env.listReviews()
	if len(reviews) != 1 {
		t.Fatalf("reviews = %d, want 1", len(reviews))
	}
	if reviews[0].Email != "changed@example.test" || reviews[0].DisclosedGivenNames != "José" || reviews[0].StoredGivenNames != "Anna" {
		t.Errorf("review = %+v, want changed/José/Anna", reviews[0])
	}

	resp := env.do(http.MethodPost, "/api/v1/admin/identity-reviews/"+reviews[0].ID.String()+"/approve", nil)
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("approve = %d, want 200", resp.StatusCode)
	}

	if n := env.membershipCount(orgID, "changed@example.test"); n != 1 {
		t.Errorf("membership after approve = %d, want 1", n)
	}
	if given, _ := env.userName("changed@example.test"); given != "José" {
		t.Errorf("name after approve = %q, want José (updated to disclosed)", given)
	}
	if n := env.invitationCount(orgID, "changed@example.test"); n != 0 {
		t.Errorf("invitation after approve = %d, want 0 (consumed)", n)
	}
	if n := env.reviewCount("pending"); n != 0 {
		t.Errorf("pending reviews after approve = %d, want 0", n)
	}
}

func TestIdentityReviewReject(t *testing.T) {
	env := setup(t, "boss@example.test")
	orgID := env.createOrg("Acme", "acme")
	env.seedPendingReview(orgID, "changed@example.test")
	env.login("boss@example.test")

	reviews := env.listReviews()
	if len(reviews) != 1 {
		t.Fatalf("reviews = %d, want 1", len(reviews))
	}

	resp := env.do(http.MethodPost, "/api/v1/admin/identity-reviews/"+reviews[0].ID.String()+"/reject", nil)
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("reject = %d, want 200", resp.StatusCode)
	}

	if n := env.membershipCount(orgID, "changed@example.test"); n != 0 {
		t.Errorf("membership after reject = %d, want 0", n)
	}
	if n := env.invitationCount(orgID, "changed@example.test"); n != 1 {
		t.Errorf("invitation after reject = %d, want 1 (still pending)", n)
	}
	if n := env.reviewCount("rejected"); n != 1 {
		t.Errorf("rejected reviews = %d, want 1", n)
	}
	if given, _ := env.userName("changed@example.test"); given != "Anna" {
		t.Errorf("name after reject = %q, want unchanged Anna", given)
	}
}

func TestAcceptReReviewIsIdempotent(t *testing.T) {
	env := setup(t)
	orgID := env.createOrg("Acme", "acme")
	env.createUserNamed("changed@example.test", "Anna", "Berg")
	token := env.createInvitation(orgID, "changed@example.test", "José", "Berg")
	env.discloses("changed@example.test", "José", "Berg")

	for i := 0; i < 2; i++ {
		resp := env.acceptInvite(token)
		_ = resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("accept %d = %d, want 200", i, resp.StatusCode)
		}
	}
	if n := env.reviewCount("pending"); n != 1 {
		t.Errorf("pending reviews after re-accept = %d, want 1 (no duplicate)", n)
	}
	if n := env.auditCount(orgID, "user.identity_review_required"); n != 1 {
		t.Errorf("review-required events after re-accept = %d, want 1 (no duplicate audit)", n)
	}
}

func TestAcceptAfterRejectionIsRejected(t *testing.T) {
	env := setup(t, "boss@example.test")
	orgID := env.createOrg("Acme", "acme")
	env.seedPendingReview(orgID, "changed@example.test")

	env.login("boss@example.test")
	reviewID := env.listReviews()[0].ID
	rej := env.do(http.MethodPost, "/api/v1/admin/identity-reviews/"+reviewID.String()+"/reject", nil)
	_ = rej.Body.Close()
	if rej.StatusCode != http.StatusOK {
		t.Fatalf("reject = %d, want 200", rej.StatusCode)
	}

	// Re-accepting the still-pending invitation must report the rejection, not a
	// fresh "pending review".
	invID := env.invitationID(orgID, "changed@example.test")
	env.discloses("changed@example.test", "José", "Berg")
	acc := env.do(http.MethodPost, "/api/v1/invitations/"+invID.String()+"/accept",
		jsonBody(`{"disclosureToken":"test-token"}`))
	if acc.StatusCode != http.StatusForbidden {
		t.Errorf("re-accept after rejection = %d, want 403", acc.StatusCode)
	}
	var body struct{ Code string }
	decode(t, acc, &body)
	if body.Code != "identity_rejected" {
		t.Errorf("error code = %q, want identity_rejected", body.Code)
	}
	if n := env.membershipCount(orgID, "changed@example.test"); n != 0 {
		t.Errorf("membership = %d, want 0", n)
	}
}

func TestResolveUnknownReview(t *testing.T) {
	env := setup(t, "boss@example.test")
	env.login("boss@example.test")

	resp := env.do(http.MethodPost, "/api/v1/admin/identity-reviews/"+uuid.NewString()+"/approve", nil)
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("approve unknown = %d, want 404", resp.StatusCode)
	}
}

func TestIdentityReviewsRequirePlatformAdmin(t *testing.T) {
	env := setup(t)
	env.login("member@example.test")

	resp := env.do(http.MethodGet, "/api/v1/admin/identity-reviews", nil)
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("list as non-admin = %d, want 403", resp.StatusCode)
	}
}
