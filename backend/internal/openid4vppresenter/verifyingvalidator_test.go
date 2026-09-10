package openid4vppresenter

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/privacybydesign/yivi-businesswallet/backend/internal/devverifier"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/eudiholder"
)

func signedRequest(t *testing.T, id devverifier.Identity, mutate func(*devverifier.Request)) []byte {
	t.Helper()
	dcql, err := devverifier.SimpleDCQL("kvk", "nl.kvk.registration", []string{"company_name"})
	if err != nil {
		t.Fatal(err)
	}
	req := devverifier.Request{
		Nonce:        "n-0S6_WzA2Mj",
		State:        "abc",
		ResponseURI:  "https://verifier.test/response",
		ResponseMode: "direct_post",
		DCQLQuery:    dcql,
	}
	if mutate != nil {
		mutate(&req)
	}
	jar, err := devverifier.SignRequestObject(id, req)
	if err != nil {
		t.Fatal(err)
	}
	return []byte(jar)
}

func newVerifying(t *testing.T, id devverifier.Identity) *VerifyingValidator {
	t.Helper()
	trust, err := eudiholder.NewVerifierTrust(id.RootPEM(), false)
	if err != nil {
		t.Fatal(err)
	}
	return NewVerifyingValidator(trust, Policy{})
}

// A request signed by a relying party whose root the wallet trusts, for the
// client_id its certificate's SAN carries, is accepted; its identity is the
// certificate subject unless the request names the verifier.
func TestVerifyingValidatorAcceptsTrustedChain(t *testing.T) {
	id, err := devverifier.NewIdentity("verifier.test")
	if err != nil {
		t.Fatal(err)
	}
	v := newVerifying(t, id)

	ro, err := v.Validate(context.Background(), id.ClientID(), signedRequest(t, id, nil))
	if err != nil {
		t.Fatalf("Validate: %v", err)
	}
	if ro.VerifierIdentity != "verifier.test" || ro.Nonce != "n-0S6_WzA2Mj" || ro.State != "abc" || ro.Raw == "" {
		t.Errorf("RequestObject = %+v", ro)
	}
	if !strings.Contains(string(ro.DCQLQuery), `"nl.kvk.registration"`) {
		t.Errorf("DCQLQuery not carried over: %s", ro.DCQLQuery)
	}

	named, err := v.Validate(context.Background(), id.ClientID(), signedRequest(t, id, func(r *devverifier.Request) { r.ClientName = "Acme Verifier" }))
	if err != nil {
		t.Fatal(err)
	}
	if named.VerifierIdentity != "Acme Verifier" {
		t.Errorf("VerifierIdentity = %q, want the client_name", named.VerifierIdentity)
	}
}

func TestVerifyingValidatorRejects(t *testing.T) {
	id, err := devverifier.NewIdentity("verifier.test")
	if err != nil {
		t.Fatal(err)
	}
	other, err := devverifier.NewIdentity("verifier.test")
	if err != nil {
		t.Fatal(err)
	}
	v := newVerifying(t, id)
	good := signedRequest(t, id, nil)

	tampered := []byte(strings.Replace(string(good), ".", ".A", 1))

	cases := []struct {
		name     string
		clientID string
		body     []byte
	}{
		{"client_id names another host than the SAN", "x509_san_dns:impostor.test", good},
		{"chain ends in an untrusted root", other.ClientID(), signedRequest(t, other, nil)},
		{"tampered payload", id.ClientID(), tampered},
		{"structural rule still applies (http response_uri)", id.ClientID(), signedRequest(t, id, func(r *devverifier.Request) { r.ResponseURI = "http://verifier.test/r" })},
		{"direct_post.jwt without keys", id.ClientID(), signedRequest(t, id, func(r *devverifier.Request) { r.ResponseMode = "direct_post.jwt" })},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := v.Validate(context.Background(), tc.clientID, tc.body); !errors.Is(err, ErrInvalidRequestObject) {
				t.Fatalf("err = %v, want ErrInvalidRequestObject", err)
			}
		})
	}
}

func TestVerifyingValidatorAcceptsEncryptedResponseMode(t *testing.T) {
	id, err := devverifier.NewIdentity("verifier.test")
	if err != nil {
		t.Fatal(err)
	}
	key, err := devverifier.NewEncryptionKey()
	if err != nil {
		t.Fatal(err)
	}
	ro, err := newVerifying(t, id).Validate(context.Background(), id.ClientID(), signedRequest(t, id, func(r *devverifier.Request) {
		r.ResponseMode = "direct_post.jwt"
		r.EncryptionKey = &key.PublicKey
	}))
	if err != nil {
		t.Fatalf("Validate: %v", err)
	}
	if ro.ResponseMode != "direct_post.jwt" {
		t.Errorf("ResponseMode = %q", ro.ResponseMode)
	}
}
