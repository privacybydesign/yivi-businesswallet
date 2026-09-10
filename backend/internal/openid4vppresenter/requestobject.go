package openid4vppresenter

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/privacybydesign/irmago/eudi/openid4vp"
)

// Validator turns a fetched Request Object into the validated RequestObject the
// slice persists, or rejects it. The service never stores anything a Validator
// did not return, so nothing unvalidated reaches the org-picker UI. Two
// implementations: VerifyingValidator (the default — signature and x509_san_dns
// chain verification through irmago) and UnverifiedDecoder (structural only,
// dev / CI).
type Validator interface {
	// Validate checks requestObject against clientID — the client_id the verifier
	// invoked the wallet with, which must be the one the Request Object binds to.
	Validate(ctx context.Context, clientID string, requestObject []byte) (RequestObject, error)
	// ClientIDPrefixes lists the client_id prefixes this validator can bind, for
	// the wallet-metadata document.
	ClientIDPrefixes() []string
}

// requestFields is what both validators check once the JAR is decoded: the
// protocol constraints this slice can answer, independent of who signed it.
type requestFields struct {
	clientID       string
	responseType   string
	responseMode   string
	responseURI    string
	nonce          string
	state          string
	dcqlQuery      json.RawMessage
	encryptionKeys json.RawMessage
}

// checkFields applies the protocol constraints and assembles the RequestObject.
func checkFields(policy Policy, expectedClientID, identity, raw string, f requestFields) (RequestObject, error) {
	if f.clientID != expectedClientID {
		return RequestObject{}, fmt.Errorf("%w: client_id does not match the invocation", ErrInvalidRequestObject)
	}
	if f.responseType != string(openid4vp.ResponseType_VpToken) {
		return RequestObject{}, fmt.Errorf("%w: unsupported response_type", ErrInvalidRequestObject)
	}
	switch openid4vp.ResponseMode(f.responseMode) {
	case openid4vp.ResponseMode_DirectPost:
	case openid4vp.ResponseMode_DirectPostJwt:
		// The encrypted form needs the verifier's keys; refusing here keeps a
		// request the response step could never answer out of the picker.
		if len(f.encryptionKeys) == 0 {
			return RequestObject{}, fmt.Errorf("%w: direct_post.jwt without client_metadata.jwks", ErrInvalidRequestObject)
		}
	default:
		return RequestObject{}, fmt.Errorf("%w: unsupported response_mode", ErrInvalidRequestObject)
	}
	if _, err := policy.checkURL(f.responseURI); err != nil {
		return RequestObject{}, fmt.Errorf("%w: response_uri: %w", ErrInvalidRequestObject, err)
	}
	if !validNonce(f.nonce) {
		return RequestObject{}, fmt.Errorf("%w: missing or malformed nonce", ErrInvalidRequestObject)
	}
	if err := checkDCQL(f.dcqlQuery); err != nil {
		return RequestObject{}, fmt.Errorf("%w: dcql_query: %w", ErrInvalidRequestObject, err)
	}
	return RequestObject{
		ClientID:         expectedClientID,
		VerifierIdentity: identity,
		Nonce:            f.nonce,
		State:            f.state,
		ResponseURI:      f.responseURI,
		ResponseMode:     f.responseMode,
		DCQLQuery:        f.dcqlQuery,
		Raw:              raw,
	}, nil
}

// UnverifiedDecoder validates a Request Object structurally — a well-formed,
// non-"none" JWS whose payload binds to the invoking client_id, carries the
// fields a response needs, and is not expired — without verifying its signature
// or the client_id's certificate chain. Dev / CI only, behind
// OPENID4VP_PRESENTER_ALLOW_UNVERIFIED_REQUEST_OBJECTS; VerifyingValidator is
// the default.
type UnverifiedDecoder struct {
	policy Policy
	now    func() time.Time
}

func NewUnverifiedDecoder(policy Policy) *UnverifiedDecoder {
	return &UnverifiedDecoder{policy: policy, now: time.Now}
}

// The client_id prefix the structural decoder resolves an identity for. Verifiers
// using pre-registered client ids or other prefixes are refused: their identity
// cannot be shown to the person picking an organization.
var unverifiedClientIDPrefixes = []string{
	strings.TrimSuffix(string(openid4vp.ClientIdentifierPrefix_X509SanDns), ":"),
}

func (*UnverifiedDecoder) ClientIDPrefixes() []string { return unverifiedClientIDPrefixes }

