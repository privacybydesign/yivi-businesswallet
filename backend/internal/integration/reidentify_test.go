//go:build integration

package integration

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/privacybydesign/yivi-businesswallet/backend/internal/organization"
)

type identitySettingsBody struct {
	Configured                  bool    `json:"configured"`
	EmployeeIntervalMonths      *int    `json:"employeeIntervalMonths"`
	ExternalIntervalMonths      *int    `json:"externalIntervalMonths"`
	ReminderDaysBefore          []int32 `json:"reminderDaysBefore"`
	OverdueReminderIntervalDays int     `json:"overdueReminderIntervalDays"`
	OverdueReminderMaxCount     int     `json:"overdueReminderMaxCount"`
	CredentialMaxAgeDays        *int    `json:"credentialMaxAgeDays"`
	OverdueConsequence          string  `json:"overdueConsequence"`
}

// namedMember is activeMember with a real legal name, which the
// re-identification disclosure is matched against.
func (e *testEnv) namedMember(orgID uuid.UUID, email, givenNames, lastName string, verifiedAt time.Time) uuid.UUID {
	e.t.Helper()
	userID := e.createUserNamed(email, givenNames, lastName)
	if _, err := e.pool.Exec(context.Background(),
		`INSERT INTO memberships (user_id, organization_id, role, member_type, identity_verified_at)
		 VALUES ($1, $2, $3, 'employee', $4)`,
		userID, orgID, organization.RoleMember, verifiedAt,
	); err != nil {
		e.t.Fatalf("named member %q: %v", email, err)
	}
	return userID
}

func (e *testEnv) putIdentitySettings(slug string, body identitySettingsBody) identitySettingsBody {
	e.t.Helper()
	raw, err := json.Marshal(body)
	if err != nil {
		e.t.Fatalf("marshal settings: %v", err)
	}
	resp := e.do(http.MethodPut, "/api/v1/orgs/"+slug+"/identity-settings", strings.NewReader(string(raw)))
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		e.t.Fatalf("PUT identity-settings = %d, want 200", resp.StatusCode)
	}
	var out identitySettingsBody
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		e.t.Fatalf("decode settings: %v", err)
	}
	return out
}

