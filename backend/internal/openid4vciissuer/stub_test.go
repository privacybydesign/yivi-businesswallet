package openid4vciissuer_test

import (
	"context"
	"testing"
	"time"

	"github.com/privacybydesign/yivi-businesswallet/backend/internal/openid4vciissuer"
)

// The stub offer carries what was issued, so the stub holder can hold the
// real type and claims instead of a placeholder.
func TestStubOfferCarriesTypeAndClaims(t *testing.T) {
	t.Parallel()
	offer, err := openid4vciissuer.NewStubIssuer().CreateOffer(context.Background(), openid4vciissuer.OfferRequest{
		CredentialConfigID: "RechtbankBeschermingsbewindSdJwt",
		VCT:                "nl.rechtspraak.beschermingsbewind",
		DisplayName:        "Beschikking beschermingsbewind",
		IssuerName:         "Rechtbank Gelderland (Demo)",
		Claims:             map[string]any{"zaaknummer": "C/05/412345", "omvang": "Alle goederen"},
	})
	if err != nil {
		t.Fatalf("create offer: %v", err)
	}

	got, ok := openid4vciissuer.StubOfferContent(offer.OfferURI)
	if !ok {
		t.Fatalf("StubOfferContent(%q) not ok", offer.OfferURI)
	}
	if got.VCT != "nl.rechtspraak.beschermingsbewind" || got.DisplayName != "Beschikking beschermingsbewind" || got.IssuerName != "Rechtbank Gelderland (Demo)" {
		t.Errorf("offer = %+v", got)
	}
	if got.Claims["zaaknummer"] != "C/05/412345" || got.Claims["omvang"] != "Alle goederen" {
		t.Errorf("claims = %v", got.Claims)
	}
}

// Anything but a stub offer with a type is left to the caller's fallback.
func TestStubOfferContentRejectsOtherOffers(t *testing.T) {
	t.Parallel()
	untyped, err := openid4vciissuer.NewStubIssuer().CreateOffer(context.Background(), openid4vciissuer.OfferRequest{CredentialConfigID: "X"})
	if err != nil {
		t.Fatalf("create offer: %v", err)
	}
	for _, uri := range []string{
		"openid-credential-offer://?credential_offer=%7B%7D",
		untyped.OfferURI,
		"openid-credential-offer://stub?vct=a&claims=not-base64!",
	} {
		if _, ok := openid4vciissuer.StubOfferContent(uri); ok {
			t.Errorf("StubOfferContent(%q) ok, want fallback", uri)
		}
	}
}

// A claim delay keeps a fresh offer pending; without one it is issued at once.
func TestStubClaimDelay(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	for _, tc := range []struct {
		name   string
		issuer openid4vciissuer.StubIssuer
		want   string
	}{
		{"no delay", openid4vciissuer.NewStubIssuer(), openid4vciissuer.StatusIssued},
		{"delay", openid4vciissuer.NewStubIssuerWithClaimDelay(time.Hour), openid4vciissuer.StatusPending},
	} {
		offer, err := tc.issuer.CreateOffer(ctx, openid4vciissuer.OfferRequest{CredentialConfigID: "X"})
		if err != nil {
			t.Fatalf("%s: create offer: %v", tc.name, err)
		}
		st, err := tc.issuer.Status(ctx, "", offer.IssuanceID)
		if err != nil {
			t.Fatalf("%s: status: %v", tc.name, err)
		}
		if st.Status != tc.want {
			t.Errorf("%s: status = %q, want %q", tc.name, st.Status, tc.want)
		}
	}
}
