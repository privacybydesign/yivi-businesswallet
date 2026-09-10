package devverifier

import (
	"crypto"
	"crypto/ecdsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/lestrrat-go/jwx/v3/jwa"
	"github.com/lestrrat-go/jwx/v3/jwe"
	"github.com/lestrrat-go/jwx/v3/jwk"
	"github.com/lestrrat-go/jwx/v3/jws"
	"github.com/privacybydesign/irmago/eudi/credentials/sdjwtvc"
	"github.com/privacybydesign/irmago/eudi/didjwk"
	"github.com/privacybydesign/irmago/eudi/didkey"
	eudijwt "github.com/privacybydesign/irmago/eudi/jwt"
	"github.com/privacybydesign/irmago/eudi/sdjwt"
)

// cryptoSHA256 is the thumbprint hash for JWK key ids.
const cryptoSHA256 = crypto.SHA256

// registeredClaims are the SD-JWT VC claims that are protocol, not content.
var registeredClaims = map[string]struct{}{
	"iss": {}, "vct": {}, "vct#integrity": {}, "cnf": {}, "_sd": {}, "_sd_alg": {},
	"iat": {}, "exp": {}, "nbf": {}, "status": {}, "sub": {}, "fed": {},
}

const (
	// sdHashAlgorithm is the only _sd_alg this verifier computes sd_hash with
	// (the SD-JWT default).
	sdHashAlgorithm = "sha-256"
	kbJwtTyp        = "kb+jwt"
	// kbJwtMaxSkew is how far in the future a KB-JWT's iat may lie.
	kbJwtMaxSkew = 3 * time.Minute
)

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

