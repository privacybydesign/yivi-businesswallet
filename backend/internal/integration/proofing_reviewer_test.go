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
	closed := base + "?status=approved"
	one := base + "/" + session.ID

	member := env.loginAs("member@acme.test")
	env.addMembership(member, orgID, organization.RoleMember)
	assertNoReviewWithoutRight(t, env, waiting, one, member)

	env.loginAs("admin@acme.test")
	assertAdminGivesReviewRight(t, env, orgID, member)
	own := sendRequestToMember(t, env, base, flow.ID, admin.ID)

	env.loginAs("member@acme.test")
	assertReviewerDecides(t, env, base, waiting, one, own.ID)
	assertReviewerLosesDecided(t, env, orgID, closed, one, member)

	// An admin still reads all of it.
	env.loginAs("admin@acme.test")
	if list := decodeJSON[[]proofingRequestResp](t, env.do(http.MethodGet, closed+"&subjectKind=external", nil)); len(list) != 1 {
		t.Errorf("approved external for an admin = %+v, want the one decided", list)
	}
	resp := env.do(http.MethodGet, one+"/result", nil)
	if resp.StatusCode != http.StatusOK {
		t.Errorf("result of an approved check, for an admin = %d, want 200", resp.StatusCode)
	}
	_ = resp.Body.Close()

	// Taken away, the member is back to their own.
	resp = env.putJSON(reviewRightPath(member), map[string]any{"canReview": false})
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

// reviewRightPath is the route that sets the reviewer right of member id.
func reviewRightPath(id uuid.UUID) string {
	return "/api/v1/orgs/acme/members/" + id.String() + "/review-right"
}

// assertNoReviewWithoutRight checks that a member without the reviewer right,
// the caller, sees and decides nothing of what waits, and cannot give
// themselves the right.
func assertNoReviewWithoutRight(t *testing.T, env *testEnv, waiting, one string, member uuid.UUID) {
	t.Helper()
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
	resp := env.putJSON(reviewRightPath(member), map[string]any{"canReview": true})
	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("a member giving themselves the right = %d, want 403", resp.StatusCode)
	}
	_ = resp.Body.Close()
}

// assertAdminGivesReviewRight checks that the admin, the caller, gives member
// the reviewer right, once however often it is sent, and only to a member of
// the org.
func assertAdminGivesReviewRight(t *testing.T, env *testEnv, orgID, member uuid.UUID) {
	t.Helper()
	resp := env.putJSON(reviewRightPath(member), map[string]any{})
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("a body without canReview = %d, want 400", resp.StatusCode)
	}
	_ = resp.Body.Close()
	resp = env.putJSON(reviewRightPath(uuid.New()), map[string]any{"canReview": true})
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("the right for a stranger = %d, want 404", resp.StatusCode)
	}
	_ = resp.Body.Close()
	for range 2 {
		resp = env.putJSON(reviewRightPath(member), map[string]any{"canReview": true})
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
}

// sendRequestToMember has the admin, the caller, send a request on flowID to
// the member adminID: no business of the reviewer until it waits for review.
// An engine that decides nothing keeps it pending.
func sendRequestToMember(t *testing.T, env *testEnv, base, flowID string, adminID uuid.UUID) proofingRequestResp {
	t.Helper()
	env.proofing.Outcome = ""
	resp := env.putJSON("/api/v1/orgs/acme/identity-proofing/flow-selection",
		map[string]any{"flowIds": []string{flowID}, "defaultFlowId": flowID})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("make the flow available to members = %d, want 200", resp.StatusCode)
	}
	_ = resp.Body.Close()
	own := decodeJSON[proofingRequestResp](t, env.postJSON(base, map[string]any{"userId": adminID, "flowId": flowID}))
	if own.Status != "pending" || own.External {
		t.Fatalf("request to a member = %+v, want pending and not external", own)
	}
	return own
}

// assertReviewerDecides checks that a reviewer, the caller, lists what waits,
// opens it and decides it, and still reaches no other request: not ownID,
// sent to a member, nor a list by status alone.
func assertReviewerDecides(t *testing.T, env *testEnv, base, waiting, one, ownID string) {
	t.Helper()
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
	wantProofingError(t, "a reviewer opening a request sent to a member", env.do(http.MethodGet, base+"/"+ownID, nil),
		http.StatusNotFound, "session_not_found")
	if got := decodeJSON[proofingRequestResp](t, env.do(http.MethodGet, one, nil)); got.Status != "needs_review" {
		t.Errorf("request opened by a reviewer = %+v, want the one waiting", got)
	}
	// While it waits the reviewer reads what the check proved, to decide on it.
	resp := env.do(http.MethodGet, one+"/result", nil)
	if resp.StatusCode != http.StatusOK {
		t.Errorf("result of a check waiting, for a reviewer = %d, want 200", resp.StatusCode)
	}
	_ = resp.Body.Close()
	resp = env.postJSON(one+"/review", map[string]any{"decision": "approve", "reason": "document checked by hand"})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("decision by a reviewer = %d, want 200", resp.StatusCode)
	}
	if decided := decodeJSON[proofingRequestResp](t, resp); decided.Status != "approved" {
		t.Errorf("decided = %+v, want approved", decided)
	}
}

