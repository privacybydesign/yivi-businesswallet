//go:build integration

package signing

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"sync"
	"testing"

	"github.com/digitorus/pdfsign/verify"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/privacybydesign/yivi-businesswallet/backend/internal/crypto"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/openid4vpverifier"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/signingprovider"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/yivicsc"
)

// The whole thing an organisation gets with nothing configured: the signing
// ceremony driving the wallet's own signing service, whose authorization is a
// Yivi disclosure. Only the verifier is faked; the service keeps its authority and
// the signer's key in the real schema, and the PDF that comes out is verified.

const (
	yiviTestKey         = "000102030405060708090a0b0c0d0e0f101112131415161718191a1b1c1d1e1f"
	yiviTestAppURL      = "https://wallet.example.org"
	yiviTestRedirectURI = yiviTestAppURL + "/api/v1" + CallbackPath
)

// yiviWallet stands in for the hosted verifier and the signer's Yivi app: every
// presentation it is asked for is answered with the person it holds.
type yiviWallet struct {
	mu      sync.Mutex
	holder  openid4vpverifier.Presentation
	sources [][]string
}

func (w *yiviWallet) StartSigning(_ context.Context, req openid4vpverifier.SigningRequest) (openid4vpverifier.Session, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.sources = append(w.sources, req.Sources)
	id := uuid.NewString()
	return openid4vpverifier.Session{TransactionID: id, WalletLink: "openid4vp://?request_uri=" + id}, nil
}

func (w *yiviWallet) Result(context.Context, string) (openid4vpverifier.Presentation, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.holder, nil
}

func (w *yiviWallet) hold(p openid4vpverifier.Presentation) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.holder = p
}

func passportOf(given, family, born string) openid4vpverifier.Presentation {
	return openid4vpverifier.Presentation{ByCredential: map[string]map[string]string{
		"passport": {
			openid4vpverifier.ClaimGivenNames:  given,
			openid4vpverifier.ClaimFamilyName:  family,
			openid4vpverifier.ClaimDateOfBirth: born,
		},
	}}
}

// yiviConnection is the connection every org has by default: the wallet's own
// signing service and its built-in client.
type yiviConnection struct{ baseURL, clientSecret string }

func (c yiviConnection) ResolveConnection(context.Context, uuid.UUID) (string, string, string, error) {
	return c.baseURL, yivicsc.ClientID, c.clientSecret, nil
}

func (yiviConnection) Available(context.Context, uuid.UUID) (bool, error) { return true, nil }

// yiviBench is the signing service wired to the wallet's own signing service as
// cmd/api wires it, with the signing service's routes on a test server.
type yiviBench struct {
	t      *testing.T
	svc    *Service
	wallet *yiviWallet
	server *httptest.Server
}

func newYiviBench(t *testing.T, store *Store, members []OrgMember, notifier signerNotifier) *yiviBench {
	t.Helper()
	cipher, err := crypto.NewCipher(yiviTestKey)
	if err != nil {
		t.Fatalf("cipher: %v", err)
	}
	subjectSecret, clientSecret, err := yivicsc.DeriveSecrets(cipher)
	if err != nil {
		t.Fatalf("derive secrets: %v", err)
	}
	mux := http.NewServeMux()
	server := httptest.NewServer(mux)
	t.Cleanup(server.Close)

	wallet := &yiviWallet{}
	hosted := yivicsc.NewService(yivicsc.NewStore(store.db, cipher), wallet, subjectSecret, yivicsc.Config{
		PublicURL:  server.URL,
		AppBaseURL: yiviTestAppURL,
		Clients:    []yivicsc.Client{{ID: yivicsc.ClientID, Secret: clientSecret, RedirectURI: yiviTestRedirectURI}},
	})
	if err := hosted.Ping(context.Background()); err != nil {
		t.Fatalf("ping the signing service: %v", err)
	}
	yivicsc.NewHandler(hosted).Register(mux)

	svc := NewService(store, yivicsc.NewProvider(hosted, signingprovider.NewClient()),
		yiviConnection{baseURL: hosted.BaseURL(), clientSecret: clientSecret},
		stubMembers{members: members}, stubOrgs{}, nil, notifier,
		// A remote provider's callback, which the built-in service must not get.
		"http://localhost:8080/api/v1/signing/callback", yiviTestAppURL, "")
	svc.UseHostedProvider(hosted.BaseURL(), yiviTestRedirectURI)
	return &yiviBench{t: t, svc: svc, wallet: wallet, server: server}
}

