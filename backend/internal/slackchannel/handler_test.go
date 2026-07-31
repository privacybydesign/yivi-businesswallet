package slackchannel

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/privacybydesign/yivi-businesswallet/backend/internal/organization"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/respond"
)

// fakeStore stands in for *Store in the route round-trips below.
type fakeStore struct {
	settings Settings
	saved    *SettingsInput
	err      error
}

func (s *fakeStore) GetSettings(context.Context, uuid.UUID) (Settings, error) {
	return s.settings, nil
}

func (s *fakeStore) Upsert(_ context.Context, _ uuid.UUID, in SettingsInput) (Settings, error) {
	if s.err != nil {
		return Settings{}, s.err
	}
	s.saved = &in
	s.settings = Settings{Configured: true, Enabled: in.Enabled, HasWebhook: s.settings.HasWebhook}
	if in.WebhookURL != nil {
		s.settings.HasWebhook = *in.WebhookURL != ""
	}
	return s.settings, nil
}

type fakeTester struct {
	called bool
	err    error
}

func (f *fakeTester) SendTest(context.Context, uuid.UUID, string) error {
	f.called = true
	return f.err
}

// serve runs a request through the registered routes with pass-through auth and an
// organisation plus role in context, so the wiring (pattern, admin gate, decode,
// respond) is exercised as the router will run it.
func serve(t *testing.T, store settingsStore, channel testSender, role string, req *http.Request) *httptest.ResponseRecorder {
	t.Helper()
	passthrough := func(next http.Handler) http.Handler { return next }
	mux := http.NewServeMux()
	NewHandler(store, channel, passthrough, passthrough).Register(mux)

	ctx := organization.ContextWithOrg(req.Context(),
		organization.Organization{ID: uuid.New(), Slug: "acme", Name: "Acme B.V."})
	ctx = organization.ContextWithRole(ctx, role)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req.WithContext(ctx))
	return rec
}

func put(body string) *http.Request {
	return httptest.NewRequest(http.MethodPut, "/orgs/acme/slack/settings", strings.NewReader(body))
}

func TestPutSettingsSavesTheWebhook(t *testing.T) {
	store := &fakeStore{}

	rec := serve(t, store, &fakeTester{}, organization.RoleAdmin,
		put(`{"webhookUrl":" https://hooks.slack.com/services/T000/B000/xxx ","enabled":true}`))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body)
	}
	if store.saved == nil || store.saved.WebhookURL == nil {
		t.Fatal("the store was not asked to save a webhook")
	}
	if want := "https://hooks.slack.com/services/T000/B000/xxx"; *store.saved.WebhookURL != want {
		t.Errorf("saved %q, want the trimmed url %q", *store.saved.WebhookURL, want)
	}
	if !store.saved.Enabled {
		t.Error("saved Enabled = false, want the requested true")
	}
}

// The webhook URL is a secret: once saved it is never handed back, so the screen
// learns only that one is in place.
func TestSettingsResponseNeverCarriesTheWebhook(t *testing.T) {
	secret := "https://hooks.slack.com/services/T000/B000/supersecrettoken"
	store := &fakeStore{}

	saved := serve(t, store, &fakeTester{}, organization.RoleAdmin,
		put(`{"webhookUrl":"`+secret+`","enabled":true}`))
	read := serve(t, store, &fakeTester{}, organization.RoleAdmin,
		httptest.NewRequest(http.MethodGet, "/orgs/acme/slack/settings", nil))

	for name, rec := range map[string]*httptest.ResponseRecorder{"put": saved, "get": read} {
		if strings.Contains(rec.Body.String(), "supersecrettoken") {
			t.Errorf("%s body = %s, want no webhook url in it", name, rec.Body)
		}
		var got Settings
		if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
			t.Fatalf("%s: decode response: %v", name, err)
		}
		if !got.HasWebhook {
			t.Errorf("%s HasWebhook = false, want true after saving one", name)
		}
	}
}

// A body that omits webhookUrl only changes the flag; one that sends it empty
// clears the stored URL. The two must not collapse into each other.
func TestPutSettingsSeparatesKeepingFromClearing(t *testing.T) {
	t.Run("omitted keeps", func(t *testing.T) {
		store := &fakeStore{}
		serve(t, store, &fakeTester{}, organization.RoleAdmin, put(`{"enabled":false}`))
		if store.saved == nil || store.saved.WebhookURL != nil {
			t.Errorf("saved %+v, want no webhook change", store.saved)
		}
	})
	t.Run("empty clears", func(t *testing.T) {
		store := &fakeStore{}
		serve(t, store, &fakeTester{}, organization.RoleAdmin, put(`{"webhookUrl":"","enabled":false}`))
		if store.saved == nil || store.saved.WebhookURL == nil || *store.saved.WebhookURL != "" {
			t.Errorf("saved %+v, want the webhook cleared", store.saved)
		}
	})
}

