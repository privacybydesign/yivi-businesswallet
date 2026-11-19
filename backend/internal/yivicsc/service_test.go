package yivicsc

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/privacybydesign/yivi-businesswallet/backend/internal/openid4vpverifier"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/signingprovider"
)

const (
	testClientSecret = "test-client-secret"
	testAppURL       = "https://wallet.example"
	testRedirectURI  = "https://wallet.example/api/v1/signing/callback"
)

// memoryStore is the credential store without a database.
type memoryStore struct {
	mu          sync.Mutex
	ca          *authority
	credentials map[uuid.UUID]credential
}

func newMemoryStore() *memoryStore {
	return &memoryStore{credentials: map[uuid.UUID]credential{}}
}

func (m *memoryStore) authority(_ context.Context, now time.Time) (*authority, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.ca == nil {
		ca, err := newAuthority(now)
		if err != nil {
			return nil, err
		}
		m.ca = ca
	}
	return m.ca, nil
}

func (m *memoryStore) credentialFor(_ context.Context, subject []byte, source string, now time.Time,
	issue func() (*ecdsa.PrivateKey, *x509.Certificate, error),
) (credential, error) {
	m.mu.Lock()
	for id, c := range m.credentials {
		if !bytes.Equal(c.subjectKey, subject) {
			continue
		}
		if c.cert.NotAfter.After(now) {
			m.mu.Unlock()
			return c, nil
		}
		delete(m.credentials, id)
	}
	m.mu.Unlock()

	key, cert, err := issue()
	if err != nil {
		return credential{}, err
	}
	c := credential{id: uuid.New(), subjectKey: subject, source: source, cert: cert, key: key}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.credentials[c.id] = c
	return c, nil
}

func (m *memoryStore) credentialByID(_ context.Context, id uuid.UUID) (credential, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	c, ok := m.credentials[id]
	if !ok {
		return credential{}, ErrCredentialNotFound
	}
	return c, nil
}

// fakeVerifier stands in for the hosted verifier: it records what each
// presentation asked for and answers with whatever the test disclosed.
type fakeVerifier struct {
	mu        sync.Mutex
	requests  []openid4vpverifier.SigningRequest
	disclosed map[string]openid4vpverifier.Presentation
	startErr  error
}

func (f *fakeVerifier) StartSigning(_ context.Context, req openid4vpverifier.SigningRequest) (openid4vpverifier.Session, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.startErr != nil {
		return openid4vpverifier.Session{}, f.startErr
	}
	f.requests = append(f.requests, req)
	id := "tx-" + uuid.NewString()
	return openid4vpverifier.Session{TransactionID: id, WalletLink: "openid4vp://?request_uri=" + id}, nil
}

func (f *fakeVerifier) Result(_ context.Context, id string) (openid4vpverifier.Presentation, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	p, ok := f.disclosed[id]
	if !ok {
		return openid4vpverifier.Presentation{}, openid4vpverifier.ErrPending
	}
	return p, nil
}

func (f *fakeVerifier) lastRequest() openid4vpverifier.SigningRequest {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.requests[len(f.requests)-1]
}

// passport is a presentation of a passport naming one person.
func passport(given, family, born string) openid4vpverifier.Presentation {
	return openid4vpverifier.Presentation{ByCredential: map[string]map[string]string{
		"passport": {
			openid4vpverifier.ClaimGivenNames:  given,
			openid4vpverifier.ClaimFamilyName:  family,
			openid4vpverifier.ClaimDateOfBirth: born,
		},
	}}
}

// personalData is a presentation of the registered personal data.
func personalData(given, prefix, family, born string) openid4vpverifier.Presentation {
	return openid4vpverifier.Presentation{ByCredential: map[string]map[string]string{
		"personaldata": {
			openid4vpverifier.ClaimPersonalFirstNames:  given,
			openid4vpverifier.ClaimPersonalPrefix:      prefix,
			openid4vpverifier.ClaimPersonalFamilyName:  family,
			openid4vpverifier.ClaimPersonalDateOfBirth: born,
		},
	}}
}