// authorize walks a signer through one authorization, as their browser would: to
// the authorize URL, to the page it redirects to, and (the wallet having
// disclosed) on to the callback, whose own redirect it returns.
func (b *yiviBench) authorize(authorizeURL string) string {
	b.t.Helper()
	noFollow := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	resp, err := noFollow.Get(authorizeURL)
	if err != nil {
		b.t.Fatalf("GET authorize: %v", err)
	}
	_ = resp.Body.Close()
	page := resp.Header.Get("Location")
	if resp.StatusCode != http.StatusFound || !strings.HasPrefix(page, yiviTestAppURL+yivicsc.AuthorizePagePath) {
		b.t.Fatalf("authorize answered %d to %q, want a redirect to the authorization page", resp.StatusCode, page)
	}
	id := strings.TrimPrefix(page, yiviTestAppURL+yivicsc.AuthorizePagePath)

	viewResp, err := http.Get(b.server.URL + yivicsc.BasePath + "/authorizations/" + id)
	if err != nil {
		b.t.Fatalf("GET authorization: %v", err)
	}
	defer func() { _ = viewResp.Body.Close() }()
	var view yivicsc.AuthorizationView
	if err := json.NewDecoder(viewResp.Body).Decode(&view); err != nil {
		b.t.Fatalf("decode authorization: %v", err)
	}
	if view.RedirectURL == "" {
		b.t.Fatalf("authorization = %+v, want it settled with a redirect", view)
	}
	callback, err := url.Parse(view.RedirectURL)
	if err != nil {
		b.t.Fatalf("parse callback: %v", err)
	}
	if !strings.HasPrefix(view.RedirectURL, yiviTestRedirectURI) {
		b.t.Fatalf("callback = %q, want the built-in client's own", view.RedirectURL)
	}
	return b.svc.HandleCallback(context.Background(), callback.Query().Get("code"), callback.Query().Get("state"))
}

// makeAPIKey is one of the org's API keys, which a request made over the API names.
func makeAPIKey(t *testing.T, pool *pgxpool.Pool, orgID, createdBy uuid.UUID) uuid.UUID {
	t.Helper()
	var id uuid.UUID
	err := pool.QueryRow(context.Background(),
		`INSERT INTO identity_proofing_api_keys (organization_id, name, prefix, secret_hash, created_by)
		 VALUES ($1, 'Backend', 'yp_live_test', $2, $3) RETURNING id`,
		orgID, []byte(uuid.NewString()), createdBy).Scan(&id)
	if err != nil {
		t.Fatalf("create api key: %v", err)
	}
	return id
}

func readSamplePDF(t *testing.T) []byte {
	t.Helper()
	pdf, err := os.ReadFile("testdata/sample.pdf")
	if err != nil {
		t.Fatalf("read sample pdf: %v", err)
	}
	return pdf
}

