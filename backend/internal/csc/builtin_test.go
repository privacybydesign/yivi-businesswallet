package csc

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/privacybydesign/yivi-businesswallet/backend/internal/organization"
)

var testBuiltIn = &BuiltIn{
	BaseURL:  "https://wallet.example.org/api/v1/yivi-csc",
	ClientID: "yivi-business-wallet", ClientSecret: "secret",
	Info: Info{Name: "Yivi Business Wallet signing service", Specs: "2.0.0.2"},
}

func TestNormalizeDropsWhatTheBuiltInServiceDoesNotTake(t *testing.T) {
	secret := "left-over"
	out, err := Normalize(SettingsInput{
		Enabled: true, ProviderKind: ProviderKindYivi,
		// Left in the form from another kind; not even a valid URL.
		BaseURL: "not a url", ClientID: "old-client", ClientSecret: &secret,
	})
	if err != nil {
		t.Fatalf("Normalize: %v", err)
	}
	if out.BaseURL != "" || out.ClientID != "" || !out.Enabled {
		t.Errorf("normalized = %+v, want only the kind and enabled kept", out)
	}
	if out.ClientSecret == nil || *out.ClientSecret != "" {
		t.Error("choosing the built-in service must clear a stored client secret")
	}
}

func TestProviderKindsForListsTheBuiltInServiceFirstWhereItRuns(t *testing.T) {
	kinds := ProviderKindsFor(testBuiltIn)
	if len(kinds) != len(ProviderKinds())+1 || kinds[0].ID != ProviderKindYivi || kinds[0].DefaultBaseURL != testBuiltIn.BaseURL {
		t.Errorf("kinds = %+v, want the built-in service first with its own base URL", kinds)
	}
	for _, k := range ProviderKindsFor(nil) {
		if k.ID == ProviderKindYivi {
			t.Error("a deployment that does not run the built-in service must not offer it")
		}
	}
	if !KnownProviderKind(ProviderKindYivi) {
		t.Error("the built-in kind must be a known kind")
	}
}

// fakeSettingsStore answers with one fixed configuration.
type fakeSettingsStore struct {
	settings Settings
	upserted *SettingsInput
	err      error
}

func (f *fakeSettingsStore) GetSettings(context.Context, uuid.UUID) (Settings, error) {
	return f.settings, nil
}

func (f *fakeSettingsStore) Upsert(_ context.Context, _ uuid.UUID, in SettingsInput) (Settings, error) {
	f.upserted = &in
	return f.settings, f.err
}

func (f *fakeSettingsStore) BaseURLFor(context.Context, uuid.UUID) (string, error) {
	if f.settings.BaseURL == "" {
		return "", ErrNotConfigured
	}
	return f.settings.BaseURL, nil
}

// unreachable fails the test when a connection test goes over the network.
type unreachable struct{ t *testing.T }

func (u unreachable) Info(context.Context, string) (Info, error) {
	u.t.Fatal("the built-in service must be answered in process, not probed over HTTP")
	return Info{}, nil
}

func asOrgAdmin(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx := organization.ContextWithRole(
			organization.ContextWithOrg(r.Context(), organization.Organization{ID: uuid.New(), Slug: "acme"}), organization.RoleAdmin)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

func passThrough(next http.Handler) http.Handler { return next }

func TestConnectionTestOfTheBuiltInServiceStaysInProcess(t *testing.T) {
	store := &fakeSettingsStore{settings: Settings{Configured: true, Enabled: true, ProviderKind: ProviderKindYivi, BaseURL: testBuiltIn.BaseURL}}
	mux := http.NewServeMux()
	NewHandler(store, unreachable{t}, testBuiltIn, passThrough, asOrgAdmin).Register(mux)

	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/orgs/acme/csc/test", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body)
	}
	var info Info
	if err := json.Unmarshal(rec.Body.Bytes(), &info); err != nil || info != testBuiltIn.Info {
		t.Fatalf("info = %+v, %v; want the built-in service's own", info, err)
	}
}

func TestSettingsOfferTheBuiltInServiceAndSayWhenItIsNotThere(t *testing.T) {
	store := &fakeSettingsStore{settings: Settings{Configured: true, Enabled: true, ProviderKind: ProviderKindYivi, BaseURL: testBuiltIn.BaseURL}}
	mux := http.NewServeMux()
	NewHandler(store, unreachable{t}, testBuiltIn, passThrough, asOrgAdmin).Register(mux)

	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/orgs/acme/csc/settings", nil))
	var got settingsResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if rec.Code != http.StatusOK || got.ProviderKind != ProviderKindYivi || !got.Enabled || got.ProviderKinds[0].ID != ProviderKindYivi {
		t.Fatalf("settings = %d %+v, want the built-in service, enabled and offered first", rec.Code, got)
	}

	store.err = ErrBuiltInUnavailable
	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodPut, "/orgs/acme/csc/settings",
		strings.NewReader(`{"enabled":true,"providerKind":"yivi","baseUrl":"","clientId":""}`)))
	if rec.Code != http.StatusConflict || !strings.Contains(rec.Body.String(), "built_in_unavailable") {
		t.Fatalf("saving the built-in kind where it does not run = %d %s, want 409 built_in_unavailable", rec.Code, rec.Body)
	}
	if store.upserted == nil || store.upserted.ProviderKind != ProviderKindYivi {
		t.Fatalf("upserted = %+v, want the built-in kind", store.upserted)
	}
}