// bench is the service behind a test server, with the client a signing
// application drives it with.
type bench struct {
	t        *testing.T
	svc      *Service
	verifier *fakeVerifier
	store    *memoryStore
	server   *httptest.Server
	client   *signingprovider.Client
	now      time.Time
}

func newBench(t *testing.T) *bench {
	t.Helper()
	b := &bench{t: t, verifier: &fakeVerifier{disclosed: map[string]openid4vpverifier.Presentation{}}, store: newMemoryStore(), now: time.Now()}
	mux := http.NewServeMux()
	b.server = httptest.NewServer(mux)
	t.Cleanup(b.server.Close)
	b.svc = newService(b.store, b.verifier, []byte("subject-secret"), Config{
		PublicURL:  b.server.URL,
		AppBaseURL: testAppURL,
		Clients:    []Client{{ID: ClientID, Secret: testClientSecret, RedirectURI: testRedirectURI}},
	}, func() time.Time { return b.now })
	NewHandler(b.svc).Register(mux)
	b.client = signingprovider.NewClient()
	return b
}

// authorize sends the browser to /oauth2/authorize and returns where it was
// redirected.
func (b *bench) authorize(params signingprovider.AuthorizeParams) *url.URL {
	b.t.Helper()
	return b.get(b.client.AuthorizeURL(b.svc.BaseURL(), params))
}