func TestMemberSignsWithTheBuiltInSigningService(t *testing.T) {
	store, pool := newStore(t)
	ctx := context.Background()
	orgID := makeOrg(t, pool, "acme")
	alice := makeUser(t, pool, "alice@acme.example")
	b := newYiviBench(t, store, []OrgMember{{UserID: alice, Name: "Alice", Email: "alice@acme.example"}}, &capturingNotifier{})
	b.wallet.hold(passportOf("Alice", "Jansen", "1980-01-02"))

	requestID, err := b.svc.CreateRequest(ctx, orgID, alice, "acme", "Contract.pdf", readSamplePDF(t),
		[]SignerInput{{Kind: KindInternal, UserID: alice}}, ModeParallel, RecipientInput{Channel: ChannelNone})
	if err != nil {
		t.Fatalf("CreateRequest: %v", err)
	}

	link, err := b.svc.StartLink(ctx, orgID, alice, "acme")
	if err != nil {
		t.Fatalf("StartLink: %v", err)
	}
	if dest := b.authorize(link.AuthorizeURL); !strings.HasSuffix(dest, "link=ok") {
		t.Fatalf("link returned to %q, want link=ok", dest)
	}

	sign, err := b.svc.StartSign(ctx, orgID, alice, "acme", requestID)
	if err != nil {
		t.Fatalf("StartSign: %v", err)
	}
	if dest := b.authorize(sign.AuthorizeURL); !strings.Contains(dest, requestID.String()) {
		t.Fatalf("sign returned to %q, want the request", dest)
	}

	req, err := b.svc.GetRequest(ctx, orgID, alice, requestID, false)
	if err != nil {
		t.Fatalf("GetRequest: %v", err)
	}
	if req.Status != StatusCompleted {
		t.Fatalf("request status = %q (signer %q), want completed", req.Status, req.Signers[0].Status)
	}
	signed, _, err := b.svc.GetSignedDocument(ctx, orgID, alice, requestID, false)
	if err != nil {
		t.Fatalf("GetSignedDocument: %v", err)
	}
	result, err := verify.Verify(bytes.NewReader(signed), int64(len(signed)))
	if err != nil {
		t.Fatalf("verify the signed PDF: %v", err)
	}
	if len(result.Signers) != 1 || !result.Signers[0].ValidSignature {
		t.Fatalf("signers = %+v, want one valid signature", result.Signers)
	}
	if got := result.Signers[0].Name; got != "Alice Jansen" {
		t.Errorf("signature names %q, want the name Alice disclosed", got)
	}
}

// Somebody else's wallet cannot sign with Alice's certificate: the signing leg is
// refused, her attempt fails, and the request stays open for her.
func TestBuiltInSigningServiceRefusesAnotherPersonsWallet(t *testing.T) {
	store, pool := newStore(t)
	ctx := context.Background()
	orgID := makeOrg(t, pool, "acme")
	alice := makeUser(t, pool, "alice@acme.example")
	b := newYiviBench(t, store, []OrgMember{{UserID: alice, Name: "Alice", Email: "alice@acme.example"}}, &capturingNotifier{})
	b.wallet.hold(passportOf("Alice", "Jansen", "1980-01-02"))

	requestID, err := b.svc.CreateRequest(ctx, orgID, alice, "acme", "Contract.pdf", readSamplePDF(t),
		[]SignerInput{{Kind: KindInternal, UserID: alice}}, ModeParallel, RecipientInput{Channel: ChannelNone})
	if err != nil {
		t.Fatalf("CreateRequest: %v", err)
	}
	link, err := b.svc.StartLink(ctx, orgID, alice, "acme")
	if err != nil {
		t.Fatalf("StartLink: %v", err)
	}
	b.authorize(link.AuthorizeURL)

	b.wallet.hold(passportOf("Mallory", "Other", "1990-03-04"))
	sign, err := b.svc.StartSign(ctx, orgID, alice, "acme", requestID)
	if err != nil {
		t.Fatalf("StartSign: %v", err)
	}
	b.authorize(sign.AuthorizeURL)

	req, err := b.svc.GetRequest(ctx, orgID, alice, requestID, false)
	if err != nil {
		t.Fatalf("GetRequest: %v", err)
	}
	if req.Status != StatusAwaitingSignatures || req.Signers[0].Status != SignerFailed {
		t.Fatalf("request = %q, signer = %q; want it still awaiting with Alice's attempt failed", req.Status, req.Signers[0].Status)
	}
}

