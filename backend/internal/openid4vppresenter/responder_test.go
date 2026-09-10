package openid4vppresenter

import (
	"encoding/json"
	"testing"

	"github.com/privacybydesign/irmago/eudi/openid4vp"

	"github.com/privacybydesign/yivi-businesswallet/backend/internal/devverifier"
)

func TestResponderDirectPostForm(t *testing.T) {
	r := NewResponder(Policy{})
	form, err := r.responseForm(Transaction{ResponseMode: "direct_post", State: "s1"}, openid4vp.VpToken{"kvk": {"a~b~"}})
	if err != nil {
		t.Fatal(err)
	}
	if form.Get("state") != "s1" {
		t.Errorf("state = %q", form.Get("state"))
	}
	var token map[string][]string
	if err := json.Unmarshal([]byte(form.Get("vp_token")), &token); err != nil || token["kvk"][0] != "a~b~" {
		t.Errorf("vp_token = %q (%v)", form.Get("vp_token"), err)
	}
	if form.Has("response") {
		t.Error("direct_post must not carry an encrypted response")
	}
}

// direct_post.jwt: the response is a JWE to the key the verifier published in
// the request object's client_metadata, and only the verifier can open it.
func TestResponderDirectPostJWTEncryptsToVerifierKey(t *testing.T) {
	id, err := devverifier.NewIdentity("verifier.test")
	if err != nil {
		t.Fatal(err)
	}
	key, err := devverifier.NewEncryptionKey()
	if err != nil {
		t.Fatal(err)
	}
	dcql, err := devverifier.SimpleDCQL("kvk", "nl.kvk.registration", nil)
	if err != nil {
		t.Fatal(err)
	}
	jar, err := devverifier.SignRequestObject(id, devverifier.Request{
		Nonce: "nonce", State: "s2", ResponseURI: "https://verifier.test/r", ResponseMode: "direct_post.jwt",
		DCQLQuery: dcql, EncryptionKey: &key.PublicKey,
	})
	if err != nil {
		t.Fatal(err)
	}
	r := NewResponder(Policy{})
	form, err := r.responseForm(Transaction{ResponseMode: "direct_post.jwt", State: "s2", RequestObject: jar}, openid4vp.VpToken{"kvk": {"a~b~"}})
	if err != nil {
		t.Fatal(err)
	}
	if form.Has("vp_token") || form.Has("state") {
		t.Error("encrypted mode must not carry plaintext parameters")
	}
	token, state, err := devverifier.DecryptResponse(form.Get("response"), key)
	if err != nil {
		t.Fatalf("decrypt: %v", err)
	}
	if state != "s2" || token["kvk"][0] != "a~b~" {
		t.Errorf("decrypted = %v / %q", token, state)
	}
	other, err := devverifier.NewEncryptionKey()
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := devverifier.DecryptResponse(form.Get("response"), other); err == nil {
		t.Error("another key opened the response")
	}
}
