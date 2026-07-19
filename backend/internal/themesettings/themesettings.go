// Package themesettings is the org-scoped branding of an organization's business
// wallet: a primary colour, an accent colour and a logo. The frontend maps the
// colours onto its design tokens at runtime and renders the logo in place of the
// default wordmark, so the wallet visibly belongs to the tenant. There is no
// secret here — the values are purely presentational. Reads are open to any
// member (the app themes itself for everyone); writes are org-admin only.
package themesettings

import "time"

// Settings is the view of an org's theme. Configured is false when no row exists
// yet; every field then holds its zero value and the frontend keeps the default
// Yivi look. Colours are CSS hex strings ("" when unset); LogoURI is a URI.
type Settings struct {
	Configured   bool       `json:"configured"`
	PrimaryColor string     `json:"primaryColor"`
	AccentColor  string     `json:"accentColor"`
	LogoURI      string     `json:"logoUri"`
	UpdatedAt    *time.Time `json:"updatedAt,omitempty"`
}

// SettingsInput is an upsert of an org's theme. Empty strings clear a field back
// to the default look.
type SettingsInput struct {
	PrimaryColor string
	AccentColor  string
	LogoURI      string
}
