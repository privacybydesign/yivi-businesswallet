package export

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/privacybydesign/yivi-businesswallet/backend/internal/attestation"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/audit"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/eudiholder"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/organization"
)

// runSection stages one writer and returns what it produced: the manifest
// entry plus every file's bytes keyed by bundle path.
func runSection(t *testing.T, w SectionWriter) (Section, map[string][]byte) {
	t.Helper()
	dir := t.TempDir()
	section := newBundle(dir, unlimitedBudget).section(w.Key(), sectionDirs[w.Key()])
	if err := w.Write(context.Background(), testOrg().ID, section); err != nil {
		t.Fatalf("Write() = %v, want nil", err)
	}

	entry := section.manifest()
	files := make(map[string][]byte, len(entry.Files))
	for _, f := range entry.Files {
		files[f.Path] = readStaged(t, dir, f.Path)
	}
	return entry, files
}

func readStaged(t *testing.T, dir, bundlePath string) []byte {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(dir, filepath.FromSlash(bundlePath)))
	if err != nil {
		t.Fatalf("reading staged %s: %v", bundlePath, err)
	}
	return data
}

func decodeInto(t *testing.T, data []byte, v any) {
	t.Helper()
	if err := json.Unmarshal(data, v); err != nil {
		t.Fatalf("decoding %s: %v", data, err)
	}
}

// --- owner identification ---

type fakeOrgs struct {
	org         organization.Organization
	departments []organization.Department
	entries     []organization.MemberEntry
	pages       int
	err         error
}

func (f *fakeOrgs) GetByID(context.Context, uuid.UUID) (organization.Organization, error) {
	return f.org, f.err
}

func (f *fakeOrgs) ListDepartments(context.Context, uuid.UUID) ([]organization.Department, error) {
	return f.departments, f.err
}

func (f *fakeOrgs) ListMemberEntries(_ context.Context, _ uuid.UUID, p organization.MemberListParams) ([]organization.MemberEntry, int, error) {
	if f.err != nil {
		return nil, 0, f.err
	}
	f.pages++
	if p.Offset >= len(f.entries) {
		return nil, len(f.entries), nil
	}
	end := min(p.Offset+p.Limit, len(f.entries))
	return f.entries[p.Offset:end], len(f.entries), nil
}

func TestOwnerIdentificationWriterExportsTheOrgAndItsPeople(t *testing.T) {
	expires := time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)
	orgs := &fakeOrgs{
		org: organization.Organization{
			ID: testOrg().ID, Name: "Caesar Groep B.V.", Slug: "caesar",
			KVKNumber: "12345678", EUID: "NLKVK.12345678",
			DigitalAddress: "qerds:nl:caesar", Status: "active",
			BootstrappedAt: time.Date(2026, 1, 8, 11, 22, 31, 0, time.UTC),
			LogoURI:        "/api/v1/orgs/caesar/theme/logo",
		},
		departments: []organization.Department{{ID: uuid.New(), Name: "Legal"}},
		entries: []organization.MemberEntry{
			{Status: "active", Email: "sam@example.org", GivenNames: "Sam", LastName: "Smith", Role: "admin", Verified: true},
			{Status: "invited", Email: "kim@example.org", Role: "member", ExpiresAt: &expires},
		},
	}

	entry, files := runSection(t, NewOwnerIdentificationWriter(orgs))

	if entry.Counts["departments"] != 1 || entry.Counts["members"] != 2 {
		t.Errorf("counts = %v, want 1 department and 2 members", entry.Counts)
	}

	var profile orgProfile
	decodeInto(t, files["owner-identification/organization.json"], &profile)
	if profile.KVKNumber != "12345678" || profile.EUID != "NLKVK.12345678" {
		t.Errorf("profile = %+v, want the register identity", profile)
	}
	if profile.BootstrappedAt != "2026-01-08T11:22:31Z" {
		t.Errorf("bootstrappedAt = %q, want RFC 3339 UTC", profile.BootstrappedAt)
	}
	// The logo is an API path into this deployment, meaningless to a receiver.
	if bytes := string(files["owner-identification/organization.json"]); strings.Contains(bytes, "logoUri") {
		t.Errorf("organization.json carries logoUri: %s", bytes)
	}

	var members []memberRecord
	decodeInto(t, files["owner-identification/members.json"], &members)
	if len(members) != 2 {
		t.Fatalf("members = %d, want 2", len(members))
	}
	if members[1].Status != "invited" || members[1].ExpiresAt == nil {
		t.Errorf("invited entry = %+v, want status invited with an expiry", members[1])
	}
}

