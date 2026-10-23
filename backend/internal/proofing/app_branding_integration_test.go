//go:build integration

package proofing

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/google/uuid"

	"github.com/privacybydesign/yivi-businesswallet/backend/internal/audit"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/testdb"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/themesettings"
)

// orgThemeInput is the theme every org of these tests has.
var orgThemeInput = themesettings.SettingsInput{
	PrimaryColor: "#036BD2", AccentColor: "#5412C7", TextColor: "#101E1E", SurfaceColor: "#FBFBFC",
	ErrorColor: "#D1293D", TopbarColor: "#101E1E", FontFamily: `"Alexandria", system-ui, sans-serif`,
}

// A session gets the branding of whoever it was sent to: a member the org's
// theme; an external person the org's proofing colours and logo when it has
// white-labeling, else the org's whole theme and logo, under the name its
// proofing mail signs with. One org's request never resolves under another org.
func TestAppBrandingResolver(t *testing.T) {
	pool, _ := testdb.Fresh(t)
	ctx := context.Background()
	recorder := audit.NopRecorder{}
	requests := NewRequestStore(pool, recorder, newTestCipher(t))
	settings := NewOrgSettingsStore(pool, recorder)
	themes := themesettings.NewStore(pool, recorder)
	resolver := NewAppBrandingResolver(requests, settings, themes)

	otherOrgID := makeOrg(t, pool, "acme")
	requester := makeUser(t, pool, "sam@example.org")
	orgLogo := []byte("org-logo")
	member := Subject{Kind: SubjectMember, UserID: &requester, Name: "Sam de Vries", Email: "sam@example.org"}
	external := externalSubject("anna@example.org")

	// newRequest is a request to subject at an org with the theme above and the
	// proofing branding and logo given.
	slugs := 0
	newRequest := func(branding Branding, logo []byte, subject Subject) (orgID, requestID uuid.UUID) {
		t.Helper()
		slugs++
		orgID = makeOrg(t, pool, fmt.Sprintf("cm-%d", slugs))
		if _, err := themes.Save(ctx, orgID, orgThemeInput,
			themesettings.LogoUpdate{Replace: true, Logo: themesettings.Logo{Bytes: orgLogo, ContentType: "image/png"}}); err != nil {
			t.Fatalf("save org theme: %v", err)
		}
		saveProofingBranding(t, settings, orgID, branding, logo)
		req, err := requests.Create(ctx, newStoredRequest(orgID, requester, subject))
		if err != nil {
			t.Fatalf("create request: %v", err)
		}
		return orgID, req.ID
	}

	// orgTheme is the org's theme as a request that has no look of its own gets it.
	orgTheme := func(name string) AppBranding {
		return AppBranding{
			Name: name, PrimaryColor: "#036BD2", SecondaryColor: "#5412C7", TextColor: "#101E1E",
			SurfaceColor: "#FBFBFC", ErrorColor: "#D1293D", HeaderColor: "#101E1E",
			FontFamily: `"Alexandria", system-ui, sans-serif`, HasLogo: true,
		}
	}
	whiteLabelLogo, logoOnlyLogo := []byte("white-label-logo"), []byte("logo-only-logo")

	for name, tc := range map[string]struct {
		branding Branding
		logo     []byte
		subject  Subject
		want     func(orgName string) AppBranding
		wantLogo []byte
	}{
		"member of an org with white-labeling": {
			Branding{DisplayName: "CM Tickets", SecondaryColor: "#112233"},
			whiteLabelLogo, member,
			func(orgName string) AppBranding { return orgTheme(orgName) }, orgLogo,
		},
		"external person, white-labeling and a logo": {
			Branding{DisplayName: "CM Tickets Concerten", SecondaryColor: "#112233"},
			whiteLabelLogo, external,
			func(string) AppBranding {
				return AppBranding{Name: "CM Tickets Concerten", SecondaryColor: "#112233", HasLogo: true}
			},
			whiteLabelLogo,
		},
		"external person, a whole theme of its own": {
			Branding{
				DisplayName: "CM Theater", PrimaryColor: "#0A7C59", SecondaryColor: "#112233", TextColor: "#222222",
				SurfaceColor: "#F7F7F7", BorderColor: "#DDDDDD", LinkColor: "#0055AA", SuccessColor: "#2E7D32",
				WarningColor: "#ED6C02", ErrorColor: "#C62828", TopbarColor: "#0A3D2E",
				FontFamily: `"Open Sans", system-ui, sans-serif`,
			},
			nil, external,
			func(string) AppBranding {
				return AppBranding{
					Name: "CM Theater", PrimaryColor: "#0A7C59", SecondaryColor: "#112233", TextColor: "#222222",
					SurfaceColor: "#F7F7F7", BorderColor: "#DDDDDD", LinkColor: "#0055AA", SuccessColor: "#2E7D32",
					WarningColor: "#ED6C02", ErrorColor: "#C62828", HeaderColor: "#0A3D2E",
					FontFamily: `"Open Sans", system-ui, sans-serif`,
				}
			},
			nil,
		},
		"external person, only a font of its own": {
			Branding{DisplayName: "CM Musea", FontFamily: `"Nunito", system-ui, sans-serif`},
			nil, external,
			func(string) AppBranding {
				return AppBranding{Name: "CM Musea", FontFamily: `"Nunito", system-ui, sans-serif`}
			},
			nil,
		},
		"external person, no white-labeling": {
			Branding{DisplayName: "CM Festivals"},
			nil, external,
			func(string) AppBranding { return orgTheme("CM Festivals") }, orgLogo,
		},
		"external person, only a logo": {
			Branding{},
			logoOnlyLogo, external,
			func(orgName string) AppBranding { return orgTheme(orgName) }, logoOnlyLogo,
		},
	} {
		orgID, requestID := newRequest(tc.branding, tc.logo, tc.subject)
		var orgName string
		if err := pool.QueryRow(ctx, `SELECT name FROM organizations WHERE id = $1`, orgID).Scan(&orgName); err != nil {
			t.Fatalf("%s: read org name: %v", name, err)
		}
		want := tc.want(orgName)
		got, err := resolver.Branding(ctx, orgID, requestID)
		if err != nil || got == nil || *got != want {
			t.Errorf("%s: Branding = %+v, %v; want %+v", name, got, err, want)
		}
		assertAppLogo(t, resolver, name, orgID, requestID, tc.wantLogo)
		assertNoAppBrandingUnder(t, resolver, name, otherOrgID, requestID)
	}
}

