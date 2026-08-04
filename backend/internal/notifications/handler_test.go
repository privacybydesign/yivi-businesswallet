package notifications

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/privacybydesign/yivi-businesswallet/backend/internal/organization"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/respond"
)

// putSettings parses and validates the body before it reads the org from
// context, so the rejection paths are exercisable without the middleware chain.
func request(body string) *http.Request {
	return httptest.NewRequest(http.MethodPut, "/orgs/acme/notifications/settings", strings.NewReader(body))
}

func TestParseSettingsRequestNormalizes(t *testing.T) {
	in, err := parseSettingsRequest(request(
		`{"subscriptions":{"membership.invited":["slack","email","slack"],"qerds.message_sent":[]}}`))
	if err != nil {
		t.Fatalf("parseSettingsRequest: %v", err)
	}
	want := map[string][]ChannelID{"membership.invited": {ChannelEmail, ChannelSlack}}
	if !reflect.DeepEqual(in.Subscriptions, want) {
		t.Errorf("Subscriptions = %v, want %v", in.Subscriptions, want)
	}
}

func TestParseSettingsRequestAcceptsAnEmptyDocument(t *testing.T) {
	in, err := parseSettingsRequest(request(`{"subscriptions":{}}`))
	if err != nil {
		t.Fatalf("parseSettingsRequest: %v", err)
	}
	if len(in.Subscriptions) != 0 {
		t.Errorf("Subscriptions = %v, want empty (unsubscribe from everything)", in.Subscriptions)
	}
}

func TestParseSettingsRequestRejectsBadInput(t *testing.T) {
	cases := map[string]struct {
		body string
		code string
	}{
		"not json":        {`not json at all`, "invalid_body"},
		"unknown event":   {`{"subscriptions":{"nonsense.made_up":["email"]}}`, "invalid_input"},
		"unknown channel": {`{"subscriptions":{"membership.invited":["pigeon"]}}`, "invalid_input"},
		// A body that never mentions subscriptions, or misspells the key, would
		// otherwise normalize to an empty document and wipe the org's whole set.
		"no subscriptions key": {`{}`, "invalid_input"},
		"misspelled key":       {`{"subscription":{"membership.invited":["email"]}}`, "invalid_body"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := parseSettingsRequest(request(tc.body))
			var apiErr *respond.APIError
			if !errors.As(err, &apiErr) || apiErr.Code != tc.code {
				t.Fatalf("err = %v, want a %s APIError", err, tc.code)
			}
			if apiErr.Status != http.StatusBadRequest {
				t.Errorf("status = %d, want 400", apiErr.Status)
			}
		})
	}
}

// fakeStore stands in for *Store in the route round-trip below.
type fakeStore struct {
	settings Settings
	saved    *SettingsInput
}

func (s *fakeStore) GetSettings(context.Context, uuid.UUID) (Settings, error) {
	return s.settings, nil
}

func (s *fakeStore) Save(_ context.Context, _ uuid.UUID, in SettingsInput) (Settings, error) {
	s.saved = &in
	s.settings = Settings{Configured: true, Subscriptions: in.Subscriptions}
	return s.settings, nil
}

// serve runs a request through the registered routes with pass-through auth and
// an organisation plus role in context, so the wiring (pattern, admin gate,
// decode, respond) is exercised as the router will run it.
func serve(t *testing.T, store settingsStore, role string, req *http.Request) *httptest.ResponseRecorder {
	t.Helper()
	passthrough := func(next http.Handler) http.Handler { return next }
	mux := http.NewServeMux()
	NewHandler(store, passthrough, passthrough).Register(mux)

	ctx := organization.ContextWithOrg(req.Context(),
		organization.Organization{ID: uuid.New(), Slug: "acme"})
	ctx = organization.ContextWithRole(ctx, role)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req.WithContext(ctx))
	return rec
}

func TestGetSettingsServesSubscriptionsAndCatalog(t *testing.T) {
	store := &fakeStore{settings: Settings{
		Configured:    true,
		Subscriptions: map[string][]ChannelID{"membership.invited": {ChannelEmail}},
	}}

	rec := serve(t, store, organization.RoleAdmin,
		httptest.NewRequest(http.MethodGet, "/orgs/acme/notifications/settings", nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body)
	}
	var got settingsResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if !got.Configured || len(got.Subscriptions["membership.invited"]) != 1 {
		t.Errorf("body = %+v, want the stored subscriptions", got)
	}
	if len(got.Events) == 0 || len(got.Channels) == 0 {
		t.Error("the response carries no catalogue for the settings screen")
	}
}

func TestPutSettingsSavesTheNormalizedDocument(t *testing.T) {
	store := &fakeStore{}
	body := strings.NewReader(`{"subscriptions":{"membership.invited":["slack","email"]}}`)

	rec := serve(t, store, organization.RoleAdmin,
		httptest.NewRequest(http.MethodPut, "/orgs/acme/notifications/settings", body))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body)
	}
	if store.saved == nil {
		t.Fatal("the store was not asked to save")
	}
	want := []ChannelID{ChannelEmail, ChannelSlack}
	if !reflect.DeepEqual(store.saved.Subscriptions["membership.invited"], want) {
		t.Errorf("saved %v, want %v", store.saved.Subscriptions, want)
	}
}

func TestPutSettingsRejectsAnUnknownEventWithA400(t *testing.T) {
	body := strings.NewReader(`{"subscriptions":{"nonsense.made_up":["email"]}}`)

	rec := serve(t, &fakeStore{}, organization.RoleAdmin,
		httptest.NewRequest(http.MethodPut, "/orgs/acme/notifications/settings", body))

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400: %s", rec.Code, rec.Body)
	}
}

// Who gets told what is administrative configuration, so a plain member is
// turned away from both routes.
func TestSettingsRoutesAreAdminOnly(t *testing.T) {
	cases := map[string]*http.Request{
		"get": httptest.NewRequest(http.MethodGet, "/orgs/acme/notifications/settings", nil),
		"put": httptest.NewRequest(http.MethodPut, "/orgs/acme/notifications/settings",
			strings.NewReader(`{"subscriptions":{}}`)),
	}
	for name, req := range cases {
		t.Run(name, func(t *testing.T) {
			rec := serve(t, &fakeStore{}, organization.RoleMember, req)
			if rec.Code != http.StatusForbidden {
				t.Errorf("status = %d, want 403 for a non-admin member", rec.Code)
			}
		})
	}
}

func TestSettingsResponseCarriesTheCatalog(t *testing.T) {
	got := newSettingsResponse(Settings{Subscriptions: map[string][]ChannelID{}})
	if len(got.Events) != len(Catalog()) {
		t.Errorf("events = %d, want the whole catalog (%d)", len(got.Events), len(Catalog()))
	}
	if !reflect.DeepEqual(got.Channels, Channels()) {
		t.Errorf("channels = %v, want %v", got.Channels, Channels())
	}
}