func TestPutSettingsRejectsBadInput(t *testing.T) {
	cases := map[string]struct {
		body   string
		status int
		code   string
	}{
		"not json":        {`not json at all`, http.StatusBadRequest, "invalid_body"},
		"misspelled key":  {`{"webhook":"x"}`, http.StatusBadRequest, "invalid_body"},
		"not a slack url": {`{"webhookUrl":"https://example.org/hook"}`, http.StatusBadRequest, "invalid_input"},
		"plain http":      {`{"webhookUrl":"http://hooks.slack.com/services/x"}`, http.StatusBadRequest, "invalid_input"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			rec := serve(t, &fakeStore{}, &fakeTester{}, organization.RoleAdmin, put(tc.body))
			if rec.Code != tc.status {
				t.Fatalf("status = %d, want %d: %s", rec.Code, tc.status, rec.Body)
			}
			if got := errorCode(t, rec); got != tc.code {
				t.Errorf("code = %q, want %q", got, tc.code)
			}
		})
	}
}

// A deployment with no Slack key cannot hold the secret at all. The admin pasting
// the URL is the one who finds out, so they are told rather than shown a 500.
func TestPutSettingsReportsAMissingEncryptionKey(t *testing.T) {
	store := &fakeStore{err: ErrNoEncryptionKey}

	rec := serve(t, store, &fakeTester{}, organization.RoleAdmin,
		put(`{"webhookUrl":"https://hooks.slack.com/services/T000/B000/xxx","enabled":true}`))

	if rec.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409: %s", rec.Code, rec.Body)
	}
	if got := errorCode(t, rec); got != "no_encryption_key" {
		t.Errorf("code = %q, want no_encryption_key", got)
	}
}

func TestSendTestAnswers(t *testing.T) {
	cases := map[string]struct {
		err    error
		status int
		code   string
	}{
		"delivered":      {nil, http.StatusNoContent, ""},
		"not configured": {ErrNotConfigured, http.StatusConflict, "not_configured"},
		"refused":        {&DeliveryError{Reason: "404 Not Found: no_service"}, http.StatusBadGateway, "webhook_failed"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			channel := &fakeTester{err: tc.err}

			rec := serve(t, &fakeStore{}, channel, organization.RoleAdmin,
				httptest.NewRequest(http.MethodPost, "/orgs/acme/slack/test", nil))

			if rec.Code != tc.status {
				t.Fatalf("status = %d, want %d: %s", rec.Code, tc.status, rec.Body)
			}
			if !channel.called {
				t.Error("the channel was not asked to post a test notification")
			}
			if tc.code == "" {
				return
			}
			if got := errorCode(t, rec); got != tc.code {
				t.Errorf("code = %q, want %q", got, tc.code)
			}
		})
	}
}

// The admin is the one who can fix a refused webhook, so Slack's own reason
// reaches them instead of only the log.
func TestSendTestPassesTheRefusalOn(t *testing.T) {
	channel := &fakeTester{err: &DeliveryError{Reason: "404 Not Found: no_service"}}

	rec := serve(t, &fakeStore{}, channel, organization.RoleAdmin,
		httptest.NewRequest(http.MethodPost, "/orgs/acme/slack/test", nil))

	if !strings.Contains(rec.Body.String(), "no_service") {
		t.Errorf("body = %s, want the refusal in it", rec.Body)
	}
}

// Who gets told what is administrative configuration, and the read says whether a
// webhook is in place, so a plain member is turned away from all three routes.
func TestSlackRoutesAreAdminOnly(t *testing.T) {
	cases := map[string]*http.Request{
		"get":  httptest.NewRequest(http.MethodGet, "/orgs/acme/slack/settings", nil),
		"put":  put(`{"enabled":false}`),
		"test": httptest.NewRequest(http.MethodPost, "/orgs/acme/slack/test", nil),
	}
	for name, req := range cases {
		t.Run(name, func(t *testing.T) {
			rec := serve(t, &fakeStore{}, &fakeTester{}, organization.RoleMember, req)
			if rec.Code != http.StatusForbidden {
				t.Errorf("status = %d, want 403 for a non-admin member", rec.Code)
			}
		})
	}
}

func errorCode(t *testing.T, rec *httptest.ResponseRecorder) string {
	t.Helper()
	var body struct {
		Code string `json:"code"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode error response %s: %v", rec.Body, err)
	}
	return body.Code
}

// The validation error is rendered into a response body, so it names the shape it
// wants and repeats nothing of what was sent.
func TestValidationErrorRepeatsNoInput(t *testing.T) {
	_, err := parseSettingsRequest(put(`{"webhookUrl":"https://evil.example.org/T0SECRET"}`))

	var apiErr *respond.APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("err = %v, want an APIError", err)
	}
	if strings.Contains(apiErr.Message, "T0SECRET") {
		t.Errorf("message = %q, want no part of the submitted value", apiErr.Message)
	}
}