// assertReviewerLosesDecided checks that the identity check one, decided, is
// no longer the reviewer's to read: the right reaches what waits, not every
// person the org ever checked. The decision is audited under member's name.
func assertReviewerLosesDecided(t *testing.T, env *testEnv, orgID uuid.UUID, closed, one string, member uuid.UUID) {
	t.Helper()
	for _, path := range []string{closed, closed + "&subjectKind=external", closed + "&subjectKind=member"} {
		if list := decodeJSON[[]proofingRequestResp](t, env.do(http.MethodGet, path, nil)); len(list) != 0 {
			t.Errorf("GET %s for a reviewer = %+v, want only the ones they sent", path, list)
		}
	}
	wantProofingError(t, "a reviewer opening an approved check", env.do(http.MethodGet, one, nil),
		http.StatusNotFound, "session_not_found")
	for _, route := range []string{"/result", "/data-matches", "/data-export"} {
		wantProofingError(t, "a reviewer reading "+route+" of an approved check", env.do(http.MethodGet, one+route, nil),
			http.StatusNotFound, "session_not_found")
	}
	wantProofingError(t, "a reviewer deciding an approved check", env.postJSON(one+"/review",
		map[string]any{"decision": "reject", "reason": "again"}), http.StatusNotFound, "session_not_found")
	var actor uuid.UUID
	if err := env.pool.QueryRow(context.Background(),
		`SELECT actor_user_id FROM audit_events WHERE action = $1 AND organization_id = $2`,
		audit.IdentityProofingReviewDecided, orgID).Scan(&actor); err != nil {
		t.Fatalf("read the decision's audit event: %v", err)
	}
	if actor != member {
		t.Errorf("decision audited under %s, want the reviewer %s", actor, member)
	}
}

// A reviewer reaches every data request of the org, whatever its status: what
// the Reviews page lists, opens and decides, its Closed tab included. A data
// request is the person's own ask, and its page stays readable once decided.
func TestProofingReviewerDataRequests(t *testing.T) {
	env := setup(t)
	env.proofing.Outcome = proofingprovider.StatusApproved
	orgID := env.createOrg("Acme", "acme")
	admin := env.login("admin@acme.test")
	env.addMembership(admin.ID, orgID, organization.RoleAdmin)
	env.proofingDefaultFlow("acme")
	// See my data runs on the org's standard flow (#344), created on the first
	// send of its kind.
	key := env.proofingKey("acme", "CI")
	asked := decodeJSON[proofingAPISessionResp](t, env.apiCall(http.MethodPost, "/api/v1/proofing/sessions", key.Secret,
		map[string]any{"email": "anna@example.org", "flowKind": "data_access", "sendMail": false}))
	// The read is what puts the proven person's request in review.
	if read := decodeJSON[proofingAPISessionResp](t, env.apiCall(http.MethodGet, "/api/v1/proofing/sessions/"+asked.ID,
		key.Secret, nil)); read.Status != "needs_review" {
		t.Fatalf("data request = %+v, want needs_review", read)
	}

	reviewer := env.loginAs("reviewer@acme.test")
	env.addMembership(reviewer, orgID, organization.RoleMember)
	env.loginAs("admin@acme.test")
	resp := env.putJSON("/api/v1/orgs/acme/members/"+reviewer.String()+"/review-right", map[string]any{"canReview": true})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("giving the right = %d, want 200", resp.StatusCode)
	}
	_ = resp.Body.Close()

	env.loginAs("reviewer@acme.test")
	base := "/api/v1/orgs/acme/identity-proofing/requests"
	one := base + "/" + asked.ID
	if list := decodeJSON[[]proofingRequestResp](t, env.do(http.MethodGet, base+"?flowKind=data_request", nil)); len(list) != 1 {
		t.Errorf("data requests for a reviewer = %+v, want the one asked", list)
	}
	for _, route := range []string{"", "/result", "/data-matches"} {
		resp = env.do(http.MethodGet, one+route, nil)
		if resp.StatusCode != http.StatusOK {
			t.Errorf("GET %s of a data request in review, for a reviewer = %d, want 200", route, resp.StatusCode)
		}
		_ = resp.Body.Close()
	}
	resp = env.postJSON(one+"/review", map[string]any{"decision": "approve", "reason": "it is Anna"})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("decision by a reviewer = %d, want 200", resp.StatusCode)
	}
	_ = resp.Body.Close()

	// Decided, it is still the reviewer's to read: the Closed tab lists it, its
	// page opens and its data downloads.
	if list := decodeJSON[[]proofingRequestResp](t, env.do(http.MethodGet, base+"?flowKind=data_request&status=approved", nil)); len(list) != 1 || list[0].Status != "approved" {
		t.Errorf("approved data requests for a reviewer = %+v, want the one decided", list)
	}
	for _, route := range []string{"", "/data-matches", "/data-export"} {
		resp = env.do(http.MethodGet, one+route, nil)
		if resp.StatusCode != http.StatusOK {
			t.Errorf("GET %s of an approved data request, for a reviewer = %d, want 200", route, resp.StatusCode)
		}
		_ = resp.Body.Close()
	}
}

