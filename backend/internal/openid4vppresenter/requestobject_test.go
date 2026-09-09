package openid4vppresenter

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"
)

const testClientID = "x509_san_dns:verifier.example.com"

func jws(t *testing.T, header, payload map[string]any) []byte {
	t.Helper()
	enc := func(v map[string]any) string {
		b, err := json.Marshal(v)
		if err != nil {
			t.Fatalf("marshal: %v", err)
		}
		return base64.RawURLEncoding.EncodeToString(b)
	}
	return []byte(enc(header) + "." + enc(payload) + ".c2ln")
}

func validPayload() map[string]any {
	return map[string]any{
		"client_id":     testClientID,
		"response_type": "vp_token",
		"response_mode": "direct_post",
		"response_uri":  "https://verifier.example.com/response",
		"nonce":         "n-0S6_WzA2Mj",
		"state":         "abc",
		"dcql_query": map[string]any{
			"credentials": []map[string]any{{"id": "kvk", "format": "dc+sd-jwt"}},
		},
	}
}

func TestUnverifiedDecoderAcceptsWellFormedRequest(t *testing.T) {
	d := NewUnverifiedDecoder(Policy{})
	ro, err := d.Validate(context.Background(), testClientID, jws(t, map[string]any{"alg": "ES256"}, validPayload()))
	if err != nil {
		t.Fatalf("Validate: %v", err)
	}
	if ro.VerifierIdentity != "verifier.example.com" {
		t.Errorf("VerifierIdentity = %q", ro.VerifierIdentity)
	}
	if ro.Nonce != "n-0S6_WzA2Mj" || ro.State != "abc" || ro.ResponseURI != "https://verifier.example.com/response" {
		t.Errorf("fields not carried over: %+v", ro)
	}
	if len(ro.DCQLQuery) == 0 {
		t.Error("DCQLQuery empty")
	}
}

func TestUnverifiedDecoderRejects(t *testing.T) {
	d := NewUnverifiedDecoder(Policy{})
	d.now = func() time.Time { return time.Unix(2_000_000_000, 0) }
	header := map[string]any{"alg": "ES256"}
	with := func(mut func(p map[string]any)) []byte {
		p := validPayload()
		mut(p)
		return jws(t, header, p)
	}
	cases := []struct {
		name string
		body []byte
	}{
		{"not a JWS", []byte("nope")},
		{"alg none", jws(t, map[string]any{"alg": "none"}, validPayload())},
		{"empty signature", []byte(strings.TrimSuffix(string(jws(t, header, validPayload())), "c2ln"))},
		{"client_id mismatch", with(func(p map[string]any) { p["client_id"] = "x509_san_dns:other.example.com" })},
		{"unsupported prefix", with(func(p map[string]any) { p["client_id"] = "pre-registered" })},
		{"direct_post.jwt without keys", with(func(p map[string]any) { p["response_mode"] = "direct_post.jwt" })},
		{"response_type", with(func(p map[string]any) { p["response_type"] = "code" })},
		{"http response_uri", with(func(p map[string]any) { p["response_uri"] = "http://verifier.example.com/r" })},
		{"missing nonce", with(func(p map[string]any) { delete(p, "nonce") })},
		{"nonce not url-safe", with(func(p map[string]any) { p["nonce"] = "a b" })},
		{"missing dcql", with(func(p map[string]any) { delete(p, "dcql_query") })},
		{"empty dcql", with(func(p map[string]any) { p["dcql_query"] = map[string]any{"credentials": []any{}} })},
		{"expired", with(func(p map[string]any) { p["exp"] = 1_000_000_000 })},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			clientID := testClientID
			if tc.name == "unsupported prefix" {
				clientID = "pre-registered"
			}
			_, err := d.Validate(context.Background(), clientID, tc.body)
			if !errors.Is(err, ErrInvalidRequestObject) {
				t.Fatalf("err = %v, want ErrInvalidRequestObject", err)
			}
		})
	}
}

func TestUnverifiedDecoderAllowsHTTPResponseURIOnlyWhenInsecure(t *testing.T) {
	p := validPayload()
	p["response_uri"] = "http://localhost:9999/response"
	body := jws(t, map[string]any{"alg": "ES256"}, p)
	if _, err := NewUnverifiedDecoder(Policy{}).Validate(context.Background(), testClientID, body); err == nil {
		t.Fatal("http response_uri accepted under the default policy")
	}
	if _, err := NewUnverifiedDecoder(Policy{AllowInsecureHTTP: true}).Validate(context.Background(), testClientID, body); err != nil {
		t.Fatalf("http response_uri refused under AllowInsecureHTTP: %v", err)
	}
}

// direct_post.jwt is accepted only when the verifier supplied encryption keys;
// without them the response step could never answer, so the request is refused
// before it reaches the picker.
func TestUnverifiedDecoderDirectPostJWTNeedsKeys(t *testing.T) {
	d := NewUnverifiedDecoder(Policy{})
	p := validPayload()
	p["response_mode"] = "direct_post.jwt"
	if _, err := d.Validate(context.Background(), testClientID, jws(t, map[string]any{"alg": "ES256"}, p)); !errors.Is(err, ErrInvalidRequestObject) {
		t.Fatalf("direct_post.jwt without jwks: err = %v, want ErrInvalidRequestObject", err)
	}
	p["client_metadata"] = map[string]any{"jwks": map[string]any{"keys": []map[string]any{{"kty": "EC", "crv": "P-256", "x": "x", "y": "y"}}}}
	ro, err := d.Validate(context.Background(), testClientID, jws(t, map[string]any{"alg": "ES256"}, p))
	if err != nil {
		t.Fatalf("direct_post.jwt with jwks: %v", err)
	}
	if ro.ResponseMode != "direct_post.jwt" || ro.Raw == "" {
		t.Errorf("RequestObject = %+v", ro)
	}
}
