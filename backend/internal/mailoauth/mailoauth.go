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
	"regexp"
	"strconv"
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
// because a field is missing, so a half-filled configuration fails before a
// request goes out rather than as a refusal from the endpoint. No caller
// distinguishes it today and none needs to: email.parseSettingsRequest refuses to
// enable an XOAUTH2 configuration that is missing a field, so an org that reaches
// a send has one that is complete. This is the guard behind that, for a caller
// that does not go through the settings screen.
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

// tokenFetch is one exchange in flight, which every other caller for the same
// credential waits on instead of opening its own. Without it a cache miss is per
// caller rather than per credential, and the miss arrives concurrently in the
// realistic case: notifications.Dispatcher runs each Channel.Notify on its own
// goroutine, so several outbox rows for one org fan out at once and each would
// spend a request against a token endpoint that is rate-limited per tenant.
type tokenFetch struct {
	done  chan struct{}
	token string
	err   error
}

// await blocks until the joined exchange finishes, or until this caller's own
// deadline passes first. The exchange runs on the leading caller's context, so a
// leader that gives up fails the callers waiting on it; they are notification
// sends, delivered at most once and retried by retrying the flow, and one lost
// message costs less than a fan-out that opens a token request per recipient.
func (f *tokenFetch) await(ctx context.Context) (string, error) {
	select {
	case <-f.done:
		return f.token, f.err
	case <-ctx.Done():
		return "", fmt.Errorf("mailoauth: token: %w", ctx.Err())
	}
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

	mu       sync.Mutex
	cache    map[cacheKey]cachedToken
	inFlight map[cacheKey]*tokenFetch
}

// NewMicrosoft builds the token source against the public Microsoft endpoint.
func NewMicrosoft(client *http.Client) *Microsoft {
	return &Microsoft{
		loginBaseURL: DefaultLoginBaseURL,
		http:         client,
		now:          time.Now,
		cache:        map[cacheKey]cachedToken{},
		inFlight:     map[cacheKey]*tokenFetch{},
	}
}

// WithEndpoint points the source at a different login host. It exists for tests
// and for a sovereign cloud deployment, which has its own login host. The URL is
// used as a prefix, with no trailing slash.
func (m *Microsoft) WithEndpoint(loginBaseURL string) *Microsoft {
	m.loginBaseURL = strings.TrimRight(loginBaseURL, "/")
	return m
}

// Token returns an access token for creds, from the cache when one is still good
// and from a single shared exchange when several callers miss at once. It returns
// ErrIncompleteConfig when a field is missing.
func (m *Microsoft) Token(ctx context.Context, creds Credentials) (string, error) {
	if !creds.complete() {
		return "", ErrIncompleteConfig
	}
	key := keyFor(creds)

	m.mu.Lock()
	if token, ok := m.cachedLocked(key); ok {
		m.mu.Unlock()
		return token, nil
	}
	if joined, ok := m.inFlight[key]; ok {
		m.mu.Unlock()
		return joined.await(ctx)
	}
	fetch := &tokenFetch{done: make(chan struct{})}
	m.inFlight[key] = fetch
	m.mu.Unlock()

	token, lifetime, err := m.fetch(ctx, creds)

	m.mu.Lock()
	delete(m.inFlight, key)
	// A lifetime at or under the margin is not worth caching; it is still worth
	// using for this send.
	if err == nil && lifetime > expiryMargin {
		m.evictLapsedLocked()
		m.cache[key] = cachedToken{token: token, expiresAt: m.now().Add(lifetime - expiryMargin)}
	}
	m.mu.Unlock()

	// The waiters read these only after the close, which is the edge that
	// publishes them.
	fetch.token, fetch.err = token, err
	close(fetch.done)
	return token, err
}

func (m *Microsoft) cachedLocked(key cacheKey) (string, bool) {
	entry, ok := m.cache[key]
	if !ok || !m.now().Before(entry.expiresAt) {
		return "", false
	}
	return entry.token, true
}

