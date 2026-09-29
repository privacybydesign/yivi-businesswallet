//go:build integration

package integration

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/privacybydesign/yivi-businesswallet/backend/internal/organization"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/verification"
)

const (
	apvVCT    = "nl.nijmegen.apv.standplaatsvergunning"
	apvNumber = "APV-2026-01834"
)

type verificationTemplateResp struct {
	ID     string   `json:"id"`
	Name   string   `json:"name"`
	VCT    string   `json:"vct"`
	Claims []string `json:"claims"`
}

type verificationResp struct {
	ID           string               `json:"id"`
	Status       string               `json:"status"`
	TemplateName string               `json:"templateName"`
	WalletLink   string               `json:"walletLink"`
	BrowserLink  string               `json:"browserLink"`
	Claims       map[string]string    `json:"claims"`
	Checks       []verification.Check `json:"checks"`
	Valid        *bool                `json:"valid"`
}

// seedIssuedPermit writes a claimed (or revoked) APV permit into the org's
// issuance ledger the way the issue flow leaves it, without going through the
// hosted issuer: the ledger row is what the verification grades against.
func seedIssuedPermit(t *testing.T, env *testEnv, orgID uuid.UUID, number, status string) {
	t.Helper()
	attrs, err := json.Marshal(map[string]string{
		"vergunningnummer": number, "vergunninghouder_kvk": "12345678", "markt": "Grote Markt Nijmegen", "standplaats": "B-12",
	})
	if err != nil {
		t.Fatalf("marshal attributes: %v", err)
	}
	_, err = env.pool.Exec(context.Background(),
		`INSERT INTO issued_attestations (organization_id, schema_vct, recipient_kind, recipient_ref, attributes, status, delivery, claim_token)
		 VALUES ($1, $2, 'organization', 'groentekraam@qerds.localhost', $3, $4, 'qerds', $5)`,
		orgID, apvVCT, attrs, status, "claim-"+number,
	)
	if err != nil {
		t.Fatalf("seed issued permit %s: %v", number, err)
	}
}

func createVerificationTemplate(t *testing.T, env *testEnv, slug string) verificationTemplateResp {
	t.Helper()
	resp := env.postJSON("/api/v1/orgs/"+slug+"/verifications/templates", map[string]any{
		"name":    "APV standplaatsvergunning",
		"vct":     apvVCT,
		"claims":  []string{"vergunningnummer", " markt ", ""},
		"purpose": "Controle op de markt",
	})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create verification template = %d, want 201", resp.StatusCode)
	}
	return decodeJSON[verificationTemplateResp](t, resp)
}

func getVerification(t *testing.T, env *testEnv, slug, id string) verificationResp {
	t.Helper()
	resp := env.do(http.MethodGet, "/api/v1/orgs/"+slug+"/verifications/"+id, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("get verification = %d, want 200", resp.StatusCode)
	}
	return decodeJSON[verificationResp](t, resp)
}

