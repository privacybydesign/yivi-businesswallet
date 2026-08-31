package export

import (
	"archive/zip"
	"bytes"
	"context"
	"io"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/privacybydesign/yivi-businesswallet/backend/internal/attestation"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/audit"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/organization"
)

// secretLiterals are the values export.md §7 excludes.
var secretLiterals = map[string]string{
	"invitation token": "inv_tok_0f9c2b7a4e1d5c83",
	"claim token":      "clm_tok_7a1e93b2d4f60c85",
	"issuance id":      "issuance_2f8b41c6e07d",
	"credential uuid":  "cred_9931ab4f7c02",
	"offer uri":        "openid-credential-offer://?credential_offer_uri=https%3A%2F%2Fissuer%2Foffer%2Fs3cr3t",
	"tx code":          "tx_9184",
	"smtp password":    "smtp_pw_Zx91QeR7ubn",
	"session token":    "ybw_session_5c2ad9f10b3e",
	"holder key":       "-----BEGIN EC PRIVATE KEY-----MHcCAQEEIB",
	"wsca secret":      "wsca_activation_4b7e02fd",
}

// seededWriters runs the real writers over stores holding both the org's data
// and, in the fields that carry them, the secrets above. A writer that copied a
// store struct wholesale instead of naming fields would ship them.
func seededWriters() []SectionWriter {
	invitationID := uuid.New()
	now := time.Date(2026, 4, 4, 4, 4, 4, 0, time.UTC)

	orgs := &fakeOrgs{
		org: organization.Organization{
			ID: testOrg().ID, Name: testOrg().Name, Slug: testOrg().Slug,
			KVKNumber: testOrg().KVKNumber, EUID: testOrg().EUID,
			DigitalAddress: testOrg().DigitalAddress, Status: testOrg().Status,
			BootstrappedAt: now,
		},
		departments: []organization.Department{{ID: uuid.New(), Name: "Legal"}},
		entries: []organization.MemberEntry{{
			Status: "invited", InvitationID: &invitationID, Email: "kim@example.org",
			Role: "member", ExpiresAt: &now,
		}},
	}

	attestations := &fakeAttestations{
		issued: []attestation.Issued{{
			ID: uuid.New(), SchemaVCT: "nl.kvk.registration",
			RecipientKind: "email", RecipientRef: "kim@example.org",
			Attributes: map[string]string{"company_name": testOrg().Name},
			Status:     "offered", Delivery: "email",
			IssuanceID:     secretLiterals["issuance id"],
			CredentialUUID: secretLiterals["credential uuid"],
			CreatedAt:      now, UpdatedAt: now,
		}},
		held: []attestation.HeldAttestation{{
			ID: uuid.New(), CredentialRef: "8c1d", VCT: "nl.kvk.registration",
			Issuer: "https://issuer.example", Source: attestation.HeldSourceQERDS,
			ReceivedAt: now,
		}},
		keys: []attestation.Key{{
			ID: uuid.New(), Kind: "seal", Label: "Qualified seal",
			ProviderRef: "kms://qtsp/key/17", Status: "active",
		}},
	}

	events := &fakeAuditReader{pages: []audit.Page{{Events: []audit.Event{{
		ID: uuid.New(), OccurredAt: now, Action: "membership.invited",
		TargetType: "membership", TargetID: "kim@example.org",
	}}}}}

	return []SectionWriter{
		NewOwnerIdentificationWriter(orgs),
		NewAttestationsWriter(attestations, fakeHolder{raw: map[string][]byte{
			"8c1d": []byte("eyJhbGciOiJFUzI1NiJ9.eyJ2Y3QiOiJubC5rdmsucmVnaXN0cmF0aW9uIn0.sig~"),
		}}),
		NewAuditRecordsWriter(events),
	}
}

// bundleBytes returns every byte a receiver would get: the raw ZIP plus each
// entry's decompressed content, so a secret cannot hide behind deflate.
func bundleBytes(t *testing.T, sections []string) []byte {
	t.Helper()

	svc := NewService(&fakeRecorder{}, seededWriters())
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

// The positive control for the scan above: it asserts the scanner finds values
// the bundle genuinely carries. Without it, a change that emptied the bundle
// would turn the secrets guard into a test that passes by looking at nothing.
func TestSecretScanReadsTheBundle(t *testing.T) {
	bundle := bundleBytes(t, nil)

	present := map[string]string{
		"organization name": testOrg().Name,
		"kvk number":        testOrg().KVKNumber,
		"schema version":    SchemaVersion,
		"member email":      "kim@example.org",
		"issued ledger row": "nl.kvk.registration",
		"audit action":      "membership.invited",
	}
	for label, value := range present {
		if !bytes.Contains(bundle, []byte(value)) {
			t.Errorf("the scan did not find the %s (%q), so it is not reading the bundle", label, value)
		}
	}
}

// A secret must not survive a filtered export either: narrowing the sections
// must never be the thing that decides whether a credential leaks.
func TestFilteredBundleCarriesNoSecrets(t *testing.T) {
	for _, key := range []string{SectionOwnerIdentification, SectionAttestations, SectionAuditRecords} {
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
