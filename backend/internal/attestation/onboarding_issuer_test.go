package attestation

import (
	"testing"

	"github.com/privacybydesign/yivi-businesswallet/backend/internal/organization"
)

func onboardingMember() organization.OnboardingMember {
	return organization.OnboardingMember{
		OrganizationName: "Acme",
		GivenNames:       "Alice",
		LastName:         "Jones",
		Email:            "alice@acme.test",
		Phone:            "+31600000000",
		Role:             "admin",
		JobTitle:         "Engineer",
		DepartmentName:   "Platform",
	}
}

func TestOnboardingSourceValuesMapsMemberFields(t *testing.T) {
	values := onboardingSourceValues(onboardingMember())

	cases := map[string]string{
		SourceMemberGivenNames: "Alice",
		SourceMemberLastName:   "Jones",
		SourceMemberFullName:   "Alice Jones",
		SourceMemberEmail:      "alice@acme.test",
		SourceMemberPhone:      "+31600000000",
		SourceMemberRole:       "admin",
		SourceMemberJobTitle:   "Engineer",
		SourceMemberDepartment: "Platform",
	}
	for token, want := range cases {
		if got := values[token]; got != want {
			t.Errorf("token %q = %q, want %q", token, got, want)
		}
	}

	// preferredName has no onboarding value; org.* tokens never apply to a member.
	if got := values[SourceMemberPreferredName]; got != "" {
		t.Errorf("preferredName resolved to %q, want empty", got)
	}
	if _, ok := values[SourceOrgName]; ok {
		t.Error("org.* token present in member source values")
	}
}

func TestOnboardingFullNameTrimsMissingParts(t *testing.T) {
	m := organization.OnboardingMember{GivenNames: "Bob"}
	if got := onboardingSourceValues(m)[SourceMemberFullName]; got != "Bob" {
		t.Errorf("fullName = %q, want %q", got, "Bob")
	}
}

func TestResolveOnboardingAttributesBindingOverridesDefault(t *testing.T) {
	template := Template{
		DefaultAttributes: map[string]string{"level": "standard", "email": "placeholder"},
		AttributeSources:  map[string]string{"email": SourceMemberEmail, "org": SourceOrgName},
	}
	sources := onboardingSourceValues(onboardingMember())

	attrs := resolveOnboardingAttributes(template, sources)

	// A binding that resolves to a value wins over the default.
	if attrs["email"] != "alice@acme.test" {
		t.Errorf("email = %q, want the resolved member e-mail", attrs["email"])
	}
	// An unbound default is preserved.
	if attrs["level"] != "standard" {
		t.Errorf("level = %q, want the static default", attrs["level"])
	}
	// org.name resolves to "" for a member, so the "org" attribute is absent
	// rather than blanked (it has no default to fall back to).
	if _, ok := attrs["org"]; ok {
		t.Errorf("org attribute present (%q); an unresolved binding with no default should be omitted", attrs["org"])
	}
}

func TestResolveOnboardingAttributesUnresolvedBindingKeepsDefault(t *testing.T) {
	template := Template{
		DefaultAttributes: map[string]string{"dept": "Unknown"},
		AttributeSources:  map[string]string{"dept": SourceMemberDepartment},
	}
	// Member with no department: the binding resolves to "", so the default stays.
	sources := onboardingSourceValues(organization.OnboardingMember{GivenNames: "Bob"})

	if got := resolveOnboardingAttributes(template, sources)["dept"]; got != "Unknown" {
		t.Errorf("dept = %q, want the static default when the binding is unresolved", got)
	}
}
