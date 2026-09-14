package seed

import (
	"strings"
	"testing"

	"github.com/privacybydesign/yivi-businesswallet/backend/internal/attestation"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/identity"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/registryprovider"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/user"
)

// TestPartnerOrgFixturesAreWellFormed guards the staging partner seed fixtures:
// every org must carry a bare address local-part (no "@", else qerdsAddress
// double-appends the domain), slugs and KVK numbers must be unique so no two
// partners collide on ON CONFLICT (slug), every org must have at least one admin,
// and every admin e-mail must parse (EnsurePartnerOrganizations parses them and
// would fail the whole seed otherwise).
func TestPartnerOrgFixturesAreWellFormed(t *testing.T) {
	seenSlug := map[string]bool{}
	seenKVK := map[string]bool{}
	for _, p := range partnerOrganizations {
		o := p.org
		if o.slug == "" {
			t.Errorf("partner org %q has an empty slug", o.name)
		}
		if o.addressLocal == "" || strings.Contains(o.addressLocal, "@") {
			t.Errorf("partner org %q addressLocal = %q, want a bare local-part with no domain", o.slug, o.addressLocal)
		}
		if seenSlug[o.slug] {
			t.Errorf("partner org slug %q is duplicated", o.slug)
		}
		seenSlug[o.slug] = true
		if seenKVK[o.kvkNumber] {
			t.Errorf("partner org KVK number %q is duplicated", o.kvkNumber)
		}
		seenKVK[o.kvkNumber] = true
		if o.repKind != "" {
			t.Errorf("partner org %q should carry no representative (we hold no register identity), got repKind %q", o.slug, o.repKind)
		}
		if len(p.team) == 0 {
			t.Errorf("partner org %q has no team members to make admin", o.slug)
		}
		for _, m := range p.team {
			if _, err := user.ParseEmail(m.email); err != nil {
				t.Errorf("partner org %q member e-mail %q does not parse: %v", o.slug, m.email, err)
			}
		}
	}
}

// TestQerdsAddressUsesConfiguredDomain pins that a seeded org's QERDS address is
// assembled from the configured domain, so staging can seed real addresses
// (qerds.staging.yivi.app) instead of the hardcoded qerds.localhost (issue #104).
func TestQerdsAddressUsesConfiguredDomain(t *testing.T) {
	if got, want := qerdsAddress("yivi", "qerds.staging.yivi.app"), "yivi@qerds.staging.yivi.app"; got != want {
		t.Errorf("qerdsAddress(staging) = %q, want %q", got, want)
	}
	if got, want := qerdsAddress("yivi", "qerds.localhost"), "yivi@qerds.localhost"; got != want {
		t.Errorf("qerdsAddress(local) = %q, want %q", got, want)
	}
}

// TestDemoOrgAddressLocalPartsHaveNoDomain guards that the org fixtures store only
// the local-part: a stray "@domain" here would be double-appended by qerdsAddress
// and would also pin the domain back to a literal, defeating the fix.
func TestDemoOrgAddressLocalPartsHaveNoDomain(t *testing.T) {
	orgs := append([]demoOrganization{kvkRegisterOrg}, demoOrganizations...)
	for _, c := range communityOrganizations {
		orgs = append(orgs, c.org)
	}
	for _, o := range orgs {
		if o.addressLocal == "" {
			t.Errorf("demo org %q has an empty addressLocal", o.slug)
		}
		if strings.Contains(o.addressLocal, "@") {
			t.Errorf("demo org %q addressLocal = %q, want a bare local-part with no domain", o.slug, o.addressLocal)
		}
	}
}

// TestDemoOrgsMatchRegister guards the reconciliation: every seeded demo company's
// KVK identity and primary representative must exist in the register's fake API
// (registryprovider.DemoRegistrations), so a seeded user can open a wallet and
// match a real representative and the two never drift.
func TestDemoOrgsMatchRegister(t *testing.T) {
	data := registryprovider.DefaultDataset()

	for _, o := range demoOrganizations {
		reg, ok := data[o.kvkNumber]
		if !ok {
			t.Errorf("demo org %q (kvk %s) is not in the register dataset", o.slug, o.kvkNumber)
			continue
		}
		if reg.LegalName != o.name || reg.EUID != o.euid {
			t.Errorf("demo org %q identity = %q/%q, register has %q/%q", o.slug, o.name, o.euid, reg.LegalName, reg.EUID)
		}

		want := identity.Name{GivenNames: o.repGiven, LastName: o.repFamily}
		matched := false
		for _, rep := range reg.Representatives {
			stored := identity.Name{GivenNames: rep.GivenNames, LastName: rep.FamilyName}
			if identity.Reconcile(want, &stored) != identity.Review &&
				rep.DateOfBirth == o.repDOB && rep.Kind == o.repKind && rep.Authority == o.repAuth {
				matched = true
				break
			}
		}
		if !matched {
			t.Errorf("demo org %q primary representative %s %s (%s/%s, dob %s) has no matching register entry",
				o.slug, o.repGiven, o.repFamily, o.repKind, o.repAuth, o.repDOB)
		}
	}
}

