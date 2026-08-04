// Package issuersettings is the org-scoped configuration of an organization's
// Veramo issuer instance: the instance name offers route to (the {instance} path
// segment at the hosted issuer) plus the display name / logo used as the issuer's
// wallet-facing branding. There is no secret here — the hosted issuer's admin
// token is deployment-global (the same token is rendered into every instance's
// config), so per-org routing needs only the instance name. See
// .ai/features/attestations.md.
package issuersettings

import "time"

// Settings is the view of an org's issuer instance configuration. Configured is
// false when no row exists yet; the handler then defaults InstanceName to the
// org slug so the UI shows the effective default. HasLogo reports whether an
// uploaded logo is stored; LogoURI is the API path that serves it for the admin
// preview (set by the handler, "" when no logo is stored). The wallet-facing
// bundle embeds the logo as a data: URI instead (see Store.BundleConfig).
type Settings struct {
	Configured   bool       `json:"configured"`
	InstanceName string     `json:"instanceName"`
	DisplayName  string     `json:"displayName"`
	LogoURI      string     `json:"logoUri"`
	HasLogo      bool       `json:"-"`
	Enabled      bool       `json:"enabled"`
	UpdatedAt    *time.Time `json:"updatedAt,omitempty"`
}

// SettingsInput is an upsert of an org's issuer instance configuration. The logo
// is applied separately via LogoUpdate so the branding can be changed without
// re-uploading the logo.
type SettingsInput struct {
	InstanceName string
	DisplayName  string
	Enabled      bool
}

// Logo is an uploaded issuer logo image held in the store.
type Logo struct {
	Bytes       []byte
	ContentType string
}

// LogoUpdate describes what to do with the stored logo when saving settings.
// Replace false leaves the existing logo untouched (so the other fields can be
// changed on their own). Replace true with a non-empty Logo stores it; Replace
// true with an empty Logo clears the logo.
type LogoUpdate struct {
	Replace bool
	Logo    Logo
}
