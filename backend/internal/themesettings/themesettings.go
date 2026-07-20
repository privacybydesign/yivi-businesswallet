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
// Yivi look. Colours are CSS hex strings ("" when unset). HasLogo reports whether
// an uploaded logo is stored; LogoURI is the API path that serves it (set by the
// handler, "" when no logo is stored).
type Settings struct {
	Configured   bool       `json:"configured"`
	PrimaryColor string     `json:"primaryColor"`
	AccentColor  string     `json:"accentColor"`
	LogoURI      string     `json:"logoUri"`
	HasLogo      bool       `json:"-"`
	UpdatedAt    *time.Time `json:"updatedAt,omitempty"`
}

// SettingsInput is an upsert of an org's colours. Empty strings clear a field
// back to the default look.
type SettingsInput struct {
	PrimaryColor string
	AccentColor  string
}

// Logo is an uploaded theme logo image held in the store.
type Logo struct {
	Bytes       []byte
	ContentType string
}

// LogoUpdate describes what to do with the stored logo when saving settings.
// Replace false leaves the existing logo untouched (so colours can be changed on
// their own). Replace true with a non-empty Logo stores it; Replace true with an
// empty Logo clears the logo back to the default wordmark.
type LogoUpdate struct {
	Replace bool
	Logo    Logo
}
