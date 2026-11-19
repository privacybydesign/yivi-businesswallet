//go:build integration

package proofing

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/privacybydesign/yivi-businesswallet/backend/internal/audit"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/testdb"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/themesettings"
)

// orgThemeInput is the theme every org of these tests has.
var orgThemeInput = themesettings.SettingsInput{
	PrimaryColor: "#036BD2", AccentColor: "#5412C7", TextColor: "#101E1E", SurfaceColor: "#FBFBFC",
	ErrorColor: "#D1293D", TopbarColor: "#101E1E", FontFamily: `"Alexandria", system-ui, sans-serif`,
}

// themed is the org's theme under name, as a request gets it.
func themed(name string) AppBranding {
	return AppBranding{
		Name: name, PrimaryColor: "#036BD2", SecondaryColor: "#5412C7", TextColor: "#101E1E",
		SurfaceColor: "#FBFBFC", ErrorColor: "#D1293D", HeaderColor: "#101E1E",
		FontFamily: `"Alexandria", system-ui, sans-serif`, HasLogo: true,
	}
}

// brandingEnv is a resolver over real stores, and orgs with the theme above.
type brandingEnv struct {
	t         *testing.T
	pool      *pgxpool.Pool
	requests  *RequestStore
	settings  *OrgSettingsStore
	flows     *FlowBrandingStore
	resolver  *AppBrandingResolver
	requester uuid.UUID
	themeLogo []byte
	slugs     int
}

func newBrandingEnv(t *testing.T) *brandingEnv {
	t.Helper()
	pool, _ := testdb.Fresh(t)
	recorder := audit.NopRecorder{}
	e := &brandingEnv{
		t: t, pool: pool, requests: NewRequestStore(pool, recorder, newTestCipher(t)),
		settings: NewOrgSettingsStore(pool, recorder), flows: NewFlowBrandingStore(pool, recorder),
		themeLogo: []byte("theme-logo"),
	}
	e.resolver = NewAppBrandingResolver(e.requests, e.settings, e.flows, themesettings.NewStore(pool, recorder))
	e.requester = makeUser(t, pool, "sam@example.org")
	return e
}

// org is a new org with the theme above, its logo, and branding.
func (e *brandingEnv) org(branding Branding) (uuid.UUID, string) {
	e.t.Helper()
	e.slugs++
	name := fmt.Sprintf("cm-%d", e.slugs)
	orgID := makeOrg(e.t, e.pool, name)
	themes := themesettings.NewStore(e.pool, audit.NopRecorder{})
	if _, err := themes.Save(context.Background(), orgID, orgThemeInput,
		themesettings.LogoUpdate{Replace: true, Logo: themesettings.Logo{Bytes: e.themeLogo, ContentType: contentTypePNG}}); err != nil {
		e.t.Fatalf("save org theme: %v", err)
	}
	if _, err := e.settings.SaveBranding(context.Background(), orgID, branding); err != nil {
		e.t.Fatalf("save branding: %v", err)
	}
	return orgID, name
}

func (e *brandingEnv) request(orgID uuid.UUID, flowID string, subject Subject) uuid.UUID {
	e.t.Helper()
	in := newStoredRequest(orgID, e.requester, subject)
	if flowID != "" {
		in.Flow.ID = flowID
	}
	req, err := e.requests.Create(context.Background(), in)
	if err != nil {
		e.t.Fatalf("create request: %v", err)
	}
	return req.ID
}

func (e *brandingEnv) member() Subject {
	return Subject{Kind: SubjectMember, UserID: &e.requester, Name: "Sam de Vries", Email: "sam@example.org"}
}

// check asserts requestID's branding and logo: want nil is none and no logo.
func (e *brandingEnv) check(name string, orgID, requestID uuid.UUID, want *AppBranding, wantLogo []byte) {
	e.t.Helper()
	ctx := context.Background()
	got, err := e.resolver.Branding(ctx, orgID, requestID)
	switch {
	case err != nil:
		e.t.Errorf("%s: Branding: %v", name, err)
	case want == nil && got != nil, want != nil && (got == nil || *got != *want):
		e.t.Errorf("%s: Branding = %+v; want %+v", name, got, want)
	}
	logo, err := e.resolver.Logo(ctx, orgID, requestID)
	switch {
	case wantLogo == nil && !errors.Is(err, ErrNoAppLogo):
		e.t.Errorf("%s: Logo = %q, %v; want ErrNoAppLogo", name, logo.Bytes, err)
	case wantLogo != nil && (err != nil || !bytes.Equal(logo.Bytes, wantLogo)):
		e.t.Errorf("%s: Logo = %q, %v; want %q", name, logo.Bytes, err, wantLogo)
	}
}

