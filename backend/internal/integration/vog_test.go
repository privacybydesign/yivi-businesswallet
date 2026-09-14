//go:build integration

package integration

import (
	"bytes"
	"context"
	"encoding/json"
	"mime/multipart"
	"net/http"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/privacybydesign/yivi-businesswallet/backend/internal/organization"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/vog/vogtest"
)

// vogUploadResponse mirrors organization's uploadVogResponse.
type vogUploadResponse struct {
	Result          string   `json:"result"`
	MissingCodes    []string `json:"missingCodes,omitempty"`
	RejectionReason string   `json:"rejectionReason,omitempty"`
}

// doMultipart posts a single-file multipart form, the shape e.do (JSON/no body)
// cannot express.
func (e *testEnv) doMultipart(path string, fieldName, fileName string, content []byte) *http.Response {
	e.t.Helper()
	var body bytes.Buffer
	w := multipart.NewWriter(&body)
	part, err := w.CreateFormFile(fieldName, fileName)
	if err != nil {
		e.t.Fatalf("create form file: %v", err)
	}
	if _, err := part.Write(content); err != nil {
		e.t.Fatalf("write form file: %v", err)
	}
	if err := w.Close(); err != nil {
		e.t.Fatalf("close multipart writer: %v", err)
	}

	req, err := http.NewRequest(http.MethodPost, e.server.URL+path, &body)
	if err != nil {
		e.t.Fatalf("new request POST %s: %v", path, err)
	}
	req.Header.Set("Content-Type", w.FormDataContentType())
	resp, err := e.client.Do(req)
	if err != nil {
		e.t.Fatalf("do POST %s: %v", path, err)
	}
	return resp
}

// sampleVOG is a real, AES-encrypted Justis document (vogtest); the flows
// below go through the actual PDFium parser, so the identity a test stores for
// the member has to be the one printed on it.
func sampleVOG() vogtest.Sample {
	return vogtest.Samples()[0]
}

// matchingVOGPDF is the sample's bytes; pair it with setSampleIdentity.
func matchingVOGPDF(t *testing.T) []byte {
	t.Helper()
	return sampleVOG().PDF
}

// setSampleIdentity stores the identity printed on the sample VOG for the
// member, so an upload of it passes the identity match.
func setSampleIdentity(t *testing.T, e *testEnv, orgID, userID uuid.UUID) {
	t.Helper()
	s := sampleVOG()
	setDateOfBirth(t, e, orgID, userID, s.GivenNames, s.Surname, s.DateOfBirth)
}

func setDateOfBirth(t *testing.T, e *testEnv, orgID, userID uuid.UUID, given, last string, dob time.Time) {
	t.Helper()
	if _, err := e.pool.Exec(context.Background(),
		`UPDATE users SET given_names = $1, last_name = $2 WHERE id = $3`, given, last, userID); err != nil {
		t.Fatalf("set user name: %v", err)
	}
	if _, err := e.pool.Exec(context.Background(),
		`UPDATE memberships SET date_of_birth = $1 WHERE organization_id = $2 AND user_id = $3`, dob, orgID, userID); err != nil {
		t.Fatalf("set date of birth: %v", err)
	}
}

func TestSelfUploadVogValid(t *testing.T) {
	e := setup(t)
	me := e.login("jan@example.test")
	orgID := e.createOrg("Acme", "acme")
	e.addMembership(me.ID, orgID, organization.RoleMember)
	setSampleIdentity(t, e, orgID, me.ID)

	resp := e.doMultipart("/api/v1/orgs/acme/me/vog", "file", "vog.pdf", matchingVOGPDF(t))
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("upload status = %d, want 200", resp.StatusCode)
	}
	var body vogUploadResponse
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if body.Result != "valid" {
		t.Errorf("result = %q, want valid", body.Result)
	}
}

func TestSelfUploadVogRequiresDateOfBirth(t *testing.T) {
	e := setup(t)
	me := e.login("jan@example.test")
	orgID := e.createOrg("Acme", "acme")
	e.addMembership(me.ID, orgID, organization.RoleMember)
	// No date of birth set: the member must re-identify first.

	resp := e.doMultipart("/api/v1/orgs/acme/me/vog", "file", "vog.pdf", matchingVOGPDF(t))
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusConflict {
		t.Errorf("upload status = %d, want 409", resp.StatusCode)
	}
}

