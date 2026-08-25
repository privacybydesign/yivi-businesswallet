package config

import (
	"strings"
	"testing"
)

// loadWith sets the given environment for one Load call, plus the DATABASE_URL
// every load requires.
func loadWith(t *testing.T, env map[string]string) (Config, error) {
	t.Helper()
	t.Setenv(envDatabaseURL, "postgres://user:pass@localhost:5432/db")
	for k, v := range env {
		t.Setenv(k, v)
	}
	return Load()
}

func TestLoadPostGuardURLsDefaultToProduction(t *testing.T) {
	cfg, err := loadWith(t, nil)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.PostGuardWebsiteURL != defaultPostGuardWebsiteURL {
		t.Errorf("website URL = %q, want %q", cfg.PostGuardWebsiteURL, defaultPostGuardWebsiteURL)
	}
	if cfg.PostGuardPkgURL != defaultPostGuardPkgURL {
		t.Errorf("pkg URL = %q, want %q", cfg.PostGuardPkgURL, defaultPostGuardPkgURL)
	}
	if cfg.PostGuardCryptifyURL != defaultPostGuardCryptifyURL {
		t.Errorf("cryptify URL = %q, want %q", cfg.PostGuardCryptifyURL, defaultPostGuardCryptifyURL)
	}
}

func TestLoadPostGuardURLsStaging(t *testing.T) {
	cfg, err := loadWith(t, map[string]string{
		envPostGuardPkgURL:      "https://pkg.staging.postguard.eu",
		envPostGuardCryptifyURL: "https://storage.staging.postguard.eu",
		envPostGuardWebsiteURL:  "https://staging.postguard.eu",
	})
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.PostGuardWebsiteURL != "https://staging.postguard.eu" {
		t.Errorf("website URL = %q, want the staging website", cfg.PostGuardWebsiteURL)
	}
}

// A staging upload target with the production website left in place is the
// reported failure: the recipient gets a link the production site cannot resolve.
func TestLoadRejectsMixedPostGuardEnvironments(t *testing.T) {
	cases := map[string]map[string]string{
		"staging upload, default production website": {
			envPostGuardPkgURL:      "https://pkg.staging.postguard.eu",
			envPostGuardCryptifyURL: "https://storage.staging.postguard.eu",
		},
		"staging website, default production upload": {
			envPostGuardWebsiteURL: "https://staging.postguard.eu",
		},
		"only storage switched": {
			envPostGuardCryptifyURL: "https://storage.staging.postguard.eu",
		},
	}
	for name, env := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := loadWith(t, env)
			if err == nil {
				t.Fatal("Load succeeded, want an error on mixed PostGuard environments")
			}
			if !strings.Contains(err.Error(), "different environments") {
				t.Errorf("error = %v, want it to name the mismatch", err)
			}
		})
	}
}

func TestLoadRejectsNonAbsolutePostGuardWebsiteURL(t *testing.T) {
	for _, raw := range []string{"postguard.eu", "/download", "ftp://postguard.eu", "https://"} {
		t.Run(raw, func(t *testing.T) {
			_, err := loadWith(t, map[string]string{envPostGuardWebsiteURL: raw})
			if err == nil {
				t.Fatalf("Load succeeded for %q, want an error", raw)
			}
			if !strings.Contains(err.Error(), "absolute http(s) URL") {
				t.Errorf("error = %v, want it to name the URL requirement", err)
			}
		})
	}
}

// internal/email refuses to render a link that is not an absolute http(s) URL, and
// the claim/invite links are APP_BASE_URL plus a path. A scheme-less value used to
// boot clean and then fail every credential-offer and invitation send.
func TestLoadRejectsNonAbsoluteAppBaseURL(t *testing.T) {
	for _, raw := range []string{"wallet.example.org", "/claim", "ftp://wallet.example.org", "http://"} {
		t.Run(raw, func(t *testing.T) {
			_, err := loadWith(t, map[string]string{envAppBaseURL: raw})
			if err == nil {
				t.Fatalf("Load succeeded for %s=%q, want an error", envAppBaseURL, raw)
			}
			if !strings.Contains(err.Error(), envAppBaseURL) {
				t.Errorf("error = %v, want it to name %s", err, envAppBaseURL)
			}
			if !strings.Contains(err.Error(), "absolute http(s) URL") {
				t.Errorf("error = %v, want it to name the URL requirement", err)
			}
		})
	}
}

func TestLoadAcceptsAppBaseURLDefaultAndOverride(t *testing.T) {
	cfg, err := loadWith(t, nil)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.AppBaseURL != defaultAppBaseURL {
		t.Errorf("app base URL = %q, want the default %q", cfg.AppBaseURL, defaultAppBaseURL)
	}

	cfg, err = loadWith(t, map[string]string{envAppBaseURL: "https://wallet.example.org/"})
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.AppBaseURL != "https://wallet.example.org/" {
		t.Errorf("app base URL = %q, want the value as configured", cfg.AppBaseURL)
	}
}

