package config

import (
	"os"
	"testing"

	"gopkg.in/yaml.v3"
)

// composePath is the root Compose file, from this package's directory.
const composePath = "../../../compose.yaml"

// backendComposeEnvironment returns the environment block compose.yaml declares
// on the backend service.
func backendComposeEnvironment(t *testing.T) map[string]string {
	t.Helper()

	raw, err := os.ReadFile(composePath)
	if err != nil {
		t.Fatalf("read %s: %v", composePath, err)
	}
	var file struct {
		Services map[string]struct {
			Environment map[string]string `yaml:"environment"`
		} `yaml:"services"`
	}
	if err := yaml.Unmarshal(raw, &file); err != nil {
		t.Fatalf("parse %s: %v", composePath, err)
	}
	backend, ok := file.Services["backend"]
	if !ok {
		t.Fatal("compose.yaml has no backend service")
	}
	return backend.Environment
}

// TestComposePassesPostGuardURLsToBackend guards the passthrough itself. Compose
// does not forward a root .env variable into a container unless the service
// declares it, so a variable this package reads but compose.yaml omits is set in
// .env, looks configured, and silently keeps the built-in default.
func TestComposePassesPostGuardURLsToBackend(t *testing.T) {
	backendEnvironment := backendComposeEnvironment(t)

	for _, key := range []string{envPostGuardWebsiteURL, envPostGuardPkgURL, envPostGuardCryptifyURL} {
		value, ok := backendEnvironment[key]
		if !ok {
			t.Errorf("backend service does not pass %s through; setting it in .env would do nothing", key)
			continue
		}
		// The empty default keeps the Go-side default authoritative.
		if want := "${" + key + ":-}"; value != want {
			t.Errorf("backend %s = %q, want %q", key, value, want)
		}
	}
}

// TestComposePassesMailDefaultLocaleToBackend holds the mail fallback locale to
// the same passthrough rule. Without the declaration the documented .env setting
// is silently ignored and every unlocalised send falls back to English.
func TestComposePassesMailDefaultLocaleToBackend(t *testing.T) {
	backendEnvironment := backendComposeEnvironment(t)

	value, ok := backendEnvironment[envMailDefaultLocale]
	if !ok {
		t.Fatalf("backend service does not pass %s through; setting it in .env would do nothing", envMailDefaultLocale)
	}
	if want := "${" + envMailDefaultLocale + ":-}"; value != want {
		t.Errorf("backend %s = %q, want %q", envMailDefaultLocale, value, want)
	}
}
