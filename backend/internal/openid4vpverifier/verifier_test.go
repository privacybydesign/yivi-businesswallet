package openid4vpverifier

import (
	"encoding/base64"
	"encoding/json"
	"testing"
)

// disclosure encodes one SD-JWT VC disclosure array [salt, name, value].
func disclosure(t *testing.T, name string, value any) string {
	t.Helper()
	b, err := json.Marshal([]any{"salt", name, value})
	if err != nil {
		t.Fatalf("marshal disclosure: %v", err)
	}
	return base64.RawURLEncoding.EncodeToString(b)
}

// sdjwt builds a compact SD-JWT VC: issuer JWT ~ disclosures... ~ (KB trailer).
func sdjwt(t *testing.T, disclosures ...string) string {
	t.Helper()
	s := "issuer.jwt.sig"
	for _, d := range disclosures {
		s += "~" + d
	}
	return s + "~" // trailing '~' = empty key-binding slot
}

func TestParseDisclosuresFlattensAcrossCredentials(t *testing.T) {
	vp := map[string][]string{
		"passport": {sdjwt(t,
			disclosure(t, ClaimGivenNames, "Alice"),
			disclosure(t, ClaimFamilyName, "Owner"),
			disclosure(t, ClaimDateOfBirth, "1980-01-02"),
		)},
		"email": {sdjwt(t, disclosure(t, ClaimEmail, "alice@example.com"))},
		"phone": {sdjwt(t, disclosure(t, ClaimPhone, "+31600000000"))},
	}

	claims := parseDisclosures(vp)

	want := map[string]string{
		ClaimGivenNames:  "Alice",
		ClaimFamilyName:  "Owner",
		ClaimDateOfBirth: "1980-01-02",
		ClaimEmail:       "alice@example.com",
		ClaimPhone:       "+31600000000",
	}
	for k, v := range want {
		if claims[k] != v {
			t.Errorf("claim %q = %q, want %q", k, claims[k], v)
		}
	}
}

func TestDisclosuresOfIgnoresJWTOnlyToken(t *testing.T) {
	// An issuer JWT with no disclosures and no trailing '~' must yield nothing,
	// not panic or misread the JWT body as a disclosure.
	if got := disclosuresOf("only.a.jwt"); len(got) != 0 {
		t.Fatalf("got %v, want empty", got)
	}
}

func TestStringifyNonStringClaim(t *testing.T) {
	// SD-JWT values are not always strings (e.g. a boolean over18 claim).
	if got := stringify(true); got != "true" {
		t.Errorf("stringify(true) = %q, want \"true\"", got)
	}
	if got := stringify(float64(42)); got != "42" {
		t.Errorf("stringify(42) = %q, want \"42\"", got)
	}
}

func TestLoginQueryOffersPassportOrIDCard(t *testing.T) {
	q := loginQuery()
	if len(q.Credentials) != 4 {
		t.Fatalf("credentials = %d, want 4 (passport, idcard, email, phone)", len(q.Credentials))
	}
	// The first credential_set must be the passport-OR-idcard choice.
	if len(q.CredentialSets) == 0 || len(q.CredentialSets[0].Options) != 2 {
		t.Fatalf("first credential set is not a 2-way choice: %+v", q.CredentialSets)
	}
}
