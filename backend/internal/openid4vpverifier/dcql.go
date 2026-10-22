package openid4vpverifier

import "slices"

// vct values for the pbdf staging credentials we request (SD-JWT VC / dc+sd-jwt).
const (
	vctPassport = "pbdf-staging.pbdf.passport"
	vctIDCard   = "pbdf-staging.pbdf.idcard"
	// vctResidencePermit carries the same claims as the passport and id-card.
	vctResidencePermit = "pbdf-staging.pbdf.residencepermit"
	vctEmail           = "pbdf-staging.sidn-pbdf.email"
	vctPhone           = "pbdf-staging.sidn-pbdf.mobilenumber"
	// vctVog is the opt-in member-screening credential (#242 §4): a statement by
	// Stichting Privacy by Design about a VOG PDF it validated at issuance, not a
	// live Justis statement - the org accepts that trade-off knowingly when it
	// turns this method on (ScreeningSettings.AcceptYiviCredential).
	vctVog = "pbdf-staging.pbdf.vog"

	formatSDJWT = "dc+sd-jwt"

	// Credential ids used in the identity query (identityQuery below), reused by
	// verifier.go to pick out the identity credential's issuer `iat` for the
	// re-identification freshness check.
	credIDPassport        = "passport"
	credIDIDCard          = "idcard"
	credIDResidencePermit = "residencepermit"
	credIDEmail           = "email"
	credIDPhone           = "phone"
	credIDVog             = "vog"
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
	// ScopeIdentity additionally discloses a verified identity (passport,
	// id-card OR residence permit) and phone, for flows that must match a real
	// person (invitation accept, and later the KVK-facing wallet bootstrap).
	ScopeIdentity
	// ScopeVog discloses the pbdf.vog credential (#242 §4), for the opt-in
	// credential-disclosure screening path. Its claim list is dynamic (only the
	// org's required aspect flags), so callers pass it via claims.
	ScopeVog
	// ScopeIdentityVog discloses everything ScopeIdentity does plus the pbdf.vog
	// credential, in one presentation: for a member who has never identified
	// and is asked for a VOG, one scan establishes the identity the VOG is then
	// matched against. Its vog claim list is dynamic like ScopeVog's.
	ScopeIdentityVog
	// ScopeProofing discloses a passport, id-card OR residence permit, for the
	// identity proofing Yivi method (internal/proofing): its photo only when the
	// flow keeps it.
	ScopeProofing
)

func queryFor(scope Scope, claims []string) dcqlQuery {
	switch scope {
	case ScopeIdentity:
		return identityQuery()
	case ScopeVog:
		return vogQuery(claims)
	case ScopeIdentityVog:
		return identityVogQuery(claims)
	case ScopeProofing:
		return proofingQuery(claims)
	default:
		return loginQuery()
	}
}

// loginQuery discloses only the email address (data minimisation): login just
// needs to identify the account. See §3 of .ai/features/auth-openid4vp.md.
func loginQuery() dcqlQuery {
	return dcqlQuery{
		Credentials: []dcqlCredential{
			{ID: credIDEmail, Format: formatSDJWT, Meta: dcqlMeta{[]string{vctEmail}}, Claims: claimPaths(ClaimEmail)},
		},
		CredentialSets: []dcqlCredentialSet{
			{Options: [][]string{{credIDEmail}}},
		},
	}
}

// identityQuery discloses a verified identity (passport, id-card OR residence
// permit) plus email and phone, for flows that must match a real person.
func identityQuery() dcqlQuery {
	return dcqlQuery{
		Credentials: []dcqlCredential{
			{ID: credIDPassport, Format: formatSDJWT, Meta: dcqlMeta{[]string{vctPassport}}, Claims: claimPaths(ClaimGivenNames, ClaimFamilyName, ClaimDateOfBirth, ClaimNationality)},
			{ID: credIDIDCard, Format: formatSDJWT, Meta: dcqlMeta{[]string{vctIDCard}}, Claims: claimPaths(ClaimGivenNames, ClaimFamilyName, ClaimDateOfBirth, ClaimNationality)},
			{ID: credIDResidencePermit, Format: formatSDJWT, Meta: dcqlMeta{[]string{vctResidencePermit}}, Claims: claimPaths(ClaimGivenNames, ClaimFamilyName, ClaimDateOfBirth, ClaimNationality)},
			{ID: credIDEmail, Format: formatSDJWT, Meta: dcqlMeta{[]string{vctEmail}}, Claims: claimPaths(ClaimEmail)},
			{ID: credIDPhone, Format: formatSDJWT, Meta: dcqlMeta{[]string{vctPhone}}, Claims: claimPaths(ClaimPhone)},
		},
		CredentialSets: []dcqlCredentialSet{
			{Options: [][]string{{credIDPassport}, {credIDIDCard}, {credIDResidencePermit}}},
			{Options: [][]string{{credIDEmail}}},
			{Options: [][]string{{credIDPhone}}},
		},
	}
}

