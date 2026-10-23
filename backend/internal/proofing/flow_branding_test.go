package proofing

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"

	"github.com/privacybydesign/yivi-businesswallet/backend/internal/organization"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/respond"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/themesettings"
)

// fakeFlowBranding holds each flow's own branding in memory.
type fakeFlowBranding struct {
	byFlow map[string]FlowBranding
	logos  map[string]Logo
}

func newFakeFlowBranding() *fakeFlowBranding {
	return &fakeFlowBranding{byFlow: map[string]FlowBranding{}, logos: map[string]Logo{}}
}

func (f *fakeFlowBranding) Get(_ context.Context, _ uuid.UUID, flowID string) (FlowBranding, error) {
	return f.byFlow[flowID], nil
}

func (f *fakeFlowBranding) Save(_ context.Context, _ uuid.UUID, flowID string, b FlowBranding, logo LogoChange) (FlowBranding, error) {
	if !b.Custom {
		delete(f.byFlow, flowID)
		delete(f.logos, flowID)
		return FlowBranding{}, nil
	}
	b.HasLogo = f.byFlow[flowID].HasLogo
	if logo.Replace {
		b.HasLogo = len(logo.Logo.Bytes) > 0
		f.logos[flowID] = logo.Logo
	}
	f.byFlow[flowID] = b
	return b, nil
}

func (f *fakeFlowBranding) Logo(_ context.Context, _ uuid.UUID, flowID string) (Logo, error) {
	if !f.byFlow[flowID].HasLogo {
		return Logo{}, ErrNoLogo
	}
	return f.logos[flowID], nil
}

const contentTypePNG = "image/png"

var (
	orgLogo  = Logo{Bytes: []byte("\x89PNG org"), ContentType: contentTypePNG}
	flowLogo = Logo{Bytes: []byte("\x89PNG flow"), ContentType: contentTypePNG}
)

// brandedFixture is a fixture whose org has a full branding and whose chipFlow
// has its own, which sets only a name and a logo.
func brandedFixture(t *testing.T) fixture {
	t.Helper()
	f := newFixture()
	f.svc.flowBrandings = newFakeFlowBranding()
	ctx := context.Background()
	if _, err := f.svc.SaveBranding(ctx, testOrg.ID, Branding{
		DisplayName: "Acme Verzekeringen", PrimaryColor: "#1F5B4A",
		SupportContact: "help@acme.example", PrivacyURL: "https://acme.example/privacy",
	}, LogoChange{Replace: true, Logo: orgLogo}); err != nil {
		t.Fatalf("SaveBranding: %v", err)
	}
	if _, err := f.svc.SaveFlowBranding(ctx, testOrg, chipFlow.ID, FlowBranding{
		Custom: true, Branding: Branding{DisplayName: " Acme Reizen "},
	}, LogoChange{Replace: true, Logo: flowLogo}); err != nil {
		t.Fatalf("SaveFlowBranding: %v", err)
	}
	return f
}

