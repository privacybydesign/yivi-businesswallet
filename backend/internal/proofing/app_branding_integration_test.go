//go:build integration

package proofing

import (
	"bytes"
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"

	"github.com/privacybydesign/yivi-businesswallet/backend/internal/audit"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/organization"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/testdb"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/themesettings"
)

// A session gets the branding of whoever requested it: the org's own requests
// the org's theme; a customer's the customer's own colours and logo when it
// has white-labeling, else the org's whole theme and logo, under the
// customer's name. One org's request never resolves under another org.
func TestAppBrandingResolver(t *testing.T) {
	pool, _ := testdb.Fresh(t)
	ctx := context.Background()
	recorder := audit.NopRecorder{}
	requests := NewRequestStore(pool, recorder, newTestCipher(t))
	customers := NewCustomerStore(pool, recorder)
	themes := themesettings.NewStore(pool, recorder)
	resolver := NewAppBrandingResolver(requests, customers, themes, organization.NewStore(pool, recorder))

	orgID := makeOrg(t, pool, "cm")
	otherOrgID := makeOrg(t, pool, "acme")
	requester := makeUser(t, pool, "sam@example.org")
	orgLogo := []byte("org-logo")
	if _, err := themes.Save(ctx, orgID,
		themesettings.SettingsInput{
			PrimaryColor: "#036BD2", AccentColor: "#5412C7", TextColor: "#101E1E", SurfaceColor: "#FBFBFC",
			ErrorColor: "#D1293D", TopbarColor: "#101E1E", FontFamily: `"Alexandria", system-ui, sans-serif`,
		},
		themesettings.LogoUpdate{Replace: true, Logo: themesettings.Logo{Bytes: orgLogo, ContentType: "image/png"}}); err != nil {
		t.Fatalf("save org theme: %v", err)
	}

	branded, err := customers.Create(ctx, orgID, requester, "CM Tickets")
	if err != nil {
		t.Fatalf("create customer: %v", err)
	}
	customerLogo := []byte("customer-logo")
	if _, err := customers.SaveBranding(ctx, orgID, branded.ID,
		CustomerBranding{DisplayName: "CM Tickets Concerten", SecondaryColor: "#112233"},
		LogoChange{Replace: true, Logo: CustomerLogo{Bytes: customerLogo, ContentType: "image/png"}}); err != nil {
		t.Fatalf("save customer branding: %v", err)
	}
	plain, err := customers.Create(ctx, orgID, requester, "CM Festivals")
	if err != nil {
		t.Fatalf("create customer: %v", err)
	}
	fullTheme := CustomerBranding{
		PrimaryColor: "#0A7C59", SecondaryColor: "#112233", TextColor: "#222222", SurfaceColor: "#F7F7F7",
		BorderColor: "#DDDDDD", LinkColor: "#0055AA", SuccessColor: "#2E7D32", WarningColor: "#ED6C02",
		ErrorColor: "#C62828", TopbarColor: "#0A3D2E", FontFamily: `"Open Sans", system-ui, sans-serif`,
	}
	themed, err := customers.Create(ctx, orgID, requester, "CM Theater")
	if err != nil {
		t.Fatalf("create customer: %v", err)
	}
	if _, err := customers.SaveBranding(ctx, orgID, themed.ID, fullTheme, LogoChange{}); err != nil {
		t.Fatalf("save customer branding: %v", err)
	}
	fontOnly, err := customers.Create(ctx, orgID, requester, "CM Musea")
	if err != nil {
		t.Fatalf("create customer: %v", err)
	}
	if _, err := customers.SaveBranding(ctx, orgID, fontOnly.ID,
		CustomerBranding{FontFamily: `"Nunito", system-ui, sans-serif`}, LogoChange{}); err != nil {
		t.Fatalf("save customer branding: %v", err)
	}
	logoOnly, err := customers.Create(ctx, orgID, requester, "CM Sport")
	if err != nil {
		t.Fatalf("create customer: %v", err)
	}
	logoOnlyLogo := []byte("logo-only-logo")
	if _, err := customers.SaveBranding(ctx, orgID, logoOnly.ID, CustomerBranding{},
		LogoChange{Replace: true, Logo: CustomerLogo{Bytes: logoOnlyLogo, ContentType: "image/png"}}); err != nil {
		t.Fatalf("save customer branding: %v", err)
	}

	// orgTheme is the org's theme as every one of its requests gets it.
	orgTheme := func(name string) AppBranding {
		return AppBranding{
			Name: name, PrimaryColor: "#036BD2", SecondaryColor: "#5412C7", TextColor: "#101E1E",
			SurfaceColor: "#FBFBFC", ErrorColor: "#D1293D", HeaderColor: "#101E1E",
			FontFamily: `"Alexandria", system-ui, sans-serif`, HasLogo: true,
		}
	}

	newRequest := func(customerID *uuid.UUID) uuid.UUID {
		t.Helper()
		subject := Subject{UserID: &requester, Name: "Sam de Vries", Email: "sam@example.org"}
		if customerID != nil {
			subject = Subject{CustomerID: customerID, Email: "anna@example.org"}
		}
		req, err := requests.Create(ctx, newStoredRequest(orgID, requester, subject))
		if err != nil {
			t.Fatalf("create request: %v", err)
		}
		return req.ID
	}

	for name, tc := range map[string]struct {
		requestID uuid.UUID
		want      AppBranding
		logo      []byte
	}{
		"org's own request": {
			newRequest(nil),
			orgTheme("cm"),
			orgLogo,
		},
		"customer with white-labeling": {
			newRequest(&branded.ID),
			AppBranding{Name: "CM Tickets Concerten", SecondaryColor: "#112233", HasLogo: true},
			customerLogo,
		},
		"customer with a whole theme of its own": {
			newRequest(&themed.ID),
			AppBranding{
				Name: "CM Theater", PrimaryColor: "#0A7C59", SecondaryColor: "#112233", TextColor: "#222222",
				SurfaceColor: "#F7F7F7", BorderColor: "#DDDDDD", LinkColor: "#0055AA", SuccessColor: "#2E7D32",
				WarningColor: "#ED6C02", ErrorColor: "#C62828", HeaderColor: "#0A3D2E",
				FontFamily: `"Open Sans", system-ui, sans-serif`,
			},
			nil,
		},
		"customer with only a font of its own": {
			newRequest(&fontOnly.ID),
			AppBranding{Name: "CM Musea", FontFamily: `"Nunito", system-ui, sans-serif`},
			nil,
		},
		"customer without white-labeling": {
			newRequest(&plain.ID),
			orgTheme("CM Festivals"),
			orgLogo,
		},
		"customer with only a logo": {
			newRequest(&logoOnly.ID),
			orgTheme("CM Sport"),
			logoOnlyLogo,
		},
	} {
		got, err := resolver.Branding(ctx, orgID, tc.requestID)
		if err != nil || got == nil || *got != tc.want {
			t.Errorf("%s: Branding = %+v, %v; want %+v", name, got, err, tc.want)
		}
		logo, err := resolver.Logo(ctx, orgID, tc.requestID)
		switch {
		case tc.logo == nil && !errors.Is(err, ErrNoAppLogo):
			t.Errorf("%s: Logo = %q, %v; want ErrNoAppLogo", name, logo.Bytes, err)
		case tc.logo != nil && (err != nil || !bytes.Equal(logo.Bytes, tc.logo)):
			t.Errorf("%s: Logo = %q, %v; want %q", name, logo.Bytes, err, tc.logo)
		}
		if _, err := resolver.Branding(ctx, otherOrgID, tc.requestID); !errors.Is(err, ErrRequestNotFound) {
			t.Errorf("%s: Branding under another org = %v; want ErrRequestNotFound", name, err)
		}
		if _, err := resolver.Logo(ctx, otherOrgID, tc.requestID); !errors.Is(err, ErrRequestNotFound) {
			t.Errorf("%s: Logo under another org = %v; want ErrRequestNotFound", name, err)
		}
	}
}

