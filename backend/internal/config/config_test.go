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
