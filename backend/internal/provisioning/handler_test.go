package provisioning

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
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/provisioner"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/respond"
)

func putRequest(body string) *http.Request {
	return httptest.NewRequest(http.MethodPut, "/orgs/acme/provisioning/settings", strings.NewReader(body))
}

func TestParseSettingsRequestNormalizes(t *testing.T) {
	in, err := parseSettingsRequest(putRequest(`{
		"enabled": true,
		"source": " entra ",
		"tenantId": "  tenant  ",
		"clientId": "client",
		"clientSecret": " s3cret ",
		"groupId": " staff ",
		"adminGroupIds": ["admins", "  ", "admins", "owners"]
	}`))
	if err != nil {
		t.Fatalf("parseSettingsRequest: %v", err)
	}
	if in.Source != provisioner.SourceEntra || in.TenantID != "tenant" || in.GroupID != "staff" {
		t.Errorf("input = %+v, want the trimmed values", in)
	}
	if in.ClientSecret == nil || *in.ClientSecret != "s3cret" {
		t.Errorf("clientSecret = %v, want the trimmed secret", in.ClientSecret)
	}
	// Duplicates and blanks would otherwise make the audit diff of the next save
	// read as a change nobody made.
	if want := []string{"admins", "owners"}; !reflect.DeepEqual(in.AdminGroupIDs, want) {
		t.Errorf("adminGroupIds = %v, want %v", in.AdminGroupIDs, want)
	}
}

func TestParseSettingsRequestKeepsTheStoredSecretWhenOmitted(t *testing.T) {
	in, err := parseSettingsRequest(putRequest(
		`{"source":"entra","tenantId":"t","clientId":"c","adminGroupIds":[]}`))
	if err != nil {
		t.Fatalf("parseSettingsRequest: %v", err)
	}
	if in.ClientSecret != nil {
		t.Errorf("clientSecret = %v, want nil so the stored secret survives a save", in.ClientSecret)
	}
}

func TestParseSettingsRequestRejectsBadInput(t *testing.T) {
	cases := map[string]struct {
		body string
		code string
	}{
		"not json":       {`not json at all`, "invalid_body"},
		"unknown source": {`{"source":"okta","adminGroupIds":[]}`, "invalid_input"},
		"no source":      {`{"adminGroupIds":[]}`, "invalid_input"},
		// A body that never mentions adminGroupIds would otherwise normalize to
		// "nobody is an admin", so a typo would silently demote every admin.
		"no adminGroupIds": {`{"source":"entra"}`, "invalid_input"},
		"misspelled key":   {`{"source":"entra","adminGroups":[]}`, "invalid_body"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := parseSettingsRequest(putRequest(tc.body))
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

// fakeConfigStore stands in for *Store in the route round-trip below.
type fakeConfigStore struct {
	settings Settings
	saved    *SettingsInput
}

func (s *fakeConfigStore) GetSettings(context.Context, uuid.UUID) (Settings, error) {
	return s.settings, nil
}

func (s *fakeConfigStore) Save(_ context.Context, _ uuid.UUID, in SettingsInput) (Settings, error) {
	s.saved = &in
	s.settings = Settings{
		Configured: true, Enabled: in.Enabled, Source: in.Source,
		TenantID: in.TenantID, ClientID: in.ClientID, GroupID: in.GroupID,
		AdminGroupIDs: in.AdminGroupIDs, HasClientSecret: in.ClientSecret != nil,
	}
	return s.settings, nil
}

type fakeSyncer struct {
	result Result
	err    error
	calls  int
}

func (f *fakeSyncer) Sync(context.Context, uuid.UUID) (Result, error) {
	f.calls++
	return f.result, f.err
}

// serve runs a request through the registered routes with pass-through auth and
// an organisation plus role in context, so the wiring (pattern, admin gate,
// decode, respond) is exercised as the router will run it.
func serve(t *testing.T, store configStore, sync syncer, role string, req *http.Request) *httptest.ResponseRecorder {
	t.Helper()
	passthrough := func(next http.Handler) http.Handler { return next }
	mux := http.NewServeMux()
	NewHandler(store, sync, passthrough, passthrough).Register(mux)

	ctx := organization.ContextWithOrg(req.Context(),
		organization.Organization{ID: uuid.New(), Slug: "acme"})
	ctx = organization.ContextWithRole(ctx, role)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req.WithContext(ctx))
	return rec
}

func TestGetSettingsNeverServesTheClientSecret(t *testing.T) {
	store := &fakeConfigStore{settings: Settings{
		Configured: true, Enabled: true, Source: provisioner.SourceEntra,
		TenantID: "tenant", ClientID: "client", HasClientSecret: true,
		AdminGroupIDs: []string{"admins"},
	}}

	rec := serve(t, store, &fakeSyncer{}, organization.RoleAdmin,
		httptest.NewRequest(http.MethodGet, "/orgs/acme/provisioning/settings", nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body)
	}
	// The whole point of HasClientSecret is that the secret itself never leaves
	// the database, so assert on the wire bytes rather than the decoded struct.
	if strings.Contains(rec.Body.String(), "clientSecret") {
		t.Errorf("body mentions clientSecret: %s", rec.Body)
	}
	var got settingsResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if !got.HasClientSecret || len(got.Sources) == 0 {
		t.Errorf("body = %+v, want the secret flag and the source list", got)
	}
}

func TestPutSettingsSavesTheNormalizedInput(t *testing.T) {
	store := &fakeConfigStore{}

	rec := serve(t, store, &fakeSyncer{}, organization.RoleAdmin, putRequest(
		`{"enabled":true,"source":"entra","tenantId":"t","clientId":"c","adminGroupIds":["a","a"]}`))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body)
	}
	if store.saved == nil {
		t.Fatal("the store was not asked to save")
	}
	if want := []string{"a"}; !reflect.DeepEqual(store.saved.AdminGroupIDs, want) {
		t.Errorf("saved adminGroupIds = %v, want %v", store.saved.AdminGroupIDs, want)
	}
}