// saveProofingBranding saves the proofing branding of orgID and, when logo is
// set, its logo; it saves nothing when both are empty.
func saveProofingBranding(t *testing.T, settings *OrgSettingsStore, orgID uuid.UUID, branding Branding, logo []byte) {
	t.Helper()
	if branding == (Branding{}) && logo == nil {
		return
	}
	change := LogoChange{}
	if logo != nil {
		change = LogoChange{Replace: true, Logo: Logo{Bytes: logo, ContentType: "image/png"}}
	}
	if _, err := settings.SaveBranding(context.Background(), orgID, branding, change); err != nil {
		t.Fatalf("save proofing branding: %v", err)
	}
}

// assertAppLogo checks the logo the app gets for requestID at orgID: wantLogo,
// or ErrNoAppLogo when that is nil.
func assertAppLogo(t *testing.T, resolver *AppBrandingResolver, name string, orgID, requestID uuid.UUID, wantLogo []byte) {
	t.Helper()
	logo, err := resolver.Logo(context.Background(), orgID, requestID)
	switch {
	case wantLogo == nil && !errors.Is(err, ErrNoAppLogo):
		t.Errorf("%s: Logo = %q, %v; want ErrNoAppLogo", name, logo.Bytes, err)
	case wantLogo != nil && (err != nil || !bytes.Equal(logo.Bytes, wantLogo)):
		t.Errorf("%s: Logo = %q, %v; want %q", name, logo.Bytes, err, wantLogo)
	}
}

// assertNoAppBrandingUnder checks that requestID resolves no branding and no
// logo under otherOrgID, an org it does not belong to.
func assertNoAppBrandingUnder(t *testing.T, resolver *AppBrandingResolver, name string, otherOrgID, requestID uuid.UUID) {
	t.Helper()
	ctx := context.Background()
	if _, err := resolver.Branding(ctx, otherOrgID, requestID); !errors.Is(err, ErrRequestNotFound) {
		t.Errorf("%s: Branding under another org = %v; want ErrRequestNotFound", name, err)
	}
	if _, err := resolver.Logo(ctx, otherOrgID, requestID); !errors.Is(err, ErrRequestNotFound) {
		t.Errorf("%s: Logo under another org = %v; want ErrRequestNotFound", name, err)
	}
}

// An org that set no theme and no proofing branding gives the app nothing: it
// keeps its own look, whoever the request went to.
func TestAppBrandingResolverNone(t *testing.T) {
	pool, _ := testdb.Fresh(t)
	ctx := context.Background()
	recorder := audit.NopRecorder{}
	requests := NewRequestStore(pool, recorder, newTestCipher(t))
	resolver := NewAppBrandingResolver(requests, NewOrgSettingsStore(pool, recorder), themesettings.NewStore(pool, recorder))

	orgID := makeOrg(t, pool, "acme")
	requester := makeUser(t, pool, "sam@example.org")
	for _, subject := range []Subject{
		{Kind: SubjectMember, UserID: &requester, Name: "Sam de Vries", Email: "sam@example.org"},
		externalSubject("anna@example.org"),
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

// An org whose proofing has only colours of its own has no logo: the org
// theme's is not mixed into its white-labeling.
func TestAppBrandingColoursOnly(t *testing.T) {
	pool, _ := testdb.Fresh(t)
	ctx := context.Background()
	recorder := audit.NopRecorder{}
	requests := NewRequestStore(pool, recorder, newTestCipher(t))
	settings := NewOrgSettingsStore(pool, recorder)
	themes := themesettings.NewStore(pool, recorder)
	resolver := NewAppBrandingResolver(requests, settings, themes)

	orgID := makeOrg(t, pool, "cm")
	requester := makeUser(t, pool, "sam@example.org")
	if _, err := themes.Save(ctx, orgID, themesettings.SettingsInput{PrimaryColor: "#036BD2", TopbarColor: "#101E1E"},
		themesettings.LogoUpdate{Replace: true, Logo: themesettings.Logo{Bytes: []byte("org-logo"), ContentType: "image/png"}}); err != nil {
		t.Fatalf("save org theme: %v", err)
	}
	if _, err := settings.SaveBranding(ctx, orgID, Branding{PrimaryColor: "#0A7C59"}, LogoChange{}); err != nil {
		t.Fatalf("save proofing branding: %v", err)
	}
	req, err := requests.Create(ctx, newStoredRequest(orgID, requester, externalSubject("anna@example.org")))
	if err != nil {
		t.Fatalf("create request: %v", err)
	}
	want := AppBranding{Name: "cm", PrimaryColor: "#0A7C59"}
	if got, err := resolver.Branding(ctx, orgID, req.ID); err != nil || got == nil || *got != want {
		t.Errorf("Branding = %+v, %v; want %+v", got, err, want)
	}
	if _, err := resolver.Logo(ctx, orgID, req.ID); !errors.Is(err, ErrNoAppLogo) {
		t.Errorf("Logo = %v; want ErrNoAppLogo", err)
	}
}
