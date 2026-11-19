package proofing

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/privacybydesign/yivi-businesswallet/backend/internal/email"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/proofingprovider"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/respond"
)

// saveOrigins sets the org's redirect origins as a settings update does.
func saveOrigins(svc *Service, origins []string) (Settings, error) {
	return svc.UpdateSettings(context.Background(), testOrg.ID, SettingsChange{RedirectOrigins: &origins})
}

func TestRedirectOriginsAreHTTPS(t *testing.T) {
	f := newFixture()
	saved, err := saveOrigins(f.svc,
		[]string{"https://Portal.Acme.example/", "http://localhost:3000", "https://portal.acme.example", "http://[::1]:8080"})
	if err != nil {
		t.Fatalf("saveOrigins: %v", err)
	}
	if want := []string{"https://portal.acme.example", "http://localhost:3000", "http://[::1]:8080"}; !slices.Equal(saved.RedirectOrigins, want) {
		t.Errorf("origins = %v, want %v (lowercased, once each)", saved.RedirectOrigins, want)
	}
	tooMany := make([]string, maxRedirectOrigins+1)
	for i := range tooMany {
		tooMany[i] = "https://a" + strings.Repeat("a", i) + ".example"
	}
	for name, origins := range map[string][]string{
		"plain http":     {"http://portal.acme.example"},
		"a path":         {"https://portal.acme.example/done"},
		"a query":        {"https://portal.acme.example?x=1"},
		"credentials":    {"https://user@portal.acme.example"},
		"no host":        {"https://"},
		"another scheme": {"javascript:alert(1)"},
		"too many":       tooMany,
		// Each would mean something else in the hosted page's frame-ancestors.
		"any host":         {"https://*"},
		"any subdomain":    {"https://*.acme.example"},
		"a CSP keyword":    {"https://'self'"},
		"a CSP separator":  {"https://a.example;script-src"},
		"a leading hyphen": {"https://-acme.example"},
	} {
		if _, err := saveOrigins(f.svc, origins); !errors.Is(err, ErrInvalidInput) {
			t.Errorf("%s: %v, want ErrInvalidInput", name, err)
		}
	}
}

func TestHostedRedirectAllowedOnly(t *testing.T) {
	f := newFixture()
	f.svc.SetHostedBaseURL(testHostedBase)
	if _, err := saveOrigins(f.svc, []string{"https://portal.acme.example"}); err != nil {
		t.Fatalf("saveOrigins: %v", err)
	}
	by := Requester{Name: "Portal key", APIKeyID: new(uuid.UUID)}
	for name, redirect := range map[string]string{
		"another origin":  "https://evil.example/done",
		"a lookalike":     "https://portal.acme.example.evil.example/done",
		"plain http":      "http://portal.acme.example/done",
		"a relative path": "/done",
	} {
		_, err := f.svc.CreateRequest(context.Background(), testOrg, by,
			NewRequest{Channel: ChannelHosted, RedirectURL: redirect})
		if !errors.Is(err, ErrRedirectNotAllowed) && !errors.Is(err, ErrInvalidInput) {
			t.Errorf("%s: %v, want the redirect refused", name, err)
		}
	}
	if _, err := f.svc.CreateRequest(context.Background(), testOrg, by, NewRequest{
		Channel: ChannelOnScreen, RedirectURL: "https://portal.acme.example/done",
	}); !errors.Is(err, ErrInvalidInput) {
		t.Errorf("on-screen request with a redirect: %v, want ErrInvalidInput", err)
	}

	sent, err := f.svc.CreateRequest(context.Background(), testOrg, by, NewRequest{
		Channel: ChannelHosted, Language: email.LocaleNL,
		RedirectURL: "https://PORTAL.acme.example/done?ref=42",
	})
	if err != nil {
		t.Fatalf("CreateRequest(allowed redirect): %v", err)
	}
	if sent.Request.RedirectURL != "https://PORTAL.acme.example/done?ref=42" || sent.Request.Language != email.LocaleNL {
		t.Errorf("stored redirect %q language %q; want them as sent",
			sent.Request.RedirectURL, sent.Request.Language)
	}
	token, _ := strings.CutPrefix(sent.HostedURL, testHostedBase)
	if _, err := f.svc.StartHosted(context.Background(), token, proofingprovider.MethodIdem); err != nil {
		t.Fatalf("StartHosted: %v", err)
	}
	if got := f.ips.sessions[0].Language; got != string(email.LocaleNL) {
		t.Errorf("IPS session language = %q, want the link's %q", got, email.LocaleNL)
	}
}