// An invited entry carries an invitation id addressing an in-flight handshake in
// this deployment; the token behind it is a bearer credential.
func TestOwnerIdentificationWriterOmitsInvitationHandles(t *testing.T) {
	invitationID := uuid.New()
	orgs := &fakeOrgs{
		entries: []organization.MemberEntry{
			{Status: "invited", InvitationID: &invitationID, Email: "kim@example.org", Role: "member"},
		},
	}

	_, files := runSection(t, NewOwnerIdentificationWriter(orgs))

	if body := string(files["owner-identification/members.json"]); strings.Contains(body, invitationID.String()) {
		t.Errorf("members.json carries the invitation id: %s", body)
	}
}

// The member list is offset-paginated with no unbounded mode, so a directory
// larger than one page must still export whole.
func TestOwnerIdentificationWriterPagesTheWholeDirectory(t *testing.T) {
	entries := make([]organization.MemberEntry, memberPageSize+5)
	for i := range entries {
		entries[i] = organization.MemberEntry{
			Status: "active", Email: fmt.Sprintf("m%d@example.org", i), Role: "member",
		}
	}
	orgs := &fakeOrgs{entries: entries}

	entry, files := runSection(t, NewOwnerIdentificationWriter(orgs))

	if entry.Counts["members"] != len(entries) {
		t.Errorf("members count = %d, want %d", entry.Counts["members"], len(entries))
	}
	var members []memberRecord
	decodeInto(t, files["owner-identification/members.json"], &members)
	if len(members) != len(entries) {
		t.Errorf("exported %d members, want %d", len(members), len(entries))
	}
	if orgs.pages < 2 {
		t.Errorf("read %d pages, want the loop to run past the first", orgs.pages)
	}
}

// --- audit records ---

type fakeAuditReader struct {
	pages []audit.Page
	calls int
}

func (f *fakeAuditReader) ListForOrganization(_ context.Context, _ uuid.UUID, _ *audit.Cursor, _ int) (audit.Page, error) {
	if f.calls >= len(f.pages) {
		return audit.Page{}, errors.New("read past the end")
	}
	page := f.pages[f.calls]
	f.calls++
	return page, nil
}

func TestAuditRecordsWriterFollowsTheCursorToTheEnd(t *testing.T) {
	first := audit.Event{
		ID: uuid.New(), OccurredAt: time.Date(2026, 7, 1, 9, 0, 0, 0, time.UTC),
		Action: "membership.invited", TargetType: "membership", TargetID: "kim@example.org",
		Metadata: json.RawMessage(`{"after":{"role":"member"}}`),
	}
	cursor := audit.EncodeCursor(audit.Cursor{OccurredAt: first.OccurredAt, ID: first.ID})
	second := audit.Event{
		ID: uuid.New(), OccurredAt: time.Date(2026, 6, 1, 9, 0, 0, 0, time.UTC),
		Action: "organization.created", TargetType: "organization", TargetID: testOrg().ID.String(),
	}
	reader := &fakeAuditReader{pages: []audit.Page{
		{Events: []audit.Event{first}, NextCursor: &cursor},
		{Events: []audit.Event{second}},
	}}

	entry, files := runSection(t, NewAuditRecordsWriter(reader))

	if reader.calls != 2 {
		t.Errorf("made %d reads, want the cursor followed to the end", reader.calls)
	}
	if entry.Counts["events"] != 2 {
		t.Errorf("events count = %d, want 2", entry.Counts["events"])
	}

	var records []auditRecord
	decodeInto(t, files["audit-records/events.json"], &records)
	if len(records) != 2 {
		t.Fatalf("exported %d events, want 2", len(records))
	}
	if records[0].OccurredAt != "2026-07-01T09:00:00Z" {
		t.Errorf("occurredAt = %q, want RFC 3339 UTC", records[0].OccurredAt)
	}
	// The {before, after} envelope is the event's own record of what changed.
	if string(records[0].Metadata) != `{"after":{"role":"member"}}` {
		t.Errorf("metadata = %s, want it preserved verbatim", records[0].Metadata)
	}
	// An event with no metadata must still decode; a zero RawMessage would not.
	if string(records[1].Metadata) != "null" {
		t.Errorf("empty metadata = %s, want null", records[1].Metadata)
	}
}

