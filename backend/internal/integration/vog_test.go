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

func matchingVOGPDF(t *testing.T) []byte {
	return vogtest.BuildTestPDF(t, []string{
		"kenmerk: ABC12345XYZ",
		"Datum: 01-02-2024",
		"Geslachtsnaam: Jansen",
		"Tussenvoegsels: van der",
		"Voornamen: Jan Willem",
		"Geboortedatum: 03-04-1990",
		"profiel: 11 43",
	})
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
	setDateOfBirth(t, e, orgID, me.ID, "Jan Willem", "van der Jansen", time.Date(1990, 4, 3, 0, 0, 0, 0, time.UTC))

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
	setDateOfBirth(t, e, orgID, member, "Jan Willem", "van der Jansen", time.Date(1990, 4, 3, 0, 0, 0, 0, time.UTC))

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