// TestVerificationHTTPFlow drives the requester side of issue #245 end to end
// against the assembled router: an admin defines a verification template, a
// member starts a check and gets a QR-able link, the poll stays pending until
// the holder answers, and the disclosure is graded against the ledger - a
// claimed permit is valid, a revoked one is not - with the outcome audited.
func TestVerificationHTTPFlow(t *testing.T) {
	env := setup(t)
	const slug = "nijmegen"
	admin := env.login("admin@nijmegen.test")
	orgID := env.createOrg("Gemeente Nijmegen", slug)
	env.addMembership(admin.ID, orgID, organization.RoleAdmin)
	seedIssuedPermit(t, env, orgID, apvNumber, "claimed")
	seedIssuedPermit(t, env, orgID, "APV-2026-00001", "revoked")

	tpl := createVerificationTemplate(t, env, slug)
	if tpl.VCT != apvVCT || strings.Join(tpl.Claims, ",") != "vergunningnummer,markt" {
		t.Fatalf("template = %+v, want trimmed claims without the empty one", tpl)
	}

	// Start a check: the verifier is asked for exactly the template's query and
	// the browser gets both link forms to render.
	env.fake.queryPending = true
	resp := env.postJSON("/api/v1/orgs/"+slug+"/verifications", map[string]any{"templateId": tpl.ID})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("start verification = %d, want 201", resp.StatusCode)
	}
	started := decodeJSON[verificationResp](t, resp)
	if started.Status != "pending" || started.TemplateName != "APV standplaatsvergunning" {
		t.Fatalf("started = %+v", started)
	}
	if len(env.fake.queries) != 1 || env.fake.queries[0].VCT != apvVCT || len(env.fake.queries[0].Claims) != 2 {
		t.Fatalf("verifier queries = %+v, want the template's", env.fake.queries)
	}
	if !strings.HasPrefix(started.WalletLink, "openid4vp://?") {
		t.Errorf("wallet link = %q", started.WalletLink)
	}
	if !strings.HasPrefix(started.BrowserLink, "http://app.test/openid4vp?client_id=") {
		t.Errorf("browser link = %q", started.BrowserLink)
	}

	// Still pending while the holder has not answered.
	if v := getVerification(t, env, slug, started.ID); v.Status != "pending" || v.Valid != nil || v.BrowserLink == "" {
		t.Fatalf("pending view = %+v", v)
	}

	// The holder presents the claimed permit: graded valid, links withdrawn.
	env.fake.queryPending = false
	env.fake.queryClaims = map[string]string{"vergunningnummer": apvNumber, "markt": "Grote Markt Nijmegen"}
	done := getVerification(t, env, slug, started.ID)
	if done.Status != "completed" || done.Valid == nil || !*done.Valid {
		t.Fatalf("completed view = %+v, want valid", done)
	}
	if done.Claims["vergunningnummer"] != apvNumber || done.BrowserLink != "" || done.WalletLink != "" {
		t.Errorf("completed view = %+v", done)
	}
	if len(done.Checks) != 4 {
		t.Errorf("checks = %+v, want four", done.Checks)
	}
	for _, c := range done.Checks {
		if !c.Passed {
			t.Errorf("check %s failed: %+v", c.Name, c)
		}
	}

	// A second check meets the revoked permit: not valid, and the ledger status
	// is named.
	resp = env.postJSON("/api/v1/orgs/"+slug+"/verifications", map[string]any{"templateId": tpl.ID})
	second := decodeJSON[verificationResp](t, resp)
	env.fake.queryClaims = map[string]string{"vergunningnummer": "APV-2026-00001", "markt": "Grote Markt Nijmegen"}
	revoked := getVerification(t, env, slug, second.ID)
	if revoked.Status != "completed" || revoked.Valid == nil || *revoked.Valid {
		t.Fatalf("revoked view = %+v, want invalid", revoked)
	}
	var sawRevoked bool
	for _, c := range revoked.Checks {
		if c.Name == verification.CheckNotRevoked {
			sawRevoked = !c.Passed && c.Detail == "revoked"
		}
	}
	if !sawRevoked {
		t.Errorf("checks = %+v, want not_revoked failed with detail revoked", revoked.Checks)
	}

	// History lists both, newest first, with their verdicts.
	resp = env.do(http.MethodGet, "/api/v1/orgs/"+slug+"/verifications", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("list verifications = %d, want 200", resp.StatusCode)
	}
	history := decodeJSON[[]verificationResp](t, resp)
	if len(history) != 2 || history[0].ID != second.ID || history[1].ID != started.ID {
		t.Fatalf("history = %+v", history)
	}

	// Audit: started twice, completed twice, and the completion names the verdict.
	var completedEvents int
	if err := env.pool.QueryRow(context.Background(),
		`SELECT count(*) FROM audit_events WHERE organization_id = $1 AND action = 'verification.completed'`, orgID,
	).Scan(&completedEvents); err != nil {
		t.Fatalf("count audit events: %v", err)
	}
	if completedEvents != 2 {
		t.Errorf("verification.completed events = %d, want 2", completedEvents)
	}
	var metadata []byte
	if err := env.pool.QueryRow(context.Background(),
		`SELECT metadata FROM audit_events WHERE organization_id = $1 AND action = 'verification.completed' AND target_id = $2`, orgID, second.ID,
	).Scan(&metadata); err != nil {
		t.Fatalf("read audit metadata: %v", err)
	}
	var envelope struct {
		After struct {
			Valid        bool     `json:"valid"`
			FailedChecks []string `json:"failedChecks"`
		} `json:"after"`
	}
	if err := json.Unmarshal(metadata, &envelope); err != nil {
		t.Fatalf("decode audit metadata: %v", err)
	}
	if envelope.After.Valid || len(envelope.After.FailedChecks) != 1 || envelope.After.FailedChecks[0] != verification.CheckNotRevoked {
		t.Errorf("audit metadata = %s, want an invalid verdict naming not_revoked", metadata)
	}
	if strings.Contains(string(metadata), "APV-2026-00001") {
		t.Errorf("audit metadata = %s, must not carry the disclosed claims", metadata)
	}
}

