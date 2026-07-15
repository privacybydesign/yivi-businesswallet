package openid4vpverifier

// vct values for the pbdf staging credentials we request (SD-JWT VC / dc+sd-jwt).
const (
	vctPassport = "pbdf-staging.pbdf.passport"
	vctIDCard   = "pbdf-staging.pbdf.idcard"
	vctEmail    = "pbdf-staging.sidn-pbdf.email"
	vctPhone    = "pbdf-staging.sidn-pbdf.mobilenumber"

	formatSDJWT = "dc+sd-jwt"
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

// loginQuery discloses a verified identity (passport OR id-card) plus email and
// phone. It is used for both login and identity-accept — with OpenID4VP the name
// is always available, unlike the former email-only IRMA login. See §3 of
// .ai/features/auth-openid4vp.md.
func loginQuery() dcqlQuery {
	return dcqlQuery{
		Credentials: []dcqlCredential{
			{ID: "passport", Format: formatSDJWT, Meta: dcqlMeta{[]string{vctPassport}}, Claims: claimPaths(ClaimGivenNames, ClaimFamilyName, ClaimDateOfBirth, ClaimNationality)},
			{ID: "idcard", Format: formatSDJWT, Meta: dcqlMeta{[]string{vctIDCard}}, Claims: claimPaths(ClaimGivenNames, ClaimFamilyName, ClaimDateOfBirth, ClaimNationality)},
			{ID: "email", Format: formatSDJWT, Meta: dcqlMeta{[]string{vctEmail}}, Claims: claimPaths(ClaimEmail)},
			{ID: "phone", Format: formatSDJWT, Meta: dcqlMeta{[]string{vctPhone}}, Claims: claimPaths(ClaimPhone)},
		},
		CredentialSets: []dcqlCredentialSet{
			{Options: [][]string{{"passport"}, {"idcard"}}},
			{Options: [][]string{{"email"}}},
			{Options: [][]string{{"phone"}}},
		},
	}
}