// The org's theme is every request's look: a member's under the org's name, an
// external person's under the name its mail signs with. With the theme off,
// no request gets one. One org's request never resolves under another org.
func TestAppBrandingResolver(t *testing.T) {
	e := newBrandingEnv(t)
	external := externalSubject("anna@example.org")

	orgID, orgName := e.org(Branding{ShowTheme: true})
	want := themed(orgName)
	e.check("member", orgID, e.request(orgID, "", e.member()), &want, e.themeLogo)
	e.check("external person", orgID, e.request(orgID, "", external), &want, e.themeLogo)

	orgID, orgName = e.org(Branding{DisplayName: "CM Tickets", ShowTheme: true})
	member, signed := themed(orgName), themed("CM Tickets")
	e.check("member, display name", orgID, e.request(orgID, "", e.member()), &member, e.themeLogo)
	requestID := e.request(orgID, "", external)
	e.check("external person, display name", orgID, requestID, &signed, e.themeLogo)

	other, _ := e.org(Branding{ShowTheme: true})
	if _, err := e.resolver.Branding(context.Background(), other, requestID); !errors.Is(err, ErrRequestNotFound) {
		t.Errorf("Branding under another org = %v; want ErrRequestNotFound", err)
	}
	if _, err := e.resolver.Logo(context.Background(), other, requestID); !errors.Is(err, ErrRequestNotFound) {
		t.Errorf("Logo under another org = %v; want ErrRequestNotFound", err)
	}

	orgID, _ = e.org(Branding{DisplayName: "CM Tickets"})
	e.check("member, theme off", orgID, e.request(orgID, "", e.member()), nil, nil)
	e.check("external person, theme off", orgID, e.request(orgID, "", external), nil, nil)
}

// An org that set no theme gives the app nothing: it keeps its own look,
// whoever the request went to.
func TestAppBrandingResolverNone(t *testing.T) {
	pool, _ := testdb.Fresh(t)
	ctx := context.Background()
	recorder := audit.NopRecorder{}
	requests := NewRequestStore(pool, recorder, newTestCipher(t))
	resolver := NewAppBrandingResolver(requests, NewOrgSettingsStore(pool, recorder), NewFlowBrandingStore(pool, recorder),
		themesettings.NewStore(pool, recorder))

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

// A request to an external person on a flow with its own branding gets its
// name, colour and logo over the theme; without a logo of its own, the
// theme's. A flow with the org's branding, and a member's request on any flow,
// get the theme as it is.
func TestAppBrandingFlow(t *testing.T) {
	e := newBrandingEnv(t)
	external := externalSubject("anna@example.org")
	ownLogo := []byte("flow-logo")
	orgID, orgName := e.org(Branding{DisplayName: "CM Tickets", ShowTheme: true})
	ctx := context.Background()

	if _, err := e.flows.Save(ctx, orgID, "custom", FlowBranding{Custom: true, Branding: Branding{
		DisplayName: "CM Concerten", PrimaryColor: "#0A7C59",
	}}, LogoChange{Replace: true, Logo: Logo{Bytes: ownLogo, ContentType: contentTypePNG}}); err != nil {
		t.Fatalf("save flow branding: %v", err)
	}
	if _, err := e.flows.Save(ctx, orgID, "named", FlowBranding{Custom: true, Branding: Branding{
		DisplayName: "CM Zomer",
	}}, LogoChange{}); err != nil {
		t.Fatalf("save flow branding: %v", err)
	}

	concerten := themed("CM Concerten")
	concerten.PrimaryColor = "#0A7C59"
	e.check("custom flow", orgID, e.request(orgID, "custom", external), &concerten, ownLogo)
	zomer := themed("CM Zomer")
	e.check("custom flow without a logo", orgID, e.request(orgID, "named", external), &zomer, e.themeLogo)
	tickets := themed("CM Tickets")
	e.check("org-branded flow", orgID, e.request(orgID, "org", external), &tickets, e.themeLogo)
	member := themed(orgName)
	e.check("member on a custom flow", orgID, e.request(orgID, "custom", e.member()), &member, e.themeLogo)
}
