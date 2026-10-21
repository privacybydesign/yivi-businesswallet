//go:build integration

package integration

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/privacybydesign/yivi-businesswallet/backend/internal/audit"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/organization"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/proofingprovider"
)

// canReview reads whether the caller decides reviews, as GET /orgs/{slug} tells the app.
func (e *testEnv) canReview(slug string) bool {
	e.t.Helper()
	return decodeJSON[struct {
		CanReview bool `json:"canReview"`
	}](e.t, e.do(http.MethodGet, "/api/v1/orgs/"+slug, nil)).CanReview
}

// An admin gives a member the reviewer right: with it the member lists what
// waits in the org, opens it and decides it, as an admin does, and the decision
// is audited under their name; without it, or once it is taken away, the member
// reaches none of that. Only an admin sets the right.
func TestProofingReviewerRightFlow(t *testing.T) {
	env := setup(t)
	env.proofing.Outcome = proofingprovider.StatusNeedsReview
	orgID := env.createOrg("Acme", "acme")
	admin := env.login("admin@acme.test")
	env.addMembership(admin.ID, orgID, organization.RoleAdmin)

	flow := env.proofingDefaultFlow("acme")
	key := env.proofingKey("acme", "CI")
	session := decodeJSON[proofingAPISessionResp](t, env.apiCall(http.MethodPost, "/api/v1/proofing/sessions", key.Secret,
		map[string]any{"email": "anna@example.org", "sendMail": false}))
	// The read is what records the engine's outcome on the request.
	if read := decodeJSON[proofingAPISessionResp](t, env.apiCall(http.MethodGet, "/api/v1/proofing/sessions/"+session.ID,
		key.Secret, nil)); read.Status != "needs_review" {
		t.Fatalf("session = %+v, want needs_review", read)
	}

	base := "/api/v1/orgs/acme/identity-proofing/requests"
	waiting := base + "?status=needs_review"
	one := base + "/" + session.ID
	rightPath := func(id uuid.UUID) string { return "/api/v1/orgs/acme/members/" + id.String() + "/review-right" }

	// A member without the right sees and decides nothing of what waits.
	member := env.loginAs("member@acme.test")
	env.addMembership(member, orgID, organization.RoleMember)
	if env.canReview("acme") {
		t.Error("a member without the right canReview = true")
	}
	if list := decodeJSON[[]proofingRequestResp](t, env.do(http.MethodGet, waiting, nil)); len(list) != 0 {
		t.Errorf("waiting for a member without the right = %+v, want none", list)
	}
	wantProofingError(t, "a member opening a request they did not send", env.do(http.MethodGet, one, nil),
		http.StatusNotFound, "session_not_found")
	for _, route := range []struct{ method, path string }{
		{http.MethodPost, one + "/review"},
		{http.MethodGet, one + "/result"},
		{http.MethodGet, one + "/data-matches"},
		{http.MethodGet, one + "/data-export"},
	} {
		resp := env.do(route.method, route.path, strings.NewReader(`{"decision":"approve","reason":"x"}`))
		if resp.StatusCode != http.StatusForbidden {
			t.Errorf("member without the right %s %s = %d, want 403", route.method, route.path, resp.StatusCode)
		}
		_ = resp.Body.Close()
	}
	resp := env.putJSON(rightPath(member), map[string]any{"canReview": true})
	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("a member giving themselves the right = %d, want 403", resp.StatusCode)
	}
	_ = resp.Body.Close()

	// Only an admin gives it, to a member of the org.
	env.loginAs("admin@acme.test")
	resp = env.putJSON(rightPath(member), map[string]any{})
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("a body without canReview = %d, want 400", resp.StatusCode)
	}
	_ = resp.Body.Close()
	resp = env.putJSON(rightPath(uuid.New()), map[string]any{"canReview": true})
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("the right for a stranger = %d, want 404", resp.StatusCode)
	}
	_ = resp.Body.Close()
	for range 2 {
		resp = env.putJSON(rightPath(member), map[string]any{"canReview": true})
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("giving the right = %d, want 200", resp.StatusCode)
		}
		if got := decodeJSON[memberEntry](t, resp); !got.CanReview || got.IdentityStatus != "never" {
			t.Errorf("member after the grant = %+v, want canReview and the derived identityStatus never", got)
		}
	}
	if n := env.auditCount(orgID, audit.MembershipReviewChanged); n != 1 {
		t.Errorf("review_changed audits after a grant sent twice = %d, want 1", n)
	}
	if m := byEmail(env.listMembers("acme", organization.StatusActive), "member@acme.test"); m == nil || !m.CanReview {
		t.Errorf("listed member = %+v, want canReview", m)
	}

	// A request the admin sent to a member is no business of the reviewer until it
	// waits for review; an engine that decides nothing keeps it pending.
	env.proofing.Outcome = ""
	resp = env.putJSON("/api/v1/orgs/acme/identity-proofing/flow-selection",
		map[string]any{"flowIds": []string{flow.ID}, "defaultFlowId": flow.ID})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("make the flow available to members = %d, want 200", resp.StatusCode)
	}
	_ = resp.Body.Close()
	own := decodeJSON[proofingRequestResp](t, env.postJSON(base, map[string]any{"userId": admin.ID, "flowId": flow.ID}))
	if own.Status != "pending" || own.External {
		t.Fatalf("request to a member = %+v, want pending and not external", own)
	}

	// With it, the member lists what waits, opens it and decides it.
	env.loginAs("member@acme.test")
	if !env.canReview("acme") {
		t.Error("a member with the right canReview = false")
	}
	if list := decodeJSON[[]proofingRequestResp](t, env.do(http.MethodGet, waiting, nil)); len(list) != 1 || list[0].Status != "needs_review" {
		t.Errorf("waiting for a reviewer = %+v, want the one waiting", list)
	}
	if list := decodeJSON[[]proofingRequestResp](t, env.do(http.MethodGet, base, nil)); len(list) != 0 {
		t.Errorf("all requests of a reviewer = %+v, want only the ones they sent", list)
	}
	// A status alone does not make the list org-wide: only the ones the Reviews
	// page reads do.
	if list := decodeJSON[[]proofingRequestResp](t, env.do(http.MethodGet, base+"?status=pending", nil)); len(list) != 0 {
		t.Errorf("pending for a reviewer = %+v, want only the ones they sent", list)
	}
	// What is stored decides, not what the engine would report: it is not asked.
	env.proofing.Outcome = proofingprovider.StatusNeedsReview
	wantProofingError(t, "a reviewer opening a request sent to a member", env.do(http.MethodGet, base+"/"+own.ID, nil),
		http.StatusNotFound, "session_not_found")
	if got := decodeJSON[proofingRequestResp](t, env.do(http.MethodGet, one, nil)); got.Status != "needs_review" {
		t.Errorf("request opened by a reviewer = %+v, want the one waiting", got)
	}
	resp = env.postJSON(one+"/review", map[string]any{"decision": "approve", "reason": "document checked by hand"})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("decision by a reviewer = %d, want 200", resp.StatusCode)
	}
	if decided := decodeJSON[proofingRequestResp](t, resp); decided.Status != "approved" {
		t.Errorf("decided = %+v, want approved", decided)
	}
	closed := base + "?status=approved"
	if list := decodeJSON[[]proofingRequestResp](t, env.do(http.MethodGet, closed+"&subjectKind=external", nil)); len(list) != 1 || list[0].Status != "approved" {
		t.Errorf("approved external for a reviewer = %+v, want the one decided", list)
	}
	for _, path := range []string{closed, closed + "&subjectKind=member"} {
		if list := decodeJSON[[]proofingRequestResp](t, env.do(http.MethodGet, path, nil)); len(list) != 0 {
			t.Errorf("GET %s for a reviewer = %+v, want only the ones they sent", path, list)
		}
	}
	if got := decodeJSON[proofingRequestResp](t, env.do(http.MethodGet, one, nil)); got.Status != "approved" {
		t.Errorf("decided request opened by a reviewer = %+v, want approved", got)
	}
	var actor uuid.UUID
	if err := env.pool.QueryRow(context.Background(),
		`SELECT actor_user_id FROM audit_events WHERE action = $1 AND organization_id = $2`,
		audit.IdentityProofingReviewDecided, orgID).Scan(&actor); err != nil {
		t.Fatalf("read the decision's audit event: %v", err)
	}
	if actor != member {
		t.Errorf("decision audited under %s, want the reviewer %s", actor, member)
	}

	// Taken away, the member is back to their own.
	env.loginAs("admin@acme.test")
	resp = env.putJSON(rightPath(member), map[string]any{"canReview": false})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("taking the right = %d, want 200", resp.StatusCode)
	}
	_ = resp.Body.Close()
	env.loginAs("member@acme.test")
	resp = env.postJSON(one+"/review", map[string]any{"decision": "reject", "reason": "again"})
	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("decision after the right is taken = %d, want 403", resp.StatusCode)
	}
	_ = resp.Body.Close()
	if n := env.auditCount(orgID, audit.MembershipReviewChanged); n != 2 {
		t.Errorf("review_changed audits after a grant and a revoke = %d, want 2", n)
	}
}