// TestVerificationAuthorization pins the gates: templates are admin-managed,
// running a check needs membership, and a session is invisible outside its org.
func TestVerificationAuthorization(t *testing.T) {
	env := setup(t)
	admin := env.login("admin@nijmegen.test")
	orgID := env.createOrg("Gemeente Nijmegen", "nijmegen")
	env.addMembership(admin.ID, orgID, organization.RoleAdmin)
	tpl := createVerificationTemplate(t, env, "nijmegen")

	resp := env.postJSON("/api/v1/orgs/nijmegen/verifications/templates", map[string]any{"name": "x", "vct": apvVCT, "claims": []string{}})
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("template without claims = %d, want 400", resp.StatusCode)
	}
	_ = resp.Body.Close()

	resp = env.postJSON("/api/v1/orgs/nijmegen/verifications", map[string]any{"templateId": uuid.NewString()})
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("start with unknown template = %d, want 404", resp.StatusCode)
	}
	_ = resp.Body.Close()

	// A member (not admin) may run a check but not manage templates.
	memberID := env.loginAs("handhaver@nijmegen.test")
	env.addMembership(memberID, orgID, organization.RoleMember)
	resp = env.postJSON("/api/v1/orgs/nijmegen/verifications/templates", map[string]any{"name": "x", "vct": apvVCT, "claims": []string{"a"}})
	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("member creating a template = %d, want 403", resp.StatusCode)
	}
	_ = resp.Body.Close()
	resp = env.do(http.MethodDelete, "/api/v1/orgs/nijmegen/verifications/templates/"+tpl.ID, nil)
	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("member deleting a template = %d, want 403", resp.StatusCode)
	}
	_ = resp.Body.Close()
	resp = env.postJSON("/api/v1/orgs/nijmegen/verifications", map[string]any{"templateId": tpl.ID})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("member starting a check = %d, want 201", resp.StatusCode)
	}
	started := decodeJSON[verificationResp](t, resp)

	// Another org's member cannot see it.
	outsiderID := env.loginAs("outsider@other.test")
	otherID := env.createOrg("Other", "other")
	env.addMembership(outsiderID, otherID, organization.RoleAdmin)
	resp = env.do(http.MethodGet, "/api/v1/orgs/nijmegen/verifications/"+started.ID, nil)
	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("outsider reading a check = %d, want 403", resp.StatusCode)
	}
	_ = resp.Body.Close()
	resp = env.do(http.MethodGet, "/api/v1/orgs/other/verifications/"+started.ID, nil)
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("check under another org = %d, want 404", resp.StatusCode)
	}
	_ = resp.Body.Close()

	// The admin deletes the template; the session keeps its name.
	env.loginAs("admin@nijmegen.test")
	resp = env.do(http.MethodDelete, "/api/v1/orgs/nijmegen/verifications/templates/"+tpl.ID, nil)
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("delete template = %d, want 204", resp.StatusCode)
	}
	_ = resp.Body.Close()
	if v := getVerification(t, env, "nijmegen", started.ID); v.TemplateName != "APV standplaatsvergunning" {
		t.Errorf("session after template delete = %+v", v)
	}
}
