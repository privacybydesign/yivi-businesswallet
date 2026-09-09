package devverifier

import (
	"crypto"
	"crypto/ecdsa"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/lestrrat-go/jwx/v3/jwa"
	"github.com/lestrrat-go/jwx/v3/jwe"
	"github.com/privacybydesign/irmago/eudi/credentials/sdjwtvc"
	eudijwt "github.com/privacybydesign/irmago/eudi/jwt"
	"github.com/privacybydesign/irmago/eudi/sdjwt"
)

// cryptoSHA256 is the thumbprint hash for JWK key ids.
const cryptoSHA256 = crypto.SHA256

// registeredClaims are the SD-JWT VC claims that are protocol, not content.
var registeredClaims = map[string]struct{}{
	"iss": {}, "vct": {}, "vct#integrity": {}, "cnf": {}, "_sd": {}, "_sd_alg": {},
	"iat": {}, "exp": {}, "nbf": {}, "status": {}, "sub": {},
}

// Presented is one credential from a vp_token, as the verifier saw it.
type Presented struct {
	QueryID string
	VCT     string
	Issuer  string
	// Verified is true when the issuer signature chained to the verifier's
	// issuer trust and the key-binding JWT checked out (signature against cnf,
	// sd_hash, nonce, audience). When false, Error says why and Claims are the
	// disclosed values as decoded, not as verified.
	Verified bool
	Error    string
	Claims   map[string]any
	Raw      string
}

// DecryptResponse opens a direct_post.jwt `response` JWE with the session's
// encryption key and returns the vp_token and state it carried.
func DecryptResponse(response string, key *ecdsa.PrivateKey) (map[string][]string, string, error) {
	plaintext, err := jwe.Decrypt([]byte(response), jwe.WithKey(jwa.ECDH_ES(), key))
	if err != nil {
		return nil, "", fmt.Errorf("devverifier: decrypt response: %w", err)
	}
	var payload struct {
		VPToken map[string][]string `json:"vp_token"`
		State   string              `json:"state"`
	}
	if err := json.Unmarshal(plaintext, &payload); err != nil {
		return nil, "", fmt.Errorf("devverifier: decode decrypted response: %w", err)
	}
	return payload.VPToken, payload.State, nil
}

// ParseVPToken decodes the vp_token form parameter of a direct_post response.
func ParseVPToken(raw string) (map[string][]string, error) {
	var token map[string][]string
	if err := json.Unmarshal([]byte(raw), &token); err != nil {
		return nil, fmt.Errorf("devverifier: decode vp_token: %w", err)
	}
	return token, nil
}

// VerifyVPToken checks every presentation in token with irmago's verifier-side
// SD-JWT VC processor: issuer signature against issuerTrust, disclosure digests,
// and the key-binding JWT against the credential's cnf key, nonce and audience
// (the verifier's client_id). A presentation that fails is still decoded so the
// page can show what arrived next to why it was refused.
func VerifyVPToken(token map[string][]string, issuerTrust eudijwt.X509VerificationContext, nonce, clientID string) []Presented {
	processor := sdjwtvc.NewVerifierVerificationProcessor(true, sdjwtvc.SdJwtVcVerificationContext{
		X509VerificationContext: issuerTrust,
		Clock:                   eudijwt.NewSystemClock(),
		JwtVerifier:             sdjwt.NewJwxJwtVerifier(),
		ExpectedNonce:           nonce,
		ExpectedAudience:        clientID,
	})
	var out []Presented
	for queryID, presentations := range token {
		for _, raw := range presentations {
			p := Presented{QueryID: queryID, Raw: raw}
			verified, err := processor.ParseAndVerifySdJwtVc(sdjwtvc.SdJwtVcKb(raw))
			if err != nil {
				p.Error = err.Error()
				p.VCT, p.Issuer, p.Claims = decodeUnverified(raw)
			} else {
				p.Verified = true
				p.VCT = verified.IssuerSignedJwtPayload.VerifiableCredentialType
				p.Issuer = verified.IssuerSignedJwtPayload.Issuer
				p.Claims = contentClaims(verified.ProcessedSdJwtPayload)
			}
			out = append(out, p)
		}
	}
	return out
}

// decodeUnverified reads what a presentation says about itself without
// checking any signature: the issuer JWT's vct and iss and the disclosed
// claims, flat. For display next to a verification error only.
func decodeUnverified(raw string) (vct, issuer string, claims map[string]any) {
	parts := strings.Split(raw, "~")
	claims = map[string]any{}
	jwtParts := strings.Split(parts[0], ".")
	if len(jwtParts) == 3 {
		var payload map[string]any
		if b, err := base64.RawURLEncoding.DecodeString(jwtParts[1]); err == nil && json.Unmarshal(b, &payload) == nil {
			vct, _ = payload["vct"].(string)
			issuer, _ = payload["iss"].(string)
			for k, v := range contentClaims(payload) {
				claims[k] = v
			}
		}
	}
	// Disclosures sit between the issuer JWT and the (possibly empty) KB-JWT.
	for _, d := range parts[1 : len(parts)-1] {
		b, err := base64.RawURLEncoding.DecodeString(d)
		if err != nil {
			continue
		}
		var disclosure []any
		if json.Unmarshal(b, &disclosure) != nil || len(disclosure) != 3 {
			continue
		}
		if key, ok := disclosure[1].(string); ok {
			claims[key] = disclosure[2]
		}
	}
	return vct, issuer, claims
}

func contentClaims(payload map[string]any) map[string]any {
	out := map[string]any{}
	for k, v := range payload {
		if _, registered := registeredClaims[k]; registered {
			continue
		}
		out[k] = v
	}
	return out
}

// ErrNoPresentations is returned when a response carried an empty vp_token.
var ErrNoPresentations = errors.New("devverifier: vp_token holds no presentations")
