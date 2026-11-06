// Package csc is the per-organization connection settings for a Cloud Signature
// Consortium (CSC) API v2 signing provider — a remote QTSP the business wallet
// drives to create qualified signatures. The wallet is the requestor here, the
// same external-provider relationship internal/qerdsprovider has with a QERDS
// access point; this slice only stores how to reach a provider and can probe it.
//
// What it holds: a provider kind (a hosted reference "sample" or a "custom"
// endpoint), the CSC base URL, the OAuth client id registered at the QTSP, and
// the client secret. The secret is the credential — it is encrypted at rest under
// the deployment CSC key, never returned to the frontend (Settings reports
// HasClientSecret, not the secret), and never put in a log line or error.
//
// The signing ceremony itself (OAuth authorize over OID4VP, credentials/list,
// signHash, PAdES assembly) is not here yet — this is the settings section and a
// connection test only.
package csc

import (
	"errors"
	"net/url"
	"strings"
	"time"
)

// ErrNotConfigured means the organization has no CSC provider configured (no row,
// or a row with no base URL), so there is nothing to reach.
var ErrNotConfigured = errors.New("csc: no signing provider configured for organization")

// ErrNoEncryptionKey means the deployment has no CSC encryption key, so a client
// secret cannot be stored. It is a deployment misconfiguration, but the admin
// saving the settings is the one who sees it, so the handler answers it with its
// own code rather than a 500.
var ErrNoEncryptionKey = errors.New("csc: no encryption key configured; cannot store a client secret")

// ProviderKind selects how the CSC endpoint is understood. It is validated in Go
// (KnownProviderKind), not by a DB CHECK, so adding a kind is a Go-only change.
type ProviderKind string

const (
	// ProviderKindSample is the hosted demo QTSP we offer (issue #194). Its default
	// base URL is that demo environment; request credentials at support@yivi.app.
	ProviderKindSample ProviderKind = "sample"
	// ProviderKindCustom is any other CSC API v2 endpoint (a real QTSP).
	ProviderKindCustom ProviderKind = "custom"
)

// SampleBaseURL is the base URL of the hosted demo QTSP we offer. Request
// credentials for it at support@yivi.app.
const SampleBaseURL = "https://csc-signer.staging.yivi.app"

// ProviderKindInfo is one selectable provider kind plus the base URL to pre-fill
// when it is chosen, so the settings screen renders the choices from one response
// and cannot drift from the kinds this deployment knows.
type ProviderKindInfo struct {
	ID             ProviderKind `json:"id"`
	DefaultBaseURL string       `json:"defaultBaseUrl"`
}

// ProviderKinds returns the selectable kinds, in the order the settings screen
// lists them.
func ProviderKinds() []ProviderKindInfo {
	return []ProviderKindInfo{
		{ID: ProviderKindSample, DefaultBaseURL: SampleBaseURL},
		{ID: ProviderKindCustom, DefaultBaseURL: ""},
	}
}

// KnownProviderKind reports whether kind is one this deployment can configure.
func KnownProviderKind(kind ProviderKind) bool {
	for _, k := range ProviderKinds() {
		if k.ID == kind {
			return true
		}
	}
	return false
}

// Settings is the non-secret view of an org's CSC configuration (never the client
// secret). Configured is false when no row exists yet.
type Settings struct {
	Configured   bool         `json:"configured"`
	Enabled      bool         `json:"enabled"`
	ProviderKind ProviderKind `json:"providerKind"`
	BaseURL      string       `json:"baseUrl"`
	ClientID     string       `json:"clientId"`
	// HasClientSecret reports whether a client secret is stored, so the screen can
	// show one is in place and offer to replace it without ever receiving it.
	HasClientSecret bool       `json:"hasClientSecret"`
	UpdatedAt       *time.Time `json:"updatedAt,omitempty"`
}

// SettingsInput is a full replacement of an org's CSC configuration. ClientSecret
// is a pointer for the same reason the SMTP password is: nil keeps the stored
// secret, a pointer to "" clears it, and any other value replaces it.
type SettingsInput struct {
	Enabled      bool
	ProviderKind ProviderKind
	BaseURL      string
	ClientID     string
	ClientSecret *string
}

// Info is what a connection test read back from the provider's /csc/v2/info: the
// provider's own name and the CSC spec version it reports. It carries nothing the
// far side wrote beyond these two identifying fields.
type Info struct {
	Name  string `json:"name"`
	Specs string `json:"specs"`
}

// TestError reports that a connection test did not succeed. Reason is safe to show
// an admin: it names the status this side read or that the endpoint was
// unreachable — never a byte the far side wrote, and never the URL.
type TestError struct {
	Reason string
}

func (e *TestError) Error() string { return "csc: connection test failed: " + e.Reason }

// Normalize trims and validates an input, returning a canonical copy. Storing the
// canonical form keeps a saved configuration comparable to the next one, so the
// audit diff reads as the change the admin actually made.
func Normalize(in SettingsInput) (SettingsInput, error) {
	out := SettingsInput{
		Enabled:      in.Enabled,
		ProviderKind: ProviderKind(strings.TrimSpace(string(in.ProviderKind))),
		ClientID:     strings.TrimSpace(in.ClientID),
	}
	if out.ProviderKind == "" {
		out.ProviderKind = ProviderKindSample
	}
	if !KnownProviderKind(out.ProviderKind) {
		return SettingsInput{}, errors.New("unknown provider kind " + string(out.ProviderKind))
	}
	baseURL, err := NormalizeBaseURL(in.BaseURL)
	if err != nil {
		return SettingsInput{}, err
	}
	out.BaseURL = baseURL
	if in.ClientSecret != nil {
		secret := strings.TrimSpace(*in.ClientSecret)
		out.ClientSecret = &secret
	}
	return out, nil
}

// ErrInvalidBaseURL means the base URL is not an http(s) URL with a host. Its
// message repeats no part of the input.
var ErrInvalidBaseURL = errors.New("csc: the base URL must be an http or https URL with a host")

// NormalizeBaseURL trims a submitted CSC base URL and checks it is an http(s) URL
// with a host and no embedded credentials. An empty string is returned as such
// (a configuration with no endpoint yet), because clearing it is not an error.
// The host is lowercased and any trailing slash is dropped so what is stored (and
// what /csc/v2/info is appended to) is one shape whatever was pasted.
//
// Unlike the Teams/Slack webhook, the host is not pinned: a QTSP is any endpoint
// the tenant chose, the same admin-configured egress as the SMTP host and the
// directory tenant. The reachability of that endpoint is the admin's decision;
// the route that saves it is org-admin only.
func NormalizeBaseURL(raw string) (string, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return "", nil
	}
	parsed, err := url.Parse(trimmed)
	if err != nil {
		return "", ErrInvalidBaseURL
	}
	if (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.User != nil {
		return "", ErrInvalidBaseURL
	}
	if parsed.Hostname() == "" {
		return "", ErrInvalidBaseURL
	}
	parsed.Host = strings.ToLower(parsed.Host)
	parsed.Path = strings.TrimRight(parsed.Path, "/")
	// Clear the fragment AND the query: Client.Info builds its endpoint by string
	// concatenation (base + "/csc/v2/info"), so a stored query would fold the probe
	// path into it (base "http://h?tenant=x" -> query "tenant=x/csc/v2/info", path
	// "/"), reaching the server root and passing the test without ever hitting /info.
	// A trailing "?" (ForceQuery) does the same, so drop that too.
	parsed.Fragment = ""
	parsed.RawQuery = ""
	parsed.ForceQuery = false
	return parsed.String(), nil
}