// The signing API end to end: a request made with an API key and no mail, signed
// by an external signee over the link the API handed back.
func TestAPIRequestIsSignedOverItsSigningLink(t *testing.T) {
	store, pool := newStore(t)
	ctx := context.Background()
	orgID := makeOrg(t, pool, "acme")
	admin := makeUser(t, pool, "admin@acme.example")
	keyID := makeAPIKey(t, pool, orgID, admin)
	notifier := &capturingNotifier{}
	b := newYiviBench(t, store, nil, notifier)
	b.wallet.hold(passportOf("Jan", "Jansen", "1980-01-02"))

	req, links, err := b.svc.CreateAPIRequest(ctx, orgID, keyID, "acme", APIRequestInput{
		Filename: "Contract.pdf", PDF: readSamplePDF(t), Mode: ModeSequential,
		Signers: []SignerInput{
			{Kind: KindExternal, Email: "jan@example.org", Name: "Jan Jansen"},
			{Kind: KindExternal, Email: "piet@example.org", Name: "Piet Pietersen"},
		},
		Recipient: RecipientInput{Channel: ChannelNone},
		SendMail:  false, ReturnURL: "https://portal.example.org/contracts/42",
	})
	if err != nil {
		t.Fatalf("CreateAPIRequest: %v", err)
	}
	if req.CreatedVia != CreatedViaAPI || req.ReturnURL == "" {
		t.Errorf("request = %+v, want it made over the API with its return page", req)
	}
	if len(notifier.tokens) != 0 || len(notifier.members) != 0 {
		t.Errorf("mail was sent (%d links, %d members) for a request made with none", len(notifier.tokens), len(notifier.members))
	}
	// Sequential: only the first signee has a link.
	jan, piet := req.Signers[0], req.Signers[1]
	if links[jan.ID] == "" || links[piet.ID] != "" || len(links) != 1 {
		t.Fatalf("links = %v, want one, for the first signee", links)
	}
	if _, err := b.svc.IssueSignerLink(ctx, orgID, req.ID, piet.ID); err != ErrNotYourTurn {
		t.Fatalf("a link for the second signee = %v, want ErrNotYourTurn", err)
	}

	token := strings.TrimPrefix(links[jan.ID], yiviTestAppURL+"/sign/")
	view, err := b.svc.ExternalView(ctx, token)
	if err != nil {
		t.Fatalf("ExternalView: %v", err)
	}
	wantReturn := "https://portal.example.org/contracts/42?request_id=" + req.ID.String() + "&status=pending"
	if view.ReturnURL != wantReturn {
		t.Errorf("return link = %q, want %q", view.ReturnURL, wantReturn)
	}

	link, err := b.svc.StartExternalLink(ctx, token)
	if err != nil {
		t.Fatalf("StartExternalLink: %v", err)
	}
	b.authorize(link.AuthorizeURL)
	sign, err := b.svc.StartExternalSign(ctx, token)
	if err != nil {
		t.Fatalf("StartExternalSign: %v", err)
	}
	b.authorize(sign.AuthorizeURL)

	// Jan signed; nobody is mailed that it is Piet's turn, the caller asks for his link.
	if len(notifier.tokens) != 0 {
		t.Errorf("the next signee was mailed for a request made with no mail")
	}
	pietLink, err := b.svc.IssueSignerLink(ctx, orgID, req.ID, piet.ID)
	if err != nil || !strings.HasPrefix(pietLink, yiviTestAppURL+"/sign/") {
		t.Fatalf("Piet's link = %q, %v", pietLink, err)
	}
	if _, err := b.svc.IssueSignerLink(ctx, orgID, req.ID, jan.ID); err != ErrAlreadySigned {
		t.Fatalf("a link for a signee who signed = %v, want ErrAlreadySigned", err)
	}
	if _, _, err := b.svc.GetAPIDocument(ctx, orgID, req.ID); err != ErrNotCompleted {
		t.Fatalf("the document before everyone signed = %v, want ErrNotCompleted", err)
	}

	b.wallet.hold(passportOf("Piet", "Pietersen", "1990-03-04"))
	pietToken := strings.TrimPrefix(pietLink, yiviTestAppURL+"/sign/")
	link, err = b.svc.StartExternalLink(ctx, pietToken)
	if err != nil {
		t.Fatalf("StartExternalLink (Piet): %v", err)
	}
	b.authorize(link.AuthorizeURL)
	sign, err = b.svc.StartExternalSign(ctx, pietToken)
	if err != nil {
		t.Fatalf("StartExternalSign (Piet): %v", err)
	}
	b.authorize(sign.AuthorizeURL)

	done, err := b.svc.GetAPIRequest(ctx, orgID, req.ID)
	if err != nil || done.Status != StatusCompleted {
		t.Fatalf("request = %q, %v; want completed", done.Status, err)
	}
	signed, _, err := b.svc.GetAPIDocument(ctx, orgID, req.ID)
	if err != nil {
		t.Fatalf("GetAPIDocument: %v", err)
	}
	result, err := verify.Verify(bytes.NewReader(signed), int64(len(signed)))
	if err != nil || len(result.Signers) != 2 {
		t.Fatalf("verify = %+v, %v; want two signatures", result, err)
	}
	for _, s := range result.Signers {
		if !s.ValidSignature {
			t.Errorf("signature of %q is not valid", s.Name)
		}
	}
}