// TestRegisterOnlyCompanyIsOpenable is the regression for the unreachable
// open-wallet happy path: at least one validatable KVK number must NOT be seeded
// as an organisation, otherwise every validated requester is bounced with
// ErrAlreadyRegistered when OpenWallet tries to create the org. It also pins the
// register-only demo company (OpenableKVKNumber) as that openable entry.
func TestRegisterOnlyCompanyIsOpenable(t *testing.T) {
	seededKVK := map[string]bool{registryprovider.RegisterKVKNumber: true}
	for _, o := range demoOrganizations {
		seededKVK[o.kvkNumber] = true
	}
	for _, c := range communityOrganizations {
		seededKVK[c.org.kvkNumber] = true
	}

	if seededKVK[registryprovider.OpenableKVKNumber] {
		t.Fatalf("register-only company %s must not be seeded as an org", registryprovider.OpenableKVKNumber)
	}
	if _, ok := registryprovider.DefaultDataset()[registryprovider.OpenableKVKNumber]; !ok {
		t.Fatalf("register-only company %s must be a consultable register entry", registryprovider.OpenableKVKNumber)
	}

	openable := 0
	for kvk := range registryprovider.DefaultDataset() {
		if !seededKVK[kvk] {
			openable++
		}
	}
	if openable == 0 {
		t.Fatal("no validatable KVK number is openable: every register entry is already seeded as an org, so OpenWallet's positive path is unreachable")
	}
}

// TestCommunityOrgFixturesAreWellFormed guards the dev-demo community
// organisations (the church and the football club): they must not collide with
// any other seeded org on slug or KVK number (ON CONFLICT (slug) would silently
// return the other org and the departments would land on it), must stay out of
// the register dataset (they are provisioned directly, not opened through the
// register flow), carry no representative and no members, and every department
// name must be unique within its org (UNIQUE (organization_id, name)).
func TestCommunityOrgFixturesAreWellFormed(t *testing.T) {
	seenSlug := map[string]bool{kvkRegisterOrg.slug: true}
	seenKVK := map[string]bool{kvkRegisterOrg.kvkNumber: true}
	for _, o := range demoOrganizations {
		seenSlug[o.slug] = true
		seenKVK[o.kvkNumber] = true
	}
	register := registryprovider.DefaultDataset()

	for _, c := range communityOrganizations {
		o := c.org
		if o.slug == "" || o.name == "" {
			t.Errorf("community org %+v has an empty slug or name", o)
		}
		if seenSlug[o.slug] {
			t.Errorf("community org slug %q collides with another seeded org", o.slug)
		}
		seenSlug[o.slug] = true
		if seenKVK[o.kvkNumber] {
			t.Errorf("community org %q KVK number %q collides with another seeded org", o.slug, o.kvkNumber)
		}
		seenKVK[o.kvkNumber] = true
		if _, ok := register[o.kvkNumber]; ok {
			t.Errorf("community org %q KVK number %q must not be a register entry", o.slug, o.kvkNumber)
		}
		if o.repKind != "" {
			t.Errorf("community org %q should carry no representative, got repKind %q", o.slug, o.repKind)
		}
		if len(c.departments) == 0 {
			t.Errorf("community org %q has no departments; its structure is the point of seeding it", o.slug)
		}
		seenDept := map[string]bool{}
		for _, d := range c.departments {
			if d == "" {
				t.Errorf("community org %q has an empty department name", o.slug)
			}
			if seenDept[d] {
				t.Errorf("community org %q department %q is duplicated", o.slug, d)
			}
			seenDept[d] = true
		}
	}

	for _, m := range demoMemberships {
		for _, c := range communityOrganizations {
			if m.slug == c.org.slug {
				t.Errorf("community org %q must have no seeded members, got %q", c.org.slug, m.email)
			}
		}
	}
}

