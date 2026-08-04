package email

import (
	"net/http"
	"testing"

	"github.com/privacybydesign/yivi-businesswallet/backend/internal/mailer"
)

const settingsPath = "/orgs/acme/email/settings"

// validPlain is a complete password configuration, the body every case here
// varies one field of.
func validPlain() map[string]any {
	return map[string]any{
		"host":        "mail.example.org",
		"port":        587,
		"username":    "acme",
		"fromAddress": "no-reply@acme.example",
		"enabled":     true,
	}
}

// validXOAuth2 is a complete Microsoft 365 configuration.
func validXOAuth2() map[string]any {
	return map[string]any{
		"host":          "smtp.office365.com",
		"port":          587,
		"authMechanism": string(mailer.AuthXOAuth2),
		"tenantId":      "tenant-1",
		"clientId":      "client-1",
		"clientSecret":  "s3cret",
		"fromAddress":   "no-reply@acme.example",
		"enabled":       true,
	}
}

func putSettings(t *testing.T, store *stubStore, body map[string]any) int {
	t.Helper()
	return serve(t, store, &stubMailService{}, http.MethodPut, settingsPath, body).Code
}

// A body written before XOAUTH2 existed carries no mechanism, and that has to go
// on saving a password configuration rather than failing validation.
func TestPutSettingsDefaultsToPasswordAuth(t *testing.T) {
	store := newStubStore()

	if code := putSettings(t, store, validPlain()); code != http.StatusOK {
		t.Fatalf("status = %d, want 200", code)
	}
	if store.upserted == nil {
		t.Fatal("nothing reached the store")
	}
	if store.upserted.AuthMechanism != mailer.AuthPlain {
		t.Errorf("AuthMechanism = %q, want %q", store.upserted.AuthMechanism, mailer.AuthPlain)
	}
}

func TestPutSettingsAcceptsAnXOAuth2Configuration(t *testing.T) {
	store := newStubStore()

	if code := putSettings(t, store, validXOAuth2()); code != http.StatusOK {
		t.Fatalf("status = %d, want 200", code)
	}
	got := store.upserted
	if got == nil {
		t.Fatal("nothing reached the store")
	}
	if got.AuthMechanism != mailer.AuthXOAuth2 {
		t.Errorf("AuthMechanism = %q, want %q", got.AuthMechanism, mailer.AuthXOAuth2)
	}
	if got.TenantID != "tenant-1" || got.ClientID != "client-1" {
		t.Errorf("app registration = %q/%q, want tenant-1/client-1", got.TenantID, got.ClientID)
	}
	if got.ClientSecret == nil || *got.ClientSecret != "s3cret" {
		t.Errorf("ClientSecret = %v, want the submitted secret", got.ClientSecret)
	}
}

// A mechanism the transport cannot speak is a 400, not a row that fails at the
// first send — and the column's own CHECK constraint would answer with a 500.
func TestPutSettingsRejectsAnUnknownMechanism(t *testing.T) {
	store := newStubStore()
	body := validPlain()
	body["authMechanism"] = "cram-md5"

	if code := putSettings(t, store, body); code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", code)
	}
	if store.upserted != nil {
		t.Error("an unknown mechanism reached the store")
	}
}

// An enabled XOAUTH2 configuration with no credential only fails at the first
// send, which is a mail nobody receives. Catch it where the fields are.
func TestPutSettingsRequiresTheAppRegistrationWhenEnabled(t *testing.T) {
	for name, drop := range map[string]string{
		"no tenant": "tenantId",
		"no client": "clientId",
		"no secret": "clientSecret",
	} {
		t.Run(name, func(t *testing.T) {
			store := newStubStore()
			body := validXOAuth2()
			delete(body, drop)

			if code := putSettings(t, store, body); code != http.StatusBadRequest {
				t.Fatalf("status = %d, want 400", code)
			}
			if store.upserted != nil {
				t.Error("an incomplete configuration reached the store")
			}
		})
	}
}

// A stored secret is a credential: omitting the field means "keep it", so a save
// that only changes the host must not be refused for not repeating it.
func TestPutSettingsKeepsAStoredClientSecret(t *testing.T) {
	store := newStubStore()
	store.settings = Settings{Configured: true, HasClientSecret: true}
	body := validXOAuth2()
	delete(body, "clientSecret")

	if code := putSettings(t, store, body); code != http.StatusOK {
		t.Fatalf("status = %d, want 200", code)
	}
	if store.upserted == nil {
		t.Fatal("nothing reached the store")
	}
	if store.upserted.ClientSecret != nil {
		t.Errorf("ClientSecret = %v, want nil so the stored one is kept", *store.upserted.ClientSecret)
	}
}

// An org part-way through setting up Microsoft 365 saves its work switched off,
// the same latitude the directory-sync screen gives.
func TestPutSettingsAllowsAnIncompleteDisabledConfiguration(t *testing.T) {
	store := newStubStore()
	body := validXOAuth2()
	delete(body, "clientSecret")
	delete(body, "tenantId")
	body["enabled"] = false

	if code := putSettings(t, store, body); code != http.StatusOK {
		t.Fatalf("status = %d, want 200", code)
	}
}

// The checks that predate XOAUTH2 still apply to both mechanisms.
func TestPutSettingsStillRequiresHostPortAndFromAddress(t *testing.T) {
	tests := map[string]func(map[string]any){
		"no host":           func(b map[string]any) { b["host"] = "  " },
		"port out of range": func(b map[string]any) { b["port"] = 0 },
		"no from address":   func(b map[string]any) { b["fromAddress"] = "" },
	}
	for name, mangle := range tests {
		t.Run(name, func(t *testing.T) {
			store := newStubStore()
			body := validXOAuth2()
			mangle(body)

			if code := putSettings(t, store, body); code != http.StatusBadRequest {
				t.Fatalf("status = %d, want 400", code)
			}
		})
	}
}
