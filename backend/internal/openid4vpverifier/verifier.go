// Package openid4vpverifier is the client seam to an OpenID4VP verifier (the EUDI
// reference Verifier Endpoint). Our backend is a requestor / orchestrator in
// front of the hosted verifier — it does NOT implement the verifier role or the
// SD-JWT / key-binding cryptography (the verifier does that and performs trust-
// chain verification). It replaces internal/irmarequestor as the disclosure seam.
// See .ai/features/auth-openid4vp.md.
package openid4vpverifier

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"maps"
	"strconv"
	"strings"
	"time"
)

// Presentation status values reported by Status: PENDING until the holder
// completes the presentation, then DONE.
const (
	StatusPending = "PENDING"
	StatusDone    = "DONE"
)

// Claim keys disclosed by the credentials we request (pbdf staging schemes).
const (
	ClaimEmail       = "email"
	ClaimGivenNames  = "firstName"
	ClaimFamilyName  = "lastName"
	ClaimDateOfBirth = "dateOfBirth"
	ClaimNationality = "nationality"
	ClaimPhone       = "mobilenumber"

	// pbdf.vog claim keys (#242 §4). Named distinctly from the identity claims
	// above even where the value happens to coincide (ClaimDateOfBirth), because
	// they come from a different credential's own attribute names.
	ClaimVogIssueDate   = "issueDate"
	ClaimVogSurname     = "surname"
	ClaimVogPrefix      = "prefix"
	ClaimVogGivenNames  = "givenNames"
	ClaimVogDateOfBirth = "dateOfBirth"
	// vogAspectClaimPrefix names the credential's per-aspect yes/no attributes
	// (aspect11 .. aspect91). VogAspectClaim builds one from a two-digit code.
	vogAspectClaimPrefix = "aspect"
)

// VogAspectClaim returns the pbdf.vog claim name for a function-aspect code
// (e.g. "11" -> "aspect11"), the credential's own attribute naming.
func VogAspectClaim(code string) string {
	return vogAspectClaimPrefix + code
}

// ErrPending means the holder has not completed the presentation yet. The hosted
// verifier returns a non-2xx for a pending (or unknown/expired) transaction and
// does not distinguish them, so an unknown session also surfaces as ErrPending;
// the frontend's own timeout bounds the wait.
var ErrPending = errors.New("openid4vpverifier: presentation pending")

// Session is a started presentation: the verifier-minted transaction id (kept
// strictly server-side — never handed to the client, which polls via our own
// opaque id) plus the wallet deeplink to render as a QR / universal link.
type Session struct {
	TransactionID string
	WalletLink    string
}

// Presentation is the verified, disclosed claim set, flattened across the
// requested credentials.
type Presentation struct {
	Claims map[string]string
	// IdentityIssuedAt is the `iat` claim of the identity credential's (passport or
	// id-card) issuer-signed JWT — the moment the wallet obtained that credential
	// from its issuer, not the physical document's issue date. Used by a
	// freshness check on re-identification (see .ai/features/auth-openid4vp.md and
	// the member-reidentification feature); zero when the presentation carried no
	// identity credential or its `iat` could not be read.
	IdentityIssuedAt time.Time
}

// vpTokenResponse is the verifier's disclosed payload: credential id -> SD-JWT VCs.
type vpTokenResponse struct {
	VPToken map[string][]string `json:"vp_token"`
}

// parseDisclosures flattens the SD-JWT VC disclosures across all credentials into
// a claim map. It decodes disclosures only — signature / KB-JWT / trust-chain
// verification is the verifier's responsibility (see the package doc).
func parseDisclosures(vp map[string][]string) map[string]string {
	claims := map[string]string{}
	for _, tokens := range vp {
		for _, tok := range tokens {
			maps.Copy(claims, disclosuresOf(tok))
		}
	}
	return claims
}

// disclosuresOf decodes the SD-JWT VC compact form: segments joined by '~', the
// first being the issuer-signed JWT and the last the key-binding JWT (or an empty
// trailer). Each middle segment is base64url(JSON [salt, claimName, claimValue]).
func disclosuresOf(sdjwt string) map[string]string {
	out := map[string]string{}
	parts := strings.Split(sdjwt, "~")
	if len(parts) <= 2 {
		return out
	}
	for _, d := range parts[1 : len(parts)-1] {
		if d == "" {
			continue
		}
		raw, err := base64.RawURLEncoding.DecodeString(d)
		if err != nil {
			continue
		}
		var arr []any
		if err := json.Unmarshal(raw, &arr); err != nil || len(arr) < 3 {
			continue
		}
		name, ok := arr[1].(string)
		if !ok {
			continue
		}
		out[name] = stringify(arr[2])
	}
	return out
}

// identityIssuedAt returns the `iat` of the identity credential (passport or
// id-card, whichever is present) in vp, zero when neither is present or its
// `iat` could not be read.
func identityIssuedAt(vp map[string][]string) time.Time {
	for _, id := range [...]string{credIDPassport, credIDIDCard} {
		tokens := vp[id]
		if len(tokens) == 0 {
			continue
		}
		if t, ok := issuerIssuedAt(tokens[0]); ok {
			return t
		}
	}
	return time.Time{}
}

// issuerIssuedAt reads the `iat` claim from an SD-JWT VC's issuer-signed JWT (the
// first `~`-separated segment: header.payload.signature). This is the moment the
// wallet obtained the credential from its issuer — a registered top-level claim
// carried on every presentation — not the physical document's own issue date.
// Decoding the payload here is not signature verification (the hosted verifier
// already did that; see the package doc); it only reads an already-trusted claim.
func issuerIssuedAt(sdjwt string) (time.Time, bool) {
	jwt := sdjwt
	if i := strings.IndexByte(sdjwt, '~'); i >= 0 {
		jwt = sdjwt[:i]
	}
	segments := strings.Split(jwt, ".")
	if len(segments) < 2 {
		return time.Time{}, false
	}
	raw, err := base64.RawURLEncoding.DecodeString(segments[1])
	if err != nil {
		return time.Time{}, false
	}
	var payload struct {
		IssuedAt int64 `json:"iat"`
	}
	if err := json.Unmarshal(raw, &payload); err != nil || payload.IssuedAt <= 0 {
		return time.Time{}, false
	}
	return time.Unix(payload.IssuedAt, 0).UTC(), true
}

func stringify(v any) string {
	switch t := v.(type) {
	case string:
		return t
	case float64:
		return strconv.FormatFloat(t, 'f', -1, 64)
	case bool:
		if t {
			return "true"
		}
		return "false"
	default:
		b, _ := json.Marshal(v)
		return string(b)
	}
}