// The reader's actor also carries avatar state, which is neither portable data
// nor meant to leave the API.
func TestAuditRecordsWriterExportsActorsWithoutAvatarState(t *testing.T) {
	updated := time.Date(2026, 5, 5, 5, 5, 5, 0, time.UTC)
	actorID := uuid.New()
	reader := &fakeAuditReader{pages: []audit.Page{{Events: []audit.Event{{
		ID: uuid.New(), OccurredAt: updated, Action: "organization.updated",
		TargetType: "organization", TargetID: testOrg().ID.String(),
		Actor: &audit.EventActor{
			UserID: actorID, GivenNames: "Sam", LastName: "Smith",
			AvatarURI: "/api/v1/orgs/caesar/members/x/avatar",
			HasAvatar: true, AvatarUpdatedAt: &updated,
		},
	}}}}}

	_, files := runSection(t, NewAuditRecordsWriter(reader))

	body := string(files["audit-records/events.json"])
	if strings.Contains(body, "avatar") {
		t.Errorf("events.json carries avatar state: %s", body)
	}
	var records []auditRecord
	decodeInto(t, files["audit-records/events.json"], &records)
	if records[0].Actor == nil || records[0].Actor.UserID != actorID {
		t.Errorf("actor = %+v, want the acting user", records[0].Actor)
	}
}

// --- attestations ---

type fakeAttestations struct {
	issued    []attestation.Issued
	held      []attestation.HeldAttestation
	schemas   []attestation.Schema
	templates []attestation.Template
	keys      []attestation.Key
}

func (f *fakeAttestations) ListIssued(context.Context, uuid.UUID) ([]attestation.Issued, error) {
	return f.issued, nil
}

func (f *fakeAttestations) ListHeld(context.Context, uuid.UUID) ([]attestation.HeldAttestation, error) {
	return f.held, nil
}

func (f *fakeAttestations) ListSchemas(context.Context, uuid.UUID) ([]attestation.Schema, error) {
	return f.schemas, nil
}

func (f *fakeAttestations) ListTemplates(context.Context, uuid.UUID) ([]attestation.Template, error) {
	return f.templates, nil
}

func (f *fakeAttestations) ListKeys(context.Context, uuid.UUID) ([]attestation.Key, error) {
	return f.keys, nil
}

type fakeHolder struct{ raw map[string][]byte }

func (f fakeHolder) Raw(_ context.Context, _ uuid.UUID, ref string) ([]byte, error) {
	token, ok := f.raw[ref]
	if !ok {
		return nil, eudiholder.ErrCredentialNotFound
	}
	return token, nil
}

func TestAttestationsWriterCarriesCredentialsVerbatim(t *testing.T) {
	token := []byte("eyJhbGciOiJFUzI1NiJ9.eyJ2Y3QiOiJubC5rdmsucmVnaXN0cmF0aW9uIn0.sig~WyJhIl0~")
	messageID := uuid.New()
	store := &fakeAttestations{
		held: []attestation.HeldAttestation{{
			ID: uuid.New(), CredentialRef: "8c1d", VCT: "nl.kvk.registration",
			Issuer: "https://issuer.example", Source: attestation.HeldSourceQERDS,
			SourceMessageID: &messageID,
			ReceivedAt:      time.Date(2026, 3, 3, 3, 3, 3, 0, time.UTC),
		}},
	}

	entry, files := runSection(t, NewAttestationsWriter(store, fakeHolder{raw: map[string][]byte{"8c1d": token}}))

	path := "attestations/credentials/8c1d.sdjwt"
	// The issuer signed these bytes; anything but a verbatim copy stops verifying.
	if got := files[path]; string(got) != string(token) {
		t.Errorf("credential bytes = %q, want the stored token", got)
	}
	if entry.Counts["credentials"] != 1 {
		t.Errorf("credentials count = %d, want 1", entry.Counts["credentials"])
	}

	var held []heldRecord
	decodeInto(t, files["attestations/held.json"], &held)
	if held[0].Format != sdJwtFormat || held[0].Path != path {
		t.Errorf("held row = %+v, want the format token and the credential path", held[0])
	}
	if held[0].SourceMessageID == nil || *held[0].SourceMessageID != messageID {
		t.Errorf("sourceMessageId = %v, want the QERDS cross-link", held[0].SourceMessageID)
	}

	for _, f := range entry.Files {
		if f.Path == path && f.MediaType != sdJwtMediaType {
			t.Errorf("mediaType = %q, want %q", f.MediaType, sdJwtMediaType)
		}
	}
}

