// Package email is the org-scoped e-mail capability: per-organization SMTP
// settings (password encrypted at rest) plus sending transactional mail such as
// the "your credential is ready" notification to natural-person attestation
// recipients. The SMTP wire protocol lives in internal/mailer; this slice owns
// the settings, encryption and message composition.
package email

import (
	"errors"
	"time"
)

// ErrNotConfigured means the organization has no usable (present + enabled) SMTP
// configuration, so mail cannot be sent.
var ErrNotConfigured = errors.New("email: smtp not configured for organization")

// Settings is the non-secret view of an org's SMTP configuration (never the
// password). Configured is false when no row exists yet.
type Settings struct {
	Configured  bool   `json:"configured"`
	Host        string `json:"host"`
	Port        int    `json:"port"`
	Username    string `json:"username"`
	FromName    string `json:"fromName"`
	FromAddress string `json:"fromAddress"`
	Enabled     bool   `json:"enabled"`
	// HasPassword reports whether a password is stored, so the UI can show
	// "unchanged" without ever receiving the secret.
	HasPassword bool       `json:"hasPassword"`
	UpdatedAt   *time.Time `json:"updatedAt,omitempty"`
}

// SettingsInput is an upsert of an org's SMTP configuration. Password is optional
// on update: when nil the stored password is kept; when a non-nil empty string it
// is cleared (no-auth relay).
type SettingsInput struct {
	Host        string
	Port        int
	Username    string
	Password    *string
	FromName    string
	FromAddress string
	Enabled     bool
}
