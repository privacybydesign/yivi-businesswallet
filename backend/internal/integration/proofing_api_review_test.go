//go:build integration

package integration

import (
	"net/http"
	"strings"
	"testing"

	"github.com/privacybydesign/yivi-businesswallet/backend/internal/organization"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/proofingprovider"
)

type apiReviewResp struct {
	ID        string   `json:"id"`
	Status    string   `json:"status"`
	Waiting   []string `json:"waiting"`
	Decisions []struct {
		Kind      string `json:"kind"`
		Outcome   string `json:"outcome"`
		DecidedBy string `json:"decidedBy"`
	} `json:"decisions"`
	Result *struct {
		Identity *apiReviewIdentity `json:"identity"`
		Photo    *apiReviewPhoto    `json:"photo"`
	} `json:"result"`
}

type apiReviewIdentity struct {
	FamilyName string `json:"familyName"`
}

type apiReviewPhoto struct {
	Data string `json:"data"`
}

// apiErrorCode is the code of an error answer.
func reviewErrorCode(t *testing.T, resp *http.Response) string {
	t.Helper()
	return decodeJSON[struct {
		Code string `json:"code"`
	}](t, resp).Code
}

// An org's software reviews a session with its API key alone: it reads what
// waits with the identity and the photos, decides it as a named reviewer, and
// the wallet shows that reviewer's decision.
func TestProofingAPIReviewFlow(t *testing.T) {
	env := setup(t)
	env.proofing.Outcome = proofingprovider.StatusNeedsReview
	orgID := env.createOrg("Acme", "acme")
	me := env.login("admin@acme.test")
	env.addMembership(me.ID, orgID, organization.RoleAdmin)
	env.proofingDefaultFlow("acme")
	key := env.proofingKey("acme", "Back office")
	session := decodeJSON[proofingAPISessionResp](t, env.apiCall(http.MethodPost, "/api/v1/proofing/sessions", key.Secret,
		map[string]any{"email": "anna@example.org", "sendMail": false}))
	review := "/api/v1/proofing/sessions/" + session.ID + "/review"

	waiting := decodeJSON[apiReviewResp](t, env.apiCall(http.MethodGet, review, key.Secret, nil))
	if len(waiting.Waiting) != 1 || waiting.Waiting[0] != "identity" || waiting.Result == nil ||
		waiting.Result.Identity == nil || waiting.Result.Identity.FamilyName == "" ||
		waiting.Result.Photo == nil || waiting.Result.Photo.Data == "" {
		t.Fatalf("review = %+v, want the identity waiting, with the identity and the photo", waiting)
	}

	decide := "/api/v1/proofing/sessions/" + session.ID + "/review-items/identity"
	assertReviewDecisionsRefused(t, env, decide, key.Secret)

	decided := decodeJSON[apiReviewResp](t, env.apiCall(http.MethodPost, decide, key.Secret,
		map[string]any{"decision": "confirm", "reason": "Document checked by hand", "reviewer": "Jan Jansen"}))
	if decided.Status != "approved" || len(decided.Waiting) != 0 || len(decided.Decisions) != 1 ||
		decided.Decisions[0].DecidedBy != "Jan Jansen" {
		t.Errorf("after the decision = %+v, want approved, nothing waiting, decided by Jan Jansen", decided)
	}
	if decided.Result == nil || decided.Result.Photo != nil {
		t.Errorf("result after the decision = %+v, want it without the photos", decided.Result)
	}
	resp := env.apiCall(http.MethodPost, decide, key.Secret,
		map[string]any{"decision": "reject", "reason": "Again", "reviewer": "Jan Jansen"})
	if code := reviewErrorCode(t, resp); resp.StatusCode != http.StatusConflict || code != "not_under_review" {
		t.Errorf("deciding again = %d %s, want 409 not_under_review", resp.StatusCode, code)
	}

	// The wallet names the reviewer the key named.
	inWallet := decodeJSON[walletDecisions](t, env.do(http.MethodGet, "/api/v1/orgs/acme/identity-proofing/requests/"+session.ID, nil))
	if len(inWallet.Decisions) != 1 || inWallet.Decisions[0].DecidedBy != "Jan Jansen" {
		t.Errorf("wallet decisions = %+v, want Jan Jansen", inWallet.Decisions)
	}

	// Another org's key reaches none of it.
	globexID := env.createOrg("Globex", "globex")
	env.addMembership(me.ID, globexID, organization.RoleAdmin)
	foreign := env.proofingKey("globex", "Other")
	resp = env.apiCall(http.MethodGet, review, foreign.Secret, nil)
	if code := reviewErrorCode(t, resp); resp.StatusCode != http.StatusNotFound || code != "session_not_found" {
		t.Errorf("another org's review = %d %s, want 404 session_not_found", resp.StatusCode, code)
	}
}

// walletDecisions is a request's decisions as the wallet lists them.
type walletDecisions struct {
	Decisions []walletDecision `json:"decisions"`
}

type walletDecision struct {
	DecidedBy string `json:"decidedBy"`
}

