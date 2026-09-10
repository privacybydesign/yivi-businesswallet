package devverifier

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/lestrrat-go/jwx/v3/jwa"
	"github.com/lestrrat-go/jwx/v3/jwk"
	"github.com/privacybydesign/irmago/eudi/openid4vp"
)

const (
	// requestObjectTyp is the JAR media type in the JWS header (RFC 9101).
	requestObjectTyp = "oauth-authz-req+jwt"
	// selfIssuedAudience is the audience OpenID4VP requests carry when the wallet
	// has no own identifier (OpenID4VP 1.0 §5.1); the hosted Yivi verifier sends it.
	selfIssuedAudience = "https://self-issued.me/v2"
	requestLifetime    = 5 * time.Minute
	nonceBytes         = 16
	// contentEncryption is the JWE content encryption the dev verifier accepts
	// for direct_post.jwt (the OpenID4VP default).
	contentEncryption = "A128GCM"
)

// Request is what a verifier decides for one Authorization Request.
type Request struct {
	Nonce        string
	State        string
	ResponseURI  string
	ResponseMode string
	DCQLQuery    json.RawMessage
	// EncryptionKey, when set, is published in client_metadata.jwks so a
	// direct_post.jwt response can be encrypted to it.
	EncryptionKey *ecdsa.PublicKey
	// ClientName is shown by the wallet as the verifier's name when the
	// certificate carries no Yivi requestor data.
	ClientName string
}

// RandomToken returns a fresh URL-safe random string for a nonce or state.
func RandomToken() (string, error) {
	b := make([]byte, nonceBytes)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

// NewEncryptionKey mints the ephemeral P-256 key a direct_post.jwt response is
// encrypted to.
func NewEncryptionKey() (*ecdsa.PrivateKey, error) {
	return ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
}

// SimpleDCQL builds a single-credential DCQL query for vct asking for the given
// top-level claims (all of them when claims is empty).
func SimpleDCQL(queryID, vct string, claims []string) (json.RawMessage, error) {
	cred := map[string]any{
		"id":     queryID,
		"format": "dc+sd-jwt",
		"meta":   map[string]any{"vct_values": []string{vct}},
	}
	if len(claims) > 0 {
		paths := make([]map[string]any, 0, len(claims))
		for _, c := range claims {
			paths = append(paths, map[string]any{"path": []string{c}})
		}
		cred["claims"] = paths
	}
	return json.Marshal(map[string]any{"credentials": []any{cred}})
}

// SignRequestObject signs the Authorization Request as a JAR with id's key, the
// chain in x5c and the client_id bound to id's DNS name — the exact shape the
// hosted Yivi verifier emits and irmago's relying-party validation verifies.
func SignRequestObject(id Identity, req Request) (string, error) {
	var dcql any
	if err := json.Unmarshal(req.DCQLQuery, &dcql); err != nil {
		return "", fmt.Errorf("devverifier: dcql_query: %w", err)
	}
	now := time.Now()
	claims := jwt.MapClaims{
		"aud":           selfIssuedAudience,
		"iat":           now.Unix(),
		"exp":           now.Add(requestLifetime).Unix(),
		"client_id":     id.ClientID(),
		"response_type": string(openid4vp.ResponseType_VpToken),
		"response_mode": req.ResponseMode,
		"response_uri":  req.ResponseURI,
		"nonce":         req.Nonce,
		"state":         req.State,
		"dcql_query":    dcql,
	}
	metadata := map[string]any{
		"vp_formats_supported": map[string]any{
			"dc+sd-jwt": map[string]any{"sd-jwt_alg_values": []string{"ES256"}, "kb-jwt_alg_values": []string{"ES256"}},
		},
	}
	if req.ClientName != "" {
		metadata["client_name"] = req.ClientName
	}
	if req.EncryptionKey != nil {
		set, err := encryptionJWKS(req.EncryptionKey)
		if err != nil {
			return "", err
		}
		metadata["jwks"] = set
		metadata["encrypted_response_enc_values_supported"] = []string{contentEncryption}
	}
	claims["client_metadata"] = metadata

	token := jwt.NewWithClaims(jwt.SigningMethodES256, claims)
	token.Header["typ"] = requestObjectTyp
	token.Header["x5c"] = id.x5c()
	signed, err := token.SignedString(id.Key)
	if err != nil {
		return "", fmt.Errorf("devverifier: sign request object: %w", err)
	}
	return signed, nil
}

// encryptionJWKS publishes pub as a JWK Set entry with the algorithm and use a
// wallet needs to pick it: ECDH-ES key agreement, enc.
func encryptionJWKS(pub *ecdsa.PublicKey) (any, error) {
	key, err := jwk.Import(pub)
	if err != nil {
		return nil, err
	}
	thumb, err := key.Thumbprint(cryptoSHA256)
	if err != nil {
		return nil, err
	}
	for k, v := range map[string]any{
		jwk.KeyIDKey:     base64.RawURLEncoding.EncodeToString(thumb),
		jwk.AlgorithmKey: jwa.ECDH_ES(),
		jwk.KeyUsageKey:  jwk.ForEncryption,
	} {
		if err := key.Set(k, v); err != nil {
			return nil, err
		}
	}
	set := jwk.NewSet()
	if err := set.AddKey(key); err != nil {
		return nil, err
	}
	raw, err := json.Marshal(set)
	if err != nil {
		return nil, err
	}
	var out any
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, err
	}
	return out, nil
}
