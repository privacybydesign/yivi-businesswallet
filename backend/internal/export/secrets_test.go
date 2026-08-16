package export

import (
	"archive/zip"
	"bytes"
	"context"
	"io"
	"testing"
)

// secretLiterals are the values export.md §7 excludes.
var secretLiterals = map[string]string{
	"invitation token": "inv_tok_0f9c2b7a4e1d5c83",
	"claim token":      "clm_tok_7a1e93b2d4f60c85",
	"issuance id":      "issuance_2f8b41c6e07d",
	"offer uri":        "openid-credential-offer://?credential_offer_uri=https%3A%2F%2Fissuer%2Foffer%2Fs3cr3t",
	"tx code":          "tx_9184",
	"smtp password":    "smtp_pw_Zx91QeR7ubn",
	"session token":    "ybw_session_5c2ad9f10b3e",
	"holder key":       "-----BEGIN EC PRIVATE KEY-----MHcCAQEEIB",
	"wsca secret":      "wsca_activation_4b7e02fd",
}

func bundleBytes(t *testing.T, sections []string) []byte {
	t.Helper()

	svc := NewService(&fakeRecorder{}, allWriters())
	fixedClock(svc)
	archive, err := svc.Export(context.Background(), testOrg(), sections)
	if err != nil {
		t.Fatalf("Export() = %v, want nil", err)
	}
	defer func() { _ = archive.Close() }()

	raw, err := io.ReadAll(archive.Reader())
	if err != nil {
		t.Fatalf("reading bundle: %v", err)
	}

	var all bytes.Buffer
	all.Write(raw)

	zr, err := zip.NewReader(bytes.NewReader(raw), int64(len(raw)))
	if err != nil {
		t.Fatalf("opening bundle as zip: %v", err)
	}
	for _, f := range zr.File {
		all.WriteString(f.Name)
		rc, err := f.Open()
		if err != nil {
			t.Fatalf("opening %s: %v", f.Name, err)
		}
		if _, err := io.Copy(&all, rc); err != nil {
			_ = rc.Close()
			t.Fatalf("reading %s: %v", f.Name, err)
		}
		_ = rc.Close()
	}
	return all.Bytes()
}

// The scan is byte-level rather than field-by-field because the leak it exists
// to catch rides inside a nested envelope — an audit event's {before, after}
// metadata, a JSONB column — that no struct comparison would look at.
func TestBundleCarriesNoSecrets(t *testing.T) {
	bundle := bundleBytes(t, nil)

	for label, secret := range secretLiterals {
		if bytes.Contains(bundle, []byte(secret)) {
			t.Errorf("the bundle contains the %s (%q)", label, secret)
		}
	}
}

func TestSecretScanReadsTheBundle(t *testing.T) {
	bundle := bundleBytes(t, nil)

	present := map[string]string{
		"organization name": testOrg().Name,
		"kvk number":        testOrg().KVKNumber,
		"schema version":    SchemaVersion,
		"section key":       SectionAuditRecords,
	}
	for label, value := range present {
		if !bytes.Contains(bundle, []byte(value)) {
			t.Errorf("the scan did not find the %s (%q), so it is not reading the bundle", label, value)
		}
	}
}

func TestFilteredBundleCarriesNoSecrets(t *testing.T) {
	for _, key := range SectionOrder {
		t.Run(key, func(t *testing.T) {
			bundle := bundleBytes(t, []string{key})
			for label, secret := range secretLiterals {
				if bytes.Contains(bundle, []byte(secret)) {
					t.Errorf("the %s bundle contains the %s", key, label)
				}
			}
		})
	}
}