// TestExpandTeamsNumbersEveryTeam pins the team expansion: one department per
// team in every group, numbered from 1, followed by the irregular extras in
// order — so a change to sdvbTeamGroups shows up as a count, not a silent gap.
func TestExpandTeamsNumbersEveryTeam(t *testing.T) {
	groups := []sdvbTeamGroup{
		{section: "Jeugd", code: "JO19", count: 2},
		{section: "Senioren", code: "Mannen", count: 1},
	}
	got := expandTeams(groups, []string{"Jeugd Mini's"})
	want := []string{"Jeugd JO19-1", "Jeugd JO19-2", "Senioren Mannen-1", "Jeugd Mini's"}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Fatalf("expandTeams = %q, want %q", got, want)
	}

	total := len(sdvbExtraTeams)
	for _, g := range sdvbTeamGroups {
		total += g.count
	}
	if n := len(expandTeams(sdvbTeamGroups, sdvbExtraTeams)); n != total {
		t.Fatalf("SDVB expands to %d departments, want %d", n, total)
	}
}

// TestKVKRegisterOrgNotConsultable guards that the KVK register participant is not
// itself a consultable company in the dataset.
func TestKVKRegisterOrgNotConsultable(t *testing.T) {
	if _, ok := registryprovider.DefaultDataset()[registryprovider.RegisterKVKNumber]; ok {
		t.Fatalf("kvk register number %s must not be a consultable company", registryprovider.RegisterKVKNumber)
	}
	if kvkRegisterOrg.repKind != "" {
		t.Fatalf("kvk register org should have no representative, got kind %q", kvkRegisterOrg.repKind)
	}
}

// TestNijmegenApvSchemaIsWellFormed guards the fixture seedNijmegenAttestation
// writes (issue #245): it is an organization-subject schema, every attribute
// uses a type the store/editor actually support, attribute keys are unique, and
// every display entry (schema-level and per-attribute) carries both languages
// the rest of the seeded catalogue uses.
func TestNijmegenApvSchemaIsWellFormed(t *testing.T) {
	if nijmegenApvSchema.VCT == "" || nijmegenApvSchema.CredentialConfigID == "" {
		t.Fatal("nijmegenApvSchema must have a non-empty VCT and CredentialConfigID")
	}
	if nijmegenApvSchema.SubjectType != attestation.SubjectOrganization {
		t.Fatalf("nijmegenApvSchema subject type = %q, want %q", nijmegenApvSchema.SubjectType, attestation.SubjectOrganization)
	}
	if nijmegenApvTemplateName == "" {
		t.Fatal("nijmegenApvTemplateName must not be empty")
	}
	assertLangs(t, "schema display", toLangs(nijmegenApvSchema.Display))

	supported := map[string]bool{}
	for _, s := range attestation.SupportedAttributeTypes {
		supported[s] = true
	}

	seenKeys := map[string]bool{}
	for _, a := range nijmegenApvSchema.Attributes {
		if a.Key == "" {
			t.Fatal("nijmegenApvSchema has an attribute with an empty key")
		}
		if seenKeys[a.Key] {
			t.Errorf("nijmegenApvSchema attribute key %q is duplicated", a.Key)
		}
		seenKeys[a.Key] = true
		if !supported[a.Type] {
			t.Errorf("nijmegenApvSchema attribute %q has unsupported type %q", a.Key, a.Type)
		}
		assertLangs(t, "attribute "+a.Key+" display", toLangsLabel(a.Display))
	}
}

func toLangs(names []attestation.LocalizedName) []string {
	langs := make([]string, len(names))
	for i, n := range names {
		langs[i] = n.Lang
	}
	return langs
}

func toLangsLabel(labels []attestation.LocalizedLabel) []string {
	langs := make([]string, len(labels))
	for i, l := range labels {
		langs[i] = l.Lang
	}
	return langs
}

func assertLangs(t *testing.T, what string, langs []string) {
	t.Helper()
	want := map[string]bool{"en": true, "nl": true}
	got := map[string]bool{}
	for _, l := range langs {
		got[l] = true
	}
	for lang := range want {
		if !got[lang] {
			t.Errorf("%s is missing language %q", what, lang)
		}
	}
}