func TestDeclinedHostedLinkCancels(t *testing.T) {
	f := newFixture()
	_, token := f.sendHosted(t)
	declined, err := f.svc.DeclineHosted(context.Background(), token)
	if err != nil {
		t.Fatalf("DeclineHosted: %v", err)
	}
	if got := declined.EffectiveStatus(f.svc.now()); got != StatusCancelled {
		t.Errorf("declined status = %s, want cancelled", got)
	}
	if _, err := f.svc.StartHosted(context.Background(), token, proofingprovider.MethodIdem); !errors.Is(err, ErrSessionOver) {
		t.Errorf("start after decline = %v, want ErrSessionOver", err)
	}
	if _, err := f.svc.DeclineHosted(context.Background(), token); !errors.Is(err, ErrSessionOver) {
		t.Errorf("second decline = %v, want ErrSessionOver", err)
	}
	if len(f.ips.sessions) != 0 {
		t.Errorf("a declined link made %d IPS sessions, want none", len(f.ips.sessions))
	}
}

func TestStartedHostedLinkNoDecline(t *testing.T) {
	f := newFixture()
	_, token := f.sendHosted(t)
	if _, err := f.svc.StartHosted(context.Background(), token, proofingprovider.MethodIdem); err != nil {
		t.Fatalf("StartHosted: %v", err)
	}
	if _, err := f.svc.DeclineHosted(context.Background(), token); !errors.Is(err, ErrLinkStarted) {
		t.Errorf("decline after start = %v, want ErrLinkStarted", err)
	}
}

func TestHostedPageFrameOrigins(t *testing.T) {
	f := newFixture()
	_, token := f.sendHosted(t)
	h := NewHandler(f.svc, nil, nil)
	csp := func(path string) string {
		header := http.Header{}
		h.PageHeaders(httptest.NewRequest(http.MethodGet, path, nil), header)
		return header.Get(headerCSP)
	}
	if got := csp("/p/" + token); got != frameNone {
		t.Errorf("no origins: CSP = %q, want %q", got, frameNone)
	}
	if _, err := saveOrigins(f.svc, []string{"https://portal.acme.example", "http://localhost:3000"}); err != nil {
		t.Fatalf("saveOrigins: %v", err)
	}
	if got, want := csp("/p/"+token), "frame-ancestors https://portal.acme.example http://localhost:3000"; got != want {
		t.Errorf("with origins: CSP = %q, want %q", got, want)
	}
	if got := csp("/p/unknown"); got != frameNone {
		t.Errorf("unknown link: CSP = %q, want %q", got, frameNone)
	}
	for _, path := range []string{"/", "/acme/identity-proofing", "/p/", "/p/" + token + "/x"} {
		if got := csp(path); got != "" {
			t.Errorf("%s: CSP = %q, want none set", path, got)
		}
	}
}

// fakeFlowHosted keeps hosted page settings in memory.
type fakeFlowHosted struct{ byFlow map[string]FlowHosted }

func (f *fakeFlowHosted) Get(_ context.Context, _ uuid.UUID, flowID string) (FlowHosted, error) {
	if s, ok := f.byFlow[flowID]; ok {
		return s, nil
	}
	return DefaultFlowHosted(), nil
}

func (f *fakeFlowHosted) Disabled(context.Context, uuid.UUID) ([]string, error) {
	var out []string
	for id, h := range f.byFlow {
		if !h.Enabled {
			out = append(out, id)
		}
	}
	return out, nil
}

func (f *fakeFlowHosted) ReturnURLs(context.Context, uuid.UUID) (map[string]string, error) {
	out := map[string]string{}
	for id, h := range f.byFlow {
		if h.ReturnURL != "" {
			out[id] = h.ReturnURL
		}
	}
	return out, nil
}

func (f *fakeFlowHosted) Save(_ context.Context, _ uuid.UUID, flowID string, s FlowHosted) (FlowHosted, error) {
	f.byFlow[flowID] = s
	return s, nil
}

