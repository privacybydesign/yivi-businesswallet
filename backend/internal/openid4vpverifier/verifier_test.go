package openid4vpverifier

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"slices"
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

var stagingVCTs = vctsFor(DefaultCredentialScheme)

func TestVCTsFollowTheScheme(t *testing.T) {
	if got := vctsFor("pbdf"); got.passport != "pbdf.pbdf.passport" || got.email != "pbdf.sidn-pbdf.email" {
		t.Errorf("vctsFor(pbdf) = %+v", got)
	}
	if vctsFor("") != stagingVCTs {
		t.Error("an empty scheme should fall back to the default (staging)")
	}
}

// The scheme set at New reaches both the start request and the credential
// Result names.
func TestClientUsesTheConfiguredScheme(t *testing.T) {
	var started startRequest
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.Method == http.MethodGet {
			_, _ = w.Write([]byte(`{"vp_token":{"idcard":["jwt~~"]}}`))
			return
		}
		_ = json.NewDecoder(r.Body).Decode(&started)
		_, _ = w.Write([]byte(`{"transaction_id":"tx","client_id":"cid","request_uri":"https://verifier.example/req"}`))
	}))
	defer srv.Close()

	client := New(srv.URL, "", "1", "", "pbdf", srv.Client())
	if _, err := client.StartPresentation(context.Background(), ScopeLogin); err != nil {
		t.Fatalf("StartPresentation: %v", err)
	}
	if got := started.DCQLQuery.Credentials[0].Meta.VctValues; !slices.Equal(got, []string{"pbdf.sidn-pbdf.email"}) {
		t.Errorf("login vct_values = %v, want the pbdf email credential", got)
	}

	p, err := client.Result(context.Background(), "tx")
	if err != nil {
		t.Fatalf("Result: %v", err)
	}
	if doc, ok := p.Document(); !ok || doc.Credential != "pbdf.pbdf.idcard" {
		t.Errorf("Document() = %+v, %v, want the pbdf id-card", doc, ok)
	}
}

func TestLoginQueryIsEmailOnly(t *testing.T) {
	q := stagingVCTs.loginQuery()
	if len(q.Credentials) != 1 || q.Credentials[0].ID != "email" {
		t.Fatalf("login query should request only email, got %+v", q.Credentials)
	}
}

func TestIdentityQueryDocuments(t *testing.T) {
	q := stagingVCTs.identityQuery()
	if len(q.Credentials) != 5 {
		t.Fatalf("credentials = %d, want 5 (passport, idcard, residencepermit, email, phone)", len(q.Credentials))
	}
	// The first credential_set must be the passport-OR-idcard-OR-residencepermit choice.
	if len(q.CredentialSets) == 0 || len(q.CredentialSets[0].Options) != 3 {
		t.Fatalf("first credential set is not a 3-way choice: %+v", q.CredentialSets)
	}
}

func TestQueryForScope(t *testing.T) {
	if got := len(stagingVCTs.queryFor(ScopeLogin, nil).Credentials); got != 1 {
		t.Errorf("ScopeLogin credentials = %d, want 1", got)
	}
	if got := len(stagingVCTs.queryFor(ScopeIdentity, nil).Credentials); got != 5 {
		t.Errorf("ScopeIdentity credentials = %d, want 5", got)
	}
	if got := len(stagingVCTs.queryFor(ScopeVog, []string{"aspect11", "aspect43"}).Credentials[0].Claims); got != 7 {
		t.Errorf("ScopeVog claims = %d, want 7 (5 core fields + 2 requested aspects)", got)
	}
	combined := stagingVCTs.queryFor(ScopeIdentityVog, []string{"aspect11", "aspect43"})
	if got := len(combined.Credentials); got != 6 {
		t.Errorf("ScopeIdentityVog credentials = %d, want 6 (identity's 5 + vog)", got)
	}
	// Four independent sets: identity document, email, phone, vog - the VOG is
	// required, not optional, in the combined presentation.
	if got := len(combined.CredentialSets); got != 4 {
		t.Errorf("ScopeIdentityVog credential sets = %d, want 4", got)
	}
	last := combined.Credentials[len(combined.Credentials)-1]
	if last.ID != credIDVog || len(last.Claims) != 7 {
		t.Errorf("ScopeIdentityVog's last credential = %+v, want vog with 7 claims", last)
	}
}

// Proofing asks for the passport, id-card or residence permit, with no email
// or phone, and its photo only when the caller asks it.
func TestProofingQueryAsksPhotoOnlyWhenAsked(t *testing.T) {
	q := stagingVCTs.queryFor(ScopeProofing, nil)
	if len(q.Credentials) != 3 || len(q.CredentialSets) != 1 || len(q.CredentialSets[0].Options) != 3 {
		t.Fatalf("proofing query = %+v, want passport OR idcard OR residencepermit only", q)
	}
	asksPhoto := func(c dcqlCredential) bool {
		return slices.ContainsFunc(c.Claims, func(cl dcqlClaim) bool { return cl.Path[0] == ClaimPhoto })
	}
	for _, c := range q.Credentials {
		if asksPhoto(c) {
			t.Errorf("%s asks for the photo unasked: %+v", c.ID, c.Claims)
		}
	}
	for _, c := range stagingVCTs.queryFor(ScopeProofing, []string{ClaimPhoto}).Credentials {
		if !asksPhoto(c) {
			t.Errorf("%s does not ask for the photo: %+v", c.ID, c.Claims)
		}
	}
}