// vogQuery discloses the pbdf.vog credential's core identity fields plus only
// the aspect claims named - the org's required function-aspect codes, never
// every aspectNN flag the credential carries and never profileCodes as a blob
// (#242 §4's data-minimisation design). aspectClaims are claim names, already
// resolved by the caller (organization.AspectClaimName).
func vogQuery(aspectClaims []string) dcqlQuery {
	return dcqlQuery{
		Credentials:    []dcqlCredential{vogCredential(aspectClaims)},
		CredentialSets: []dcqlCredentialSet{{Options: [][]string{{credIDVog}}}},
	}
}

// vogCredential is the pbdf.vog credential entry shared by vogQuery and
// identityVogQuery.
func vogCredential(aspectClaims []string) dcqlCredential {
	claims := append([]string{ClaimVogIssueDate, ClaimVogSurname, ClaimVogPrefix, ClaimVogGivenNames, ClaimVogDateOfBirth}, aspectClaims...)
	return dcqlCredential{ID: credIDVog, Format: formatSDJWT, Meta: dcqlMeta{[]string{vctVog}}, Claims: claimPaths(claims...)}
}

// identityVogQuery is identityQuery plus the pbdf.vog credential as a fourth,
// independently required credential set - so the presentation is only
// complete when the wallet disclosed both the identity and the VOG.
func identityVogQuery(aspectClaims []string) dcqlQuery {
	q := identityQuery()
	q.Credentials = append(q.Credentials, vogCredential(aspectClaims))
	q.CredentialSets = append(q.CredentialSets, dcqlCredentialSet{Options: [][]string{{credIDVog}}})
	return q
}

// ageClaims are the passport, id-card and residence-permit claims ("Yes"/"No") that answer
// whether the holder is that age or older, by age.
var ageClaims = map[int]string{12: "over12", 16: "over16", 18: "over18", 21: "over21", 65: "over65"}

// AgeClaim is the passport, id-card and residence-permit claim that answers whether the holder
// is age or older; false when the credentials carry none for age.
func AgeClaim(age int) (string, bool) {
	claim, ok := ageClaims[age]
	return claim, ok
}

func isAgeClaim(claim string) bool {
	for _, c := range ageClaims {
		if c == claim {
			return true
		}
	}
	return false
}

// proofingQuery discloses a verified identity (passport, id-card OR residence
// permit), and nothing else: no email or phone, which proofing a person does not need.
// extra are further claims of the flow's: the photo (ClaimPhoto), and an age
// claim (AgeClaim), which replaces the date of birth an age check never
// receives.
func proofingQuery(extra []string) dcqlQuery {
	names := []string{ClaimGivenNames, ClaimFamilyName, ClaimNationality}
	if !slices.ContainsFunc(extra, isAgeClaim) {
		names = append(names, ClaimDateOfBirth)
	}
	claims := claimPaths(append(names, extra...)...)
	return dcqlQuery{
		Credentials: []dcqlCredential{
			{ID: credIDPassport, Format: formatSDJWT, Meta: dcqlMeta{[]string{vctPassport}}, Claims: claims},
			{ID: credIDIDCard, Format: formatSDJWT, Meta: dcqlMeta{[]string{vctIDCard}}, Claims: claims},
			{ID: credIDResidencePermit, Format: formatSDJWT, Meta: dcqlMeta{[]string{vctResidencePermit}}, Claims: claims},
		},
		CredentialSets: []dcqlCredentialSet{
			{Options: [][]string{{credIDPassport}, {credIDIDCard}, {credIDResidencePermit}}},
		},
	}
}
