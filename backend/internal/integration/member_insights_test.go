//go:build integration

package integration

import (
	"net/http"
	"testing"
	"time"

	"github.com/privacybydesign/yivi-businesswallet/backend/internal/organization"
)

// memberInsightsBody mirrors organization.MemberInsights.
type memberInsightsBody struct {
	Members   int            `json:"members"`
	Identity  map[string]int `json:"identity"`
	Screening map[string]int `json:"screening"`
}

// The dashboard's admin overview counts the whole org by derived status - the
// same derivation the member list shows per row - and is admin-only.
func TestMemberInsightsCountsTheWholeOrg(t *testing.T) {
	env := setup(t)
	orgID := env.adminOf("acme", "Acme", "boss@example.test")
	env.acceptVogCredential("acme", "11") // requiredFor both: every member needs a VOG

	// The admin identified a year ago (no policy: still simply verified); two
	// members never did; one of those also has a valid VOG on file.
	env.namedMember(orgID, "alice@example.test", "Alice", "Anderson", time.Now().AddDate(-1, 0, 0))
	bob := env.createUserNamed("bob@example.test", "Bob", "Brown")
	env.addMembership(bob, orgID, organization.RoleMember)
	carol := env.createUserNamed("carol@example.test", "Carol", "Clark")
	env.addMembership(carol, orgID, organization.RoleMember)
	setDateOfBirth(t, env, orgID, carol, "Carol", "Clark", time.Date(1990, 4, 3, 0, 0, 0, 0, time.UTC))
	if _, err := env.pool.Exec(t.Context(),
		`UPDATE memberships SET vog_last_result = 'valid', vog_valid_until = now() + interval '1 year', vog_covered_codes = '{11}'
		 WHERE organization_id = $1 AND user_id = $2`, orgID, carol); err != nil {
		t.Fatalf("seed carol's screening: %v", err)
	}

	resp := env.do(http.MethodGet, "/api/v1/orgs/acme/member-insights", nil)
	if resp.StatusCode != http.StatusOK {
		_ = resp.Body.Close()
		t.Fatalf("member-insights = %d, want 200", resp.StatusCode)
	}
	got := decodeJSON[memberInsightsBody](t, resp)

	if got.Members != 4 {
		t.Errorf("members = %d, want 4 (admin + alice + bob + carol)", got.Members)
	}
	// The admin's own membership was seeded without a verification date.
	if got.Identity[organization.IdentityStatusNever] != 3 || got.Identity[organization.IdentityStatusVerified] != 1 {
		t.Errorf("identity = %v, want 3 never / 1 verified", got.Identity)
	}
	if got.Screening[organization.ScreeningStatusValid] != 1 || got.Screening[organization.ScreeningStatusNone] != 3 {
		t.Errorf("screening = %v, want 1 valid / 3 none", got.Screening)
	}
	if _, present := got.Screening[organization.ScreeningStatusExpired]; !present {
		t.Error("an empty status is missing from the tally; every status must be reported")
	}

	// A plain member's page asks too, without knowing its role yet: it gets the
	// shape with nothing in it, never the org's numbers.
	env.loginAs("bob@example.test")
	asMember := env.do(http.MethodGet, "/api/v1/orgs/acme/member-insights", nil)
	if asMember.StatusCode != http.StatusOK {
		_ = asMember.Body.Close()
		t.Fatalf("member-insights as a plain member = %d, want 200", asMember.StatusCode)
	}
	empty := decodeJSON[memberInsightsBody](t, asMember)
	if empty.Members != 0 {
		t.Errorf("a plain member read %d members, want 0", empty.Members)
	}
	wantAllZero(t, "identity", empty.Identity)
	wantAllZero(t, "screening", empty.Screening)
}

// wantAllZero fails for each status of a tally that counts someone.
func wantAllZero(t *testing.T, what string, tally map[string]int) {
	t.Helper()
	for status, n := range tally {
		if n != 0 {
			t.Errorf("a plain member read %s %s = %d, want 0", what, status, n)
		}
	}
}