func TestPostSyncReportsWhatTheRunDid(t *testing.T) {
	sync := &fakeSyncer{result: Result{
		MembersInvited: 2,
		Skipped:        []Skip{{Email: "ada@example.org", Reason: SkipConflict}},
	}}

	rec := serve(t, &fakeConfigStore{}, sync, organization.RoleAdmin,
		httptest.NewRequest(http.MethodPost, "/orgs/acme/provisioning/sync", nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body)
	}
	var got Result
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if got.MembersInvited != 2 || len(got.Skipped) != 1 {
		t.Errorf("body = %+v, want the run's counts and skips", got)
	}
}

func TestPostSyncMapsFailures(t *testing.T) {
	cases := map[string]struct {
		err    error
		status int
		code   string
	}{
		"not configured":  {ErrNotConfigured, http.StatusConflict, "not_configured"},
		"disabled":        {ErrDisabled, http.StatusConflict, "disabled"},
		"unknown source":  {ErrUnknownSource, http.StatusConflict, "unknown_source"},
		"empty directory": {ErrEmptyDirectory, http.StatusBadGateway, "empty_directory"},
		"incomplete":      {provisioner.ErrIncompleteConfig, http.StatusConflict, "incomplete_config"},
		"source down":     {errors.New("status 503"), http.StatusBadGateway, "sync_failed"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			rec := serve(t, &fakeConfigStore{}, &fakeSyncer{err: tc.err}, organization.RoleAdmin,
				httptest.NewRequest(http.MethodPost, "/orgs/acme/provisioning/sync", nil))

			if rec.Code != tc.status {
				t.Fatalf("status = %d, want %d: %s", rec.Code, tc.status, rec.Body)
			}
			var body struct {
				Code string `json:"code"`
			}
			if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
				t.Fatalf("decode error body: %v", err)
			}
			if body.Code != tc.code {
				t.Errorf("code = %q, want %q", body.Code, tc.code)
			}
		})
	}
}

// The configuration holds a directory credential and the sync can add and remove
// members, so a plain member is turned away from every route.
func TestProvisioningRoutesAreAdminOnly(t *testing.T) {
	cases := map[string]*http.Request{
		"get":  httptest.NewRequest(http.MethodGet, "/orgs/acme/provisioning/settings", nil),
		"put":  putRequest(`{"source":"entra","adminGroupIds":[]}`),
		"sync": httptest.NewRequest(http.MethodPost, "/orgs/acme/provisioning/sync", nil),
	}
	for name, req := range cases {
		t.Run(name, func(t *testing.T) {
			sync := &fakeSyncer{}
			rec := serve(t, &fakeConfigStore{}, sync, organization.RoleMember, req)
			if rec.Code != http.StatusForbidden {
				t.Errorf("status = %d, want 403 for a non-admin member", rec.Code)
			}
			if sync.calls != 0 {
				t.Error("a non-admin member triggered a directory sync")
			}
		})
	}
}

func TestSourceErrorMessageDoesNotEchoTheDriversText(t *testing.T) {
	// The driver's own message is stored on the settings row for the admin to
	// read; repeating it in the HTTP response would hand whatever a third party
	// wrote back to the browser.
	rec := serve(t, &fakeConfigStore{}, &fakeSyncer{err: errors.New("AADSTS7000215: secret hunter2")},
		organization.RoleAdmin, httptest.NewRequest(http.MethodPost, "/orgs/acme/provisioning/sync", nil))

	if strings.Contains(rec.Body.String(), "hunter2") {
		t.Errorf("body = %s, must not repeat the driver's message", rec.Body)
	}
}
