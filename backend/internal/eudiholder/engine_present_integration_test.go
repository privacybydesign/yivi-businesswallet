//go:build integration

package eudiholder_test

import (
	"context"
	"crypto"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/lestrrat-go/jwx/v3/jwa"
	"github.com/lestrrat-go/jwx/v3/jwk"
	"github.com/lestrrat-go/jwx/v3/jws"
	"github.com/privacybydesign/irmago/eudi/credentials/sdjwtvc"
	"github.com/privacybydesign/irmago/eudi/sdjwt"
	"github.com/privacybydesign/irmago/eudi/storage/db"
	"github.com/privacybydesign/irmago/eudi/storage/db/models"
	"gorm.io/datatypes"

	"github.com/privacybydesign/yivi-businesswallet/backend/internal/devverifier"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/eudiholder"
)

const (
	presentVCT      = "nl.kvk.registration"
	presentNonce    = "n-0S6_WzA2Mj"
	presentAudience = "x509_san_dns:verifier.test"
)

// seedHolderKey stores a fresh holder-binding key in the org's irmago storage
// the way the receive flow's software binder does (PKCS#8 + JWK thumbprint), and
// returns its public JWK for the credential's cnf.
func seedHolderKey(t *testing.T, eng *eudiholder.Engine, org uuid.UUID) (*ecdsa.PrivateKey, jwk.Key) {
	t.Helper()
	st, err := eng.StorageForTest(context.Background(), org)
	if err != nil {
		t.Fatalf("storage: %v", err)
	}
	priv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	privJWK, err := jwk.Import(priv)
	if err != nil {
		t.Fatal(err)
	}
	pubJWK, err := privJWK.PublicKey()
	if err != nil {
		t.Fatal(err)
	}
	thumb, err := pubJWK.Thumbprint(crypto.SHA256)
	if err != nil {
		t.Fatal(err)
	}
	pkcs8, err := x509.MarshalPKCS8PrivateKey(priv)
	if err != nil {
		t.Fatal(err)
	}
	err = db.NewHolderBindingKeyStore(st.Db()).StoreKeys([]models.HolderBindingKey{{
		Algorithm:           models.KeyAlgorithmECDSA,
		PrivateKey:          pkcs8,
		ECDSA:               &models.ECDSAKeyMetadata{CurveName: priv.Curve.Params().Name},
		PublicKeyThumbprint: datatypes.NullString{V: hex.EncodeToString(thumb), Valid: true},
	}})
	if err != nil {
		t.Fatalf("store holder key: %v", err)
	}
	return priv, pubJWK
}