// An org that set no theme, and a customer of it with no branding, give the
// app nothing: it keeps its own look.
func TestAppBrandingResolverNone(t *testing.T) {
	pool, _ := testdb.Fresh(t)
	ctx := context.Background()
	recorder := audit.NopRecorder{}
	requests := NewRequestStore(pool, recorder, newTestCipher(t))
	customers := NewCustomerStore(pool, recorder)
	resolver := NewAppBrandingResolver(requests, customers, themesettings.NewStore(pool, recorder),
		organization.NewStore(pool, recorder))

	orgID := makeOrg(t, pool, "acme")
	requester := makeUser(t, pool, "sam@example.org")
	customer, err := customers.Create(ctx, orgID, requester, "Initech")
	if err != nil {
		t.Fatalf("create customer: %v", err)
	}
	for _, subject := range []Subject{
		{UserID: &requester, Name: "Sam de Vries", Email: "sam@example.org"},
		{CustomerID: &customer.ID, Email: "anna@example.org"},
	} {
		req, err := requests.Create(ctx, newStoredRequest(orgID, requester, subject))
		if err != nil {
			t.Fatalf("create request: %v", err)
		}
		if got, err := resolver.Branding(ctx, orgID, req.ID); err != nil || got != nil {
			t.Errorf("Branding = %+v, %v; want none", got, err)
		}
	}
}