// A key reads the requests made over the API, and only those.
func TestAPIReadsOnlyRequestsMadeOverTheAPI(t *testing.T) {
	store, pool := newStore(t)
	ctx := context.Background()
	orgID := makeOrg(t, pool, "acme")
	alice := makeUser(t, pool, "alice@acme.example")
	keyID := makeAPIKey(t, pool, orgID, alice)
	b := newYiviBench(t, store, []OrgMember{{UserID: alice, Name: "Alice", Email: "alice@acme.example"}}, &capturingNotifier{})

	memberRequest, err := b.svc.CreateRequest(ctx, orgID, alice, "acme", "Member.pdf", readSamplePDF(t),
		[]SignerInput{{Kind: KindInternal, UserID: alice}}, ModeParallel, RecipientInput{Channel: ChannelNone})
	if err != nil {
		t.Fatalf("CreateRequest: %v", err)
	}
	apiRequest, _, err := b.svc.CreateAPIRequest(ctx, orgID, keyID, "acme", APIRequestInput{
		Filename: "Api.pdf", PDF: readSamplePDF(t), Mode: ModeParallel, SendMail: true,
		Signers:   []SignerInput{{Kind: KindExternal, Email: "jan@example.org", Name: "Jan"}},
		Recipient: RecipientInput{Channel: ChannelNone},
	})
	if err != nil {
		t.Fatalf("CreateAPIRequest: %v", err)
	}

	if _, err := b.svc.GetAPIRequest(ctx, orgID, memberRequest); err != ErrNotFound {
		t.Fatalf("a member's request over the API = %v, want ErrNotFound", err)
	}
	listed, next, err := b.svc.ListAPIRequests(ctx, orgID, "", 0)
	if err != nil || next != "" || len(listed) != 1 || listed[0].ID != apiRequest.ID {
		t.Fatalf("listed = %+v (next %q), %v; want only the API's request", listed, next, err)
	}
	// The wallet's own history shows both, and says which is which.
	all, _, err := b.svc.ListRequests(ctx, orgID, "", 0)
	if err != nil || len(all) != 2 {
		t.Fatalf("history = %d requests, %v; want both", len(all), err)
	}
	for _, r := range all {
		want := CreatedViaMember
		if r.ID == apiRequest.ID {
			want = CreatedViaAPI
		}
		if r.CreatedVia != want {
			t.Errorf("request %s created via %q, want %q", r.Filename, r.CreatedVia, want)
		}
	}
}