func TestBrandingOver(t *testing.T) {
	base := Branding{
		DisplayName: "Org", PrimaryColor: "#111111", SecondaryColor: "#333333", TextColor: "#444444",
		SurfaceColor: "#555555", BorderColor: "#666666", LinkColor: "#777777", SuccessColor: "#888888",
		WarningColor: "#999999", ErrorColor: "#AAAAAA", TopbarColor: "#BBBBBB", FontFamily: "Nunito, sans-serif",
		SupportContact: "org@example.org", PrivacyURL: "https://org.example/privacy", HasLogo: true, HidePoweredBy: true,
	}
	// with is base with the fields of own's name, colour, contact and privacy
	// link replaced: the white-label colours and font are not a flow's to set.
	with := func(name, color, contact, privacy string) Branding {
		b := base
		b.DisplayName, b.PrimaryColor, b.SupportContact, b.PrivacyURL = name, color, contact, privacy
		return b
	}
	cases := map[string]struct {
		own  Branding
		want Branding
	}{
		"nothing set": {Branding{}, base},
		"every field": {
			Branding{
				DisplayName: "Own", PrimaryColor: "#222222", SupportContact: "own@example.org",
				PrivacyURL: "https://own.example/privacy", HasLogo: true,
			},
			with("Own", "#222222", "own@example.org", "https://own.example/privacy"),
		},
		"one field": {
			Branding{PrimaryColor: "#222222"},
			with("Org", "#222222", "org@example.org", "https://org.example/privacy"),
		},
		"white-label of its own": {
			Branding{SecondaryColor: "#CCCCCC", TopbarColor: "#DDDDDD", FontFamily: "Inter, sans-serif"},
			base,
		},
	}
	for name, c := range cases {
		if got := c.own.over(base); got != c.want {
			t.Errorf("%s: over = %+v, want %+v", name, got, c.want)
		}
	}
	if got := (Branding{HasLogo: true}).over(Branding{}); !got.HasLogo {
		t.Error("an own logo over an org without one: HasLogo is false")
	}
	if got := (Branding{HidePoweredBy: true}).over(Branding{}); got.HidePoweredBy {
		t.Error("the flow's HidePoweredBy was kept; \"powered by\" is the org's")
	}
}

// A flow that is not custom wears the org's branding, whatever fields it holds.
func TestBrandedForNotCustom(t *testing.T) {
	org := Settings{Branding: Branding{DisplayName: "Org"}}
	got := org.brandedFor(FlowBranding{Branding: Branding{DisplayName: "Flow"}})
	if got.Branding.DisplayName != "Org" {
		t.Errorf("display name = %q, want the org's", got.Branding.DisplayName)
	}
}

// A mail for a flow with its own branding is signed with it, and each field it
// leaves empty is the org's; a flow without keeps the org's look.
func TestMailUsesFlowBranding(t *testing.T) {
	f := brandedFixture(t)
	ctx := context.Background()
	f.sendExternal(t, "anna@example.org", "")
	own := f.mailer.sent[0].mail
	if own.OrgName != "Acme Reizen" || own.RequesterName != "Acme Reizen" ||
		own.SupportContact != "help@acme.example" || own.PrivacyURL != "https://acme.example/privacy" ||
		own.Brand == nil || own.Brand.PrimaryColor != "#1F5B4A" || !bytes.Equal(own.Brand.Logo.Bytes, flowLogo.Bytes) {
		t.Errorf("mail on the flow with its own branding = %+v; want its name and logo, the org's other fields", own)
	}

	if _, err := f.svc.SaveFlowBranding(ctx, testOrg, chipFlow.ID, FlowBranding{}, LogoChange{}); err != nil {
		t.Fatalf("SaveFlowBranding(not custom): %v", err)
	}
	f.sendExternal(t, "bram@example.org", "")
	shared := f.mailer.sent[1].mail
	if shared.OrgName != "Acme Verzekeringen" || !bytes.Equal(shared.Brand.Logo.Bytes, orgLogo.Bytes) {
		t.Errorf("mail after going back to the org's branding = %+v; want the org's name and logo", shared)
	}
}

// failingFlowBranding is a flow branding store whose reads fail.
type failingFlowBranding struct {
	*fakeFlowBranding
}

func (failingFlowBranding) Get(context.Context, uuid.UUID, string) (FlowBranding, error) {
	return FlowBranding{}, errors.New("flow branding unreadable")
}

// A flow whose branding cannot be read gets no mail, so it never goes out in
// another look than the admin set; the request is still made.
func TestMailBlockedByBranding(t *testing.T) {
	f := brandedFixture(t)
	f.svc.flowBrandings = failingFlowBranding{newFakeFlowBranding()}

	sent := f.sendExternal(t, "anna@example.org", "")
	if sent.MailSent || len(f.mailer.sent) != 0 {
		t.Errorf("mail sent = %v (%d mails), want none", sent.MailSent, len(f.mailer.sent))
	}
	if sent.DeepLink == "" {
		t.Error("request without a deep link, want it made all the same")
	}
}

