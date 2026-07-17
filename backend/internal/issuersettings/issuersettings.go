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
// org slug so the UI shows the effective default.
type Settings struct {
	Configured   bool       `json:"configured"`
	InstanceName string     `json:"instanceName"`
	DisplayName  string     `json:"displayName"`
	LogoURI      string     `json:"logoUri"`
	Enabled      bool       `json:"enabled"`
	UpdatedAt    *time.Time `json:"updatedAt,omitempty"`
}

// SettingsInput is an upsert of an org's issuer instance configuration.
type SettingsInput struct {
	InstanceName string
	DisplayName  string
	LogoURI      string
	Enabled      bool
}
