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
)

// Claim keys disclosed by the credentials we request (pbdf staging schemes).
const (
	ClaimEmail       = "email"
	ClaimGivenNames  = "firstName"
	ClaimFamilyName  = "lastName"
	ClaimDateOfBirth = "dateOfBirth"
	ClaimNationality = "nationality"
	ClaimPhone       = "mobilenumber"
)

// ErrPending means the holder has not completed the presentation yet. The hosted
// verifier returns a non-2xx for a pending (or unknown/expired) transaction and
// does not distinguish them, so an unknown session also surfaces as ErrPending;
// the frontend's own timeout bounds the wait.
var ErrPending = errors.New("openid4vpverifier: presentation pending")

// Session is a started presentation: an opaque transaction id (kept server-side)
// plus the wallet deeplink to render as a QR / universal link.
type Session struct {
	ID         string
	WalletLink string
}

// Presentation is the verified, disclosed claim set, flattened across the
// requested credentials.
type Presentation struct {
	Claims map[string]string
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