func (b *bench) get(rawURL string) *url.URL {
	b.t.Helper()
	noFollow := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	resp, err := noFollow.Get(rawURL)
	if err != nil {
		b.t.Fatalf("GET authorize: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusFound {
		b.t.Fatalf("authorize status = %d, want 302", resp.StatusCode)
	}
	location, err := url.Parse(resp.Header.Get("Location"))
	if err != nil {
		b.t.Fatalf("parse Location: %v", err)
	}
	return location
}

// pageID is the authorization id in an authorization page's URL.
func (b *bench) pageID(page *url.URL) string {
	b.t.Helper()
	if !strings.HasPrefix(page.String(), testAppURL+AuthorizePagePath) {
		b.t.Fatalf("redirected to %s, want the authorization page", page)
	}
	return strings.TrimPrefix(page.Path, AuthorizePagePath)
}

// view reads the authorization as its page does.
func (b *bench) view(id string) AuthorizationView {
	b.t.Helper()
	resp, err := http.Get(b.svc.BaseURL() + "/authorizations/" + id)
	if err != nil {
		b.t.Fatalf("GET authorization: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		b.t.Fatalf("authorization status = %d, want 200", resp.StatusCode)
	}
	var v AuthorizationView
	if err := json.NewDecoder(resp.Body).Decode(&v); err != nil {
		b.t.Fatalf("decode authorization: %v", err)
	}
	return v
}

// disclose has the signer of a pending authorization present p.
func (b *bench) disclose(id string, p openid4vpverifier.Presentation) {
	b.t.Helper()
	b.svc.mu.Lock()
	tx := b.svc.authorizations[id].transactionID
	b.svc.mu.Unlock()
	b.verifier.mu.Lock()
	b.verifier.disclosed[tx] = p
	b.verifier.mu.Unlock()
}

// approve runs one authorization to its code: authorize, disclose, poll.
func (b *bench) approve(params signingprovider.AuthorizeParams, p openid4vpverifier.Presentation) string {
	b.t.Helper()
	id := b.pageID(b.authorize(params))
	b.disclose(id, p)
	v := b.view(id)
	if v.Status != StatusApproved {
		b.t.Fatalf("status = %q (reason %q), want approved", v.Status, v.Reason)
	}
	callback, err := url.Parse(v.RedirectURL)
	if err != nil {
		b.t.Fatalf("parse redirect URL: %v", err)
	}
	if got := callback.Query().Get("state"); got != params.State {
		b.t.Fatalf("callback state = %q, want %q", got, params.State)
	}
	if !strings.HasPrefix(v.RedirectURL, testRedirectURI) {
		b.t.Fatalf("callback = %s, want the registered redirect URI", v.RedirectURL)
	}
	return callback.Query().Get("code")
}

func serviceParams(pkce signingprovider.PKCE) signingprovider.AuthorizeParams {
	return signingprovider.AuthorizeParams{
		ClientID: ClientID, RedirectURI: testRedirectURI, State: "state-1",
		CodeChallenge: pkce.Challenge, Scope: signingprovider.ScopeService,
	}
}

func credentialParams(pkce signingprovider.PKCE, credentialID string, digest []byte) signingprovider.AuthorizeParams {
	return signingprovider.AuthorizeParams{
		ClientID: ClientID, RedirectURI: testRedirectURI, State: "state-2",
		CodeChallenge: pkce.Challenge, Scope: signingprovider.ScopeCredential,
		CredentialID: credentialID, NumSignatures: 1,
		Hashes:           []string{base64.RawURLEncoding.EncodeToString(digest)},
		HashAlgorithmOID: signingprovider.HashAlgoSHA256OID,
		Description:      "contract.pdf",
	}
}

func newPKCE(t *testing.T) signingprovider.PKCE {
	t.Helper()
	pkce, err := signingprovider.NewPKCE()
	if err != nil {
		t.Fatalf("pkce: %v", err)
	}
	return pkce
}

// link runs the service leg for a person and returns their credential.
func (b *bench) link(p openid4vpverifier.Presentation) signingprovider.Credential {
	b.t.Helper()
	ctx := context.Background()
	pkce := newPKCE(b.t)
	code := b.approve(serviceParams(pkce), p)
	token, err := b.client.ExchangeToken(ctx, b.svc.BaseURL(), ClientID, testClientSecret, code, pkce.Verifier, testRedirectURI)
	if err != nil {
		b.t.Fatalf("exchange token: %v", err)
	}
	ids, err := b.client.ListCredentials(ctx, b.svc.BaseURL(), token.AccessToken)
	if err != nil || len(ids) != 1 {
		b.t.Fatalf("list credentials = %v, %v; want one", ids, err)
	}
	cred, err := b.client.CredentialInfo(ctx, b.svc.BaseURL(), token.AccessToken, ids[0])
	if err != nil {
		b.t.Fatalf("credential info: %v", err)
	}
	return cred
}

// signToken runs the signing leg for a credential and digest and returns the
// access token that authorises the signature.
func (b *bench) signToken(cred signingprovider.Credential, digest []byte, p openid4vpverifier.Presentation) string {
	b.t.Helper()
	pkce := newPKCE(b.t)
	code := b.approve(credentialParams(pkce, cred.ID, digest), p)
	token, err := b.client.ExchangeToken(context.Background(), b.svc.BaseURL(), ClientID, testClientSecret, code, pkce.Verifier, testRedirectURI)
	if err != nil {
		b.t.Fatalf("exchange token: %v", err)
	}
	return token.AccessToken
}

func (b *bench) signHash(token, credentialID string, digest []byte) ([]string, error) {
	return b.client.SignHash(context.Background(), b.svc.BaseURL(), token, credentialID,
		[]string{base64.StdEncoding.EncodeToString(digest)},
		signingprovider.SignAlgoECDSASHA256OID, signingprovider.HashAlgoSHA256OID)
}

func TestInfoNamesItsOwnIssuer(t *testing.T) {
	b := newBench(t)
	info, err := b.client.Discover(context.Background(), b.svc.BaseURL())
	if err != nil {
		t.Fatalf("discover: %v", err)
	}
	if info.OAuth2 != b.svc.BaseURL() || info.Name != Name || info.Specs != Specs {
		t.Fatalf("info = %+v, want this service as its own issuer", info)
	}
}

// The whole ceremony, driven with the client the wallet's signing ceremony uses
// for any CSC provider: link a certificate with a passport, then authorise and
// make one signature that verifies against that certificate.
func TestLinkAndSignWithAPassport(t *testing.T) {
	b := newBench(t)
	jan := passport("JAN", "JANSEN", "1980-01-02")

	cred := b.link(jan)
	if got := cred.Certificate.Subject.CommonName; got != "JAN JANSEN" {
		t.Errorf("certificate common name = %q, want the disclosed name", got)
	}
	if cred.Certificate.KeyUsage&x509.KeyUsageContentCommitment == 0 {
		t.Error("the certificate must be good for non-repudiation")
	}
	if len(cred.Chain) != 2 {
		t.Fatalf("chain has %d certificates, want the leaf and its authority", len(cred.Chain))
	}
	roots := x509.NewCertPool()
	roots.AddCert(cred.Chain[1])
	if _, err := cred.Certificate.Verify(x509.VerifyOptions{Roots: roots, KeyUsages: []x509.ExtKeyUsage{x509.ExtKeyUsageAny}}); err != nil {
		t.Fatalf("the certificate does not chain to the authority: %v", err)
	}
	// Linking offers every source: the chip documents and the personal data.
	if got := b.verifier.lastRequest().Sources; !slices.Equal(got, sourcesOf("")) {
		t.Errorf("link asked sources %v, want every source", got)
	}

	digest := sha256.Sum256([]byte("the document"))
	id := b.pageID(b.authorize(credentialParams(newPKCE(t), cred.ID, digest[:])))
	pending := b.view(id)
	if pending.Purpose != PurposeSign || pending.Description != "contract.pdf" || pending.Source != SourceMRTD || pending.WalletLink == "" {
		t.Errorf("signing page = %+v, want the sign purpose, the description, the source and a wallet link", pending)
	}
	// Signing asks only the family the certificate was issued on.
	if got := b.verifier.lastRequest().Sources; !slices.Equal(got, mrtdSources) {
		t.Errorf("sign asked sources %v, want the travel documents only", got)
	}

	token := b.signToken(cred, digest[:], jan)
	signatures, err := b.signHash(token, cred.ID, digest[:])
	if err != nil {
		t.Fatalf("signHash: %v", err)
	}
	sig, err := base64.StdEncoding.DecodeString(signatures[0])
	if err != nil {
		t.Fatalf("decode signature: %v", err)
	}
	pub, ok := cred.Certificate.PublicKey.(*ecdsa.PublicKey)
	if !ok || !ecdsa.VerifyASN1(pub, digest[:], sig) {
		t.Fatal("the signature does not verify against the credential's certificate")
	}

	// The token authorised one signature; it is spent.
	if _, err := b.signHash(token, cred.ID, digest[:]); err == nil {
		t.Fatal("a second signHash with the same token must be refused")
	}
}

// Linking again finds the same credential: one person, one certificate.
func TestLinkingTwiceKeepsTheCredential(t *testing.T) {
	b := newBench(t)
	first := b.link(passport("Jan", "Jansen", "1980-01-02"))
	// The same person, as another document cased and spaced the name.
	second := b.link(passport("JAN", " JANSEN ", "1980-01-02"))
	if first.ID != second.ID {
		t.Errorf("credential ids differ (%s, %s): the same person got two certificates", first.ID, second.ID)
	}
	other := b.link(passport("Jan", "Jansen", "1981-01-02"))
	if other.ID == first.ID {
		t.Error("a namesake with another birth date must get their own credential")
	}
}

// A name from the population register keeps its prefix, and signing with that
// certificate asks the register's data again, not a passport.
func TestLinkAndSignWithRegisteredPersonalData(t *testing.T) {
	b := newBench(t)
	petra := personalData("Petra", "van der", "Berg", "1975-06-30")

	cred := b.link(petra)
	if got := cred.Certificate.Subject.CommonName; got != "Petra van der Berg" {
		t.Errorf("certificate common name = %q, want the prefix in the family name", got)
	}

	digest := sha256.Sum256([]byte("the document"))
	token := b.signToken(cred, digest[:], petra)
	if got := b.verifier.lastRequest().Sources; !slices.Equal(got, brpSources) {
		t.Errorf("sign asked sources %v, want the personal data only", got)
	}
	if _, err := b.signHash(token, cred.ID, digest[:]); err != nil {
		t.Fatalf("signHash: %v", err)
	}
}

// Only the person the certificate names may authorise a signature with it.
func TestSigningIsRefusedForAnotherPerson(t *testing.T) {
	b := newBench(t)
	cred := b.link(passport("Jan", "Jansen", "1980-01-02"))

	digest := sha256.Sum256([]byte("the document"))
	id := b.pageID(b.authorize(credentialParams(newPKCE(t), cred.ID, digest[:])))
	b.disclose(id, passport("Piet", "Pietersen", "1990-03-04"))

	v := b.view(id)
	if v.Status != StatusDenied || v.Reason != ReasonIdentityMismatch {
		t.Fatalf("status = %q, reason = %q; want denied for an identity mismatch", v.Status, v.Reason)
	}
	callback, err := url.Parse(v.RedirectURL)
	if err != nil {
		t.Fatalf("parse redirect URL: %v", err)
	}
	if callback.Query().Get("error") != errAccessDenied || callback.Query().Get("code") != "" {
		t.Errorf("callback = %s, want access_denied and no code", v.RedirectURL)
	}
}

func TestADisclosureWithoutABirthDateIsRefused(t *testing.T) {
	b := newBench(t)
	id := b.pageID(b.authorize(serviceParams(newPKCE(t))))
	b.disclose(id, passport("Jan", "Jansen", ""))
	if v := b.view(id); v.Status != StatusDenied || v.Reason != ReasonIdentityIncomplete {
		t.Fatalf("status = %q, reason = %q; want denied for an incomplete identity", v.Status, v.Reason)
	}
}

func TestCancelDeniesAPendingAuthorization(t *testing.T) {
	b := newBench(t)
	id := b.pageID(b.authorize(serviceParams(newPKCE(t))))
	v, err := b.svc.Cancel(id)
	if err != nil {
		t.Fatalf("cancel: %v", err)
	}
	if v.Status != StatusDenied || v.Reason != ReasonCancelled || !strings.Contains(v.RedirectURL, "error="+errAccessDenied) {
		t.Fatalf("cancelled view = %+v, want denied with an access_denied callback", v)
	}
	// A disclosure that arrives after the signer cancelled changes nothing.
	b.disclose(id, passport("Jan", "Jansen", "1980-01-02"))
	if again := b.view(id); again.Status != StatusDenied {
		t.Errorf("status after a late disclosure = %q, want it to stay denied", again.Status)
	}
}

func TestAnAuthorizationExpires(t *testing.T) {
	b := newBench(t)
	id := b.pageID(b.authorize(serviceParams(newPKCE(t))))
	b.now = b.now.Add(AuthorizationTTL + time.Second)
	if _, err := b.svc.Authorization(context.Background(), id); !errors.Is(err, ErrAuthorizationNotFound) {
		t.Fatalf("error = %v, want the authorization gone after its window", err)
	}
}

// A client or callback nobody registered is answered, never redirected to.
func TestAuthorizeRefusesAnUnknownClientOrCallback(t *testing.T) {
	b := newBench(t)
	for name, mutate := range map[string]func(*signingprovider.AuthorizeParams){
		"unknown client":   func(p *signingprovider.AuthorizeParams) { p.ClientID = "someone-else" },
		"unknown callback": func(p *signingprovider.AuthorizeParams) { p.RedirectURI = "https://attacker.example/callback" },
	} {
		t.Run(name, func(t *testing.T) {
			params := serviceParams(newPKCE(t))
			mutate(&params)
			resp, err := http.Get(b.client.AuthorizeURL(b.svc.BaseURL(), params))
			if err != nil {
				t.Fatalf("GET authorize: %v", err)
			}
			defer func() { _ = resp.Body.Close() }()
			if resp.StatusCode != http.StatusBadRequest {
				t.Fatalf("status = %d, want 400 and no redirect", resp.StatusCode)
			}
		})
	}
}

// What a known client got wrong goes back to its callback as an OAuth error.
func TestAuthorizeReportsABadRequestToTheClient(t *testing.T) {
	b := newBench(t)
	for name, mutate := range map[string]func(*signingprovider.AuthorizeParams){
		"no PKCE":       func(p *signingprovider.AuthorizeParams) { p.CodeChallenge = "" },
		"unknown scope": func(p *signingprovider.AuthorizeParams) { p.Scope = "everything" },
		"unknown credential": func(p *signingprovider.AuthorizeParams) {
			*p = credentialParams(newPKCE(t), uuid.NewString(), make([]byte, sha256.Size))
		},
		"not a digest": func(p *signingprovider.AuthorizeParams) {
			*p = credentialParams(newPKCE(t), uuid.NewString(), []byte("short"))
		},
	} {
		t.Run(name, func(t *testing.T) {
			params := serviceParams(newPKCE(t))
			mutate(&params)
			location := b.authorize(params)
			if !strings.HasPrefix(location.String(), testRedirectURI) || location.Query().Get("error") == "" {
				t.Fatalf("redirected to %s, want the callback with an error", location)
			}
			if len(b.verifier.requests) != 0 {
				t.Error("a refused request must not start a presentation")
			}
		})
	}
}

func TestTokenExchangeChecksTheClientTheCodeAndPKCE(t *testing.T) {
	b := newBench(t)
	ctx := context.Background()
	jan := passport("Jan", "Jansen", "1980-01-02")

	t.Run("wrong client secret", func(t *testing.T) {
		pkce := newPKCE(t)
		code := b.approve(serviceParams(pkce), jan)
		if _, err := b.client.ExchangeToken(ctx, b.svc.BaseURL(), ClientID, "guess", code, pkce.Verifier, testRedirectURI); err == nil {
			t.Fatal("a wrong client secret must be refused")
		}
	})
	t.Run("wrong verifier", func(t *testing.T) {
		pkce := newPKCE(t)
		code := b.approve(serviceParams(pkce), jan)
		if _, err := b.client.ExchangeToken(ctx, b.svc.BaseURL(), ClientID, testClientSecret, code, "not-the-verifier", testRedirectURI); err == nil {
			t.Fatal("a wrong PKCE verifier must be refused")
		}
		// The failed attempt spent the code.
		if _, err := b.client.ExchangeToken(ctx, b.svc.BaseURL(), ClientID, testClientSecret, code, pkce.Verifier, testRedirectURI); err == nil {
			t.Fatal("a code must not be exchangeable after a failed attempt")
		}
	})
	t.Run("code used twice", func(t *testing.T) {
		pkce := newPKCE(t)
		code := b.approve(serviceParams(pkce), jan)
		if _, err := b.client.ExchangeToken(ctx, b.svc.BaseURL(), ClientID, testClientSecret, code, pkce.Verifier, testRedirectURI); err != nil {
			t.Fatalf("first exchange: %v", err)
		}
		if _, err := b.client.ExchangeToken(ctx, b.svc.BaseURL(), ClientID, testClientSecret, code, pkce.Verifier, testRedirectURI); err == nil {
			t.Fatal("a code must be good once")
		}
	})
	t.Run("expired code", func(t *testing.T) {
		pkce := newPKCE(t)
		code := b.approve(serviceParams(pkce), jan)
		b.now = b.now.Add(codeTTL + time.Second)
		if _, err := b.client.ExchangeToken(ctx, b.svc.BaseURL(), ClientID, testClientSecret, code, pkce.Verifier, testRedirectURI); err == nil {
			t.Fatal("an expired code must be refused")
		}
	})
}

func TestSignHashSignsOnlyWhatWasAuthorised(t *testing.T) {
	b := newBench(t)
	jan := passport("Jan", "Jansen", "1980-01-02")
	cred := b.link(jan)
	authorised := sha256.Sum256([]byte("the document"))
	other := sha256.Sum256([]byte("another document"))

	t.Run("another digest", func(t *testing.T) {
		token := b.signToken(cred, authorised[:], jan)
		if _, err := b.signHash(token, cred.ID, other[:]); err == nil {
			t.Fatal("a digest the signer did not authorise must be refused")
		}
		// The refusal spent nothing: the authorised digest still signs.
		if _, err := b.signHash(token, cred.ID, authorised[:]); err != nil {
			t.Fatalf("the authorised digest after a refusal: %v", err)
		}
	})
	t.Run("a service token", func(t *testing.T) {
		pkce := newPKCE(t)
		code := b.approve(serviceParams(pkce), jan)
		token, err := b.client.ExchangeToken(context.Background(), b.svc.BaseURL(), ClientID, testClientSecret, code, pkce.Verifier, testRedirectURI)
		if err != nil {
			t.Fatalf("exchange token: %v", err)
		}
		if _, err := b.signHash(token.AccessToken, cred.ID, authorised[:]); err == nil {
			t.Fatal("a token of the service leg must not sign")
		}
	})
	t.Run("no token", func(t *testing.T) {
		if _, err := b.signHash("", cred.ID, authorised[:]); err == nil {
			t.Fatal("signHash without a token must be refused")
		}
	})
	t.Run("an expired token", func(t *testing.T) {
		token := b.signToken(cred, authorised[:], jan)
		b.now = b.now.Add(tokenTTL + time.Second)
		if _, err := b.signHash(token, cred.ID, authorised[:]); err == nil {
			t.Fatal("an expired token must be refused")
		}
	})
}

// One person cannot read another's credential with their own token.
func TestCredentialInfoIsTheSubjectsOwn(t *testing.T) {
	b := newBench(t)
	jan := b.link(passport("Jan", "Jansen", "1980-01-02"))

	pkce := newPKCE(t)
	code := b.approve(serviceParams(pkce), passport("Piet", "Pietersen", "1990-03-04"))
	piet, err := b.client.ExchangeToken(context.Background(), b.svc.BaseURL(), ClientID, testClientSecret, code, pkce.Verifier, testRedirectURI)
	if err != nil {
		t.Fatalf("exchange token: %v", err)
	}
	if _, err := b.client.CredentialInfo(context.Background(), b.svc.BaseURL(), piet.AccessToken, jan.ID); err == nil {
		t.Fatal("another person's credential must read as unknown")
	}
}

// An expired certificate is replaced under a new id, never reused to sign.
func TestAnExpiredCredentialIsReplaced(t *testing.T) {
	b := newBench(t)
	jan := passport("Jan", "Jansen", "1980-01-02")
	first := b.link(jan)

	b.now = b.now.Add(CredentialValidity + time.Hour)
	digest := sha256.Sum256([]byte("the document"))
	location := b.authorize(credentialParams(newPKCE(t), first.ID, digest[:]))
	if location.Query().Get("error") == "" {
		t.Fatalf("redirected to %s, want the expired credential refused", location)
	}
	second := b.link(jan)
	if second.ID == first.ID {
		t.Error("an expired credential must be replaced under a new id")
	}
}

// The in-process provider the ceremony drives answers for this service and
// passes every other base URL on to the remote one.
func TestProviderAnswersForItsOwnBaseURLOnly(t *testing.T) {
	b := newBench(t)
	remote := &recordingRemote{}
	p := NewProvider(b.svc, remote)
	ctx := context.Background()

	info, err := p.Discover(ctx, b.svc.BaseURL())
	if err != nil || info.OAuth2 != b.svc.BaseURL() {
		t.Fatalf("discover own = %+v, %v", info, err)
	}
	if _, err := p.Discover(ctx, "https://qtsp.example"); err != nil || remote.discovered != "https://qtsp.example" {
		t.Fatalf("another base URL must go to the remote provider (got %q, %v)", remote.discovered, err)
	}

	// The same legs the HTTP test runs, in process.
	jan := passport("Jan", "Jansen", "1980-01-02")
	pkce := newPKCE(t)
	code := b.approve(serviceParams(pkce), jan)
	token, err := p.ExchangeToken(ctx, b.svc.BaseURL(), ClientID, testClientSecret, code, pkce.Verifier, testRedirectURI)
	if err != nil {
		t.Fatalf("exchange token: %v", err)
	}
	ids, err := p.ListCredentials(ctx, b.svc.BaseURL(), token.AccessToken)
	if err != nil || len(ids) != 1 {
		t.Fatalf("list credentials = %v, %v", ids, err)
	}
	cred, err := p.CredentialInfo(ctx, b.svc.BaseURL(), token.AccessToken, ids[0])
	if err != nil || cred.Certificate.Subject.CommonName != "Jan Jansen" || len(cred.Chain) != 2 {
		t.Fatalf("credential info = %+v, %v", cred, err)
	}

	digest := sha256.Sum256([]byte("the document"))
	signPKCE := newPKCE(t)
	signCode := b.approve(credentialParams(signPKCE, cred.ID, digest[:]), jan)
	sad, err := p.ExchangeToken(ctx, b.svc.BaseURL(), ClientID, testClientSecret, signCode, signPKCE.Verifier, testRedirectURI)
	if err != nil {
		t.Fatalf("exchange signing token: %v", err)
	}
	signatures, err := p.SignHash(ctx, b.svc.BaseURL(), sad.AccessToken, cred.ID,
		[]string{base64.StdEncoding.EncodeToString(digest[:])},
		signingprovider.SignAlgoECDSASHA256OID, signingprovider.HashAlgoSHA256OID)
	if err != nil || len(signatures) != 1 {
		t.Fatalf("signHash = %v, %v", signatures, err)
	}

	// A refusal reaches the ceremony as the error a remote provider gives.
	_, err = p.SignHash(ctx, b.svc.BaseURL(), sad.AccessToken, cred.ID,
		[]string{base64.StdEncoding.EncodeToString(digest[:])},
		signingprovider.SignAlgoECDSASHA256OID, signingprovider.HashAlgoSHA256OID)
	if _, ok := errors.AsType[*signingprovider.RequestError](err); !ok {
		t.Fatalf("a spent token = %v, want a RequestError", err)
	}
}

// recordingRemote is a remote provider that only notes it was asked.
type recordingRemote struct {
	signingprovider.Client
	discovered string
}

func (r *recordingRemote) Discover(_ context.Context, baseURL string) (signingprovider.Info, error) {
	r.discovered = baseURL
	return signingprovider.Info{}, nil
}

func TestIdentityOfPrefersTheDocument(t *testing.T) {
	p := passport("Jan", "Jansen", "1980-01-02")
	p.ByCredential["personaldata"] = personalData("Other", "", "Person", "1970-01-01").ByCredential["personaldata"]
	id, err := identityOf(p)
	if err != nil || id.Source != SourceMRTD || id.CommonName() != "Jan Jansen" {
		t.Fatalf("identity = %+v, %v; want the document's", id, err)
	}
	if _, err := identityOf(openid4vpverifier.Presentation{}); !errors.Is(err, ErrIdentityIncomplete) {
		t.Fatalf("an empty presentation = %v, want ErrIdentityIncomplete", err)
	}
}

// The same name from the two families is two subjects: a certificate issued on
// one is not unlocked by the other.
func TestSubjectKeySeparatesTheFamilies(t *testing.T) {
	secret := []byte("secret")
	mrtd := Identity{Source: SourceMRTD, GivenNames: "Jan", FamilyName: "Jansen", DateOfBirth: "1980-01-02"}
	brp := mrtd
	brp.Source = SourceBRP
	if bytes.Equal(subjectKey(secret, mrtd), subjectKey(secret, brp)) {
		t.Error("the two families must not share a subject key")
	}
	cased := mrtd
	cased.GivenNames, cased.FamilyName = "JAN", "JANSEN"
	if !bytes.Equal(subjectKey(secret, mrtd), subjectKey(secret, cased)) {
		t.Error("a name must compare however it was cased")
	}
	if bytes.Equal(subjectKey(secret, mrtd), subjectKey([]byte("other"), mrtd)) {
		t.Error("the subject key must depend on the deployment's secret")
	}
}
