package proofing

import (
	"cmp"
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"

	"github.com/privacybydesign/yivi-businesswallet/backend/internal/themesettings"
)

// AppBranding is how the Idem app dresses one request's session, so it looks
// like the requester's own app: its name, theme colours (#rrggbb, "" keeps the
// app's own), font and whether a logo is served.
type AppBranding struct {
	Name           string
	PrimaryColor   string
	SecondaryColor string
	TextColor      string
	SurfaceColor   string
	BorderColor    string
	LinkColor      string
	SuccessColor   string
	WarningColor   string
	ErrorColor     string
	HeaderColor    string
	FontFamily     string
	HasLogo        bool
}

// fromTheme is the org theme as app branding under name.
func fromTheme(name string, theme themesettings.Settings) AppBranding {
	return AppBranding{
		Name:           name,
		PrimaryColor:   theme.PrimaryColor,
		SecondaryColor: theme.AccentColor,
		TextColor:      theme.TextColor,
		SurfaceColor:   theme.SurfaceColor,
		BorderColor:    theme.BorderColor,
		LinkColor:      theme.LinkColor,
		SuccessColor:   theme.SuccessColor,
		WarningColor:   theme.WarningColor,
		ErrorColor:     theme.ErrorColor,
		HeaderColor:    theme.TopbarColor,
		FontFamily:     theme.FontFamily,
		HasLogo:        theme.HasLogo,
	}
}

// ErrNoAppLogo is AppBrandingResolver.Logo's answer for a request without one.
var ErrNoAppLogo = errors.New("proofing: no app branding logo")

type appBrandingSettings interface {
	Get(ctx context.Context, orgID uuid.UUID) (Settings, error)
}

type appBrandingFlows interface {
	Get(ctx context.Context, orgID uuid.UUID, flowID string) (FlowBranding, error)
	Logo(ctx context.Context, orgID uuid.UUID, flowID string) (Logo, error)
}

type appBrandingThemes interface {
	GetSettings(ctx context.Context, orgID uuid.UUID) (themesettings.Settings, error)
	GetLogo(ctx context.Context, orgID uuid.UUID) (themesettings.Logo, error)
}

// AppBrandingResolver picks a request's branding from who it was sent to and
// the flow it runs, never from anything the app sends. The org's theme is the
// one look, for every request, unless the org turned it off
// (Branding.ShowTheme). A request to a member wears it under the org's name; a
// request to an external person under the name its proofing mail signs with,
// with its flow's own colour and logo over the theme's.
type AppBrandingResolver struct {
	requests *RequestStore
	settings appBrandingSettings
	flows    appBrandingFlows
	themes   appBrandingThemes
}

func NewAppBrandingResolver(requests *RequestStore, settings appBrandingSettings, flows appBrandingFlows, themes appBrandingThemes) *AppBrandingResolver {
	return &AppBrandingResolver{requests: requests, settings: settings, flows: flows, themes: themes}
}

// Branding is the org's requestID's branding, nil when the org shows no theme
// or set none.
func (r *AppBrandingResolver) Branding(ctx context.Context, orgID, requestID uuid.UUID) (*AppBranding, error) {
	settings, own, shown, err := r.resolve(ctx, orgID, requestID)
	if err != nil || !shown {
		return nil, err
	}
	theme, err := r.themes.GetSettings(ctx, orgID)
	if err != nil {
		return nil, fmt.Errorf("proofing: app branding org theme: %w", err)
	}
	out := fromTheme(settings.SignedAs(), theme)
	out.PrimaryColor = cmp.Or(settings.Branding.PrimaryColor, out.PrimaryColor)
	out.HasLogo = out.HasLogo || ownLogo(own)
	if out == (AppBranding{Name: out.Name}) {
		return nil, nil
	}
	return &out, nil
}

// Logo is the logo Branding reported, or ErrNoAppLogo.
func (r *AppBrandingResolver) Logo(ctx context.Context, orgID, requestID uuid.UUID) (Logo, error) {
	_, own, shown, err := r.resolve(ctx, orgID, requestID)
	switch {
	case err != nil:
		return Logo{}, err
	case !shown:
		return Logo{}, ErrNoAppLogo
	case ownLogo(own):
		return r.flows.Logo(ctx, orgID, own.flowID)
	}
	logo, err := r.themes.GetLogo(ctx, orgID)
	if errors.Is(err, themesettings.ErrNoLogo) {
		return Logo{}, ErrNoAppLogo
	}
	if err != nil {
		return Logo{}, fmt.Errorf("proofing: app branding org logo: %w", err)
	}
	return Logo{Bytes: logo.Bytes, ContentType: logo.ContentType}, nil
}

// requestFlowBranding is the flow branding of a request, none for a member's.
type requestFlowBranding struct {
	FlowBranding
	flowID string
}

func ownLogo(own requestFlowBranding) bool { return own.Custom && own.HasLogo }

// resolve is requestID's settings, an external person's with its flow's own
// branding over the org's, that flow branding, and whether the org shows its
// theme.
func (r *AppBrandingResolver) resolve(ctx context.Context, orgID, requestID uuid.UUID) (Settings, requestFlowBranding, bool, error) {
	kind, flowID, err := r.requests.SubjectKindOf(ctx, orgID, requestID)
	if err != nil {
		return Settings{}, requestFlowBranding{}, false, err
	}
	settings, err := r.settings.Get(ctx, orgID)
	if err != nil {
		return Settings{}, requestFlowBranding{}, false, fmt.Errorf("proofing: app branding settings: %w", err)
	}
	if kind != SubjectExternal {
		settings.Branding = Branding{ShowTheme: settings.Branding.ShowTheme}
		return settings, requestFlowBranding{}, settings.Branding.ShowTheme, nil
	}
	own, err := r.flows.Get(ctx, orgID, flowID)
	if err != nil {
		return Settings{}, requestFlowBranding{}, false, fmt.Errorf("proofing: app branding flow: %w", err)
	}
	return settings.brandedFor(own), requestFlowBranding{FlowBranding: own, flowID: flowID}, settings.Branding.ShowTheme, nil
}
