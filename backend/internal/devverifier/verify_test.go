package devverifier

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/lestrrat-go/jwx/v3/jwa"
	"github.com/lestrrat-go/jwx/v3/jws"
	"github.com/privacybydesign/irmago/eudi/didkey"
)

func b64(v any) string {
	b, _ := json.Marshal(v)
	return base64.RawURLEncoding.EncodeToString(b)
}

// A KB-JWT bound through a did:key cnf — the shape the Veramo issuer produces
// and the wallet's WSCA binder signs — verifies; the usual tampering does not.
func TestVerifyKeyBindingWithDIDKeyConfirmation(t *testing.T) {
	holder, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	did, err := didkey.Create(holder.PublicKey)
	if err != nil {
		t.Fatal(err)
	}
	issuerPayload := map[string]any{"vct": "nl.kvk.registration", "cnf": map[string]any{"kid": did}}
	// The SD-JWT the KB-JWT is bound to (issuer signature is not what this test checks).
	sdJwt := b64(map[string]any{"alg": "ES256"}) + "." + b64(issuerPayload) + ".sig~" + b64([]any{"salt", "legalName", "Yivi B.V."}) + "~"
	now := time.Now()

	sign := func(claims map[string]any) string {
		hdr := jws.NewHeaders()
		_ = hdr.Set("typ", kbJwtTyp)
		body, _ := json.Marshal(claims)
		out, err := jws.Sign(body, jws.WithKey(jwa.ES256(), holder, jws.WithProtectedHeaders(hdr)))
		if err != nil {
			t.Fatal(err)
		}
		return string(out)
	}
	sdHash := sha256b64(sdJwt)
	good := map[string]any{"sd_hash": sdHash, "nonce": "n1", "aud": "x509_san_dns:verifier.test", "iat": now.Unix()}

	if err := verifyKeyBinding(sign(good), sdJwt, issuerPayload, "n1", "x509_san_dns:verifier.test", now); err != nil {
		t.Fatalf("valid KB-JWT refused: %v", err)
	}
	bad := map[string]func(map[string]any){
		"wrong nonce":   func(c map[string]any) { c["nonce"] = "n2" },
		"wrong aud":     func(c map[string]any) { c["aud"] = "x509_san_dns:other.test" },
		"wrong sd_hash": func(c map[string]any) { c["sd_hash"] = sha256b64(sdJwt + "x") },
		"future iat":    func(c map[string]any) { c["iat"] = now.Add(time.Hour).Unix() },
	}
	for name, mutate := range bad {
		claims := map[string]any{}
		for k, v := range good {
			claims[k] = v
		}
		mutate(claims)
		if err := verifyKeyBinding(sign(claims), sdJwt, issuerPayload, "n1", "x509_san_dns:verifier.test", now); err == nil {
			t.Errorf("%s accepted", name)
		}
	}
	// Signed by another key than the cnf names.
	other, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	body, _ := json.Marshal(good)
	hdr := jws.NewHeaders()
	_ = hdr.Set("typ", kbJwtTyp)
	forged, _ := jws.Sign(body, jws.WithKey(jwa.ES256(), other, jws.WithProtectedHeaders(hdr)))
	if err := verifyKeyBinding(string(forged), sdJwt, issuerPayload, "n1", "x509_san_dns:verifier.test", now); err == nil || !strings.Contains(err.Error(), "signature") {
		t.Errorf("forged signature: err = %v", err)
	}
}

func sha256b64(s string) string {
	sum := sha256.Sum256([]byte(s))
	return base64.RawURLEncoding.EncodeToString(sum[:])
}