// mintOwnReidentifyToken is the in-app banner's entry point; it returns the raw
// token out of the link, which is the only way a test can get one (the admin
// request and the reminder sweep only ever mail it).
func (e *testEnv) mintOwnReidentifyToken(slug string) string {
	e.t.Helper()
	resp := e.do(http.MethodPost, "/api/v1/orgs/"+slug+"/me/reidentify-token", nil)
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		e.t.Fatalf("mint reidentify token = %d, want 200", resp.StatusCode)
	}
	var body struct {
		ReidentifyURL string `json:"reidentifyUrl"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		e.t.Fatalf("decode reidentify token: %v", err)
	}
	_, token, found := strings.Cut(body.ReidentifyURL, "/reidentify/")
	if !found || token == "" {
		e.t.Fatalf("reidentifyUrl = %q, want a /reidentify/<token> link", body.ReidentifyURL)
	}
	return token
}

func (e *testEnv) completeReidentify(token string) *http.Response {
	e.t.Helper()
	return e.do(http.MethodPost, "/api/v1/reidentify/"+token+"/complete",
		strings.NewReader(`{"disclosureToken":"`+disclosureToken+`"}`))
}

// The whole loop an admin and a member walk through: a policy is set, the member
// falls due, the admin asks them to re-identify, and the member proves their
// identity again through the same wallet disclosure invite-accept uses.
func TestAdminRequestsIdentificationAndMemberReidentifies(t *testing.T) {
	env := setup(t)
	orgID := env.adminOf("acme", "Acme", "boss@example.test")

	// Verified 13 months ago; a 12-month policy therefore makes them overdue.
	env.namedMember(orgID, "alice@example.test", "Alice", "Anderson", time.Now().AddDate(0, -13, 0))
	months := 12
	settings := env.putIdentitySettings("acme", identitySettingsBody{
		EmployeeIntervalMonths:      &months,
		ReminderDaysBefore:          []int32{30, 14, 7},
		OverdueReminderIntervalDays: 7,
		OverdueReminderMaxCount:     4,
		OverdueConsequence:          organization.OverdueConsequenceFlag,
	})
	if !settings.Configured || settings.EmployeeIntervalMonths == nil || *settings.EmployeeIntervalMonths != months {
		t.Fatalf("PUT identity-settings returned %+v", settings)
	}

	// Saving the policy recomputed the due date, so the member reads as overdue.
	alice := byEmail(env.listMembers("acme", organization.StatusActive), "alice@example.test")
	if alice == nil {
		t.Fatal("alice is not in the member list")
	}
	if alice.IdentityStatus != organization.IdentityStatusOverdue {
		t.Errorf("identityStatus = %q, want %q", alice.IdentityStatus, organization.IdentityStatusOverdue)
	}
	if alice.MemberType != organization.MemberTypeEmployee {
		t.Errorf("memberType = %q, want employee", alice.MemberType)
	}

	// The admin asks for identification now.
	resp := env.postJSON("/api/v1/orgs/acme/members/"+alice.UserID.String()+"/request-identification",
		map[string]string{"reason": "annual review"})
	if resp.StatusCode != http.StatusOK {
		_ = resp.Body.Close()
		t.Fatalf("request-identification = %d, want 200", resp.StatusCode)
	}
	requested := decodeJSON[struct {
		Requested int `json:"requested"`
	}](t, resp)
	if requested.Requested != 1 {
		t.Errorf("requested = %d, want 1", requested.Requested)
	}

	if got := byEmail(env.listMembers("acme", organization.StatusActive), "alice@example.test"); got.IdentityStatus != organization.IdentityStatusRequested {
		t.Errorf("identityStatus after the request = %q, want %q", got.IdentityStatus, organization.IdentityStatusRequested)
	} else if got.IdentityRequestedAt == nil {
		t.Error("identityRequestedAt is nil after an admin request")
	}

	// The member takes over: they mint their own link from the in-app banner and
	// re-identify with a matching passport disclosure.
	env.loginAs("alice@example.test")
	token := env.mintOwnReidentifyToken("acme")

	preview := env.do(http.MethodGet, "/api/v1/reidentify/"+token, nil)
	previewBody := decodeJSON[struct {
		OrganizationName string `json:"organizationName"`
		OrganizationSlug string `json:"organizationSlug"`
		Email            string `json:"email"`
	}](t, preview)
	if previewBody.OrganizationSlug != "acme" || previewBody.Email != "alice@example.test" {
		t.Errorf("preview = %+v, want acme / alice", previewBody)
	}

	session := env.do(http.MethodPost, "/api/v1/reidentify/"+token+"/session", nil)
	sessionBody := decodeJSON[struct {
		ID         string `json:"id"`
		WalletLink string `json:"walletLink"`
	}](t, session)
	if sessionBody.ID == "" || sessionBody.WalletLink == "" {
		t.Errorf("session = %+v, want an id and a wallet link", sessionBody)
	}

	env.discloses("alice@example.test", "Alice", "Anderson")
	env.fake.dateOfBirth = "1990-01-02"
	done := env.completeReidentify(token)
	if done.StatusCode != http.StatusOK {
		_ = done.Body.Close()
		t.Fatalf("complete reidentify = %d, want 200", done.StatusCode)
	}
	outcome := decodeJSON[struct {
		OrganizationName string `json:"organizationName"`
		OrganizationSlug string `json:"organizationSlug"`
	}](t, done)
	if outcome.OrganizationSlug != "acme" {
		t.Errorf("outcome = %+v, want acme", outcome)
	}

	// Back as the admin: the member is verified again, the request is cleared and
	// a fresh due date has been computed.
	env.loginAs("boss@example.test")
	after := byEmail(env.listMembers("acme", organization.StatusActive), "alice@example.test")
	if after.IdentityStatus != organization.IdentityStatusVerified {
		t.Errorf("identityStatus after re-identification = %q, want %q", after.IdentityStatus, organization.IdentityStatusVerified)
	}
	if after.IdentityRequestedAt != nil {
		t.Errorf("identityRequestedAt = %v, want it cleared", after.IdentityRequestedAt)
	}
	if after.IdentityDueAt == nil || !after.IdentityDueAt.After(time.Now()) {
		t.Errorf("identityDueAt = %v, want a fresh future date", after.IdentityDueAt)
	}
	if after.IdentityVerifiedAt == nil || after.IdentityVerifiedAt.Before(time.Now().Add(-time.Hour)) {
		t.Errorf("identityVerifiedAt = %v, want it refreshed to now", after.IdentityVerifiedAt)
	}

	// The audit trail carries the re-identification.
	if n := env.auditCount(orgID, "membership.identity_requested"); n != 1 {
		t.Errorf("membership.identity_requested events = %d, want 1", n)
	}
	if n := env.auditCount(orgID, "membership.identity_reverified"); n != 1 {
		t.Errorf("membership.identity_reverified events = %d, want 1", n)
	}
}

// Credential freshness (#240 §5): with a limit set, a credential the wallet
// obtained too long ago is refused with an actionable code, and the refusal is
// audited without changing the membership.
func TestReidentifyRejectsAStaleCredential(t *testing.T) {
	env := setup(t)
	orgID := env.adminOf("acme", "Acme", "boss@example.test")
	env.namedMember(orgID, "alice@example.test", "Alice", "Anderson", time.Now().AddDate(0, -13, 0))

	months, maxAge := 12, 30
	env.putIdentitySettings("acme", identitySettingsBody{
		EmployeeIntervalMonths:      &months,
		CredentialMaxAgeDays:        &maxAge,
		OverdueReminderIntervalDays: 7,
		OverdueReminderMaxCount:     4,
		OverdueConsequence:          organization.OverdueConsequenceFlag,
	})

	env.loginAs("alice@example.test")
	token := env.mintOwnReidentifyToken("acme")
	env.discloses("alice@example.test", "Alice", "Anderson")
	env.fake.identityIssuedAt = time.Now().AddDate(0, 0, -200)

	resp := env.completeReidentify(token)
	if resp.StatusCode != http.StatusConflict {
		_ = resp.Body.Close()
		t.Fatalf("complete with a stale credential = %d, want 409", resp.StatusCode)
	}
	failure := decodeJSON[struct {
		Code string `json:"code"`
	}](t, resp)
	if failure.Code != "credential_too_old" {
		t.Errorf("error code = %q, want credential_too_old", failure.Code)
	}
	if n := env.auditCount(orgID, "membership.identity_reverify_rejected"); n != 1 {
		t.Errorf("membership.identity_reverify_rejected events = %d, want 1", n)
	}

	env.loginAs("boss@example.test")
	if got := byEmail(env.listMembers("acme", organization.StatusActive), "alice@example.test"); got.IdentityStatus != organization.IdentityStatusOverdue {
		t.Errorf("identityStatus = %q, want the member still %q", got.IdentityStatus, organization.IdentityStatusOverdue)
	}

	// A credential the wallet obtained recently passes the same check.
	env.loginAs("alice@example.test")
	env.fake.identityIssuedAt = time.Now().AddDate(0, 0, -3)
	env.discloses("alice@example.test", "Alice", "Anderson")
	if resp := env.completeReidentify(env.mintOwnReidentifyToken("acme")); resp.StatusCode != http.StatusOK {
		_ = resp.Body.Close()
		t.Fatalf("complete with a fresh credential = %d, want 200", resp.StatusCode)
	}
}

// Somebody else's wallet must not satisfy a link that was sent to a member, even
// though the link itself is a bearer token.
func TestReidentifyRefusesAnotherPersonsDisclosure(t *testing.T) {
	env := setup(t)
	orgID := env.adminOf("acme", "Acme", "boss@example.test")
	env.namedMember(orgID, "alice@example.test", "Alice", "Anderson", time.Now().AddDate(0, -1, 0))

	env.loginAs("alice@example.test")
	token := env.mintOwnReidentifyToken("acme")

	env.discloses("mallory@example.test", "Mallory", "Malicious")
	resp := env.completeReidentify(token)
	if resp.StatusCode != http.StatusConflict {
		_ = resp.Body.Close()
		t.Fatalf("complete as another person = %d, want 409", resp.StatusCode)
	}
	if code := decodeJSON[struct {
		Code string `json:"code"`
	}](t, resp).Code; code != "email_mismatch" {
		t.Errorf("error code = %q, want email_mismatch", code)
	}
}

func TestReidentifyUnknownTokenIs404(t *testing.T) {
	env := setup(t)
	env.adminOf("acme", "Acme", "boss@example.test")

	resp := env.do(http.MethodGet, "/api/v1/reidentify/not-a-token", nil)
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("GET an unknown re-identification link = %d, want 404", resp.StatusCode)
	}
}

// Bulk request is the members-list action: one call, several members, one mail
// and one audit event each.
func TestBulkRequestIdentification(t *testing.T) {
	env := setup(t)
	orgID := env.adminOf("acme", "Acme", "boss@example.test")
	alice := env.namedMember(orgID, "alice@example.test", "Alice", "Anderson", time.Now())
	bob := env.namedMember(orgID, "bob@example.test", "Bob", "Brown", time.Now())

	resp := env.postJSON("/api/v1/orgs/acme/members/request-identification", map[string]any{
		"userIds": []string{alice.String(), bob.String()},
		"reason":  "spot check",
	})
	if resp.StatusCode != http.StatusOK {
		_ = resp.Body.Close()
		t.Fatalf("bulk request-identification = %d, want 200", resp.StatusCode)
	}
	if got := decodeJSON[struct {
		Requested int `json:"requested"`
	}](t, resp).Requested; got != 2 {
		t.Errorf("requested = %d, want 2", got)
	}

	for _, email := range []string{"alice@example.test", "bob@example.test"} {
		entry := byEmail(env.listMembers("acme", organization.StatusActive), email)
		if entry.IdentityStatus != organization.IdentityStatusRequested {
			t.Errorf("%s identityStatus = %q, want %q", email, entry.IdentityStatus, organization.IdentityStatusRequested)
		}
	}
	if n := env.auditCount(orgID, "membership.identity_requested"); n != 2 {
		t.Errorf("membership.identity_requested events = %d, want 2", n)
	}

	// An empty selection is a client mistake, not an empty success.
	empty := env.postJSON("/api/v1/orgs/acme/members/request-identification", map[string]any{"userIds": []string{}})
	defer func() { _ = empty.Body.Close() }()
	if empty.StatusCode != http.StatusBadRequest {
		t.Errorf("bulk request with no ids = %d, want 400", empty.StatusCode)
	}
}

// Member type drives which interval applies, so an admin has to be able to
// change it — and the due date has to follow.
func TestUpdateMemberTypeOverHTTP(t *testing.T) {
	env := setup(t)
	orgID := env.adminOf("acme", "Acme", "boss@example.test")
	alice := env.namedMember(orgID, "alice@example.test", "Alice", "Anderson", time.Now().AddDate(0, -8, 0))

	employeeMonths, externalMonths := 12, 3
	env.putIdentitySettings("acme", identitySettingsBody{
		EmployeeIntervalMonths:      &employeeMonths,
		ExternalIntervalMonths:      &externalMonths,
		OverdueReminderIntervalDays: 7,
		OverdueReminderMaxCount:     4,
		OverdueConsequence:          organization.OverdueConsequenceFlag,
	})

	if got := byEmail(env.listMembers("acme", organization.StatusActive), "alice@example.test"); got.IdentityStatus == organization.IdentityStatusOverdue {
		t.Fatalf("precondition: an employee verified 8 months ago should not be overdue under a 12-month policy")
	}

	resp := env.do(http.MethodPatch, "/api/v1/orgs/acme/members/"+alice.String()+"/type",
		strings.NewReader(`{"memberType":"external","externalOrganisation":"Contractors BV"}`))
	if resp.StatusCode != http.StatusOK {
		_ = resp.Body.Close()
		t.Fatalf("PATCH member type = %d, want 200", resp.StatusCode)
	}
	member := decodeJSON[struct {
		MemberType           string  `json:"memberType"`
		ExternalOrganisation *string `json:"externalOrganisation"`
		IdentityStatus       string  `json:"identityStatus"`
	}](t, resp)
	if member.MemberType != organization.MemberTypeExternal {
		t.Errorf("memberType = %q, want external", member.MemberType)
	}
	if member.ExternalOrganisation == nil || *member.ExternalOrganisation != "Contractors BV" {
		t.Errorf("externalOrganisation = %v, want Contractors BV", member.ExternalOrganisation)
	}
	// Under the 3-month external interval the same verification is now stale.
	if member.IdentityStatus != organization.IdentityStatusOverdue {
		t.Errorf("identityStatus = %q, want %q under the external interval", member.IdentityStatus, organization.IdentityStatusOverdue)
	}

	bad := env.do(http.MethodPatch, "/api/v1/orgs/acme/members/"+alice.String()+"/type",
		strings.NewReader(`{"memberType":"contractor"}`))
	defer func() { _ = bad.Body.Close() }()
	if bad.StatusCode != http.StatusBadRequest {
		t.Errorf("PATCH an unknown member type = %d, want 400", bad.StatusCode)
	}
}

// Inviting carries the intended member type onto the membership at accept, the
// way role, job title and department already do.
func TestInviteCarriesMemberTypeThroughAccept(t *testing.T) {
	env := setup(t)
	env.adminOf("acme", "Acme", "boss@example.test")

	resp := env.invite("acme", `{"email":"ext@example.test","givenNames":"Ext","lastName":"Ernal","memberType":"external","externalOrganisation":"Partner BV"}`)
	if resp.StatusCode != http.StatusCreated {
		_ = resp.Body.Close()
		t.Fatalf("invite = %d, want 201", resp.StatusCode)
	}
	_ = resp.Body.Close()

	invited := byEmail(env.listMembers("acme", organization.StatusInvited), "ext@example.test")
	if invited == nil || invited.MemberType != organization.MemberTypeExternal {
		t.Fatalf("invited entry = %+v, want an external", invited)
	}
	if invited.ExternalOrganisation == nil || *invited.ExternalOrganisation != "Partner BV" {
		t.Errorf("externalOrganisation = %v, want Partner BV", invited.ExternalOrganisation)
	}
}

func TestIdentitySettingsRejectAnUnknownConsequence(t *testing.T) {
	env := setup(t)
	env.adminOf("acme", "Acme", "boss@example.test")

	resp := env.do(http.MethodPut, "/api/v1/orgs/acme/identity-settings",
		strings.NewReader(`{"overdueReminderIntervalDays":7,"overdueReminderMaxCount":4,"overdueConsequence":"suspend"}`))
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("PUT identity-settings with an unknown consequence = %d, want 400", resp.StatusCode)
	}
}

// Identity settings are org-admin only: a plain member can neither read nor
// change their organisation's policy.
func TestIdentitySettingsAreAdminOnly(t *testing.T) {
	env := setup(t)
	orgID := env.adminOf("acme", "Acme", "boss@example.test")
	env.namedMember(orgID, "alice@example.test", "Alice", "Anderson", time.Now())
	env.loginAs("alice@example.test")

	get := env.do(http.MethodGet, "/api/v1/orgs/acme/identity-settings", nil)
	defer func() { _ = get.Body.Close() }()
	if get.StatusCode != http.StatusForbidden {
		t.Errorf("GET identity-settings as a member = %d, want 403", get.StatusCode)
	}

	put := env.do(http.MethodPut, "/api/v1/orgs/acme/identity-settings",
		strings.NewReader(`{"overdueReminderIntervalDays":7,"overdueReminderMaxCount":4,"overdueConsequence":"flag"}`))
	defer func() { _ = put.Body.Close() }()
	if put.StatusCode != http.StatusForbidden {
		t.Errorf("PUT identity-settings as a member = %d, want 403", put.StatusCode)
	}
}

// A plain member cannot read the member list, so their own re-identification
// state rides on the org detail they already fetch — that is what the in-app
// banner is driven from.
func TestOrgDetailCarriesTheCallersOwnIdentityState(t *testing.T) {
	env := setup(t)
	orgID := env.adminOf("acme", "Acme", "boss@example.test")
	env.namedMember(orgID, "alice@example.test", "Alice", "Anderson", time.Now().AddDate(0, -13, 0))

	months := 12
	env.putIdentitySettings("acme", identitySettingsBody{
		EmployeeIntervalMonths:      &months,
		OverdueReminderIntervalDays: 7,
		OverdueReminderMaxCount:     4,
		OverdueConsequence:          organization.OverdueConsequenceFlag,
	})

	env.loginAs("alice@example.test")
	detail := decodeJSON[struct {
		Slug     string `json:"slug"`
		Role     string `json:"role"`
		Identity *struct {
			Status string     `json:"status"`
			DueAt  *time.Time `json:"dueAt"`
		} `json:"identity"`
	}](t, env.do(http.MethodGet, "/api/v1/orgs/acme", nil))

	if detail.Role != organization.RoleMember {
		t.Errorf("role = %q, want member", detail.Role)
	}
	if detail.Identity == nil {
		t.Fatal("identity is absent for a member of the org")
	}
	if detail.Identity.Status != organization.IdentityStatusOverdue {
		t.Errorf("identity.status = %q, want %q", detail.Identity.Status, organization.IdentityStatusOverdue)
	}
	if detail.Identity.DueAt == nil {
		t.Error("identity.dueAt is nil for a member with a policy in force")
	}

	// Re-identifying clears it back to verified, which is what makes the banner
	// disappear without a reload of anything else.
	env.discloses("alice@example.test", "Alice", "Anderson")
	if resp := env.completeReidentify(env.mintOwnReidentifyToken("acme")); resp.StatusCode != http.StatusOK {
		_ = resp.Body.Close()
		t.Fatalf("complete reidentify = %d, want 200", resp.StatusCode)
	}
	after := decodeJSON[struct {
		Identity *struct {
			Status string `json:"status"`
		} `json:"identity"`
	}](t, env.do(http.MethodGet, "/api/v1/orgs/acme", nil))
	if after.Identity == nil || after.Identity.Status != organization.IdentityStatusVerified {
		t.Errorf("identity after re-identification = %+v, want verified", after.Identity)
	}
}