// assertReviewDecisionsRefused checks that decide refuses a decision without
// a reviewer or a reason, an unknown one, and one naming a wallet's own code.
func assertReviewDecisionsRefused(t *testing.T, env *testEnv, decide, secret string) {
	t.Helper()
	for _, tc := range []struct {
		what string
		body map[string]any
		code string
	}{
		{"no reviewer", map[string]any{"decision": "confirm", "reason": "Checked"}, "invalid_input"},
		{"no reason", map[string]any{"decision": "confirm", "reviewer": "Jan Jansen"}, "invalid_input"},
		{"an unknown decision", map[string]any{"decision": "approve", "reason": "Checked", "reviewer": "Jan Jansen"}, "invalid_input"},
		{"a wallet's own code", map[string]any{"decision": "reject", "reason": "Not her", "reviewer": "Jan Jansen", "errorCode": "IDENTITY_MISMATCH"}, "invalid_input"},
	} {
		resp := env.apiCall(http.MethodPost, decide, secret, tc.body)
		if code := reviewErrorCode(t, resp); resp.StatusCode != http.StatusBadRequest || code != tc.code {
			t.Errorf("%s = %d %s, want 400 %s", tc.what, resp.StatusCode, code, tc.code)
		}
	}
}

// A review link opens one session's review on the hosted review page for the
// reviewer the key named: the page reads it and decides it with the link's
// token alone, until the key is revoked.
func TestProofingReviewLinkFlow(t *testing.T) {
	env := setup(t)
	env.proofing.Outcome = proofingprovider.StatusNeedsReview
	orgID := env.createOrg("Acme", "acme")
	me := env.login("admin@acme.test")
	env.addMembership(me.ID, orgID, organization.RoleAdmin)
	env.proofingDefaultFlow("acme")
	key := env.proofingKey("acme", "Back office")
	session := decodeJSON[proofingAPISessionResp](t, env.apiCall(http.MethodPost, "/api/v1/proofing/sessions", key.Secret,
		map[string]any{"email": "anna@example.org", "sendMail": false}))
	links := "/api/v1/proofing/sessions/" + session.ID + "/review-links"

	resp := env.apiCall(http.MethodPost, links, key.Secret, map[string]any{})
	if code := reviewErrorCode(t, resp); resp.StatusCode != http.StatusBadRequest || code != "invalid_input" {
		t.Errorf("a link without a reviewer = %d %s, want 400 invalid_input", resp.StatusCode, code)
	}
	link := decodeJSON[struct {
		URL       string `json:"url"`
		ExpiresAt string `json:"expiresAt"`
	}](t, env.apiCall(http.MethodPost, links, key.Secret, map[string]any{"reviewer": "Petra de Wit"}))
	token, ok := strings.CutPrefix(link.URL, "http://wallet.test/r/")
	if !ok || token == "" || link.ExpiresAt == "" {
		t.Fatalf("link = %+v, want a /r/ page and when it expires", link)
	}

	type page struct {
		Reviewer string `json:"reviewer"`
		Request  struct {
			Status    string `json:"status"`
			Decisions []struct {
				DecidedBy string `json:"decidedBy"`
			} `json:"decisions"`
		} `json:"request"`
		Result *struct {
			Identity *apiReviewIdentity `json:"identity"`
		} `json:"result"`
	}
	opened := decodeJSON[page](t, env.do(http.MethodGet, "/api/v1/review/"+token, nil))
	if opened.Reviewer != "Petra de Wit" || opened.Request.Status != "needs_review" || opened.Result == nil ||
		opened.Result.Identity == nil || opened.Result.Identity.FamilyName == "" {
		t.Fatalf("page = %+v, want the waiting check with its identity, for Petra de Wit", opened)
	}
	decided := decodeJSON[page](t, env.postJSON("/api/v1/review/"+token+"/items/identity",
		map[string]any{"decision": "confirm", "reason": "Matches the document"}))
	if decided.Request.Status != "approved" || len(decided.Request.Decisions) != 1 ||
		decided.Request.Decisions[0].DecidedBy != "Petra de Wit" || decided.Result != nil {
		t.Errorf("page after the decision = %+v, want approved by Petra de Wit, no identity shown any more", decided)
	}
	if n := env.auditCount(orgID, "identity_proofing.review_link_created"); n != 1 {
		t.Errorf("links audited %d times, want 1", n)
	}

	resp = env.apiCall(http.MethodPost, links, key.Secret, map[string]any{"reviewer": "Petra de Wit"})
	if code := reviewErrorCode(t, resp); resp.StatusCode != http.StatusConflict || code != "nothing_to_review" {
		t.Errorf("a link once nothing waits = %d %s, want 409 nothing_to_review", resp.StatusCode, code)
	}

	// Revoking the key stops its links.
	_ = env.do(http.MethodDelete, "/api/v1/orgs/acme/identity-proofing/api-keys/"+key.ID, nil).Body.Close()
	for _, path := range []string{"/api/v1/review/" + token, "/api/v1/review/unknown-token"} {
		resp := env.do(http.MethodGet, path, nil)
		if code := reviewErrorCode(t, resp); resp.StatusCode != http.StatusNotFound || code != "review_link_not_found" {
			t.Errorf("%s = %d %s, want 404 review_link_not_found", path, resp.StatusCode, code)
		}
	}
}
