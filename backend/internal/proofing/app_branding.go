package proofing

import (
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
	Logo(ctx context.Context, orgID uuid.UUID) (Logo, error)
}

type appBrandingThemes interface {
	GetSettings(ctx context.Context, orgID uuid.UUID) (themesettings.Settings, error)
	GetLogo(ctx context.Context, orgID uuid.UUID) (themesettings.Logo, error)
}

// AppBrandingResolver picks a request's branding from who it was sent to,
// never from anything the app sends. A request to a member gets the org's
// theme. A request to an external person gets the org's proofing white-labeling
// when it has one (see whiteLabeled): its colours, font and logo, the design's
// own for the rest; else the org's theme, with the proofing logo when it has one
// and the org's otherwise. Either way under the name the org's proofing mail
// signs with.
type AppBrandingResolver struct {
	requests *RequestStore
	settings appBrandingSettings
	themes   appBrandingThemes
}

func NewAppBrandingResolver(requests *RequestStore, settings appBrandingSettings, themes appBrandingThemes) *AppBrandingResolver {
	return &AppBrandingResolver{requests: requests, settings: settings, themes: themes}
}

// Branding is the org's requestID's branding, nil when neither the org's
// proofing settings nor its theme set any.
func (r *AppBrandingResolver) Branding(ctx context.Context, orgID, requestID uuid.UUID) (*AppBranding, error) {
	kind, err := r.requests.SubjectKindOf(ctx, orgID, requestID)
	if err != nil {
		return nil, err
	}
	theme, err := r.themes.GetSettings(ctx, orgID)
	if err != nil {
		return nil, fmt.Errorf("proofing: app branding org theme: %w", err)
	}
	settings, err := r.settings.Get(ctx, orgID)
	if err != nil {
		return nil, fmt.Errorf("proofing: app branding settings: %w", err)
	}
	var out AppBranding
	if kind == SubjectExternal {
		if b := settings.Branding; whiteLabeled(b) {
			out = AppBranding{
				Name: settings.SignedAs(), PrimaryColor: b.PrimaryColor, SecondaryColor: b.SecondaryColor,
				TextColor: b.TextColor, SurfaceColor: b.SurfaceColor, BorderColor: b.BorderColor,
				LinkColor: b.LinkColor, SuccessColor: b.SuccessColor, WarningColor: b.WarningColor,
				ErrorColor: b.ErrorColor, HeaderColor: b.TopbarColor, FontFamily: b.FontFamily, HasLogo: b.HasLogo,
			}
		} else {
			out = fromTheme(settings.SignedAs(), theme)
			out.HasLogo = out.HasLogo || b.HasLogo
		}
	} else {
		out = fromTheme(settings.OrgName, theme)
	}
	if out == (AppBranding{Name: out.Name}) {
		return nil, nil
	}
	return &out, nil
}

// Logo is the logo Branding reported, or ErrNoAppLogo.
func (r *AppBrandingResolver) Logo(ctx context.Context, orgID, requestID uuid.UUID) (Logo, error) {
	kind, err := r.requests.SubjectKindOf(ctx, orgID, requestID)
	if err != nil {
		return Logo{}, err
	}
	if kind == SubjectExternal {
		settings, err := r.settings.Get(ctx, orgID)
		if err != nil {
			return Logo{}, fmt.Errorf("proofing: app branding settings: %w", err)
		}
		if whiteLabeled(settings.Branding) || settings.Branding.HasLogo {
			logo, err := r.settings.Logo(ctx, orgID)
			if errors.Is(err, ErrNoLogo) {
				return Logo{}, ErrNoAppLogo
			}
			return logo, err
		}
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

// whiteLabeled reports whether the org's proofing has white-labeling of its
// own: a colour or a font. A logo alone is a mark under the org's look, not a
// look of its own; its name, support contact and privacy link are not looks
// either.
func whiteLabeled(b Branding) bool {
	look := Branding{
		PrimaryColor: b.PrimaryColor, SecondaryColor: b.SecondaryColor, TextColor: b.TextColor,
		SurfaceColor: b.SurfaceColor, BorderColor: b.BorderColor, LinkColor: b.LinkColor,
		SuccessColor: b.SuccessColor, WarningColor: b.WarningColor, ErrorColor: b.ErrorColor,
		TopbarColor: b.TopbarColor, FontFamily: b.FontFamily,
	}
	return look != Branding{}
}
