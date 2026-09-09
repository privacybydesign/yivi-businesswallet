package openid4vpverifier

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
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

func TestLoginQueryIsEmailOnly(t *testing.T) {
	q := loginQuery()
	if len(q.Credentials) != 1 || q.Credentials[0].ID != "email" {
		t.Fatalf("login query should request only email, got %+v", q.Credentials)
	}
}

func TestIdentityQueryOffersPassportOrIDCard(t *testing.T) {
	q := identityQuery()
	if len(q.Credentials) != 4 {
		t.Fatalf("credentials = %d, want 4 (passport, idcard, email, phone)", len(q.Credentials))
	}
	// The first credential_set must be the passport-OR-idcard choice.
	if len(q.CredentialSets) == 0 || len(q.CredentialSets[0].Options) != 2 {
		t.Fatalf("first credential set is not a 2-way choice: %+v", q.CredentialSets)
	}
}

func TestQueryForScope(t *testing.T) {
	if got := len(queryFor(ScopeLogin).Credentials); got != 1 {
		t.Errorf("ScopeLogin credentials = %d, want 1", got)
	}
	if got := len(queryFor(ScopeIdentity).Credentials); got != 4 {
		t.Errorf("ScopeIdentity credentials = %d, want 4", got)
	}
}

// startBody runs one StartPresentation against a stub verifier and returns the
// JSON body it received.
func startBody(t *testing.T, intendedUseID, registrationCertificate string) map[string]any {
	t.Helper()

	var raw []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ = io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"transaction_id":"tx","client_id":"cid","request_uri":"https://verifier.example/req"}`))
	}))
	defer srv.Close()

	client := New(srv.URL, "", intendedUseID, registrationCertificate, srv.Client())
	if _, err := client.StartPresentation(context.Background(), ScopeLogin); err != nil {
		t.Fatalf("StartPresentation: %v", err)
	}

	var body map[string]any
	if err := json.Unmarshal(raw, &body); err != nil {
		t.Fatalf("unmarshal start body: %v", err)
	}
	return body
}

// The verifier refuses a request naming neither an intended use nor a
// registration certificate, so the field has to reach the wire.
func TestStartPresentationSendsIntendedUseID(t *testing.T) {
	body := startBody(t, "1", "")

	if body["intended_use_id"] != "1" {
		t.Errorf("intended_use_id = %v, want \"1\"", body["intended_use_id"])
	}
	if _, ok := body["registration_certificate"]; ok {
		t.Error("registration_certificate present, want it omitted when unset")
	}
}

func TestStartPresentationSendsRegistrationCertificate(t *testing.T) {
	body := startBody(t, "", "header.payload.signature")

	if body["registration_certificate"] != "header.payload.signature" {
		t.Errorf("registration_certificate = %v, want the configured certificate", body["registration_certificate"])
	}
	if _, ok := body["intended_use_id"]; ok {
		t.Error("intended_use_id present, want it omitted when unset")
	}
}

func TestStartPresentationOmitsBothWhenUnset(t *testing.T) {
	body := startBody(t, "", "")

	for _, key := range []string{"intended_use_id", "registration_certificate"} {
		if _, ok := body[key]; ok {
			t.Errorf("%s present, want it omitted when unset", key)
		}
	}
}

// irmago's wallet GETs the request object and ignores request_uri_method, so a
// presentation announced as "post" is refused when the wallet fetches it.
func TestStartPresentationAsksForAGetRequestURI(t *testing.T) {
	body := startBody(t, "1", "")

	if body["request_uri_method"] != requestURIMethodGet {
		t.Errorf("request_uri_method = %v, want %q", body["request_uri_method"], requestURIMethodGet)
	}
}

// issuerJWT builds an SD-JWT VC issuer-signed JWT whose payload carries iat.
func issuerJWT(t *testing.T, issuedAt int64) string {
	t.Helper()
	payload, err := json.Marshal(map[string]any{"vct": vctPassport, "iat": issuedAt})
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}
	header := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"ES256"}`))
	return header + "." + base64.RawURLEncoding.EncodeToString(payload) + ".signature"
}

// The re-identification freshness check (#240 §5) reads `iat` from the identity
// credential's issuer-signed JWT — the moment the wallet obtained it — so this
// pins that it is decoded out of the first '~' segment, per credential id.
func TestIdentityIssuedAtReadsTheIssuerJWT(t *testing.T) {
	obtained := int64(1767225600) // 2026-01-01T00:00:00Z

	passport := issuerJWT(t, obtained) + "~" + disclosure(t, ClaimGivenNames, "Alice") + "~"
	got := identityIssuedAt(map[string][]string{"passport": {passport}})
	if got.Unix() != obtained {
		t.Errorf("identityIssuedAt(passport) = %v, want unix %d", got, obtained)
	}

	// An id-card presentation is the other half of the identity credential set.
	idcard := issuerJWT(t, obtained) + "~" + disclosure(t, ClaimFamilyName, "Owner") + "~"
	if got := identityIssuedAt(map[string][]string{"idcard": {idcard}}); got.Unix() != obtained {
		t.Errorf("identityIssuedAt(idcard) = %v, want unix %d", got, obtained)
	}
}

// A presentation with no identity credential, or one whose issuer JWT cannot be
// decoded, yields the zero time rather than a bogus date — the freshness check
// then simply does not apply instead of rejecting a valid re-identification.
func TestIdentityIssuedAtIsZeroWhenUnavailable(t *testing.T) {
	cases := map[string]map[string][]string{
		"no identity credential": {"email": {sdjwt(t, disclosure(t, ClaimEmail, "a@b.test"))}},
		"undecodable issuer jwt": {"passport": {sdjwt(t, disclosure(t, ClaimGivenNames, "Alice"))}},
		"empty token list":       {"passport": {}},
		"no iat claim": {"passport": {base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"ES256"}`)) + "." +
			base64.RawURLEncoding.EncodeToString([]byte(`{"vct":"x"}`)) + ".sig~"}},
	}
	for name, vp := range cases {
		t.Run(name, func(t *testing.T) {
			if got := identityIssuedAt(vp); !got.IsZero() {
				t.Errorf("identityIssuedAt = %v, want the zero time", got)
			}
		})
	}
}