func TestFlowHostedSettingsLinks(t *testing.T) {
	f := newFixture()
	f.svc.flowHostedSettings = &fakeFlowHosted{byFlow: map[string]FlowHosted{}}
	f.svc.SetHostedBaseURL(testHostedBase)
	ctx := context.Background()
	if _, err := saveOrigins(f.svc, []string{"https://portal.acme.example"}); err != nil {
		t.Fatal(err)
	}
	by := Requester{Name: "Portal key", APIKeyID: new(uuid.UUID)}
	create := func(in NewRequest) error {
		in.Channel = ChannelHosted
		_, err := f.svc.CreateRequest(ctx, testOrg, by, in)
		return err
	}

	for name, s := range map[string]FlowHosted{
		"bad completion": {Enabled: true, Completion: "later"},
		"bad language":   {Enabled: true, Completion: CompletionRedirect, Locales: []email.Locale{"de"}},
	} {
		if _, err := f.svc.SaveFlowHosted(ctx, testOrg, appFlow.ID, s); !errors.Is(err, ErrInvalidInput) {
			t.Errorf("%s: %v, want ErrInvalidInput", name, err)
		}
	}
	if _, err := f.svc.SaveFlowHosted(ctx, testOrg, "no-such-flow", DefaultFlowHosted()); !errors.Is(err, ErrFlowNotFound) {
		t.Errorf("unknown flow: %v, want ErrFlowNotFound", err)
	}

	saved, err := f.svc.SaveFlowHosted(ctx, testOrg, appFlow.ID, FlowHosted{
		Enabled: true, Completion: CompletionDone, Locales: []email.Locale{email.LocaleNL, email.LocaleNL},
	})
	if err != nil || !slices.Equal(saved.Locales, []email.Locale{email.LocaleNL}) {
		t.Fatalf("SaveFlowHosted = %+v, %v; want nl once", saved, err)
	}
	if err := create(NewRequest{RedirectURL: "https://portal.acme.example/done"}); !errors.Is(err, ErrInvalidInput) {
		t.Errorf("redirect on a thank-you-page flow: %v, want ErrInvalidInput", err)
	}
	if err := create(NewRequest{Language: email.LocaleEN}); !errors.Is(err, ErrInvalidInput) {
		t.Errorf("a language the flow does not offer: %v, want ErrInvalidInput", err)
	}
	if err := create(NewRequest{Language: email.LocaleNL}); err != nil {
		t.Errorf("an offered language: %v", err)
	}

	if _, err := f.svc.SaveFlowHosted(ctx, testOrg, appFlow.ID, FlowHosted{Enabled: false, Completion: CompletionRedirect}); err != nil {
		t.Fatal(err)
	}
	if err := create(NewRequest{}); !errors.Is(err, ErrHostedDisabled) {
		t.Errorf("a link on a flow with its hosted page off: %v, want ErrHostedDisabled", err)
	}
}

const testReturnOrigin, testReturnPage = "https://portal.acme.example", "https://portal.acme.example/verified"

// returnPageFixture is an org whose redirect origins hold testReturnOrigin and
// https://other.example.
func returnPageFixture(t *testing.T) fixture {
	t.Helper()
	f := newFixture()
	f.svc.flowHostedSettings = &fakeFlowHosted{byFlow: map[string]FlowHosted{}}
	f.svc.SetHostedBaseURL(testHostedBase)
	if _, err := saveOrigins(f.svc, []string{testReturnOrigin, "https://other.example"}); err != nil {
		t.Fatal(err)
	}
	return f
}

func TestFlowReturnPageSave(t *testing.T) {
	f := returnPageFixture(t)
	ctx := context.Background()
	if _, err := f.svc.SaveFlowHosted(ctx, testOrg, appFlow.ID, FlowHosted{
		Enabled: true, Completion: CompletionRedirect, ReturnURL: "https://evil.example/verified",
	}); !errors.Is(err, ErrRedirectNotAllowed) {
		t.Errorf("a return page off the origins: %v, want ErrRedirectNotAllowed", err)
	}
	if _, err := f.svc.SaveFlowHosted(ctx, testOrg, appFlow.ID, FlowHosted{
		Enabled: true, Completion: CompletionDone, ReturnURL: testReturnPage,
	}); !errors.Is(err, ErrInvalidInput) {
		t.Errorf("a return page on a thank-you-page flow: %v, want ErrInvalidInput", err)
	}
	saved, err := f.svc.SaveFlowHosted(ctx, testOrg, appFlow.ID, FlowHosted{
		Enabled: true, Completion: CompletionRedirect, ReturnURL: " " + testReturnPage + " ",
	})
	if err != nil || saved.ReturnURL != testReturnPage {
		t.Fatalf("SaveFlowHosted = %+v, %v; want the return page trimmed", saved, err)
	}
}

