//go:build integration

// Package integration exercises the fully assembled router (server.New) against
// a real PostgreSQL database, with the EUDI OpenID4VP verifier replaced by an
// in-process fake. It is compiled only under the `integration` build tag.
package integration

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/privacybydesign/yivi-businesswallet/backend/internal/attestation"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/audit"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/auth"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/issuersettings"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/openid4vciissuer"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/openid4vpverifier"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/organization"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/presentation"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/server"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/session"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/testdb"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/user"
)

const sessionTTL = time.Hour

// disclosureToken is the client-facing presentation id used across these tests.
// setup seeds a presentation_sessions row mapping it to a verifier transaction id
// so /claim, /status and disclosure flows resolve it (production mints a random
// id per POST /auth/session; the fake verifier ignores the transaction id).
const disclosureToken = "test-token"

// fakeVerifier stands in for the EUDI verifier: Result discloses the configured
// email (and optionally name), so a /claim logs in as that user. The claim shape
// mirrors the one proven against extractEmail in internal/auth/disclosure_test.go.
type fakeVerifier struct {
	email      string
	givenNames string
	familyName string
}

func (f *fakeVerifier) StartPresentation(_ context.Context, _ openid4vpverifier.Scope) (openid4vpverifier.Session, error) {
	return openid4vpverifier.Session{TransactionID: "verifier-tx", WalletLink: "openid4vp://?request_uri=https%3A%2F%2Fverifier.test"}, nil
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

// stubEmailNotifier / stubQerdsNotifier satisfy the attestation delivery seams
// without sending anything (delivery is best-effort and not the unit under test).
type stubEmailNotifier struct{}

func (stubEmailNotifier) SendCredentialOffer(_ context.Context, _ uuid.UUID, _, _, _, _, _ string) error {
	return nil
}

type stubQerdsNotifier struct{}

func (stubQerdsNotifier) SendCredentialOffer(_ context.Context, _ uuid.UUID, _, _, _, _ string) error {
	return nil
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
	presentationStore := presentation.NewStore(pool, sessionTTL)
	seedPresentation(t, pool, disclosureToken)
	authService := auth.NewService(fake, presentationStore, userStore, sessionStore, orgStore)
	authHandler := auth.NewHandler(authService, sessionStore, cookieCfg, admins)
	requireUser := auth.RequireUser(sessionStore)
	orgService := organization.NewService(userStore, orgStore, authService)
	sessionIssuer := auth.NewSessionIssuer(sessionStore, cookieCfg)
	// nil mailer: invitation e-mail delivery is best-effort and not exercised here.
	orgHandler := organization.NewHandler(orgStore, orgService, audit.NewReader(pool), sessionIssuer, nil, "", requireUser, admins)

	attestationStore := attestation.NewStore(pool, audit.NewDBRecorder())
	issuerSettingsStore := issuersettings.NewStore(pool, audit.NewDBRecorder())
	attestationService := attestation.NewService(
		attestationStore, openid4vciissuer.NewStubIssuer(), issuerSettingsStore,
		stubEmailNotifier{}, stubQerdsNotifier{}, "http://app.test",
	)
	attestationHandler := attestation.NewHandler(attestationStore, attestationStore, attestationStore, attestationStore, attestationService, issuerSettingsStore, "", requireUser, orgHandler.Authorize)

	srv := httptest.NewServer(server.New(pool, authHandler, orgHandler, attestationHandler))
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

	resp := e.do(http.MethodPost, "/api/v1/auth/session/"+disclosureToken+"/claim", nil)
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
	// An org is a business wallet: the KVK identity columns are required.
	err := e.pool.QueryRow(context.Background(),
		`INSERT INTO organizations (name, slug, kvk_number, euid, digital_address)
		 VALUES ($1, $2, $3, $4, $5) RETURNING id`,
		name, slug, "kvk-"+slug, "NL.KVK."+slug, slug+"@qerds.localhost",
	).Scan(&id)
	if err != nil {
		e.t.Fatalf("create org %q: %v", slug, err)
	}
	return id
}

// seedPresentation inserts a presentation_sessions mapping so the given
// client-facing id resolves to a verifier transaction id. It writes the row
// directly (hashing the id as the store does) since these tests skip the live
// POST /auth/session that would otherwise mint the id.
func seedPresentation(t *testing.T, pool *pgxpool.Pool, id string) {
	t.Helper()
	hash := sha256.Sum256([]byte(id))
	_, err := pool.Exec(context.Background(),
		`INSERT INTO presentation_sessions (id_hash, transaction_id, expires_at)
		 VALUES ($1, $2, now() + interval '1 hour')
		 ON CONFLICT (id_hash) DO NOTHING`,
		hash[:], "verifier-tx",
	)
	if err != nil {
		t.Fatalf("seed presentation %q: %v", id, err)
	}
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