// An age check asks for the age claim and never the date of birth.
func TestProofingQueryAsksAgeInsteadOfBirthDate(t *testing.T) {
	claim, ok := AgeClaim(18)
	if !ok || claim != "over18" {
		t.Fatalf("AgeClaim(18) = %q, %v; want over18", claim, ok)
	}
	if _, ok := AgeClaim(17); ok {
		t.Error("AgeClaim(17) answered: the credentials carry no over17")
	}

	has := func(c dcqlCredential, name string) bool {
		return slices.ContainsFunc(c.Claims, func(cl dcqlClaim) bool { return cl.Path[0] == name })
	}
	for _, c := range stagingVCTs.queryFor(ScopeProofing, []string{claim}).Credentials {
		if !has(c, claim) || has(c, ClaimDateOfBirth) {
			t.Errorf("%s claims = %+v, want over18 without dateOfBirth", c.ID, c.Claims)
		}
	}

	for _, c := range stagingVCTs.queryFor(ScopeProofing, nil).Credentials {
		if !has(c, ClaimDateOfBirth) {
			t.Errorf("%s without an age check does not ask the date of birth", c.ID)
		}
	}
}

func TestDocumentPrefersPassport(t *testing.T) {
	p := Presentation{ByCredential: map[string]map[string]string{
		credIDIDCard:   {ClaimGivenNames: "Card"},
		credIDPassport: {ClaimGivenNames: "Passport"},
	}}
	doc, ok := p.Document()
	if !ok || doc.Credential != stagingVCTs.passport || doc.Claims[ClaimGivenNames] != "Passport" {
		t.Fatalf("Document() = %+v, %v", doc, ok)
	}
	idcard, ok := Presentation{ByCredential: map[string]map[string]string{credIDIDCard: {}}}.Document()
	if !ok || idcard.Credential != stagingVCTs.idCard {
		t.Fatalf("id-card Document() = %+v, %v", idcard, ok)
	}
	permit, ok := Presentation{ByCredential: map[string]map[string]string{credIDResidencePermit: {}}}.Document()
	if !ok || permit.Credential != stagingVCTs.residencePermit {
		t.Fatalf("residence permit Document() = %+v, %v", permit, ok)
	}
	if _, ok := (Presentation{ByCredential: map[string]map[string]string{credIDEmail: {}}}).Document(); ok {
		t.Fatal("an email-only presentation has no document")
	}
}

// A combined presentation carries a dateOfBirth from the identity credential
// and one from pbdf.vog; the per-credential views keep them apart where the
// flattened map cannot.
func TestPresentationKeepsCredentialClaimsApart(t *testing.T) {
	p := Presentation{
		Claims: map[string]string{ClaimDateOfBirth: "1980-01-02"},
		ByCredential: map[string]map[string]string{
			credIDPassport: {ClaimGivenNames: "Anna", ClaimDateOfBirth: "1980-01-02"},
			credIDEmail:    {ClaimEmail: "anna@example.test"},
			credIDVog:      {ClaimVogGivenNames: "Anna", ClaimVogDateOfBirth: "1999-12-31"},
		},
	}
	if got := p.IdentityClaims()[ClaimDateOfBirth]; got != "1980-01-02" {
		t.Errorf("IdentityClaims dateOfBirth = %q, want the passport's", got)
	}
	if got := p.IdentityClaims()[ClaimEmail]; got != "anna@example.test" {
		t.Errorf("IdentityClaims email = %q, want the email credential's", got)
	}
	if got := p.VogClaims()[ClaimVogDateOfBirth]; got != "1999-12-31" {
		t.Errorf("VogClaims dateOfBirth = %q, want the vog credential's", got)
	}
	if _, leaked := p.VogClaims()[ClaimGivenNames]; leaked {
		t.Error("VogClaims carries an identity-credential claim")
	}

	// Without per-credential detail both views fall back to the flat map.
	flat := Presentation{Claims: map[string]string{ClaimDateOfBirth: "1980-01-02"}}
	if got := flat.IdentityClaims()[ClaimDateOfBirth]; got != "1980-01-02" {
		t.Errorf("flat IdentityClaims dateOfBirth = %q", got)
	}
	if got := flat.VogClaims()[ClaimDateOfBirth]; got != "1980-01-02" {
		t.Errorf("flat VogClaims dateOfBirth = %q", got)
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

	client := New(srv.URL, "", intendedUseID, registrationCertificate, "", srv.Client())
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
	payload, err := json.Marshal(map[string]any{"vct": stagingVCTs.passport, "iat": issuedAt})
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

	// The id-card and residence permit are the other options of the identity
	// credential set.
	for _, id := range []string{credIDIDCard, credIDResidencePermit} {
		tok := issuerJWT(t, obtained) + "~" + disclosure(t, ClaimFamilyName, "Owner") + "~"
		if got := identityIssuedAt(map[string][]string{id: {tok}}); got.Unix() != obtained {
			t.Errorf("identityIssuedAt(%s) = %v, want unix %d", id, got, obtained)
		}
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