func TestFlowReturnPage(t *testing.T) {
	f := returnPageFixture(t)
	ctx := context.Background()
	if _, err := f.svc.SaveFlowHosted(ctx, testOrg, appFlow.ID, FlowHosted{
		Enabled: true, Completion: CompletionRedirect, ReturnURL: testReturnPage,
	}); err != nil {
		t.Fatal(err)
	}
	by := Requester{Name: "Portal key", APIKeyID: new(uuid.UUID)}
	create := func(in NewRequest) (Request, error) {
		if in.Channel == "" {
			in.Channel = ChannelHosted
		}
		sent, err := f.svc.CreateRequest(ctx, testOrg, by, in)
		return sent.Request, err
	}

	for name, tc := range map[string]struct {
		in   NewRequest
		want string
	}{
		"no redirect of its own": {NewRequest{}, testReturnPage},
		"its own redirect":       {NewRequest{RedirectURL: "https://other.example/next"}, "https://other.example/next"},
		"no redirect wanted":     {NewRequest{NoRedirect: true}, ""},
	} {
		req, err := create(tc.in)
		if err != nil || req.RedirectURL != tc.want {
			t.Errorf("%s: redirect %q, %v; want %q", name, req.RedirectURL, err, tc.want)
		}
	}
	if _, err := create(NewRequest{NoRedirect: true, RedirectURL: testReturnPage}); !errors.Is(err, ErrInvalidInput) {
		t.Errorf("no redirect wanted with a redirect: %v, want ErrInvalidInput", err)
	}
	if _, err := create(NewRequest{Channel: ChannelOnScreen, NoRedirect: true}); !errors.Is(err, ErrInvalidInput) {
		t.Errorf("an on-screen request without a redirect: %v, want ErrInvalidInput", err)
	}

	flows, err := f.svc.Flows(ctx, testOrg, FlowsAll)
	if err != nil {
		t.Fatal(err)
	}
	if i := slices.IndexFunc(flows, func(of OrgFlow) bool { return of.ID == appFlow.ID }); i < 0 || flows[i].HostedReturnURL != testReturnPage {
		t.Errorf("flow list = %+v, want the flow's return page", flows)
	}

	if _, err := saveOrigins(f.svc, []string{"https://other.example"}); !errors.Is(err, ErrRedirectOriginInUse) {
		t.Errorf("removing the return page's origin: %v, want ErrRedirectOriginInUse", err)
	}
	if _, err := saveOrigins(f.svc, []string{testReturnOrigin}); err != nil {
		t.Errorf("removing an origin no return page is on: %v", err)
	}
}

// A link keeps its redirect only while its origin is still allowed and its
// flow still ends on a redirect.
func TestHostedViewDropsStaleRedirect(t *testing.T) {
	f := newFixture()
	hosted := &fakeFlowHosted{byFlow: map[string]FlowHosted{}}
	f.svc.flowHostedSettings = hosted
	f.svc.SetHostedBaseURL(testHostedBase)
	ctx := context.Background()
	if _, err := saveOrigins(f.svc, []string{"https://portal.acme.example", "https://other.example"}); err != nil {
		t.Fatal(err)
	}
	const redirect = "https://other.example/next"
	sent, err := f.svc.CreateRequest(ctx, testOrg, Requester{Name: "Portal key", APIKeyID: new(uuid.UUID)},
		NewRequest{Channel: ChannelHosted, RedirectURL: redirect})
	if err != nil {
		t.Fatal(err)
	}
	token, _ := strings.CutPrefix(sent.HostedURL, testHostedBase)
	viewRedirect := func() string {
		r := httptest.NewRequest(http.MethodGet, "/api/v1/proof/"+token, nil)
		r.SetPathValue("token", token)
		w := httptest.NewRecorder()
		if err := NewHandler(f.svc, nil, nil).hostedView(w, r); err != nil {
			t.Fatalf("hostedView: %v", err)
		}
		var out hostedViewResponse
		if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
			t.Fatal(err)
		}
		return out.RedirectURL
	}

	if _, err := saveOrigins(f.svc, []string{"https://portal.acme.example"}); err != nil {
		t.Fatal(err)
	}
	if got := viewRedirect(); got != "" {
		t.Errorf("redirect on a removed origin = %q, want none", got)
	}
	if _, err := saveOrigins(f.svc, []string{"https://other.example"}); err != nil {
		t.Fatal(err)
	}
	if got := viewRedirect(); got != redirect {
		t.Errorf("redirect on an allowed origin = %q, want %q", got, redirect)
	}
	hosted.byFlow[appFlow.ID] = FlowHosted{Enabled: true, Completion: CompletionDone}
	if got := viewRedirect(); got != "" {
		t.Errorf("redirect once the flow ends on its thank-you page = %q, want none", got)
	}
}

