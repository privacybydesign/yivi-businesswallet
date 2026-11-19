package signing

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/textproto"
	"testing"

	"github.com/google/uuid"

	"github.com/privacybydesign/yivi-businesswallet/backend/internal/respond"
)

func TestNormalizeReturnURL(t *testing.T) {
	for raw, want := range map[string]string{
		"":                                    "",
		"  https://portal.example.org/done  ": "https://portal.example.org/done",
		"https://portal.example.org/a?b=c":    "https://portal.example.org/a?b=c",
		"http://localhost:3000/done":          "http://localhost:3000/done",
		"http://127.0.0.1/done":               "http://127.0.0.1/done",
	} {
		got, err := normalizeReturnURL(raw)
		if err != nil || got != want {
			t.Errorf("normalizeReturnURL(%q) = %q, %v; want %q", raw, got, err, want)
		}
	}
	for _, raw := range []string{
		"http://portal.example.org/done",        // plain http off the developer's own machine
		"javascript:alert(1)",                   // not a page
		"/done",                                 // no host
		"https://user:pass@portal.example.org/", // credentials in it
		"https://",                              // no host
	} {
		if _, err := normalizeReturnURL(raw); !errors.Is(err, ErrInvalidRequest) {
			t.Errorf("normalizeReturnURL(%q) = %v, want ErrInvalidRequest", raw, err)
		}
	}
}

func TestReturnLinkCarriesTheRequestAndTheSigneesOutcome(t *testing.T) {
	id := uuid.New()
	req := Request{ID: id, ReturnURL: "https://portal.example.org/contracts?tab=open"}
	got := returnLink(req, Signer{Status: SignerSigned})
	want := "https://portal.example.org/contracts?request_id=" + id.String() + "&status=signed&tab=open"
	if got != want {
		t.Errorf("returnLink = %q, want %q", got, want)
	}
	if link := returnLink(Request{ID: id}, Signer{Status: SignerSigned}); link != "" {
		t.Errorf("a request with no return page gave the link %q", link)
	}
}

// fakeAPIService records what the handler asked of the service.
type fakeAPIService struct {
	created  APIRequestInput
	request  Request
	links    map[uuid.UUID]string
	err      error
	linkErr  error
	document []byte
}

func (f *fakeAPIService) CreateAPIRequest(_ context.Context, _, _ uuid.UUID, _ string, in APIRequestInput) (Request, map[uuid.UUID]string, error) {
	f.created = in
	return f.request, f.links, f.err
}

func (f *fakeAPIService) GetAPIRequest(context.Context, uuid.UUID, uuid.UUID) (Request, error) {
	return f.request, f.err
}

func (f *fakeAPIService) ListAPIRequests(context.Context, uuid.UUID, string, int) ([]Request, string, error) {
	return []Request{f.request}, "next", f.err
}

func (f *fakeAPIService) GetAPIDocument(context.Context, uuid.UUID, uuid.UUID) ([]byte, string, error) {
	return f.document, "Contract.pdf", f.err
}

func (f *fakeAPIService) IssueSignerLink(context.Context, uuid.UUID, uuid.UUID, uuid.UUID) (string, error) {
	return "https://wallet.example.org/sign/token", f.linkErr
}

// openAuth lets every request through as one org's key.
type openAuth struct{ caller APICaller }

func (a openAuth) RequireRead(next respond.HandlerFunc) http.Handler  { return next }
func (a openAuth) RequireWrite(next respond.HandlerFunc) http.Handler { return next }
func (a openAuth) Caller(context.Context) (APICaller, error)          { return a.caller, nil }

func newAPIMux(svc *fakeAPIService) *http.ServeMux {
	mux := http.NewServeMux()
	NewAPIHandler(svc, openAuth{caller: APICaller{OrgID: uuid.New(), KeyID: uuid.New(), Slug: "acme"}}).Register(mux)
	return mux
}