func TestSelfUploadVogMismatch(t *testing.T) {
	e := setup(t)
	me := e.login("jan@example.test")
	orgID := e.createOrg("Acme", "acme")
	e.addMembership(me.ID, orgID, organization.RoleMember)
	// Stored identity does not match the PDF's name.
	setDateOfBirth(t, e, orgID, me.ID, "Someone", "Else", time.Date(1990, 4, 3, 0, 0, 0, 0, time.UTC))

	resp := e.doMultipart("/api/v1/orgs/acme/me/vog", "file", "vog.pdf", matchingVOGPDF(t))
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("upload status = %d, want 422", resp.StatusCode)
	}
	var body vogUploadResponse
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if body.Result != "mismatch" {
		t.Errorf("result = %q, want mismatch", body.Result)
	}
}

func TestAdminUploadVogOnBehalf(t *testing.T) {
	e := setup(t)
	admin := e.login("boss@example.test")
	orgID := e.createOrg("Acme", "acme")
	e.addMembership(admin.ID, orgID, organization.RoleAdmin)

	member := e.createUser("jan@example.test")
	e.addMembership(member, orgID, organization.RoleMember)
	setSampleIdentity(t, e, orgID, member)

	resp := e.doMultipart("/api/v1/orgs/acme/members/"+member.String()+"/vog", "file", "vog.pdf", matchingVOGPDF(t))
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("admin upload status = %d, want 200", resp.StatusCode)
	}
}

func TestAdminUploadVogRequiresAdminRole(t *testing.T) {
	e := setup(t)
	plain := e.login("plain@example.test")
	orgID := e.createOrg("Acme", "acme")
	e.addMembership(plain.ID, orgID, organization.RoleMember)

	other := e.createUser("other@example.test")
	e.addMembership(other, orgID, organization.RoleMember)

	resp := e.doMultipart("/api/v1/orgs/acme/members/"+other.String()+"/vog", "file", "vog.pdf", matchingVOGPDF(t))
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("admin upload as a plain member = %d, want 403", resp.StatusCode)
	}
}