// jarPayload is the subset of the Authorization Request the structural decoder
// reads. It is deliberately not irmago's AuthorizationRequest: that type models
// what a wallet *processes*; here only what is persisted and answered is decoded.
type jarPayload struct {
	ClientID       string          `json:"client_id"`
	ResponseType   string          `json:"response_type"`
	ResponseMode   string          `json:"response_mode"`
	ResponseURI    string          `json:"response_uri"`
	Nonce          string          `json:"nonce"`
	State          string          `json:"state"`
	DCQLQuery      json.RawMessage `json:"dcql_query"`
	Exp            *int64          `json:"exp"`
	ClientMetadata *struct {
		JWKS json.RawMessage `json:"jwks"`
	} `json:"client_metadata"`
}

type jarHeader struct {
	Alg string `json:"alg"`
}

const (
	jwsParts = 3
	algNone  = "none"
)

func (d *UnverifiedDecoder) Validate(_ context.Context, clientID string, requestObject []byte) (RequestObject, error) {
	raw := strings.TrimSpace(string(requestObject))
	parts := strings.Split(raw, ".")
	if len(parts) != jwsParts {
		return RequestObject{}, fmt.Errorf("%w: not a compact JWS", ErrInvalidRequestObject)
	}
	var header jarHeader
	if err := decodeSegment(parts[0], &header); err != nil {
		return RequestObject{}, fmt.Errorf("%w: header: %w", ErrInvalidRequestObject, err)
	}
	if header.Alg == "" || strings.EqualFold(header.Alg, algNone) {
		return RequestObject{}, fmt.Errorf("%w: unsigned request object", ErrInvalidRequestObject)
	}
	if parts[2] == "" {
		return RequestObject{}, fmt.Errorf("%w: empty signature", ErrInvalidRequestObject)
	}
	var p jarPayload
	if err := decodeSegment(parts[1], &p); err != nil {
		return RequestObject{}, fmt.Errorf("%w: payload: %w", ErrInvalidRequestObject, err)
	}
	if p.Exp != nil && d.now().After(time.Unix(*p.Exp, 0)) {
		return RequestObject{}, fmt.Errorf("%w: request object expired", ErrInvalidRequestObject)
	}
	identity, err := verifierIdentity(clientID)
	if err != nil {
		return RequestObject{}, err
	}
	f := requestFields{
		clientID:     p.ClientID,
		responseType: p.ResponseType,
		responseMode: p.ResponseMode,
		responseURI:  p.ResponseURI,
		nonce:        p.Nonce,
		state:        p.State,
		dcqlQuery:    p.DCQLQuery,
	}
	if p.ClientMetadata != nil {
		f.encryptionKeys = p.ClientMetadata.JWKS
	}
	return checkFields(d.policy, clientID, identity, raw, f)
}

func decodeSegment(seg string, into any) error {
	raw, err := base64.RawURLEncoding.DecodeString(seg)
	if err != nil {
		return err
	}
	return json.Unmarshal(raw, into)
}

// verifierIdentity resolves "who is asking" from the client_id binding alone. For
// x509_san_dns the identity is the DNS name the verifier's certificate must carry
// as a SAN; whether it does is the verifying validator's check.
func verifierIdentity(clientID string) (string, error) {
	prefix := string(openid4vp.ClientIdentifierPrefix_X509SanDns)
	if !strings.HasPrefix(clientID, prefix) {
		return "", fmt.Errorf("%w: unsupported client_id prefix", ErrInvalidRequestObject)
	}
	dns := strings.TrimPrefix(clientID, prefix)
	if dns == "" || strings.ContainsAny(dns, "/?#@ ") {
		return "", fmt.Errorf("%w: malformed x509_san_dns client_id", ErrInvalidRequestObject)
	}
	return dns, nil
}

// validNonce mirrors OpenID4VP's requirement that the nonce is a non-empty string
// of ASCII URL-safe characters.
func validNonce(nonce string) bool {
	if nonce == "" {
		return false
	}
	for _, c := range nonce {
		switch {
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9':
		case c == '-', c == '.', c == '_', c == '~':
		default:
			return false
		}
	}
	return true
}

// checkDCQL requires a DCQL query object with at least one credential query; the
// match itself is the holder's.
func checkDCQL(raw json.RawMessage) error {
	if len(raw) == 0 {
		return errors.New("missing")
	}
	var q struct {
		Credentials []json.RawMessage `json:"credentials"`
	}
	if err := json.Unmarshal(raw, &q); err != nil {
		return err
	}
	if len(q.Credentials) == 0 {
		return errors.New("no credential queries")
	}
	return nil
}
