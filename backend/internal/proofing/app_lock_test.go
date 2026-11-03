package proofing

import (
	"errors"
	"slices"
	"testing"

	"github.com/privacybydesign/yivi-businesswallet/backend/internal/openid4vpverifier"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/proofingprovider"
)

// A flow locked to one app offers only that app: unasked, it runs in it, and
// the other is refused.
func TestSessionMethodFollowsTheAppLock(t *testing.T) {
	yiviOnly := photoFlow
	yiviOnly.Apps = []string{appYivi}
	idemOnly := photoFlow
	idemOnly.Apps = []string{appIdem}
	for _, tc := range []struct {
		name  string
		flow  proofingprovider.Flow
		asked proofingprovider.Method
		want  proofingprovider.Method
	}{
		{"yivi only, unasked", yiviOnly, "", proofingprovider.MethodYivi},
		{"yivi only, yivi", yiviOnly, proofingprovider.MethodYivi, proofingprovider.MethodYivi},
		{"yivi only, idem", yiviOnly, proofingprovider.MethodIdem, ""},
		{"idem only, unasked", idemOnly, "", proofingprovider.MethodIdem},
		{"idem only, yivi", idemOnly, proofingprovider.MethodYivi, ""},
	} {
		got, err := sessionMethod(tc.flow, tc.asked)
		if got != tc.want || (tc.want == "") != errors.Is(err, ErrInvalidInput) {
			t.Errorf("%s: sessionMethod = %q, %v; want %q", tc.name, got, err, tc.want)
		}
	}
	if idemAppAvailable(yiviOnly) || yiviAppAvailable(idemOnly) {
		t.Error("an app the flow locks out is offered")
	}
}

// The Yivi app offers what the flow accepts: its document kinds and the
// personal data, every document for a code the credentials cannot be told by,
// and the default documents for a flow accepting any.
func TestYiviSources(t *testing.T) {
	for _, tc := range []struct {
		accepted []string
		want     []string
	}{
		{nil, nil},
		{[]string{"identity_card"}, []string{openid4vpverifier.SourceIdentityCard}},
		{[]string{"passport", "PERSONAL_DATA"}, []string{openid4vpverifier.SourcePassport, openid4vpverifier.SourcePersonalData}},
		{[]string{"P", "personal_data"}, []string{
			openid4vpverifier.SourcePersonalData, openid4vpverifier.SourcePassport,
			openid4vpverifier.SourceIdentityCard, openid4vpverifier.SourceResidencePermit,
		}},
	} {
		f := photoFlow
		f.AcceptedDocumentTypes = tc.accepted
		if got := yiviSources(f); !slices.Equal(got, tc.want) {
			t.Errorf("yiviSources(%v) = %v, want %v", tc.accepted, got, tc.want)
		}
	}
}

// The registered personal data reaches the engine under the document
// credentials' claim names, the prefix in the surname; a disclosed document
// goes as it is.
func TestDisclosedIdentityMapsPersonalData(t *testing.T) {
	p := openid4vpverifier.Presentation{ByCredential: map[string]map[string]string{"personaldata": {
		openid4vpverifier.ClaimPersonalFirstNames: "Anna Maria", openid4vpverifier.ClaimPersonalPrefix: "de",
		openid4vpverifier.ClaimPersonalFamilyName: "Vries", openid4vpverifier.ClaimPersonalDateOfBirth: "10-03-1990",
	}}}
	doc, err := disclosedIdentity(p)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]string{
		openid4vpverifier.ClaimGivenNames: "Anna Maria", openid4vpverifier.ClaimFamilyName: "de Vries",
		openid4vpverifier.ClaimDateOfBirth: "10-03-1990",
	}
	for k, v := range want {
		if doc.Claims[k] != v {
			t.Errorf("claim %s = %q, want %q", k, doc.Claims[k], v)
		}
	}
	if len(doc.Claims) != len(want) || doc.Credential != "pbdf.gemeente.personalData" {
		t.Errorf("identity = %+v, want only the mapped claims of pbdf.gemeente.personalData", doc)
	}
	if _, err := disclosedIdentity(openid4vpverifier.Presentation{}); err == nil {
		t.Error("a presentation without an identity was accepted")
	}
}