func TestRequestVogBulk(t *testing.T) {
	e := setup(t)
	admin := e.login("boss@example.test")
	orgID := e.createOrg("Acme", "acme")
	e.addMembership(admin.ID, orgID, organization.RoleAdmin)

	m1 := e.createUser("m1@example.test")
	m2 := e.createUser("m2@example.test")
	e.addMembership(m1, orgID, organization.RoleMember)
	e.addMembership(m2, orgID, organization.RoleMember)

	payload, _ := json.Marshal(map[string]any{"userIds": []string{m1.String(), m2.String()}, "reason": "annual re-check"})
	resp := e.do(http.MethodPost, "/api/v1/orgs/acme/members/request-vog", bytes.NewReader(payload))
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("request-vog status = %d, want 200", resp.StatusCode)
	}
	var body struct {
		Requested int `json:"requested"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if body.Requested != 2 {
		t.Errorf("requested = %d, want 2", body.Requested)
	}
}

func TestScreeningSettingsSaveAndGet(t *testing.T) {
	e := setup(t)
	admin := e.login("boss@example.test")
	orgID := e.createOrg("Acme", "acme")
	e.addMembership(admin.ID, orgID, organization.RoleAdmin)

	payload, _ := json.Marshal(map[string]any{
		"requiredFor":                 "both",
		"requiredCodes":               []string{"11", "43"},
		"recheckAnchor":               "issue_date",
		"overdueReminderIntervalDays": 7,
		"overdueReminderMaxCount":     4,
		"overdueConsequence":          "flag",
	})
	resp := e.do(http.MethodPut, "/api/v1/orgs/acme/screening-settings", bytes.NewReader(payload))
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("save screening settings status = %d, want 200", resp.StatusCode)
	}

	getResp := e.do(http.MethodGet, "/api/v1/orgs/acme/screening-settings", nil)
	defer func() { _ = getResp.Body.Close() }()
	var settings organization.ScreeningSettings
	if err := json.NewDecoder(getResp.Body).Decode(&settings); err != nil {
		t.Fatalf("decode settings: %v", err)
	}
	if !settings.Configured || settings.RequiredFor != "both" {
		t.Errorf("settings = %+v, want configured/both", settings)
	}
}

// ownVogBody mirrors organization's ownVogState as the org detail carries it.
type ownVogBody struct {
	Status           string `json:"status"`
	AcceptCredential bool   `json:"acceptCredential"`
	NeedsIdentity    bool   `json:"needsIdentity"`
}

type orgDetailBody struct {
	Identity *struct {
		Status string `json:"status"`
	} `json:"identity"`
	Vog *ownVogBody `json:"vog"`
}

func (e *testEnv) orgDetail(slug string) orgDetailBody {
	e.t.Helper()
	resp := e.do(http.MethodGet, "/api/v1/orgs/"+slug, nil)
	if resp.StatusCode != http.StatusOK {
		_ = resp.Body.Close()
		e.t.Fatalf("GET org detail = %d, want 200", resp.StatusCode)
	}
	return decodeJSON[orgDetailBody](e.t, resp)
}

// acceptVogCredential turns on the org's pbdf.vog opt-in with the given
// required codes, as the currently signed-in admin.
func (e *testEnv) acceptVogCredential(slug string, requiredCodes ...string) {
	e.t.Helper()
	payload, _ := json.Marshal(map[string]any{
		"requiredFor":                 "both",
		"requiredCodes":               requiredCodes,
		"recheckAnchor":               "issue_date",
		"overdueReminderIntervalDays": 7,
		"overdueReminderMaxCount":     4,
		"overdueConsequence":          "flag",
		"acceptYiviCredential":        true,
	})
	resp := e.do(http.MethodPut, "/api/v1/orgs/"+slug+"/screening-settings", bytes.NewReader(payload))
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		e.t.Fatalf("save screening settings = %d, want 200", resp.StatusCode)
	}
}

// A member who never identified (no date of birth on file) is asked for a
// VOG. Instead of the 409 the plain paths answer, one combined wallet session
// discloses identity and pbdf.vog together: the identity is written to the
// membership and the VOG is matched against it.
func TestUnidentifiedMemberDisclosesIdentityAndVogInOneSession(t *testing.T) {
	e := setup(t)
	orgID := e.adminOf("acme", "Acme", "boss@example.test")
	e.acceptVogCredential("acme", "11")

	member := e.createUserNamed("jan@example.test", "Jan Willem", "van der Jansen")
	e.addMembership(member, orgID, organization.RoleMember)
	e.loginAs("jan@example.test")

	before := e.orgDetail("acme")
	if before.Vog == nil || !before.Vog.NeedsIdentity {
		t.Fatalf("own vog state before = %+v, want needsIdentity", before.Vog)
	}

	// The plain credential path still refuses: nothing to match against yet.
	e.discloses("jan@example.test", "Jan Willem", "van der Jansen")
	e.fake.dateOfBirth = "1990-04-03"
	e.fake.vog = &fakeVog{givenNames: "Jan Willem", surname: "van der Jansen", dateOfBirth: "1990-04-03", issueDate: time.Now().AddDate(0, -1, 0).Format("2006-01-02"), aspects: []string{"11"}}
	plain := e.postJSON("/api/v1/orgs/acme/me/vog/credential-complete", map[string]string{"disclosureToken": "presentation-jan@example.test"})
	_ = plain.Body.Close()
	if plain.StatusCode != http.StatusConflict {
		t.Errorf("plain credential-complete = %d, want 409", plain.StatusCode)
	}

	session := e.do(http.MethodPost, "/api/v1/orgs/acme/me/vog/identity-credential-session", nil)
	sessionBody := decodeJSON[struct {
		ID         string `json:"id"`
		WalletLink string `json:"walletLink"`
	}](t, session)
	if sessionBody.ID == "" || sessionBody.WalletLink == "" {
		t.Fatalf("session = %+v, want an id and a wallet link", sessionBody)
	}

	done := e.postJSON("/api/v1/orgs/acme/me/vog/identity-credential-complete", map[string]string{"disclosureToken": "presentation-jan@example.test"})
	if done.StatusCode != http.StatusOK {
		_ = done.Body.Close()
		t.Fatalf("identity-credential-complete = %d, want 200", done.StatusCode)
	}
	outcome := decodeJSON[vogUploadResponse](t, done)
	if outcome.Result != "valid" {
		t.Errorf("result = %+v, want valid", outcome)
	}

	after := e.orgDetail("acme")
	if after.Identity == nil || after.Identity.Status != organization.IdentityStatusVerified {
		t.Errorf("identity after = %+v, want verified", after.Identity)
	}
	if after.Vog == nil || after.Vog.Status != organization.ScreeningStatusValid || after.Vog.NeedsIdentity {
		t.Errorf("vog after = %+v, want valid and no longer needing identity", after.Vog)
	}
	if n := e.auditCount(orgID, "membership.identity_reverified"); n != 1 {
		t.Errorf("membership.identity_reverified events = %d, want 1", n)
	}
}

// The identity half is held to the re-identification rules: a VOG session
// whose identity credential names someone else is refused with the
// re-identification code and records nothing.
func TestIdentityAndVogSessionRejectsAnotherPersonsIdentity(t *testing.T) {
	e := setup(t)
	orgID := e.adminOf("acme", "Acme", "boss@example.test")
	e.acceptVogCredential("acme")
	member := e.createUserNamed("jan@example.test", "Jan Willem", "van der Jansen")
	e.addMembership(member, orgID, organization.RoleMember)
	e.loginAs("jan@example.test")

	e.discloses("jan@example.test", "Someone", "Else")
	e.fake.dateOfBirth = "1990-04-03"
	e.fake.vog = &fakeVog{givenNames: "Someone", surname: "Else", dateOfBirth: "1990-04-03", issueDate: time.Now().Format("2006-01-02")}
	resp := e.postJSON("/api/v1/orgs/acme/me/vog/identity-credential-complete", map[string]string{"disclosureToken": "presentation-jan@example.test"})
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusConflict {
		t.Fatalf("identity-credential-complete = %d, want 409", resp.StatusCode)
	}
	var body struct {
		Code string `json:"code"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body.Code != "name_mismatch" {
		t.Errorf("code = %q, want name_mismatch", body.Code)
	}
	if e.orgDetail("acme").Vog.Status != organization.ScreeningStatusNone {
		t.Error("a screening was recorded despite the rejected identity")
	}
}

