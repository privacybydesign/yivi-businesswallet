package proofing

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"

	"github.com/privacybydesign/yivi-businesswallet/backend/internal/organization"
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

type appBrandingCustomers interface {
	Get(ctx context.Context, orgID, id uuid.UUID) (Customer, error)
	Logo(ctx context.Context, orgID, id uuid.UUID) (CustomerLogo, error)
}

type appBrandingThemes interface {
	GetSettings(ctx context.Context, orgID uuid.UUID) (themesettings.Settings, error)
	GetLogo(ctx context.Context, orgID uuid.UUID) (themesettings.Logo, error)
}

type appBrandingOrgs interface {
	GetByID(ctx context.Context, id uuid.UUID) (organization.Organization, error)
}

// AppBrandingResolver picks a request's branding from who sent it, never from
// anything the app sends. The org's own requests get the org's theme. A
// customer's request gets the customer's white-labeling when it has one (see
// whiteLabeled): its colours, font and logo, the design's own for the rest; else the
// org's theme, with the customer's logo when it has one and the org's
// otherwise. Either way under the customer's name.
type AppBrandingResolver struct {
	requests  *RequestStore
	customers appBrandingCustomers
	themes    appBrandingThemes
	orgs      appBrandingOrgs
}

func NewAppBrandingResolver(requests *RequestStore, customers appBrandingCustomers, themes appBrandingThemes, orgs appBrandingOrgs) *AppBrandingResolver {
	return &AppBrandingResolver{requests: requests, customers: customers, themes: themes, orgs: orgs}
}

// Branding is the org's requestID's branding, nil when neither the customer
// nor the org set any.
func (r *AppBrandingResolver) Branding(ctx context.Context, orgID, requestID uuid.UUID) (*AppBranding, error) {
	customerID, err := r.requests.CustomerOf(ctx, orgID, requestID)
	if err != nil {
		return nil, err
	}
	theme, err := r.themes.GetSettings(ctx, orgID)
	if err != nil {
		return nil, fmt.Errorf("proofing: app branding org theme: %w", err)
	}
	var out AppBranding
	if customerID != nil {
		customer, err := r.customers.Get(ctx, orgID, *customerID)
		if err != nil {
			return nil, fmt.Errorf("proofing: app branding customer: %w", err)
		}
		if b := customer.Branding; whiteLabeled(b) {
			out = AppBranding{
				Name: customer.SignedAs(), PrimaryColor: b.PrimaryColor, SecondaryColor: b.SecondaryColor,
				TextColor: b.TextColor, SurfaceColor: b.SurfaceColor, BorderColor: b.BorderColor,
				LinkColor: b.LinkColor, SuccessColor: b.SuccessColor, WarningColor: b.WarningColor,
				ErrorColor: b.ErrorColor, HeaderColor: b.TopbarColor, FontFamily: b.FontFamily, HasLogo: b.HasLogo,
			}
		} else {
			out = fromTheme(customer.SignedAs(), theme)
			out.HasLogo = out.HasLogo || customer.Branding.HasLogo
		}
	} else {
		org, err := r.orgs.GetByID(ctx, orgID)
		if err != nil {
			return nil, fmt.Errorf("proofing: app branding org: %w", err)
		}
		out = fromTheme(org.Name, theme)
	}
	if out == (AppBranding{Name: out.Name}) {
		return nil, nil
	}
	return &out, nil
}

// Logo is the logo Branding reported, or ErrNoAppLogo.
func (r *AppBrandingResolver) Logo(ctx context.Context, orgID, requestID uuid.UUID) (CustomerLogo, error) {
	customerID, err := r.requests.CustomerOf(ctx, orgID, requestID)
	if err != nil {
		return CustomerLogo{}, err
	}
	if customerID != nil {
		customer, err := r.customers.Get(ctx, orgID, *customerID)
		if err != nil {
			return CustomerLogo{}, fmt.Errorf("proofing: app branding customer: %w", err)
		}
		if whiteLabeled(customer.Branding) || customer.Branding.HasLogo {
			logo, err := r.customers.Logo(ctx, orgID, *customerID)
			if errors.Is(err, ErrNoCustomerLogo) {
				return CustomerLogo{}, ErrNoAppLogo
			}
			return logo, err
		}
	}
	logo, err := r.themes.GetLogo(ctx, orgID)
	if errors.Is(err, themesettings.ErrNoLogo) {
		return CustomerLogo{}, ErrNoAppLogo
	}
	if err != nil {
		return CustomerLogo{}, fmt.Errorf("proofing: app branding org logo: %w", err)
	}
	return CustomerLogo{Bytes: logo.Bytes, ContentType: logo.ContentType}, nil
}

// whiteLabeled reports whether a customer has white-labeling of its own: a
// colour or a font. A logo alone is a mark under the org's look, not a look of
// its own; its name, support contact and privacy link are not looks either.
func whiteLabeled(b CustomerBranding) bool {
	look := CustomerBranding{
		PrimaryColor: b.PrimaryColor, SecondaryColor: b.SecondaryColor, TextColor: b.TextColor,
		SurfaceColor: b.SurfaceColor, BorderColor: b.BorderColor, LinkColor: b.LinkColor,
		SuccessColor: b.SuccessColor, WarningColor: b.WarningColor, ErrorColor: b.ErrorColor,
		TopbarColor: b.TopbarColor, FontFamily: b.FontFamily,
	}
	return look != CustomerBranding{}
}