// unreadBody fails the test when the handler reads it.
type unreadBody struct{ t *testing.T }

func (b unreadBody) Read([]byte) (int, error) {
	b.t.Error("the upload body was read before the link was checked")
	return 0, io.EOF
}

// A diploma upload to an unknown link is refused before its body is read.
func TestHostedDiplomaChecksLink(t *testing.T) {
	f := newFixture()
	h := NewHandler(f.svc, nil, nil)
	r := httptest.NewRequest(http.MethodPost, "/api/v1/proof/unknown/diplomas", unreadBody{t})
	r.SetPathValue("token", "unknown")
	r.Header.Set("Content-Type", "multipart/form-data; boundary=x")
	var apiErr *respond.APIError
	if err := h.hostedAddDiplomas(httptest.NewRecorder(), r); !errors.As(err, &apiErr) || apiErr.Status != http.StatusNotFound {
		t.Errorf("upload to an unknown link = %v, want 404", err)
	}
}

// A Verzekeringsbericht upload is refused before its body is read: to an
// unknown link, and to one whose window closed.
func TestHostedEmploymentChecksLink(t *testing.T) {
	e := newEmploymentFixture(EmploymentRequired)
	_, token := e.sendHosted(t)
	checked := time.Now().Add(-EmploymentWindow - time.Minute)
	e.requests.stored.Status, e.requests.stored.CompletedAt, e.requests.stored.CheckedAt = StatusApproved, &checked, &checked
	h := NewHandler(e.svc, nil, nil)

	for _, tc := range []struct {
		name, token, code string
	}{
		{"unknown link", "unknown", "session_not_found"},
		{"window closed", token, "employment_closed"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := httptest.NewRequest(http.MethodPost, "/api/v1/proof/"+tc.token+"/employment", unreadBody{t})
			r.SetPathValue("token", tc.token)
			r.Header.Set("Content-Type", "multipart/form-data; boundary=x")
			var apiErr *respond.APIError
			if err := h.hostedAddEmployment(httptest.NewRecorder(), r); !errors.As(err, &apiErr) || apiErr.Code != tc.code {
				t.Errorf("upload = %v, want %s", err, tc.code)
			}
		})
	}
}

func TestHostedViewHidesDocNumber(t *testing.T) {
	d := newDiplomaFixture(DiplomasOff)
	sent, token := d.sendHosted(t)
	const documentNumber = "DUO-1234567890"
	d.diplomas.held[sent.Request.ID] = []Diploma{{
		Qualification: "HBO Bachelor Verpleegkunde", Institution: "Hogeschool Utrecht",
		DateAwarded: time.Date(2020, time.July, 1, 0, 0, 0, 0, time.UTC), DocumentNumber: documentNumber,
	}}
	r := httptest.NewRequest(http.MethodGet, "/api/v1/proof/"+token, nil)
	r.SetPathValue("token", token)
	w := httptest.NewRecorder()
	if err := NewHandler(d.svc, nil, nil).hostedView(w, r); err != nil {
		t.Fatalf("hostedView: %v", err)
	}
	body := w.Body.String()
	if !strings.Contains(body, "HBO Bachelor Verpleegkunde") {
		t.Fatalf("hosted view = %s, want the extract listed", body)
	}
	if strings.Contains(body, "documentNumber") || strings.Contains(body, documentNumber) {
		t.Errorf("hosted view = %s, want no document number", body)
	}
	if got := w.Header().Get(headerCacheControl); got != cacheNoStore {
		t.Errorf("Cache-Control = %q, want no-store", got)
	}
}

