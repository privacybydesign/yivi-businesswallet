package openid4vppresenter

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"github.com/lestrrat-go/jwx/v3/jwa"
	"github.com/lestrrat-go/jwx/v3/jwe"
	"github.com/lestrrat-go/jwx/v3/jwk"
	"github.com/privacybydesign/irmago/eudi/openid4vp"
)

// Responder delivers the Authorization Response to the verifier's response_uri
// (OpenID4VP 1.0 §8.2): as a form POST carrying vp_token and state for
// response_mode=direct_post, or as a single encrypted `response` JWE for
// direct_post.jwt, encrypted to a key from the request's client_metadata.jwks.
type Responder struct {
	policy Policy
	client *http.Client
}

func NewResponder(policy Policy) *Responder {
	return &Responder{policy: policy, client: newClient(policy)}
}

// directPostAck is what a verifier may answer a direct_post with: where to send
// the browser next.
type directPostAck struct {
	RedirectURI string `json:"redirect_uri"`
}

// responseMetadata is the part of the persisted Request Object the response step
// needs: the verifier's encryption keys and content-encryption preferences.
type responseMetadata struct {
	ClientMetadata *struct {
		JWKS                                json.RawMessage `json:"jwks"`
		EncryptedResponseEncValuesSupported []string        `json:"encrypted_response_enc_values_supported"`
	} `json:"client_metadata"`
}

// Send delivers token for transaction t and returns the verifier's redirect_uri,
// if it supplied one. The response_uri was validated when the Request Object was,
// but the policy is re-applied here: the row is the only place it lives in
// between, and a defence that depends on the row being untouched is not one.
func (r *Responder) Send(ctx context.Context, t Transaction, token openid4vp.VpToken) (string, error) {
	u, err := r.policy.checkURL(t.ResponseURI)
	if err != nil {
		return "", fmt.Errorf("response_uri: %w", err)
	}
	form, err := r.responseForm(t, token)
	if err != nil {
		return "", err
	}
	ctx, cancel := context.WithTimeout(ctx, requestTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, u.String(), strings.NewReader(form.Encode()))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	body, err := do(r.client, req)
	if err != nil {
		return "", err
	}
	return redirectFromAck(body), nil
}

// responseForm builds the Authorization Response parameters for t's response_mode.
func (r *Responder) responseForm(t Transaction, token openid4vp.VpToken) (url.Values, error) {
	vpToken, err := json.Marshal(token)
	if err != nil {
		return nil, fmt.Errorf("marshal vp_token: %w", err)
	}
	form := url.Values{}
	switch openid4vp.ResponseMode(t.ResponseMode) {
	case openid4vp.ResponseMode_DirectPost:
		form.Set("vp_token", string(vpToken))
		if t.State != "" {
			form.Set("state", t.State)
		}
	case openid4vp.ResponseMode_DirectPostJwt:
		payload := map[string]any{"vp_token": token}
		if t.State != "" {
			payload["state"] = t.State
		}
		encrypted, err := encryptResponse(t.RequestObject, payload)
		if err != nil {
			return nil, err
		}
		form.Set("response", encrypted)
	default:
		return nil, fmt.Errorf("unsupported response_mode %q", t.ResponseMode)
	}
	return form, nil
}

// encryptResponse produces the direct_post.jwt JWE: the payload encrypted to the
// first usable key of the request's client_metadata.jwks, with the verifier's
// preferred content encryption (A128GCM when it states none, per the spec
// default). This mirrors what irmago's wallet client does for the same response
// mode; that code is not exported, so it is reproduced here.
func encryptResponse(requestObject string, payload map[string]any) (string, error) {
	parts := strings.Split(requestObject, ".")
	if len(parts) != jwsParts {
		return "", errors.New("stored request object is not a compact JWS")
	}
	var meta responseMetadata
	if err := decodeSegment(parts[1], &meta); err != nil {
		return "", fmt.Errorf("decode request object: %w", err)
	}
	if meta.ClientMetadata == nil || len(meta.ClientMetadata.JWKS) == 0 {
		return "", errors.New("direct_post.jwt requested without client_metadata.jwks")
	}
	keys, err := jwk.Parse(meta.ClientMetadata.JWKS)
	if err != nil {
		return "", fmt.Errorf("parse client_metadata.jwks: %w", err)
	}
	enc, err := contentEncryption(meta.ClientMetadata.EncryptedResponseEncValuesSupported)
	if err != nil {
		return "", err
	}
	plaintext, err := json.Marshal(payload)
	if err != nil {
		return "", fmt.Errorf("marshal response: %w", err)
	}

	var errs []error
	for i := range keys.Len() {
		key, ok := keys.Key(i)
		if !ok {
			continue
		}
		alg, err := keyAlgorithm(key)
		if err != nil {
			errs = append(errs, err)
			continue
		}
		headers := jwe.NewHeaders()
		if kid, ok := key.KeyID(); ok && kid != "" {
			if err := headers.Set(jwe.KeyIDKey, kid); err != nil {
				errs = append(errs, err)
				continue
			}
		}
		encrypted, err := jwe.Encrypt(plaintext,
			jwe.WithKey(alg, key),
			jwe.WithContentEncryption(enc),
			jwe.WithProtectedHeaders(headers),
		)
		if err != nil {
			errs = append(errs, err)
			continue
		}
		return string(encrypted), nil
	}
	return "", fmt.Errorf("no usable encryption key in client_metadata.jwks: %w", errors.Join(errs...))
}

// keyAlgorithm is the key-management algorithm to encrypt to key with: the key's
// own alg when it states one, else ECDH-ES for an EC key — the algorithm the
// EUDI reference verifier publishes — and an error otherwise.
func keyAlgorithm(key jwk.Key) (jwa.KeyAlgorithm, error) {
	if alg, ok := key.Algorithm(); ok && alg.String() != "" {
		return alg, nil
	}
	if key.KeyType() == jwa.EC() {
		return jwa.ECDH_ES(), nil
	}
	return nil, fmt.Errorf("encryption key %s states no alg", key.KeyType())
}

// contentEncryption picks the first content-encryption algorithm the verifier
// supports that this library knows; none stated means A128GCM (OpenID4VP §8.3).
func contentEncryption(supported []string) (jwa.ContentEncryptionAlgorithm, error) {
	if len(supported) == 0 {
		return jwa.A128GCM(), nil
	}
	for _, name := range supported {
		if alg, ok := jwa.LookupContentEncryptionAlgorithm(name); ok {
			return alg, nil
		}
	}
	return jwa.EmptyContentEncryptionAlgorithm(), fmt.Errorf("no supported content encryption among %v", supported)
}

// redirectFromAck reads the optional redirect_uri from a direct_post
// acknowledgement. An acknowledgement that is not JSON is still an
// acknowledgement; only the optional redirect is lost. The browser is sent to
// the URI, so anything that is not an absolute http(s) URL is dropped — a
// verifier must not be able to hand back a javascript: or data: target.
func redirectFromAck(body []byte) string {
	if len(strings.TrimSpace(string(body))) == 0 {
		return ""
	}
	var ack directPostAck
	if err := json.Unmarshal(body, &ack); err != nil || ack.RedirectURI == "" {
		return ""
	}
	redirect, err := url.Parse(ack.RedirectURI)
	if err != nil || !redirect.IsAbs() || (redirect.Scheme != "https" && redirect.Scheme != "http") {
		return ""
	}
	return redirect.String()
}
