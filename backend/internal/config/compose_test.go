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

// TestComposePassesSlackEncryptionKeyToBackend holds the Slack key to the same
// passthrough rule. Without the declaration the key set in .env never reaches the
// container, so every attempt to save a webhook URL is refused as if the
// deployment had no key at all.
func TestComposePassesSlackEncryptionKeyToBackend(t *testing.T) {
	backendEnvironment := backendComposeEnvironment(t)

	value, ok := backendEnvironment[envSlackEncryptionKey]
	if !ok {
		t.Fatalf("backend service does not pass %s through; setting it in .env would do nothing", envSlackEncryptionKey)
	}
	if want := "${" + envSlackEncryptionKey + ":-}"; value != want {
		t.Errorf("backend %s = %q, want %q", envSlackEncryptionKey, value, want)
	}
}

// TestComposePassesTeamsEncryptionKeyToBackend holds the Microsoft Teams key to the
// same passthrough rule, for the same reason as the Slack one: without the
// declaration the key set in .env never reaches the container, so every attempt to
// save a Teams webhook URL is refused as if the deployment had no key at all.
func TestComposePassesTeamsEncryptionKeyToBackend(t *testing.T) {
	backendEnvironment := backendComposeEnvironment(t)

	value, ok := backendEnvironment[envTeamsEncryptionKey]
	if !ok {
		t.Fatalf("backend service does not pass %s through; setting it in .env would do nothing", envTeamsEncryptionKey)
	}
	if want := "${" + envTeamsEncryptionKey + ":-}"; value != want {
		t.Errorf("backend %s = %q, want %q", envTeamsEncryptionKey, value, want)
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

// TestComposePassesProvisioningEncryptionKeyToBackend holds the directory
// provisioning key to the same passthrough rule. Without the declaration the
// documented .env setting is silently ignored, and every attempt to save a
// directory client secret is refused with "no encryption key configured".
func TestComposePassesProvisioningEncryptionKeyToBackend(t *testing.T) {
	backendEnvironment := backendComposeEnvironment(t)

	value, ok := backendEnvironment[envProvisioningEncryptionKey]
	if !ok {
		t.Fatalf("backend service does not pass %s through; setting it in .env would do nothing", envProvisioningEncryptionKey)
	}
	if want := "${" + envProvisioningEncryptionKey + ":-}"; value != want {
		t.Errorf("backend %s = %q, want %q", envProvisioningEncryptionKey, value, want)
	}
}

// TestComposePassesCSCEncryptionKeyToBackend holds the CSC signing-provider key to
// the same passthrough rule. Without the declaration the key set in .env never
// reaches the container, so every attempt to save a CSC client secret is refused
// as if the deployment had no key at all.
func TestComposePassesCSCEncryptionKeyToBackend(t *testing.T) {
	backendEnvironment := backendComposeEnvironment(t)

	value, ok := backendEnvironment[envCSCEncryptionKey]
	if !ok {
		t.Fatalf("backend service does not pass %s through; setting it in .env would do nothing", envCSCEncryptionKey)
	}
	if want := "${" + envCSCEncryptionKey + ":-}"; value != want {
		t.Errorf("backend %s = %q, want %q", envCSCEncryptionKey, value, want)
	}
}

// TestComposePassesSigningRedirectURIToBackend holds the signing redirect URI to
// the same passthrough rule. It is hosted-deploy config, the same class as
// APP_BASE_URL: without the declaration a value set in .env never reaches the
// container, so the signing ceremony silently keeps the localhost default and the
// redirect_uri never matches what the QTSP registered — the exact failure this
// variable exists to fix. (Unlike SIGNING_OAUTH_ISSUER_INTERNAL, which is dev-only
// and so lives only in compose.override.yaml.)
func TestComposePassesSigningRedirectURIToBackend(t *testing.T) {
	backendEnvironment := backendComposeEnvironment(t)

	value, ok := backendEnvironment[envSigningRedirectURI]
	if !ok {
		t.Fatalf("backend service does not pass %s through; setting it in .env would do nothing", envSigningRedirectURI)
	}
	if want := "${" + envSigningRedirectURI + ":-}"; value != want {
		t.Errorf("backend %s = %q, want %q", envSigningRedirectURI, value, want)
	}
}

// Same passthrough rule: a certificate set in .env that never reaches the
// container fails the boot probe while looking configured.
func TestComposePassesEudiVerifierConfigToBackend(t *testing.T) {
	backendEnvironment := backendComposeEnvironment(t)

	for _, key := range []string{envEudiVerifierURL, envEudiIssuerChain, envEudiIntendedUseID, envEudiRegistrationCertificate} {
		value, ok := backendEnvironment[key]
		if !ok {
			t.Errorf("backend service does not pass %s through; setting it in .env would do nothing", key)
			continue
		}
		if want := "${" + key + ":-}"; value != want {
			t.Errorf("backend %s = %q, want %q", key, value, want)
		}
	}
}

// TestComposePassesExportBundleCapToBackend holds the export cap to the same
// passthrough rule. Without the declaration a deployment that raised the cap in
// .env would keep the built-in one and quietly record omissions it did not ask
// for.
func TestComposePassesExportBundleCapToBackend(t *testing.T) {
	backendEnvironment := backendComposeEnvironment(t)

	value, ok := backendEnvironment[envExportMaxBundleBytes]
	if !ok {
		t.Fatalf("backend service does not pass %s through; setting it in .env would do nothing", envExportMaxBundleBytes)
	}
	if want := "${" + envExportMaxBundleBytes + ":-}"; value != want {
		t.Errorf("backend %s = %q, want %q", envExportMaxBundleBytes, value, want)
	}
}