// A role change clears the reviewer right: an admin reviews by their role, and
// one made a member again has to be given it anew. An admin is not given it.
func TestReviewRightClearedOnRoleChange(t *testing.T) {
	env := setup(t)
	orgID := env.createOrg("Acme", "acme")
	admin := env.login("admin@acme.test")
	env.addMembership(admin.ID, orgID, organization.RoleAdmin)
	member := env.loginAs("member@acme.test")
	env.addMembership(member, orgID, organization.RoleMember)
	env.loginAs("admin@acme.test")
	members := "/api/v1/orgs/acme/members/"
	right := members + member.String() + "/review-right"
	resp := env.putJSON(right, map[string]any{"canReview": true})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("giving the right = %d, want 200", resp.StatusCode)
	}
	_ = resp.Body.Close()

	setRole := func(role string) {
		t.Helper()
		resp := env.do(http.MethodPatch, members+member.String(), strings.NewReader(`{"role":"`+role+`"}`))
		if resp.StatusCode != http.StatusNoContent {
			t.Fatalf("make the member %s = %d, want 204", role, resp.StatusCode)
		}
		_ = resp.Body.Close()
	}
	setRole(organization.RoleAdmin)
	if n := env.auditCount(orgID, audit.MembershipReviewChanged); n != 2 {
		t.Errorf("review_changed audits after a grant and a promotion = %d, want 2", n)
	}
	for _, canReview := range []bool{true, false} {
		wantProofingError(t, "the right for an admin", env.putJSON(right, map[string]any{"canReview": canReview}),
			http.StatusConflict, "admin_reviews")
	}

	setRole(organization.RoleMember)
	if m := byEmail(env.listMembers("acme", organization.StatusActive), "member@acme.test"); m == nil || m.CanReview {
		t.Errorf("member made a member again = %+v, want the right gone", m)
	}
	env.loginAs("member@acme.test")
	if env.canReview("acme") {
		t.Error("a member whose right a promotion cleared canReview = true")
	}
}

// addressReviewResp is what a request response says of its address review,
// and what a reviewer who reaches it only for that must not see.
type addressReviewResp struct {
	ID            string            `json:"id"`
	Status        string            `json:"status"`
	AddressReview string            `json:"addressReview"`
	ProofedName   string            `json:"proofedName"`
	Diplomas      []json.RawMessage `json:"diplomas"`
	// AddressReviewedAt is when the address was decided.
	AddressReviewedAt string `json:"addressReviewedAt"`
}