// createForm is the multipart form of a create: a document, one signee and
// whatever other fields the case adds.
func createForm(t *testing.T, fields map[string]string) (*bytes.Buffer, string) {
	t.Helper()
	var body bytes.Buffer
	w := multipart.NewWriter(&body)
	header := textproto.MIMEHeader{}
	header.Set("Content-Disposition", `form-data; name="document"; filename="Contract.pdf"`)
	header.Set("Content-Type", "application/pdf")
	part, err := w.CreatePart(header)
	if err != nil {
		t.Fatalf("create part: %v", err)
	}
	if _, err := part.Write([]byte("%PDF-1.4")); err != nil {
		t.Fatalf("write part: %v", err)
	}
	if err := w.WriteField("signers", `{"kind":"external","name":"Jan","email":"jan@example.org"}`); err != nil {
		t.Fatalf("write signers: %v", err)
	}
	for k, v := range fields {
		if err := w.WriteField(k, v); err != nil {
			t.Fatalf("write %s: %v", k, err)
		}
	}
	if err := w.Close(); err != nil {
		t.Fatalf("close form: %v", err)
	}
	return &body, w.FormDataContentType()
}

func postCreate(t *testing.T, mux *http.ServeMux, fields map[string]string) *httptest.ResponseRecorder {
	t.Helper()
	body, contentType := createForm(t, fields)
	req := httptest.NewRequest(http.MethodPost, "/signing/requests", body)
	req.Header.Set("Content-Type", contentType)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	return rec
}

func TestAPICreateMailsByDefaultAndHandsOutLinksWhenAskedNotTo(t *testing.T) {
	signerID := uuid.New()
	svc := &fakeAPIService{
		request: Request{ID: uuid.New(), Status: StatusAwaitingSignatures, Signers: []Signer{{ID: signerID, Kind: KindExternal, Email: "jan@example.org"}}},
		links:   map[uuid.UUID]string{signerID: "https://wallet.example.org/sign/token"},
	}
	mux := newAPIMux(svc)

	if rec := postCreate(t, mux, nil); rec.Code != http.StatusCreated {
		t.Fatalf("create status = %d, want 201: %s", rec.Code, rec.Body)
	}
	if !svc.created.SendMail {
		t.Error("a create that names no preference must mail the signees")
	}

	rec := postCreate(t, mux, map[string]string{"sendMail": "false", "returnUrl": "https://portal.example.org/done", "mode": ModeSequential})
	if rec.Code != http.StatusCreated {
		t.Fatalf("create status = %d, want 201: %s", rec.Code, rec.Body)
	}
	if svc.created.SendMail || svc.created.ReturnURL != "https://portal.example.org/done" || svc.created.Mode != ModeSequential {
		t.Errorf("created = %+v, want no mail, the return page and sequential mode", svc.created)
	}
	if svc.created.Filename != "Contract.pdf" || len(svc.created.Signers) != 1 || svc.created.Signers[0].Email != "jan@example.org" {
		t.Errorf("created = %+v, want the uploaded document and its signee", svc.created)
	}
	var out apiRequest
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(out.Signers) != 1 || out.Signers[0].SigningURL != "https://wallet.example.org/sign/token" {
		t.Errorf("answer = %+v, want the signee's signing link", out)
	}
	if out.Signers[0].Placements == nil {
		t.Error("placements must be an empty list, never null")
	}
}

func TestAPICreateRefusesAnUnreadableSendMail(t *testing.T) {
	rec := postCreate(t, newAPIMux(&fakeAPIService{}), map[string]string{"sendMail": "maybe"})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
}

func TestAPIMapsTheServicesRefusals(t *testing.T) {
	id := uuid.NewString()
	for name, tc := range map[string]struct {
		svc    *fakeAPIService
		method string
		path   string
		status int
	}{
		"unknown request":        {&fakeAPIService{err: ErrNotFound}, http.MethodGet, "/signing/requests/" + id, http.StatusNotFound},
		"bad request id":         {&fakeAPIService{}, http.MethodGet, "/signing/requests/not-an-id", http.StatusBadRequest},
		"document not ready":     {&fakeAPIService{err: ErrNotCompleted}, http.MethodGet, "/signing/requests/" + id + "/document", http.StatusConflict},
		"link for a member":      {&fakeAPIService{linkErr: ErrNotSigner}, http.MethodPost, "/signing/requests/" + id + "/signers/" + id + "/link", http.StatusNotFound},
		"link out of turn":       {&fakeAPIService{linkErr: ErrNotYourTurn}, http.MethodPost, "/signing/requests/" + id + "/signers/" + id + "/link", http.StatusConflict},
		"link after signing":     {&fakeAPIService{linkErr: ErrAlreadySigned}, http.MethodPost, "/signing/requests/" + id + "/signers/" + id + "/link", http.StatusConflict},
		"link on a finished one": {&fakeAPIService{linkErr: ErrInvalidRequest}, http.MethodPost, "/signing/requests/" + id + "/signers/" + id + "/link", http.StatusBadRequest},
		"a link":                 {&fakeAPIService{}, http.MethodPost, "/signing/requests/" + id + "/signers/" + id + "/link", http.StatusOK},
		"the list":               {&fakeAPIService{}, http.MethodGet, "/signing/requests", http.StatusOK},
		"a bad cursor":           {&fakeAPIService{err: ErrInvalidRequest}, http.MethodGet, "/signing/requests?cursor=x", http.StatusBadRequest},
	} {
		t.Run(name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			newAPIMux(tc.svc).ServeHTTP(rec, httptest.NewRequest(tc.method, tc.path, nil))
			if rec.Code != tc.status {
				t.Fatalf("status = %d, want %d: %s", rec.Code, tc.status, rec.Body)
			}
		})
	}
}