// A customer with only colours of its own has no logo: the org's is not mixed
// into the customer's white-labeling.
func TestAppBrandingResolverCustomerColoursOnly(t *testing.T) {
	pool, _ := testdb.Fresh(t)
	ctx := context.Background()
	recorder := audit.NopRecorder{}
	requests := NewRequestStore(pool, recorder, newTestCipher(t))
	customers := NewCustomerStore(pool, recorder)
	themes := themesettings.NewStore(pool, recorder)
	resolver := NewAppBrandingResolver(requests, customers, themes, organization.NewStore(pool, recorder))

	orgID := makeOrg(t, pool, "cm")
	requester := makeUser(t, pool, "sam@example.org")
	if _, err := themes.Save(ctx, orgID, themesettings.SettingsInput{PrimaryColor: "#036BD2", TopbarColor: "#101E1E"},
		themesettings.LogoUpdate{Replace: true, Logo: themesettings.Logo{Bytes: []byte("org-logo"), ContentType: "image/png"}}); err != nil {
		t.Fatalf("save org theme: %v", err)
	}
	customer, err := customers.Create(ctx, orgID, requester, "Initech")
	if err != nil {
		t.Fatalf("create customer: %v", err)
	}
	if _, err := customers.SaveBranding(ctx, orgID, customer.ID, CustomerBranding{PrimaryColor: "#0A7C59"}, LogoChange{}); err != nil {
		t.Fatalf("save customer branding: %v", err)
	}
	req, err := requests.Create(ctx, newStoredRequest(orgID, requester, Subject{CustomerID: &customer.ID, Email: "anna@example.org"}))
	if err != nil {
		t.Fatalf("create request: %v", err)
	}
	want := AppBranding{Name: "Initech", PrimaryColor: "#0A7C59"}
	if got, err := resolver.Branding(ctx, orgID, req.ID); err != nil || got == nil || *got != want {
		t.Errorf("Branding = %+v, %v; want %+v", got, err, want)
	}
	if _, err := resolver.Logo(ctx, orgID, req.ID); !errors.Is(err, ErrNoAppLogo) {
		t.Errorf("Logo = %v; want ErrNoAppLogo", err)
	}
}