// evictLapsedLocked drops entries whose token has expired. Rotating a secret
// changes the cache key, so an entry for a credential still in use is overwritten
// in place and never needs evicting, but every rotation strands the entry for the
// old secret — without this a long-lived process keeps one per rotation per org
// for as long as it runs.
func (m *Microsoft) evictLapsedLocked() {
	now := m.now()
	for key, entry := range m.cache {
		if !now.Before(entry.expiresAt) {
			delete(m.cache, key)
		}
	}
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
		// Deliberately not wrapped: encoding/json puts the first offending byte of
		// the document into its SyntaxError, and that byte is the responder's, on
		// the one path whose request carried the client secret in its form body.
		// That is the same reason statusDetail repeats only allowlisted bytes.
		return "", 0, errors.New("mailoauth: token: response was not a token document")
	}
	if body.AccessToken == "" {
		return "", 0, errors.New("mailoauth: token: response carried no access token")
	}
	return body.AccessToken, time.Duration(body.ExpiresIn) * time.Second, nil
}

// oauthRefusals is every error code RFC 6749 section 5.2 defines for a token
// endpoint, and the only codes repeated into an error here. The code field is a
// free JSON string the responder fills in and nothing validates, so matching it
// against this closed set is what makes the repeated bytes a constant from this
// file rather than bytes the far side chose. A code Microsoft adds later reads as
// no code at all until it is listed here, which costs the reason's detail and
// leaks nothing.
var oauthRefusals = map[string]bool{
	"invalid_request":        true,
	"invalid_client":         true,
	"invalid_grant":          true,
	"unauthorized_client":    true,
	"unsupported_grant_type": true,
	"invalid_scope":          true,
}

// entraErrorCode matches the AADSTSnnnnnn code Microsoft opens error_description
// with. That code is the token an admin searches for (7000215 is a wrong secret,
// 7000222 an expired one, 65001 missing consent), so it is carried through as a
// second closed shape: the match is anchored and the digits are bounded, and the
// free text after it — which is where an intermediary would quote the request
// back — is dropped with the rest of the description.
var entraErrorCode = regexp.MustCompile(`^AADSTS[0-9]{4,7}`)

// statusDetail renders a failed response as this side's own reading of its status,
// plus the OAuth error code the document carries when that code is one RFC 6749
// defines. The reason an admin needs is in Microsoft's error document, but the
// answer comes from the far side of the connection this request sent the client
// secret over in its form body, and that far side is not necessarily the tenant:
// WithEndpoint is a supported mode, and a TLS-terminating egress proxy or a
// captive portal in front of login.microsoftonline.com is an ordinary deployment
// condition. Any such responder can quote the request back — in the body, in the
// free-text error_description, or in the status line, where Go keeps the
// responder's reason phrase verbatim in resp.Status — and this error is logged at
// ERROR by respond/handler.go and notifications/dispatcher.go. So the status is
// built from the code alone and only an allowlisted refusal is repeated; anything
// else is dropped whole. Same posture as internal/slackchannel's refusalReason,
// and for the same reason: allowlisting the answer holds whatever shape a leak
// would have taken, where enumerating shapes to scrub lost twice there.
func statusDetail(resp *http.Response) string {
	detail := statusLine(resp.StatusCode)
	excerpt, _ := io.ReadAll(io.LimitReader(resp.Body, maxErrorBody))
	var document struct {
		Error            string `json:"error"`
		ErrorDescription string `json:"error_description"`
	}
	if err := json.Unmarshal(excerpt, &document); err != nil || !oauthRefusals[document.Error] {
		return detail
	}
	detail += ": " + document.Error
	if code := entraErrorCode.FindString(document.ErrorDescription); code != "" {
		detail += " (" + code + ")"
	}
	return detail
}

// statusLine renders a response's status from its code alone. resp.Status is not
// usable for this: Go parses the status line as `code SP reason-phrase` and keeps
// the phrase byte for byte, so a responder that quoted the request into its reason
// phrase would put the client secret in the error. http.StatusText is empty for a
// code it does not know, and then the number stands on its own.
func statusLine(code int) string {
	if text := http.StatusText(code); text != "" {
		return fmt.Sprintf("%d %s", code, text)
	}
	return strconv.Itoa(code)
}