func TestAPIDocumentIsThePDF(t *testing.T) {
	svc := &fakeAPIService{document: []byte("%PDF-1.4 signed")}
	rec := httptest.NewRecorder()
	newAPIMux(svc).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/signing/requests/"+uuid.NewString()+"/document", nil))
	if rec.Code != http.StatusOK || rec.Header().Get("Content-Type") != "application/pdf" || rec.Body.String() != "%PDF-1.4 signed" {
		t.Fatalf("document = %d %q %q", rec.Code, rec.Header().Get("Content-Type"), rec.Body)
	}
}

// The hosted provider's callback belongs to its own base URL only; every other
// provider keeps the callback it registered.
func TestConnectionPicksTheCallbackOfItsProvider(t *testing.T) {
	s := newTestService()
	s.redirectURI = "https://wallet.example.org/remote/callback"
	s.UseHostedProvider("https://wallet.example.org/api/v1/yivi-csc", "https://wallet.example.org/api/v1/signing/callback")

	s.settings = fixedConnection{baseURL: "https://wallet.example.org/api/v1/yivi-csc"}
	conn, err := s.connection(context.Background(), uuid.New())
	if err != nil || conn.redirectURI != "https://wallet.example.org/api/v1/signing/callback" {
		t.Fatalf("hosted connection = %+v, %v; want the hosted callback", conn, err)
	}
	s.settings = fixedConnection{baseURL: "https://qtsp.example"}
	conn, err = s.connection(context.Background(), uuid.New())
	if err != nil || conn.redirectURI != "https://wallet.example.org/remote/callback" {
		t.Fatalf("remote connection = %+v, %v; want the remote callback", conn, err)
	}
	// The override for a provider reachable on another host inside Docker never
	// applies to the hosted service, which is this process.
	s.issuerInternal = "http://qtsp-authz:8084"
	if got := s.tokenIssuer("https://wallet.example.org/api/v1/yivi-csc"); got != "https://wallet.example.org/api/v1/yivi-csc" {
		t.Errorf("hosted token issuer = %q, want it unchanged", got)
	}
	if got := s.tokenIssuer("http://localhost:8084"); got != "http://qtsp-authz:8084" {
		t.Errorf("remote token issuer = %q, want the internal override", got)
	}
}

type fixedConnection struct{ baseURL string }

func (c fixedConnection) ResolveConnection(context.Context, uuid.UUID) (string, string, string, error) {
	return c.baseURL, "client", "secret", nil
}

func (fixedConnection) Available(context.Context, uuid.UUID) (bool, error) { return true, nil }

// A signer who cancels at the provider comes back with no code: the ceremony
// ends there, without asking the provider for a token.
func TestCallbackWithoutACodeFailsTheLinkWithoutATokenExchange(t *testing.T) {
	s := newTestService()
	s.appBaseURL = "https://wallet.example.org"
	s.put("state-1", &ceremony{flow: flowLink, slug: "acme"})
	// s.provider is nil: a token exchange would panic.
	dest := s.HandleCallback(context.Background(), "", "state-1")
	if dest != "https://wallet.example.org/acme/signing?link=failed" {
		t.Fatalf("callback returned to %q, want the signing page with link=failed", dest)
	}
}
