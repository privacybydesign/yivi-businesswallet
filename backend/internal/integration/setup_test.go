//go:build integration

// Package integration exercises the fully assembled router (server.New) against
// a real PostgreSQL database, with the EUDI OpenID4VP verifier replaced by an
// in-process fake. It is compiled only under the `integration` build tag.
package integration

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/privacybydesign/yivi-businesswallet/backend/internal/audit"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/auth"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/openid4vpverifier"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/organization"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/server"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/session"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/testdb"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/user"
)

const sessionTTL = time.Hour

// fakeVerifier stands in for the EUDI verifier: Result discloses the configured
// email (and optionally name), so a /claim logs in as that user. The claim shape
// mirrors the one proven against extractEmail in internal/auth/disclosure_test.go.
type fakeVerifier struct {
	email      string
	givenNames string
	familyName string
}

func (f *fakeVerifier) StartPresentation(_ context.Context, _ openid4vpverifier.Scope) (openid4vpverifier.Session, error) {
	return openid4vpverifier.Session{ID: "test-token", WalletLink: "openid4vp://?request_uri=https%3A%2F%2Fverifier.test"}, nil
}

func (f *fakeVerifier) Result(_ context.Context, _ string) (openid4vpverifier.Presentation, error) {
	claims := map[string]string{openid4vpverifier.ClaimEmail: f.email}
	if f.givenNames != "" || f.familyName != "" {
		claims[openid4vpverifier.ClaimGivenNames] = f.givenNames
		claims[openid4vpverifier.ClaimFamilyName] = f.familyName
	}
	return openid4vpverifier.Presentation{Claims: claims}, nil
}

func (f *fakeVerifier) Status(_ context.Context, _ string) (string, error) {
	return "DONE", nil
}

type meBody struct {
	ID              uuid.UUID `json:"id"`
	Email           string    `json:"email"`
	IsPlatformAdmin bool      `json:"isPlatformAdmin"`
}

type testEnv struct {
	t      *testing.T
	server *httptest.Server
	client *http.Client
	pool   *pgxpool.Pool
	fake   *fakeVerifier
}

// setup assembles the real router exactly as cmd/api does (minus the IRMA boot
// probe and the pruner) and returns an env with a cookie-jar HTTP client so the
// session cookie set by /claim is replayed automatically.
func setup(t *testing.T, platformAdmins ...string) *testEnv {
	t.Helper()
	pool, _ := testdb.Fresh(t)

	fake := &fakeVerifier{}
	userStore := user.NewStore(pool)
	sessionStore := session.NewStore(pool, sessionTTL)
	admins := auth.NewPlatformAdmins(platformAdmins)

	// Secure must be false: httptest.NewServer is plain HTTP and the cookie jar
	// drops Secure cookies, which would silently break the cookie round-trip.
	cookieCfg := auth.CookieConfig{Secure: false, MaxAge: int(sessionTTL.Seconds())}

	orgStore := organization.NewStore(pool, audit.NewDBRecorder())
	authService := auth.NewService(fake, userStore, sessionStore, orgStore)
	authHandler := auth.NewHandler(authService, sessionStore, cookieCfg, admins)
	requireUser := auth.RequireUser(sessionStore)
	orgService := organization.NewService(userStore, orgStore, authService)
	sessionIssuer := auth.NewSessionIssuer(sessionStore, cookieCfg)
	orgHandler := organization.NewHandler(orgStore, orgService, audit.NewReader(pool), sessionIssuer, requireUser, admins)

	srv := httptest.NewServer(server.New(pool, authHandler, orgHandler))
	t.Cleanup(srv.Close)

	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatalf("cookiejar: %v", err)
	}

	return &testEnv{
		t:      t,
		server: srv,
		client: &http.Client{Jar: jar},
		pool:   pool,
		fake:   fake,
	}
}

func (e *testEnv) do(method, path string, body io.Reader) *http.Response {
	e.t.Helper()
	req, err := http.NewRequest(method, e.server.URL+path, body)
	if err != nil {
		e.t.Fatalf("new request %s %s: %v", method, path, err)
	}
	resp, err := e.client.Do(req)
	if err != nil {
		e.t.Fatalf("do %s %s: %v", method, path, err)
	}
	return resp
}

// createUser provisions a user so they can be authenticated. Login now requires
// the user to already exist (the invitation model), so tests must provision
// before claiming. Idempotent on email.
func (e *testEnv) createUser(email string) uuid.UUID {
	e.t.Helper()
	var id uuid.UUID
	err := e.pool.QueryRow(context.Background(),
		`INSERT INTO users (email, given_names, last_name) VALUES ($1, 'Test', 'User')
		 ON CONFLICT (email) DO UPDATE SET email = EXCLUDED.email RETURNING id`, email,
	).Scan(&id)
	if err != nil {
		e.t.Fatalf("create user %q: %v", email, err)
	}
	return id
}

// login provisions the user, then completes a /claim as them and returns the
// authenticated identity. The session cookie is stored in the client's jar.
func (e *testEnv) login(email string) meBody {
	e.t.Helper()
	e.createUser(email)
	e.fake.email = email

	resp := e.do(http.MethodPost, "/api/v1/auth/session/test-token/claim", nil)
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		e.t.Fatalf("claim status = %d, want 200", resp.StatusCode)
	}

	var body meBody
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		e.t.Fatalf("decode claim response: %v", err)
	}
	return body
}

// getMe fetches /me and requires a 200, returning the authenticated identity.
func (e *testEnv) getMe(t *testing.T) meBody {
	t.Helper()
	resp := e.do(http.MethodGet, "/api/v1/me", nil)
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /me = %d, want 200", resp.StatusCode)
	}
	var body meBody
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decode /me: %v", err)
	}
	return body
}

func (e *testEnv) createOrg(name, slug string) uuid.UUID {
	e.t.Helper()
	var id uuid.UUID
	err := e.pool.QueryRow(context.Background(),
		"INSERT INTO organizations (name, slug) VALUES ($1, $2) RETURNING id", name, slug,
	).Scan(&id)
	if err != nil {
		e.t.Fatalf("create org %q: %v", slug, err)
	}
	return id
}

func (e *testEnv) addMembership(userID, orgID uuid.UUID, role string) {
	e.t.Helper()
	_, err := e.pool.Exec(context.Background(),
		"INSERT INTO memberships (user_id, organization_id, role) VALUES ($1, $2, $3)", userID, orgID, role,
	)
	if err != nil {
		e.t.Fatalf("add membership: %v", err)
	}
}