// issueTestCredential builds an SD-JWT VC bound to holderKey with two
// selectively disclosable claims and stores it in the org's engine.
func issueTestCredential(t *testing.T, eng *eudiholder.Engine, org uuid.UUID, holderKey jwk.Key) {
	t.Helper()
	issuerKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	cnf, err := sdjwt.HolderKeyClaim(holderKey)
	if err != nil {
		t.Fatal(err)
	}
	issuedAt := time.Now().Add(-time.Minute)
	raw, err := sdjwtvc.NewSdJwtVcBuilder().WithPayload(
		sdjwt.Claim("iss", "https://issuer.test"),
		sdjwt.Claim("vct", presentVCT),
		sdjwt.Claim("iat", issuedAt.Unix()),
		cnf,
		sdjwt.SdClaim("company_name", "Demo B.V."),
		sdjwt.SdClaim("kvk_number", "12345678"),
	).Build(sdjwt.NewJwtCreator(issuerKey))
	if err != nil {
		t.Fatalf("build sd-jwt vc: %v", err)
	}
	processed, err := json.Marshal(map[string]any{
		"iss": "https://issuer.test", "vct": presentVCT, "iat": issuedAt.Unix(),
		"company_name": "Demo B.V.", "kvk_number": "12345678",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := eng.Store(context.Background(), org, eudiholder.Credential{
		VCT: presentVCT, IssuerURL: "https://issuer.test", CredentialIssuer: "https://issuer.test",
		Hash: "hash-present", RawToken: []byte(raw), ProcessedPayload: processed, IssuedAt: issuedAt,
	}); err != nil {
		t.Fatalf("store: %v", err)
	}
}

// TestEnginePresentSelectivelyDisclosesWithKeyBinding is the holder-side crypto of
// #112 end to end: the DCQL query asks for one of two claims, and the resulting
// presentation carries only that disclosure plus a KB-JWT signed by the stored
// holder key over the right sd_hash, nonce and audience.
func TestEnginePresentSelectivelyDisclosesWithKeyBinding(t *testing.T) {
	eng, _ := newTestEngine(t)
	ctx := context.Background()
	org := uuid.New()
	holderPriv, holderPub := seedHolderKey(t, eng, org)
	issueTestCredential(t, eng, org, holderPub)

	dcql, err := devverifier.SimpleDCQL("kvk", presentVCT, []string{"company_name"})
	if err != nil {
		t.Fatal(err)
	}
	p, err := eng.Present(ctx, org, dcql, presentNonce, presentAudience)
	if err != nil {
		t.Fatalf("Present: %v", err)
	}
	if len(p.VPToken["kvk"]) != 1 {
		t.Fatalf("vp_token = %v, want one presentation for query kvk", p.VPToken)
	}
	presentation := p.VPToken["kvk"][0]
	parts := strings.Split(presentation, "~")
	// issuer JWT ~ disclosure ~ KB-JWT: exactly one disclosure, the requested one.
	if len(parts) != 3 {
		t.Fatalf("presentation has %d '~'-separated parts, want issuer~disclosure~kbjwt: %s", len(parts), presentation)
	}
	disclosure, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(disclosure), `"company_name"`) || strings.Contains(string(disclosure), "kvk_number") {
		t.Errorf("disclosure = %s, want only company_name", disclosure)
	}

	kb := parts[2]
	payload, err := jws.Verify([]byte(kb), jws.WithKey(jwa.ES256(), &holderPriv.PublicKey))
	if err != nil {
		t.Fatalf("KB-JWT signature does not verify with the stored holder key: %v", err)
	}
	var claims struct {
		SDHash string `json:"sd_hash"`
		Nonce  string `json:"nonce"`
		Aud    string `json:"aud"`
		IAT    int64  `json:"iat"`
	}
	if err := json.Unmarshal(payload, &claims); err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256([]byte(parts[0] + "~" + parts[1] + "~"))
	if want := base64.RawURLEncoding.EncodeToString(sum[:]); claims.SDHash != want {
		t.Errorf("sd_hash = %q, want hash over issuer JWT + disclosures (%q)", claims.SDHash, want)
	}
	if claims.Nonce != presentNonce || claims.Aud != presentAudience || claims.IAT == 0 {
		t.Errorf("KB-JWT claims = %+v", claims)
	}

	// A single-instance batch stays presentable: a second request works too.
	if _, err := eng.Present(ctx, org, dcql, "second-nonce", presentAudience); err != nil {
		t.Fatalf("second Present: %v", err)
	}
}

func TestEnginePresentRefusesWhenNothingMatches(t *testing.T) {
	eng, _ := newTestEngine(t)
	ctx := context.Background()
	org := uuid.New()
	_, holderPub := seedHolderKey(t, eng, org)
	issueTestCredential(t, eng, org, holderPub)

	dcql, err := devverifier.SimpleDCQL("other", "nl.other.type", nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := eng.Present(ctx, org, dcql, presentNonce, presentAudience); !errors.Is(err, eudiholder.ErrNoMatchingCredential) {
		t.Fatalf("Present for a type the org does not hold: err = %v, want ErrNoMatchingCredential", err)
	}
	// Another org holds nothing at all.
	if _, err := eng.Present(ctx, uuid.New(), dcql, presentNonce, presentAudience); !errors.Is(err, eudiholder.ErrNoMatchingCredential) {
		t.Fatalf("Present for an empty org: err = %v, want ErrNoMatchingCredential", err)
	}
}