// The person's page gets what is asked of them, not the flow's internal name
// or its assurance level.
func TestHostedViewLeavesOutFlowLabels(t *testing.T) {
	f := newFixture()
	f.ips.flows[0].RequiredAssuranceLevel = "low"
	_, token := f.sendHosted(t)
	r := httptest.NewRequest(http.MethodGet, "/api/v1/proof/"+token, nil)
	r.SetPathValue("token", token)
	w := httptest.NewRecorder()
	if err := NewHandler(f.svc, nil, nil).hostedView(w, r); err != nil {
		t.Fatalf("hostedView: %v", err)
	}
	var view struct {
		Flow map[string]json.RawMessage `json:"flow"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &view); err != nil {
		t.Fatalf("decode hosted view: %v", err)
	}
	if view.Flow["requestedAttributes"] == nil {
		t.Fatalf("hosted view flow = %v, want what it collects", view.Flow)
	}
	for _, internal := range []string{"name", "requiredAssuranceLevel"} {
		if _, ok := view.Flow[internal]; ok {
			t.Errorf("hosted view flow carries %q; want it left out", internal)
		}
	}
	if strings.Contains(w.Body.String(), appFlow.Name) {
		t.Errorf("hosted view = %s, want no flow name %q", w.Body.String(), appFlow.Name)
	}
}

// The hosted page gets the reasons of every language, and an object even for a
// flow saved before reasons existed, so the page can tell none from a failure.
func TestHostedViewCarriesReasons(t *testing.T) {
	d := newDiplomaFixture(DiplomasOff)
	_, token := d.sendHosted(t)
	view := func() string {
		r := httptest.NewRequest(http.MethodGet, "/api/v1/proof/"+token, nil)
		r.SetPathValue("token", token)
		w := httptest.NewRecorder()
		if err := NewHandler(d.svc, nil, nil).hostedView(w, r); err != nil {
			t.Fatalf("hostedView: %v", err)
		}
		return w.Body.String()
	}

	const noReasons = `"attributeReasons":{}`
	if body := view(); !strings.Contains(body, noReasons) {
		t.Errorf("hosted view of a flow without reasons = %s, want %s", body, noReasons)
	}

	i := slices.IndexFunc(d.ips.flows, func(f proofingprovider.Flow) bool { return f.ID == appFlow.ID })
	d.ips.flows[i].AttributeReasons = documentReasons()
	const withReasons = `"attributeReasons":{"dg1":{"en":"` + reasonDocumentEN + `","nl":"` + reasonDocumentNL + `"}}`
	if body := view(); !strings.Contains(body, withReasons) {
		t.Errorf("hosted view = %s, want %s", body, withReasons)
	}
}

// The hosted page names how long its session ran when it ends: the lifetime
// the session was started with (the flow's own, else the org's fixed default),
// not whatever the flow says by the time the page is read.
func TestHostedViewSessionTTL(t *testing.T) {
	d := newDiplomaFixture(DiplomasOff)
	_, token := d.sendHosted(t)
	view := func() hostedProgressResponse {
		t.Helper()
		r := httptest.NewRequest(http.MethodGet, "/api/v1/proof/"+token, nil)
		r.SetPathValue("token", token)
		w := httptest.NewRecorder()
		if err := NewHandler(d.svc, nil, nil).hostedView(w, r); err != nil {
			t.Fatalf("hostedView: %v", err)
		}
		var out hostedProgressResponse
		if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
			t.Fatalf("decode hosted view: %v", err)
		}
		return out
	}
	// The org's lifetime is a fixed default (#341): the flow sets its own.
	setFlowTTL := func(ttl time.Duration) {
		t.Helper()
		i := slices.IndexFunc(d.ips.flows, func(f proofingprovider.Flow) bool { return f.ID == d.requests.stored.FlowID })
		if i < 0 {
			t.Fatalf("flow %s not in the engine", d.requests.stored.FlowID)
		}
		d.ips.flows[i].SessionTTLSeconds = int(ttl.Seconds())
	}

	if got := view().SessionTTLSeconds; got != 0 {
		t.Errorf("unstarted link's lifetime = %d, want none", got)
	}

	setFlowTTL(5 * time.Minute)
	started, err := d.svc.StartHosted(context.Background(), token, proofingprovider.MethodIdem)
	if err != nil {
		t.Fatalf("StartHosted: %v", err)
	}
	if got, want := newHostedProgress(started.Request, time.Now()).SessionTTLSeconds, 300; got != want {
		t.Errorf("start lifetime = %d, want the flow's %d", got, want)
	}
	if got := d.ips.sessions[len(d.ips.sessions)-1].TTL; got != 5*time.Minute {
		t.Errorf("session asked for %s, want the flow's 5m", got)
	}

	// A change of the flow afterwards leaves what the session ran with.
	setFlowTTL(2 * time.Minute)
	if got, want := view().SessionTTLSeconds, 300; got != want {
		t.Errorf("lifetime after the flow's changed = %d, want the %d it was started with", got, want)
	}
}