// SIGNING_REDIRECT_URI is concatenated into the QTSP authorize URL and the token
// exchange's redirect_uri, so a scheme-less, relative, host-less or spaced value
// must fail at boot rather than deep in the ceremony. Empty is allowed (it keeps
// the localhost default), so it is not in this set.
func TestLoadRejectsNonAbsoluteSigningRedirectURI(t *testing.T) {
	for _, raw := range []string{
		"wallet.staging.example.org/api/v1/signing/callback",
		" https://wallet.staging.example.org/api/v1/signing/callback",
		"/api/v1/signing/callback",
		"ftp://wallet.staging.example.org/callback",
		"https://",
	} {
		t.Run(raw, func(t *testing.T) {
			_, err := loadWith(t, map[string]string{envSigningRedirectURI: raw})
			if err == nil {
				t.Fatalf("Load succeeded for %s=%q, want an error", envSigningRedirectURI, raw)
			}
			// The error names the variable on both rejection paths — the
			// scheme/host check ("must be an absolute http(s) URL") and a raw
			// url.Parse failure (e.g. the leading-space case) — so assert on that.
			if !strings.Contains(err.Error(), envSigningRedirectURI) {
				t.Errorf("error = %v, want it to name %s", err, envSigningRedirectURI)
			}
		})
	}
}

// Unset leaves it empty (the caller applies the localhost default); a valid
// absolute URL is taken as configured.
func TestLoadAcceptsSigningRedirectURIEmptyOrAbsolute(t *testing.T) {
	cfg, err := loadWith(t, nil)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.SigningRedirectURI != "" {
		t.Errorf("signing redirect URI = %q, want empty when unset", cfg.SigningRedirectURI)
	}

	const want = "https://business-wallet.staging.yivi.app/api/v1/signing/callback"
	cfg, err = loadWith(t, map[string]string{envSigningRedirectURI: want})
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.SigningRedirectURI != want {
		t.Errorf("signing redirect URI = %q, want %q", cfg.SigningRedirectURI, want)
	}
}

func TestLoadAcceptsPostGuardURLsWithTrailingSlashAndPort(t *testing.T) {
	cfg, err := loadWith(t, map[string]string{
		envPostGuardPkgURL:      "http://localhost:8081",
		envPostGuardCryptifyURL: "http://localhost:8082",
		envPostGuardWebsiteURL:  "http://localhost:5173/",
	})
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.PostGuardWebsiteURL != "http://localhost:5173/" {
		t.Errorf("website URL = %q, want the value as configured", cfg.PostGuardWebsiteURL)
	}
}

func TestLoadDefaultsIntendedUseIDOnTheDefaultVerifier(t *testing.T) {
	cfg, err := loadWith(t, nil)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.EudiIntendedUseID != defaultEudiIntendedUseID {
		t.Errorf("intended use id = %q, want %q", cfg.EudiIntendedUseID, defaultEudiIntendedUseID)
	}
	if cfg.EudiRegistrationCertificate != "" {
		t.Errorf("registration certificate = %q, want empty", cfg.EudiRegistrationCertificate)
	}
}

// An intent id means nothing on a verifier that never configured it.
func TestLoadLeavesIntendedUseIDEmptyOnAnotherVerifier(t *testing.T) {
	cfg, err := loadWith(t, map[string]string{envEudiVerifierURL: "https://verifier.example.org"})
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.EudiIntendedUseID != "" {
		t.Errorf("intended use id = %q, want empty against a non-default verifier", cfg.EudiIntendedUseID)
	}
}

func TestLoadKeepsConfiguredIntendedUseID(t *testing.T) {
	cfg, err := loadWith(t, map[string]string{envEudiIntendedUseID: "yivi-business-wallet"})
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.EudiIntendedUseID != "yivi-business-wallet" {
		t.Errorf("intended use id = %q, want the configured value", cfg.EudiIntendedUseID)
	}
}

func TestLoadKeepsConfiguredRegistrationCertificate(t *testing.T) {
	cfg, err := loadWith(t, map[string]string{envEudiRegistrationCertificate: "header.payload.signature"})
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.EudiRegistrationCertificate != "header.payload.signature" {
		t.Errorf("registration certificate = %q, want the configured value", cfg.EudiRegistrationCertificate)
	}
	if cfg.EudiIntendedUseID != "" {
		t.Errorf("intended use id = %q, want empty when a certificate is configured", cfg.EudiIntendedUseID)
	}
}

func TestLoadRejectsBothEudiCredentials(t *testing.T) {
	_, err := loadWith(t, map[string]string{
		envEudiIntendedUseID:           "1",
		envEudiRegistrationCertificate: "header.payload.signature",
	})
	if err == nil {
		t.Fatal("Load accepted both an intended use id and a registration certificate")
	}
	if !strings.Contains(err.Error(), envEudiIntendedUseID) || !strings.Contains(err.Error(), envEudiRegistrationCertificate) {
		t.Errorf("error %q does not name both variables", err)
	}
}