// A flow with no logo of its own shows the org's.
func TestMailLogoFallsBackToOrg(t *testing.T) {
	f := brandedFixture(t)
	if _, err := f.svc.SaveFlowBranding(context.Background(), testOrg, chipFlow.ID, FlowBranding{
		Custom: true, Branding: Branding{DisplayName: "Acme Reizen"},
	}, LogoChange{Replace: true}); err != nil {
		t.Fatalf("SaveFlowBranding: %v", err)
	}
	f.sendExternal(t, "anna@example.org", "")
	if got := f.mailer.sent[0].mail.Brand.Logo.Bytes; !bytes.Equal(got, orgLogo.Bytes) {
		t.Errorf("logo = %q, want the org's", got)
	}
}

// The hosted page shows the flow's branding over the org's and serves the
// logo the branding names.
func TestHostedUsesFlowBranding(t *testing.T) {
	f := brandedFixture(t)
	f.svc.SetHostedBaseURL(testHostedBase)
	sent, err := f.svc.CreateRequest(context.Background(), testOrg, Requester{Name: "Portal key", APIKeyID: new(uuid.UUID)},
		NewRequest{Channel: ChannelHosted, FlowID: chipFlow.ID})
	if err != nil {
		t.Fatalf("CreateRequest(hosted): %v", err)
	}
	token := sent.HostedURL[len(testHostedBase):]

	hosted, err := f.svc.HostedRequest(context.Background(), token)
	if err != nil {
		t.Fatalf("HostedRequest: %v", err)
	}
	b := hosted.Settings.Branding
	if hosted.Settings.SignedAs() != "Acme Reizen" || b.PrimaryColor != "#1F5B4A" || !b.HasLogo {
		t.Errorf("hosted settings = %+v, want the flow's name over the org's other fields", hosted.Settings)
	}
	logo, err := f.svc.HostedLogo(context.Background(), token)
	if err != nil || !bytes.Equal(logo.Bytes, flowLogo.Bytes) {
		t.Errorf("HostedLogo = %q, %v; want the flow's", logo.Bytes, err)
	}
}

func TestSaveFlowBranding(t *testing.T) {
	f := newFixture()
	f.svc.flowBrandings = newFakeFlowBranding()
	ctx := context.Background()
	custom := func(b Branding) FlowBranding { return FlowBranding{Custom: true, Branding: b} }

	for name, b := range map[string]Branding{
		"colour":       {PrimaryColor: "green"},
		"http privacy": {PrivacyURL: "http://acme.example/privacy"},
	} {
		if _, err := f.svc.SaveFlowBranding(ctx, testOrg, appFlow.ID, custom(b), LogoChange{}); !errors.Is(err, ErrInvalidInput) {
			t.Errorf("%s: err = %v, want ErrInvalidInput", name, err)
		}
	}

	// The Branding tab sends its hidden draft fields in "same as the
	// organisation" mode too; they are not on screen, so they are not checked.
	for name, b := range map[string]Branding{
		"colour":       {PrimaryColor: "green"},
		"http privacy": {PrivacyURL: "http://acme.example/privacy"},
	} {
		if _, err := f.svc.SaveFlowBranding(ctx, testOrg, appFlow.ID, FlowBranding{Branding: b}, LogoChange{}); err != nil {
			t.Errorf("not custom, %s: err = %v, want nil", name, err)
		}
	}

	if _, err := f.svc.SaveFlowBranding(ctx, testOrg, "no-such-flow", custom(Branding{}), LogoChange{}); !errors.Is(err, ErrFlowNotFound) {
		t.Errorf("unknown flow: err = %v, want ErrFlowNotFound", err)
	}
	if _, err := f.svc.FlowBrandingLogo(ctx, testOrg, appFlow.ID); !errors.Is(err, ErrNoLogo) {
		t.Errorf("logo of a flow without one: err = %v, want ErrNoLogo", err)
	}
}

