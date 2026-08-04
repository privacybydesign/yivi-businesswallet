// Package email is the org-scoped e-mail capability: per-organization SMTP
// settings (password encrypted at rest) plus sending transactional mail such as
// the "your credential is ready" notification to natural-person attestation
// recipients. The SMTP wire protocol lives in internal/mailer; this slice owns
// the settings, encryption and message composition.
package email

import (
	"errors"
	"fmt"
	"time"

	"github.com/privacybydesign/yivi-businesswallet/backend/internal/mailer"
)

// ErrNotConfigured means the organization has no usable (present + enabled) SMTP
// configuration, so mail cannot be sent.
var ErrNotConfigured = errors.New("email: smtp not configured for organization")

// InvalidTemplateError reports a tenant template that does not satisfy
// ValidateTemplate. It carries the reason separately from the wrapping prose so a
// handler can answer 400 naming the offending field — which is what the editor
// shows beside the input — instead of turning a save mistake into a 500.
type InvalidTemplateError struct {
	Reason error
}

func (e *InvalidTemplateError) Error() string {
	return fmt.Sprintf("email: invalid template: %v", e.Reason)
}

func (e *InvalidTemplateError) Unwrap() error { return e.Reason }

// Settings is the non-secret view of an org's SMTP configuration (never the
// password, never the OAuth client secret). Configured is false when no row
// exists yet.
type Settings struct {
	Configured bool   `json:"configured"`
	Host       string `json:"host"`
	Port       int    `json:"port"`
	Username   string `json:"username"`
	// AuthMechanism is how a send authenticates: mailer.AuthPlain (username and
	// password) or mailer.AuthXOAuth2 (an OAuth2 bearer token, which is what
	// Microsoft 365 requires).
	AuthMechanism mailer.AuthMechanism `json:"authMechanism"`
	// TenantID and ClientID identify the app registration an XOAuth2 org mints
	// its token from. Empty for a password org.
	TenantID    string `json:"tenantId"`
	ClientID    string `json:"clientId"`
	FromName    string `json:"fromName"`
	FromAddress string `json:"fromAddress"`
	Enabled     bool   `json:"enabled"`
	// HasPassword reports whether a password is stored, so the UI can show
	// "unchanged" without ever receiving the secret.
	HasPassword bool `json:"hasPassword"`
	// HasClientSecret is the same for the app registration's client secret.
	HasClientSecret bool       `json:"hasClientSecret"`
	UpdatedAt       *time.Time `json:"updatedAt,omitempty"`
}

// SettingsInput is an upsert of an org's SMTP configuration. Password and
// ClientSecret are optional on update: when nil the stored value is kept; when a
// non-nil empty string it is cleared (a no-auth relay, or an org switching away
// from XOAUTH2).
type SettingsInput struct {
	Host          string
	Port          int
	Username      string
	Password      *string
	AuthMechanism mailer.AuthMechanism
	TenantID      string
	ClientID      string
	ClientSecret  *string
	FromName      string
	FromAddress   string
	Enabled       bool
}
