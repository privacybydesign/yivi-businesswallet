// Package mailoauth acquires the OAuth2 access token an SMTP XOAUTH2 send
// authenticates with. It is a client seam, like internal/provisioner: one method
// that talks to somebody else's token endpoint and returns a string.
//
// Microsoft 365 (Exchange Online) is the provider it exists for. Microsoft has
// turned off Basic Authentication for SMTP AUTH on most tenants, so a password is
// no longer a usable credential there and client submission has to present a
// bearer token instead. The flow is client credentials (app-only): mail goes out
// on a schedule with nobody signed in, so there is no user to run an auth-code
// flow against — the same reason internal/provisioner uses it for Graph.
//
// The tenant admin's side of this is an app registration with the Office 365
// Exchange Online application permission SMTP.SendAsApp, admin-consented, and a
// service principal granted send-as rights on the mailbox that From address
// belongs to.
package mailoauth

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

const (
	// DefaultLoginBaseURL is the Microsoft identity platform token host.
	DefaultLoginBaseURL = "https://login.microsoftonline.com"

	// outlookScope requests the app registration's own consented application
	// permission (SMTP.SendAsApp) rather than a signed-in user's. The .default
	// suffix is what makes it the app-only, already-consented set.
	outlookScope = "https://outlook.office365.com/.default"

	// expiryMargin is taken off a token's lifetime before it is reused, so a
	// token that is about to lapse is not handed to a send that then fails
	// halfway through a fan-out.
	expiryMargin = 2 * time.Minute

	// maxErrorBody bounds how much of a failed response is read into an error
	// message. Mirrors internal/provisioner.
	maxErrorBody = 4 << 10
)

// ErrIncompleteConfig reports credentials that cannot be exchanged for a token
// because a field is missing. It is separate from a refusal by the token
// endpoint: one is a settings screen to finish, the other a credential to fix.
var ErrIncompleteConfig = errors.New("mailoauth: incomplete oauth configuration")

// Credentials is one organisation's app registration.
type Credentials struct {
	TenantID     string
	ClientID     string
	ClientSecret string
}

func (c Credentials) complete() bool {
	return c.TenantID != "" && c.ClientID != "" && c.ClientSecret != ""
}

// cacheKey identifies a set of credentials without holding the secret: rotating
// the secret has to miss the cache, but keeping the plaintext in a long-lived map
// would put every tenant's secret in a heap dump. The digest gives both.
type cacheKey struct {
	tenantID     string
	clientID     string
	secretDigest [sha256.Size]byte
}

func keyFor(c Credentials) cacheKey {
	return cacheKey{
		tenantID:     c.TenantID,
		clientID:     c.ClientID,
		secretDigest: sha256.Sum256([]byte(c.ClientSecret)),
	}
}

type cachedToken struct {
	token     string
	expiresAt time.Time
}

// Microsoft mints tokens against the Microsoft identity platform.
//
// Tokens are cached per credential until shortly before they lapse. Without that
// every message costs a token request: a notification to an organisation's admins
// or a bulk credential offer sends one message per recipient, and the token
// endpoint is rate-limited per tenant.
type Microsoft struct {
	loginBaseURL string
	http         *http.Client
	// now is time.Now, replaced in tests to age a cached token.
	now func() time.Time

	mu    sync.Mutex
	cache map[cacheKey]cachedToken
}

// NewMicrosoft builds the token source against the public Microsoft endpoint.
func NewMicrosoft(client *http.Client) *Microsoft {
	return &Microsoft{
		loginBaseURL: DefaultLoginBaseURL,
		http:         client,
		now:          time.Now,
		cache:        map[cacheKey]cachedToken{},
	}
}

// WithEndpoint points the source at a different login host. It exists for tests
// and for a sovereign cloud deployment, which has its own login host. The URL is
// used as a prefix, with no trailing slash.
func (m *Microsoft) WithEndpoint(loginBaseURL string) *Microsoft {
	m.loginBaseURL = strings.TrimRight(loginBaseURL, "/")
	return m
}

// Token returns an access token for creds, from the cache when one is still
// good. It returns ErrIncompleteConfig when a field is missing, so the caller can
// tell "not finished configuring" from "the tenant refused us".
func (m *Microsoft) Token(ctx context.Context, creds Credentials) (string, error) {
	if !creds.complete() {
		return "", ErrIncompleteConfig
	}
	key := keyFor(creds)
	if token, ok := m.cached(key); ok {
		return token, nil
	}

	token, lifetime, err := m.fetch(ctx, creds)
	if err != nil {
		return "", err
	}
	// A lifetime at or under the margin is not worth caching; it is still worth
	// using for this send.
	if lifetime > expiryMargin {
		m.mu.Lock()
		m.cache[key] = cachedToken{token: token, expiresAt: m.now().Add(lifetime - expiryMargin)}
		m.mu.Unlock()
	}
	return token, nil
}

func (m *Microsoft) cached(key cacheKey) (string, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	entry, ok := m.cache[key]
	if !ok || !m.now().Before(entry.expiresAt) {
		return "", false
	}
	return entry.token, true
}

// fetch exchanges the client credentials for an access token and its lifetime.
func (m *Microsoft) fetch(ctx context.Context, creds Credentials) (string, time.Duration, error) {
	form := url.Values{
		"client_id":     {creds.ClientID},
		"client_secret": {creds.ClientSecret},
		"grant_type":    {"client_credentials"},
		"scope":         {outlookScope},
	}
	endpoint := m.loginBaseURL + "/" + url.PathEscape(creds.TenantID) + "/oauth2/v2.0/token"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(form.Encode()))
	if err != nil {
		return "", 0, fmt.Errorf("mailoauth: token request: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := m.http.Do(req)
	if err != nil {
		return "", 0, fmt.Errorf("mailoauth: token: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return "", 0, fmt.Errorf("mailoauth: token: %s", statusDetail(resp))
	}

	var body struct {
		AccessToken string `json:"access_token"`
		ExpiresIn   int64  `json:"expires_in"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return "", 0, fmt.Errorf("mailoauth: token: decode response: %w", err)
	}
	if body.AccessToken == "" {
		return "", 0, errors.New("mailoauth: token: response carried no access token")
	}
	return body.AccessToken, time.Duration(body.ExpiresIn) * time.Second, nil
}

// statusDetail renders a failed response as status plus a bounded excerpt of the
// body. Microsoft's error document names the reason (an expired secret, a
// permission that was never consented to), which is what an admin has to read to
// fix it. The request carried the secret in its body, not its URL, so nothing
// here can echo it back: the response body is the tenant's own error document.
func statusDetail(resp *http.Response) string {
	excerpt, _ := io.ReadAll(io.LimitReader(resp.Body, maxErrorBody))
	detail := strings.TrimSpace(string(excerpt))
	if detail == "" {
		return resp.Status
	}
	return resp.Status + ": " + detail
}