// One credential the engine cannot return must not cost the whole export: the
// held row is still the org's record that it holds one.
func TestAttestationsWriterOmitsUnavailableCredentials(t *testing.T) {
	store := &fakeAttestations{
		held: []attestation.HeldAttestation{
			{ID: uuid.New(), CredentialRef: "gone", VCT: "nl.kvk.registration"},
		},
	}

	entry, files := runSection(t, NewAttestationsWriter(store, fakeHolder{}))

	if len(entry.Omitted) != 1 || entry.Omitted[0].Reason != ReasonUnavailable {
		t.Fatalf("omitted = %+v, want one unavailable credential", entry.Omitted)
	}
	if entry.Omitted[0].Path != "attestations/credentials/gone.sdjwt" {
		t.Errorf("omitted path = %q, want the credential's path", entry.Omitted[0].Path)
	}
	if entry.Counts["credentials"] != 0 {
		t.Errorf("credentials count = %d, want 0 carried", entry.Counts["credentials"])
	}

	var held []heldRecord
	decodeInto(t, files["attestations/held.json"], &held)
	if len(held) != 1 {
		t.Fatalf("held rows = %d, want the row kept", len(held))
	}
	if held[0].Path != "" || held[0].Format != "" {
		t.Errorf("held row = %+v, want no path or format when nothing was carried", held[0])
	}
}

// The ledger row carries live handles on this deployment and its issuer.
func TestAttestationsWriterOmitsIssuanceHandles(t *testing.T) {
	store := &fakeAttestations{
		issued: []attestation.Issued{{
			ID: uuid.New(), SchemaVCT: "nl.kvk.registration",
			RecipientKind: "email", RecipientRef: "kim@example.org",
			Attributes: map[string]string{"company_name": "Caesar Groep B.V."},
			Status:     "claimed", Delivery: "email",
			IssuanceID: "issuance_2f8b41c6e07d", CredentialUUID: "cred_9931ab",
			CreatedAt: time.Now(), UpdatedAt: time.Now(),
		}},
	}

	entry, files := runSection(t, NewAttestationsWriter(store, fakeHolder{}))

	body := string(files["attestations/issued.json"])
	for _, secret := range []string{"issuance_2f8b41c6e07d", "cred_9931ab"} {
		if strings.Contains(body, secret) {
			t.Errorf("issued.json carries %q: %s", secret, body)
		}
	}
	if entry.Counts["issued"] != 1 {
		t.Errorf("issued count = %d, want 1", entry.Counts["issued"])
	}
	if !strings.Contains(body, "company_name") {
		t.Errorf("issued.json lost the attributes: %s", body)
	}
}

// Every index is written even when the org holds nothing, so a receiver can tell
// an empty ledger from a producer that skipped it.
func TestAttestationsWriterWritesEveryIndex(t *testing.T) {
	entry, files := runSection(t, NewAttestationsWriter(&fakeAttestations{}, fakeHolder{}))

	for _, name := range []string{"issued.json", "held.json", "schemas.json", "templates.json", "keys.json"} {
		if _, ok := files["attestations/"+name]; !ok {
			t.Errorf("%s is missing from the section", name)
		}
	}
	for _, key := range []string{"issued", "held", "schemas", "templates", "keys"} {
		if entry.Counts[key] != 0 {
			t.Errorf("%s count = %d, want 0", key, entry.Counts[key])
		}
	}
}

// A key row names the provider handle a receiver needs to correlate with the
// issuer or QTSP, and never key material.
func TestAttestationsWriterExportsKeyReferencesOnly(t *testing.T) {
	store := &fakeAttestations{keys: []attestation.Key{{
		ID: uuid.New(), Kind: "seal", Label: "Qualified seal",
		ProviderRef: "kms://qtsp/key/17", Status: "active",
	}}}

	_, files := runSection(t, NewAttestationsWriter(store, fakeHolder{}))

	var keys []keyRecord
	decodeInto(t, files["attestations/keys.json"], &keys)
	if len(keys) != 1 || keys[0].ProviderRef != "kms://qtsp/key/17" {
		t.Fatalf("keys = %+v, want the provider reference", keys)
	}
}
