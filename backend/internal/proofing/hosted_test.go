package proofing

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/privacybydesign/yivi-businesswallet/backend/internal/email"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/proofingprovider"
)

func TestRedirectOriginsAreOriginsOnHTTPS(t *testing.T) {
	f := newFixture(true)
	customer := f.testCustomer()
	saved, err := f.svc.SaveCustomerRedirectOrigins(context.Background(), testOrg.ID, customer.ID,
		[]string{"https://Portal.Initech.example/", "http://localhost:3000", "https://portal.initech.example"})
	if err != nil {
		t.Fatalf("SaveCustomerRedirectOrigins: %v", err)
	}
	if want := []string{"https://portal.initech.example", "http://localhost:3000"}; !slices.Equal(saved.RedirectOrigins, want) {
		t.Errorf("origins = %v, want %v (lowercased, once each)", saved.RedirectOrigins, want)
	}
	tooMany := make([]string, maxRedirectOrigins+1)
	for i := range tooMany {
		tooMany[i] = "https://a" + strings.Repeat("a", i) + ".example"
	}
	for name, origins := range map[string][]string{
		"plain http":     {"http://portal.initech.example"},
		"a path":         {"https://portal.initech.example/done"},
		"a query":        {"https://portal.initech.example?x=1"},
		"credentials":    {"https://user@portal.initech.example"},
		"no host":        {"https://"},
		"another scheme": {"javascript:alert(1)"},
		"too many":       tooMany,
	} {
		if _, err := f.svc.SaveCustomerRedirectOrigins(context.Background(), testOrg.ID, customer.ID, origins); !errors.Is(err, ErrInvalidInput) {
			t.Errorf("%s: %v, want ErrInvalidInput", name, err)
		}
	}
}

func TestAHostedRequestRedirectsOnlyToAnAllowedOrigin(t *testing.T) {
	f := newFixture(true)
	customer := f.testCustomer()
	f.svc.SetHostedBaseURL(testHostedBase)
	if _, err := f.svc.SaveCustomerRedirectOrigins(context.Background(), testOrg.ID, customer.ID,
		[]string{"https://portal.initech.example"}); err != nil {
		t.Fatalf("SaveCustomerRedirectOrigins: %v", err)
	}
	by := Requester{Name: "Portal key", APIKeyID: new(uuid.UUID)}
	for name, redirect := range map[string]string{
		"another origin":  "https://evil.example/done",
		"a lookalike":     "https://portal.initech.example.evil.example/done",
		"plain http":      "http://portal.initech.example/done",
		"a relative path": "/done",
	} {
		_, err := f.svc.CreateRequest(context.Background(), testOrg, by,
			NewRequest{CustomerID: &customer.ID, Channel: ChannelHosted, RedirectURL: redirect})
		if !errors.Is(err, ErrRedirectNotAllowed) && !errors.Is(err, ErrInvalidInput) {
			t.Errorf("%s: %v, want the redirect refused", name, err)
		}
	}
	if _, err := f.svc.CreateRequest(context.Background(), testOrg, by, NewRequest{
		CustomerID: &customer.ID, Channel: ChannelOnScreen, RedirectURL: "https://portal.initech.example/done",
	}); !errors.Is(err, ErrInvalidInput) {
		t.Errorf("on-screen request with a redirect: %v, want ErrInvalidInput", err)
	}

	sent, err := f.svc.CreateRequest(context.Background(), testOrg, by, NewRequest{
		CustomerID: &customer.ID, Channel: ChannelHosted, Language: email.LocaleNL,
		RedirectURL: "https://PORTAL.initech.example/done?ref=42",
	})
	if err != nil {
		t.Fatalf("CreateRequest(allowed redirect): %v", err)
	}
	if sent.Request.RedirectURL != "https://PORTAL.initech.example/done?ref=42" || sent.Request.Language != email.LocaleNL {
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

func TestADeclinedHostedLinkIsCancelledAndCannotStart(t *testing.T) {
	f := newFixture(true)
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

func TestAStartedHostedLinkCannotBeDeclined(t *testing.T) {
	f := newFixture(true)
	_, token := f.sendHosted(t)
	if _, err := f.svc.StartHosted(context.Background(), token, proofingprovider.MethodIdem); err != nil {
		t.Fatalf("StartHosted: %v", err)
	}
	if _, err := f.svc.DeclineHosted(context.Background(), token); !errors.Is(err, ErrLinkStarted) {
		t.Errorf("decline after start = %v, want ErrLinkStarted", err)
	}
}

func TestAHostedPageIsFramedOnlyByItsCustomersOrigins(t *testing.T) {
	f := newFixture(true)
	_, token := f.sendHosted(t)
	h := NewHandler(f.svc, nil, nil)
	csp := func(path string) string {
		header := http.Header{}
		h.PageHeaders(httptest.NewRequest(http.MethodGet, path, nil), header)
		return header.Get("Content-Security-Policy")
	}
	if got := csp("/p/" + token); got != frameNone {
		t.Errorf("no origins: CSP = %q, want %q", got, frameNone)
	}
	if _, err := f.svc.SaveCustomerRedirectOrigins(context.Background(), testOrg.ID, initech.ID,
		[]string{"https://portal.initech.example", "http://localhost:3000"}); err != nil {
		t.Fatalf("SaveCustomerRedirectOrigins: %v", err)
	}
	if got, want := csp("/p/"+token), "frame-ancestors https://portal.initech.example http://localhost:3000"; got != want {
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

func (f *fakeFlowHosted) Save(_ context.Context, _ uuid.UUID, flowID string, s FlowHosted) (FlowHosted, error) {
	f.byFlow[flowID] = s
	return s, nil
}

func TestAFlowsHostedSettingsGovernItsLinks(t *testing.T) {
	f := newFixture(true)
	f.svc.flowHostedSettings = &fakeFlowHosted{byFlow: map[string]FlowHosted{}}
	customer := f.testCustomer()
	f.svc.SetHostedBaseURL(testHostedBase)
	ctx := context.Background()
	if _, err := f.svc.SaveCustomerRedirectOrigins(ctx, testOrg.ID, customer.ID, []string{"https://portal.initech.example"}); err != nil {
		t.Fatal(err)
	}
	by := Requester{Name: "Portal key", APIKeyID: new(uuid.UUID)}
	create := func(in NewRequest) error {
		in.CustomerID, in.Channel = &customer.ID, ChannelHosted
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
	if err := create(NewRequest{RedirectURL: "https://portal.initech.example/done"}); !errors.Is(err, ErrInvalidInput) {
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