// The PDF path for a member who never identified: identify in-app first (no
// bearer-token link), then upload. The identity is the one printed on the
// sample VOG, so the upload passes the match against it.
func TestUnidentifiedMemberIdentifiesInAppThenUploads(t *testing.T) {
	e := setup(t)
	// The member was invited under the name printed on the sample VOG (an
	// identity disclosure must match the name on file), but never identified.
	s := sampleVOG()
	e.createUserNamed("dibran@example.test", s.GivenNames, s.Surname)
	me := e.login("dibran@example.test")
	orgID := e.createOrg("Acme", "acme")
	e.addMembership(me.ID, orgID, organization.RoleMember)

	blocked := e.doMultipart("/api/v1/orgs/acme/me/vog", "file", "vog.pdf", matchingVOGPDF(t))
	_ = blocked.Body.Close()
	if blocked.StatusCode != http.StatusConflict {
		t.Fatalf("upload before identifying = %d, want 409", blocked.StatusCode)
	}

	session := e.do(http.MethodPost, "/api/v1/orgs/acme/me/identity-session", nil)
	if id := decodeJSON[struct {
		ID string `json:"id"`
	}](t, session).ID; id == "" {
		t.Fatal("identity-session returned no id")
	}

	e.discloses("dibran@example.test", s.GivenNames, s.Surname)
	e.fake.dateOfBirth = s.DateOfBirth.Format("2006-01-02")
	done := e.postJSON("/api/v1/orgs/acme/me/identity-complete", map[string]string{"disclosureToken": disclosureToken})
	_ = done.Body.Close()
	if done.StatusCode != http.StatusNoContent {
		t.Fatalf("identity-complete = %d, want 204", done.StatusCode)
	}
	if detail := e.orgDetail("acme"); detail.Vog == nil || detail.Vog.NeedsIdentity {
		t.Fatalf("own vog state after identifying = %+v, want identity no longer needed", detail.Vog)
	}

	resp := e.doMultipart("/api/v1/orgs/acme/me/vog", "file", "vog.pdf", matchingVOGPDF(t))
	if resp.StatusCode != http.StatusOK {
		_ = resp.Body.Close()
		t.Fatalf("upload after identifying = %d, want 200", resp.StatusCode)
	}
	if outcome := decodeJSON[vogUploadResponse](t, resp); outcome.Result != "valid" {
		t.Errorf("result = %+v, want valid", outcome)
	}
}
