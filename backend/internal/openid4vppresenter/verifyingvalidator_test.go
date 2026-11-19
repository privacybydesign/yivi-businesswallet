package openid4vppresenter

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/privacybydesign/yivi-businesswallet/backend/internal/devverifier"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/eudiholder"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/relyingparty"
)

func signedRequest(t *testing.T, id devverifier.Identity, mutate func(*relyingparty.Request)) []byte {
	t.Helper()
	dcql, err := relyingparty.SimpleDCQL("kvk", "nl.kvk.registration", []string{"company_name"})
	if err != nil {
		t.Fatal(err)
	}
	req := relyingparty.Request{
		Nonce:        "n-0S6_WzA2Mj",
		State:        "abc",
		ResponseURI:  "https://verifier.test/response",
		ResponseMode: "direct_post",
		DCQLQuery:    dcql,
	}
	if mutate != nil {
		mutate(&req)
	}
	jar, err := relyingparty.SignRequestObject(id.Signer(), req)
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

	named, err := v.Validate(context.Background(), id.ClientID(), signedRequest(t, id, func(r *relyingparty.Request) { r.ClientName = "Acme Verifier" }))
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
		{"structural rule still applies (http response_uri)", id.ClientID(), signedRequest(t, id, func(r *relyingparty.Request) { r.ResponseURI = "http://verifier.test/r" })},
		{"direct_post.jwt without keys", id.ClientID(), signedRequest(t, id, func(r *relyingparty.Request) { r.ResponseMode = "direct_post.jwt" })},
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
	key, err := relyingparty.NewEncryptionKey()
	if err != nil {
		t.Fatal(err)
	}
	ro, err := newVerifying(t, id).Validate(context.Background(), id.ClientID(), signedRequest(t, id, func(r *relyingparty.Request) {
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

// An organization identity minted by a requester CA (x509_hash, no DNS name)
// verifies against that CA's root, and the certificate's own statements — the
// organization and the QERDS address it may send from — come out as the
// certified fields ReceiveFromQERDS binds on, whatever client_name the request
// claims for itself.
func TestVerifyingValidatorCertifiesOrganizationIdentity(t *testing.T) {
	ca, err := relyingparty.NewCA("Test Requester CA")
	if err != nil {
		t.Fatal(err)
	}
	signer, err := ca.IssueOrganization("Acme B.V.", "acme@qerds.localhost", time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(signer.ClientID, "x509_hash:") {
		t.Fatalf("client_id = %q, want an x509_hash identifier", signer.ClientID)
	}
	trust, err := eudiholder.NewVerifierTrust(ca.RootPEM(), false)
	if err != nil {
		t.Fatal(err)
	}
	dcql, err := relyingparty.SimpleDCQL("kvk", "nl.kvk.registration", nil)
	if err != nil {
		t.Fatal(err)
	}
	jar, err := relyingparty.SignRequestObject(signer, relyingparty.Request{
		Nonce: "n-1", State: "s", ResponseURI: "https://acme.test/response", ResponseMode: "direct_post",
		DCQLQuery: dcql, ClientName: "Someone Else",
	})
	if err != nil {
		t.Fatal(err)
	}
	ro, err := NewVerifyingValidator(trust, Policy{}).Validate(context.Background(), signer.ClientID, []byte(jar))
	if err != nil {
		t.Fatalf("Validate: %v", err)
	}
	if ro.CertifiedName != "Acme B.V." {
		t.Errorf("CertifiedName = %q, want the certificate's organization", ro.CertifiedName)
	}
	if len(ro.CertifiedAddresses) != 1 || ro.CertifiedAddresses[0] != "acme@qerds.localhost" {
		t.Errorf("CertifiedAddresses = %v, want the sending address", ro.CertifiedAddresses)
	}

	// Another CA's identity for the same organization is not trusted.
	other, err := relyingparty.NewCA("Other CA")
	if err != nil {
		t.Fatal(err)
	}
	forged, err := other.IssueOrganization("Acme B.V.", "acme@qerds.localhost", time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	forgedJAR, err := relyingparty.SignRequestObject(forged, relyingparty.Request{
		Nonce: "n-1", State: "s", ResponseURI: "https://acme.test/response", ResponseMode: "direct_post", DCQLQuery: dcql,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := NewVerifyingValidator(trust, Policy{}).Validate(context.Background(), forged.ClientID, []byte(forgedJAR)); !errors.Is(err, ErrInvalidRequestObject) {
		t.Errorf("untrusted CA: err = %v, want %v", err, ErrInvalidRequestObject)
	}
}