// flowBrandingForm is the form of fields, plus a logo file when logo is set.
func flowBrandingForm(t *testing.T, fields map[string]string, logo []byte) (*bytes.Buffer, string) {
	t.Helper()
	var body bytes.Buffer
	w := multipart.NewWriter(&body)
	for k, v := range fields {
		if err := w.WriteField(k, v); err != nil {
			t.Fatal(err)
		}
	}

	if logo != nil {
		part, err := w.CreateFormFile(logoFormField, "logo")
		if err != nil {
			t.Fatal(err)
		}
		if _, err := part.Write(logo); err != nil {
			t.Fatal(err)
		}
	}

	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	return &body, w.FormDataContentType()
}

func saveFlowBrandingCall(t *testing.T, h *Handler, fields map[string]string) (*httptest.ResponseRecorder, error) {
	t.Helper()
	return saveFlowBrandingLogoCall(t, h, fields, nil)
}

func saveFlowBrandingLogoCall(t *testing.T, h *Handler, fields map[string]string, logo []byte) (*httptest.ResponseRecorder, error) {
	t.Helper()
	body, contentType := flowBrandingForm(t, fields, logo)
	r := httptest.NewRequest(http.MethodPut, "/", body)
	r.Header.Set(headerContentType, contentType)
	r.SetPathValue("slug", "acme")
	r.SetPathValue("flowID", appFlow.ID)
	r = r.WithContext(organization.ContextWithOrg(r.Context(), organization.Organization{ID: testOrg.ID, Name: testOrg.Name}))
	w := httptest.NewRecorder()
	return w, h.saveFlowBranding(w, r)
}

// The form must say which branding the flow uses: a missing field is not "same
// as the organisation".
func TestFlowBrandingNeedsCustom(t *testing.T) {
	f := newFixture()
	f.svc.flowBrandings = newFakeFlowBranding()
	h := NewHandler(f.svc, nil, nil)

	_, err := saveFlowBrandingCall(t, h, map[string]string{"displayName": "Acme Reizen"})
	var apiErr *respond.APIError
	if !errors.As(err, &apiErr) || apiErr.Status != http.StatusBadRequest {
		t.Fatalf("form without custom = %v, want 400", err)
	}

	w, err := saveFlowBrandingCall(t, h, map[string]string{"custom": "true", "displayName": "Acme Reizen"})
	if err != nil {
		t.Fatalf("saveFlowBranding: %v", err)
	}
	var got flowBrandingResponse
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil || !got.Custom || got.DisplayName != "Acme Reizen" || got.LogoURI != "" {
		t.Errorf("response = %+v, %v; want a custom flow named Acme Reizen with no logo", got, err)
	}
}

// The Branding tab sends its hidden draft logo in "same as the organisation"
// mode too. A flow that is not custom drops its logo, so the upload is not
// checked; a custom one is.
func TestFlowLogoOnlyWhenCustom(t *testing.T) {
	f := newFixture()
	f.svc.flowBrandings = newFakeFlowBranding()
	h := NewHandler(f.svc, nil, nil)

	badLogos := map[string][]byte{
		"svg":       []byte(`<svg xmlns="http://www.w3.org/2000/svg"></svg>`),
		"not image": []byte("not an image"),
		"too large": bytes.Repeat([]byte{0}, themesettings.MaxLogoBytes+1),
	}
	for name, logo := range badLogos {
		w, err := saveFlowBrandingLogoCall(t, h, map[string]string{"custom": "false"}, logo)
		var got flowBrandingResponse
		if err != nil || json.Unmarshal(w.Body.Bytes(), &got) != nil || got.Custom {
			t.Errorf("not custom, %s logo: %v, %+v; want a save that is not custom", name, err, got)
		}

		_, err = saveFlowBrandingLogoCall(t, h, map[string]string{"custom": "true"}, logo)
		var apiErr *respond.APIError
		if !errors.As(err, &apiErr) || apiErr.Status < http.StatusBadRequest || apiErr.Status >= http.StatusInternalServerError {
			t.Errorf("custom, %s logo: err = %v, want a 4xx", name, err)
		}
	}
}