// VerifyVPToken checks every presentation in token: the issuer signature (x5c
// chain against issuerTrust, or the issuer's did:web keys) and the disclosure
// digests through irmago's verifier-side SD-JWT VC processor, then the
// key-binding JWT against the credential's cnf key — given as a JWK or as a
// did:key / did:jwk kid, the form the Veramo issuer uses and irmago's own
// verifier check does not yet resolve — with the expected nonce and audience
// (the verifier's client_id). A presentation that fails is still decoded so the
// page can show what arrived next to why it was refused.
func VerifyVPToken(token map[string][]string, issuerTrust eudijwt.X509VerificationContext, nonce, clientID string) []Presented {
	processor := sdjwtvc.NewVerifierVerificationProcessor(false, sdjwtvc.SdJwtVcVerificationContext{
		X509VerificationContext: issuerTrust,
		Clock:                   eudijwt.NewSystemClock(),
		JwtVerifier:             sdjwt.NewJwxJwtVerifier(),
	})
	var out []Presented
	for queryID, presentations := range token {
		for _, raw := range presentations {
			p := Presented{QueryID: queryID, Raw: raw}
			verified, err := verifyPresentation(processor, raw, nonce, clientID)
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

// verifyPresentation splits the KB-JWT off, lets irmago verify the SD-JWT VC it
// was bound to, and verifies the KB-JWT over exactly that string.
func verifyPresentation(processor *sdjwtvc.VerifierVerificationProcessor, raw, nonce, clientID string) (*sdjwtvc.VerifiedSdJwtVc, error) {
	idx := strings.LastIndex(raw, "~")
	if idx < 0 {
		return nil, errors.New("presentation has no '~' separator")
	}
	sdJwt, kbJwt := raw[:idx+1], raw[idx+1:]
	if kbJwt == "" {
		return nil, errors.New("presentation carries no key-binding JWT")
	}
	verified, err := processor.ParseAndVerifySdJwtVc(sdjwtvc.SdJwtVcKb(sdJwt))
	if err != nil {
		return nil, err
	}
	var payload map[string]any
	if err := decodeJWTPayload(strings.SplitN(sdJwt, "~", 2)[0], &payload); err != nil {
		return nil, fmt.Errorf("issuer JWT payload: %w", err)
	}
	if err := verifyKeyBinding(kbJwt, sdJwt, payload, nonce, clientID, time.Now()); err != nil {
		return nil, fmt.Errorf("key binding: %w", err)
	}
	return verified, nil
}

// verifyKeyBinding checks a KB-JWT (RFC draft-ietf-oauth-selective-disclosure-jwt
// §4.3): ES256 signature by the credential's cnf key, typ kb+jwt, sd_hash over the
// presented SD-JWT (sha-256, the _sd_alg default), nonce, audience and a sane iat.
func verifyKeyBinding(kbJwt, sdJwt string, issuerPayload map[string]any, nonce, audience string, now time.Time) error {
	if alg, ok := issuerPayload["_sd_alg"].(string); ok && alg != sdHashAlgorithm {
		return fmt.Errorf("unsupported _sd_alg %q", alg)
	}
	holderKey, err := holderKeyFromCnf(issuerPayload["cnf"])
	if err != nil {
		return err
	}
	var header struct {
		Alg string `json:"alg"`
		Typ string `json:"typ"`
	}
	if err := decodeJWTHeader(kbJwt, &header); err != nil {
		return fmt.Errorf("header: %w", err)
	}
	if header.Typ != kbJwtTyp {
		return fmt.Errorf("typ %q, want %s", header.Typ, kbJwtTyp)
	}
	alg, ok := jwa.LookupSignatureAlgorithm(header.Alg)
	if !ok || alg != jwa.ES256() {
		return fmt.Errorf("unsupported alg %q", header.Alg)
	}
	payloadJSON, err := jws.Verify([]byte(kbJwt), jws.WithKey(alg, holderKey))
	if err != nil {
		return fmt.Errorf("signature does not verify with the cnf key: %w", err)
	}
	var claims struct {
		SDHash string `json:"sd_hash"`
		Nonce  string `json:"nonce"`
		Aud    string `json:"aud"`
		IAT    int64  `json:"iat"`
	}
	if err := json.Unmarshal(payloadJSON, &claims); err != nil {
		return err
	}
	sum := sha256.Sum256([]byte(sdJwt))
	if want := base64.RawURLEncoding.EncodeToString(sum[:]); claims.SDHash != want {
		return errors.New("sd_hash does not match the presented SD-JWT")
	}
	if claims.Nonce != nonce {
		return errors.New("nonce does not match the request")
	}
	if claims.Aud != audience {
		return fmt.Errorf("aud %q, want the client_id %q", claims.Aud, audience)
	}
	if claims.IAT == 0 || time.Unix(claims.IAT, 0).After(now.Add(kbJwtMaxSkew)) {
		return errors.New("iat missing or in the future")
	}
	return nil
}

// holderKeyFromCnf resolves the confirmation key: an embedded JWK, or a kid that
// is a did:key or did:jwk carrying the key itself.
func holderKeyFromCnf(cnf any) (jwk.Key, error) {
	fields, ok := cnf.(map[string]any)
	if !ok {
		return nil, errors.New("issuer JWT has no cnf claim")
	}
	if raw, ok := fields["jwk"]; ok {
		b, err := json.Marshal(raw)
		if err != nil {
			return nil, err
		}
		return jwk.ParseKey(b)
	}
	kid, _ := fields["kid"].(string)
	switch {
	case strings.HasPrefix(kid, didjwk.Prefix):
		return didjwk.Resolve(kid)
	case strings.HasPrefix(kid, didkey.Prefix):
		pub, err := didkey.Resolve(kid)
		if err != nil {
			return nil, err
		}
		return jwk.Import(pub)
	default:
		return nil, fmt.Errorf("cnf has neither jwk nor a resolvable kid (%q)", kid)
	}
}

func decodeJWTHeader(token string, into any) error {
	return decodeSegment(strings.SplitN(token, ".", 2)[0], into)
}

func decodeJWTPayload(token string, into any) error {
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return errors.New("not a compact JWS")
	}
	return decodeSegment(parts[1], into)
}

func decodeSegment(seg string, into any) error {
	b, err := base64.RawURLEncoding.DecodeString(seg)
	if err != nil {
		return err
	}
	return json.Unmarshal(b, into)
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