// A reviewer reaches an approved check only for its address waiting for
// review: they list it and open it without the person's proofed name or
// diplomas, never its result, and decide the address with a reason. Once
// decided they still list and open it so, under Closed, but cannot decide it
// again. A plain member reaches none of it, and the check's sender, an admin,
// does not decide their own.
func TestProofingAddressReviewFlow(t *testing.T) {
	env := setup(t)
	env.proofing.Outcome = proofingprovider.StatusApproved
	orgID := env.createOrg("Acme", "acme")
	admin := env.login("admin@acme.test")
	env.addMembership(admin.ID, orgID, organization.RoleAdmin)
	flow := env.proofingDefaultFlow("acme")
	resp := env.putJSON("/api/v1/orgs/acme/identity-proofing/flows/"+flow.ID+"/address", map[string]any{"addressMode": "required"})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("ask the address on the flow = %d, want 200", resp.StatusCode)
	}
	_ = resp.Body.Close()

	base := "/api/v1/orgs/acme/identity-proofing/requests"
	sent := decodeJSON[proofingRequestResp](t, env.postJSON(base,
		map[string]any{"email": "anna@example.org", "flowId": flow.ID, "channel": "on_screen"}))
	one := base + "/" + sent.ID
	// The read is what records the engine's outcome on the request.
	if got := decodeJSON[addressReviewResp](t, env.do(http.MethodGet, one, nil)); got.Status != "approved" || got.ProofedName == "" {
		t.Fatalf("request = %+v, want approved with a proofed name", got)
	}
	resp = env.postJSON(one+"/address", map[string]any{
		"street": "Molenstraat", "houseNumber": "3", "postalCode": "6511 AB", "city": "Nijmegen", "country": "Nederland",
	})
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("enter the address = %d, want 204", resp.StatusCode)
	}
	_ = resp.Body.Close()
	review := one + "/address/review"
	wantProofingError(t, "the sender deciding their own check's address", env.postJSON(review,
		map[string]any{"decision": "confirm", "reason": "looks right"}), http.StatusForbidden, "own_review")

	member := env.loginAs("member@acme.test")
	env.addMembership(member, orgID, organization.RoleMember)
	resp = env.postJSON(review, map[string]any{"decision": "confirm", "reason": "looks right"})
	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("a member without the right deciding an address = %d, want 403", resp.StatusCode)
	}
	_ = resp.Body.Close()

	env.loginAs("admin@acme.test")
	resp = env.putJSON(reviewRightPath(member), map[string]any{"canReview": true})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("giving the right = %d, want 200", resp.StatusCode)
	}
	_ = resp.Body.Close()
	env.loginAs("member@acme.test")
	assertReviewerReachesOnlyAddress(t, env, base, one)

	wantProofingError(t, "an address decision without a reason", env.postJSON(review,
		map[string]any{"decision": "confirm", "reason": " "}), http.StatusBadRequest, "invalid_input")
	decided := decodeJSON[addressReviewResp](t, env.postJSON(review, map[string]any{"decision": "confirm", "reason": "matches the rental contract"}))
	if decided.AddressReview != "confirmed" || decided.Status != "approved" || decided.ProofedName != "" {
		t.Errorf("decided = %+v, want approved, address confirmed, no proofed name", decided)
	}
	if decided.AddressReviewedAt == "" {
		t.Error("decided carries no addressReviewedAt")
	}
	closed := decodeJSON[[]addressReviewResp](t, env.do(http.MethodGet, base+"?addressReview=confirmed", nil))
	if len(closed) != 1 || closed[0].ID != decided.ID || closed[0].ProofedName != "" || len(closed[0].Diplomas) != 0 {
		t.Errorf("confirmed addresses for a reviewer = %+v, want the one, without proofed name or diplomas", closed)
	}
	reopened := decodeJSON[addressReviewResp](t, env.do(http.MethodGet, one, nil))
	if reopened.AddressReview != "confirmed" || reopened.ProofedName != "" || len(reopened.Diplomas) != 0 {
		t.Errorf("decided request opened by a reviewer = %+v, want its confirmed address, without proofed name or diplomas", reopened)
	}
	for _, route := range []string{"/result", "/data-matches", "/data-export"} {
		wantProofingError(t, "a reviewer reading "+route+" of a check whose address was decided", env.do(http.MethodGet, one+route, nil),
			http.StatusNotFound, "session_not_found")
	}
	wantProofingError(t, "a reviewer deciding a decided address again", env.postJSON(review,
		map[string]any{"decision": "reject", "reason": "again"}), http.StatusConflict, "address_not_under_review")
}

// assertReviewerReachesOnlyAddress checks that the reviewer, the caller, lists
// and opens the check one for its pending address only: without the proofed
// name or diplomas, and never its result.
func assertReviewerReachesOnlyAddress(t *testing.T, env *testEnv, base, one string) {
	t.Helper()
	wantProofingError(t, "an unknown address review filter", env.do(http.MethodGet, base+"?addressReview=bogus", nil),
		http.StatusBadRequest, "invalid_input")
	list := decodeJSON[[]addressReviewResp](t, env.do(http.MethodGet, base+"?addressReview=pending", nil))
	if len(list) != 1 || list[0].AddressReview != "pending" || list[0].ProofedName != "" || len(list[0].Diplomas) != 0 {
		t.Errorf("pending addresses for a reviewer = %+v, want the one, without proofed name or diplomas", list)
	}
	got := decodeJSON[addressReviewResp](t, env.do(http.MethodGet, one, nil))
	if got.AddressReview != "pending" || got.ProofedName != "" || len(got.Diplomas) != 0 {
		t.Errorf("request opened by a reviewer = %+v, want its pending address, without proofed name or diplomas", got)
	}
	for _, route := range []string{"/result", "/data-matches", "/data-export"} {
		wantProofingError(t, "a reviewer reading "+route+" of a check whose address waits", env.do(http.MethodGet, one+route, nil),
			http.StatusNotFound, "session_not_found")
	}
}
