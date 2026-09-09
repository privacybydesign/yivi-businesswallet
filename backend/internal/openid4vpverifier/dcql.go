package openid4vpverifier

// vct values for the pbdf staging credentials we request (SD-JWT VC / dc+sd-jwt).
const (
	vctPassport = "pbdf-staging.pbdf.passport"
	vctIDCard   = "pbdf-staging.pbdf.idcard"
	vctEmail    = "pbdf-staging.sidn-pbdf.email"
	vctPhone    = "pbdf-staging.sidn-pbdf.mobilenumber"

	formatSDJWT = "dc+sd-jwt"

	// Credential ids used in the identity query (identityQuery below), reused by
	// verifier.go to pick out the identity credential's issuer `iat` for the
	// re-identification freshness check.
	credIDPassport = "passport"
	credIDIDCard   = "idcard"
)

// DCQL types (OpenID4VP Digital Credentials Query Language).
type dcqlQuery struct {
	Credentials    []dcqlCredential    `json:"credentials"`
	CredentialSets []dcqlCredentialSet `json:"credential_sets"`
}

type dcqlCredential struct {
	ID     string      `json:"id"`
	Format string      `json:"format"`
	Meta   dcqlMeta    `json:"meta"`
	Claims []dcqlClaim `json:"claims"`
}

type dcqlMeta struct {
	VctValues []string `json:"vct_values"`
}

type dcqlClaim struct {
	Path []string `json:"path"`
}

type dcqlCredentialSet struct {
	Options [][]string `json:"options"`
}

func claimPaths(names ...string) []dcqlClaim {
	cs := make([]dcqlClaim, len(names))
	for i, n := range names {
		cs[i] = dcqlClaim{Path: []string{n}}
	}
	return cs
}

// Scope selects which credentials a presentation requests.
type Scope int

const (
	// ScopeLogin discloses only the email — enough to identify the account.
	ScopeLogin Scope = iota
	// ScopeIdentity additionally discloses a verified identity (passport OR
	// id-card) and phone, for flows that must match a real person (invitation
	// accept, and — later — the KVK-facing wallet bootstrap).
	ScopeIdentity
)

func queryFor(scope Scope) dcqlQuery {
	if scope == ScopeIdentity {
		return identityQuery()
	}
	return loginQuery()
}

// loginQuery discloses only the email address (data minimisation): login just
// needs to identify the account. See §3 of .ai/features/auth-openid4vp.md.
func loginQuery() dcqlQuery {
	return dcqlQuery{
		Credentials: []dcqlCredential{
			{ID: "email", Format: formatSDJWT, Meta: dcqlMeta{[]string{vctEmail}}, Claims: claimPaths(ClaimEmail)},
		},
		CredentialSets: []dcqlCredentialSet{
			{Options: [][]string{{"email"}}},
		},
	}
}

// identityQuery discloses a verified identity (passport OR id-card) plus email
// and phone, for flows that must match a real person.
func identityQuery() dcqlQuery {
	return dcqlQuery{
		Credentials: []dcqlCredential{
			{ID: credIDPassport, Format: formatSDJWT, Meta: dcqlMeta{[]string{vctPassport}}, Claims: claimPaths(ClaimGivenNames, ClaimFamilyName, ClaimDateOfBirth, ClaimNationality)},
			{ID: credIDIDCard, Format: formatSDJWT, Meta: dcqlMeta{[]string{vctIDCard}}, Claims: claimPaths(ClaimGivenNames, ClaimFamilyName, ClaimDateOfBirth, ClaimNationality)},
			{ID: "email", Format: formatSDJWT, Meta: dcqlMeta{[]string{vctEmail}}, Claims: claimPaths(ClaimEmail)},
			{ID: "phone", Format: formatSDJWT, Meta: dcqlMeta{[]string{vctPhone}}, Claims: claimPaths(ClaimPhone)},
		},
		CredentialSets: []dcqlCredentialSet{
			{Options: [][]string{{credIDPassport}, {credIDIDCard}}},
			{Options: [][]string{{"email"}}},
			{Options: [][]string{{"phone"}}},
		},
	}
}
