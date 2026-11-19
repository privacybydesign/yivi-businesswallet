// Package yivicsc is the business wallet's own signing service: a Cloud
// Signature Consortium (CSC) API v2 provider, with the OAuth2 authorization
// server in front of it, whose authorization step is a Yivi disclosure over
// OpenID4VP. It is what an organisation signs with when it has connected no
// other provider (internal/csc), so signing works on every deployment without a
// remote QTSP.
//
// Who does what. The signing ceremony (internal/signing) is the client: it
// sends the browser to /oauth2/authorize, exchanges the code at /oauth2/token
// and calls credentials/list, credentials/info and signatures/signHash. This
// package is the far side of those calls. On authorize it starts a presentation
// at the hosted EUDI verifier (internal/openid4vpverifier) asking the signer's
// name and date of birth from a passport, identity card or residence permit
// (the chip-read documents) or from the registered personal data (BRP); the
// signer picks which in the Yivi app. The disclosed person is the subject the
// service issues a signing certificate to, and the person who must disclose
// again before each signature.
//
// What it is not. The keys live in the database, sealed under the deployment's
// CSC key, and the certificates chain to a certificate authority this
// deployment made for itself: no QSCD, no qualified certificate, no EU trusted
// list. A signature made here is an advanced electronic signature whose signer
// was identified with Yivi, not a qualified one.
//
// In-flight authorizations, codes and access tokens are held in memory, as the
// signing ceremony's own sessions are: they do not survive a restart and assume
// one API instance. Certificates and keys are in the database.
package yivicsc

import (
	"errors"
	"strings"
	"time"

	"github.com/privacybydesign/yivi-businesswallet/backend/internal/openid4vpverifier"
)

const (
	// BasePath is where the service is mounted under the API prefix: the CSC
	// base URL and the OAuth2 issuer are both the API's public URL plus this.
	BasePath = "/yivi-csc"
	// AuthorizePagePath is the SPA route that shows an authorization's QR code.
	AuthorizePagePath = "/signing/authorize/"

	// Name is the provider name /csc/v2/info reports.
	Name = "Yivi Business Wallet signing service"
	// Specs is the CSC API version the service implements.
	Specs = "2.0.0.2"

	// ClientID is the OAuth client the wallet's own signing ceremony uses.
	ClientID = "yivi-business-wallet"
)

const (
	// AuthorizationTTL is how long a signer has to scan the QR code and
	// disclose. It matches the signing ceremony's own session window.
	AuthorizationTTL = 5 * time.Minute
	// codeTTL is how long an authorization code may wait for its exchange.
	codeTTL = time.Minute
	// tokenTTL is how long an access token is good for. The ceremony uses it
	// within seconds of the exchange.
	tokenTTL = 5 * time.Minute
	// CredentialValidity is how long an issued signing certificate is valid.
	CredentialValidity = 365 * 24 * time.Hour
	// authorityValidity is how long the deployment's own certificate authority
	// is valid.
	authorityValidity = 10 * 365 * 24 * time.Hour
	// clockSkew backdates a certificate's start, so a verifier whose clock runs
	// slightly behind does not see it as not yet valid.
	clockSkew = 5 * time.Minute
)

// Source families: where the signer's name comes from. A certificate is issued
// on one family, and each signature is authorised by a disclosure from that
// same family, because the two write a name differently (a chip's MRZ against
// the population register's spelling) and would not compare.
const (
	// SourceMRTD is a chip-read travel document: passport, identity card or
	// residence permit.
	SourceMRTD = "mrtd"
	// SourceBRP is the registered personal data from the Dutch population
	// register (Basisregistratie Personen).
	SourceBRP = "brp"
)

// mrtdSources and brpSources are the credentials each family is disclosed from.
var (
	mrtdSources = []string{
		openid4vpverifier.SourcePassport,
		openid4vpverifier.SourceIdentityCard,
		openid4vpverifier.SourceResidencePermit,
	}
	brpSources = []string{openid4vpverifier.SourcePersonalData}
)

// sourcesOf returns the credentials a family is disclosed from; every one for
// no family, which is what linking a certificate offers.
func sourcesOf(family string) []string {
	switch family {
	case SourceMRTD:
		return mrtdSources
	case SourceBRP:
		return brpSources
	default:
		return append(append([]string{}, mrtdSources...), brpSources...)
	}
}

var (
	// ErrUnavailable means the deployment has no CSC encryption key, so the
	// service cannot keep a signing key.
	ErrUnavailable = errors.New("yivicsc: no encryption key configured; the signing service is off")
	// ErrIdentityIncomplete means a disclosure carried no usable name or date
	// of birth.
	ErrIdentityIncomplete = errors.New("yivicsc: the disclosure carried no complete identity")
	// ErrCredentialNotFound means a credential id names no credential.
	ErrCredentialNotFound = errors.New("yivicsc: credential not found")
	// ErrAuthorizationNotFound means an authorization id is unknown or expired.
	ErrAuthorizationNotFound = errors.New("yivicsc: authorization not found or expired")
)

// Identity is the person a disclosure named.
type Identity struct {
	// Source is the family the name came from (SourceMRTD or SourceBRP).
	Source      string
	GivenNames  string
	FamilyName  string
	DateOfBirth string
}

// CommonName is the name a certificate carries: given names, then family name.
func (i Identity) CommonName() string {
	return strings.TrimSpace(i.GivenNames + " " + i.FamilyName)
}

// identityOf reads the person out of a presentation: from the travel document
// when one was disclosed, else from the registered personal data.
func identityOf(p openid4vpverifier.Presentation) (Identity, error) {
	var id Identity
	if doc, ok := p.Document(); ok {
		id = Identity{
			Source:      SourceMRTD,
			GivenNames:  clean(doc.Claims[openid4vpverifier.ClaimGivenNames]),
			FamilyName:  clean(doc.Claims[openid4vpverifier.ClaimFamilyName]),
			DateOfBirth: clean(doc.Claims[openid4vpverifier.ClaimDateOfBirth]),
		}
	} else if personal, ok := p.PersonalData(); ok {
		// The register keeps a family name's prefix ("van der") apart; a
		// certificate names the person as they are known, so it goes back on.
		family := clean(personal.Claims[openid4vpverifier.ClaimPersonalPrefix] + " " +
			personal.Claims[openid4vpverifier.ClaimPersonalFamilyName])
		id = Identity{
			Source:      SourceBRP,
			GivenNames:  clean(personal.Claims[openid4vpverifier.ClaimPersonalFirstNames]),
			FamilyName:  family,
			DateOfBirth: clean(personal.Claims[openid4vpverifier.ClaimPersonalDateOfBirth]),
		}
	}
	if id.GivenNames == "" || id.FamilyName == "" || id.DateOfBirth == "" {
		return Identity{}, ErrIdentityIncomplete
	}
	return id, nil
}

// clean collapses runs of whitespace and trims, so a name is one shape however
// its credential spaced it.
func clean(s string) string {
	return strings.Join(strings.Fields(s), " ")
}
