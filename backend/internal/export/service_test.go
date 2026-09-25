package export

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"slices"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/privacybydesign/yivi-businesswallet/backend/internal/respond"
)

type recordedExport struct {
	orgID    uuid.UUID
	bundleID uuid.UUID
	sections []string
}

type fakeRecorder struct {
	calls []recordedExport
	err   error
}

func (f *fakeRecorder) RecordExport(_ context.Context, orgID, bundleID uuid.UUID, sections []string) error {
	if f.err != nil {
		return f.err
	}
	f.calls = append(f.calls, recordedExport{orgID: orgID, bundleID: bundleID, sections: slices.Clone(sections)})
	return nil
}

type stubWriter struct {
	key    string
	write  func(s *SectionBundle) error
	failed error
}

func (w stubWriter) Key() string { return w.key }

func (w stubWriter) Write(_ context.Context, _ uuid.UUID, s *SectionBundle) error {
	if w.failed != nil {
		return w.failed
	}
	if w.write == nil {
		return nil
	}
	return w.write(s)
}

// allWriters registers every section, standing in for the point at which all
// four are built. DefaultWriters registers only the ones that exist.
func allWriters() []SectionWriter {
	writers := make([]SectionWriter, 0, len(SectionOrder))
	for _, key := range SectionOrder {
		writers = append(writers, stubWriter{key: key})
	}
	return writers
}

func testOrg() Organization {
	return Organization{
		ID:             uuid.MustParse("3d1f0c8e-5b77-4a41-9f0e-0a2c6c2ad4b1"),
		Name:           "Caesar Groep B.V.",
		Slug:           "caesar",
		KVKNumber:      "12345678",
		EUID:           "NLKVK.12345678",
		DigitalAddress: "qerds:nl:caesar",
		Status:         "active",
		BootstrappedAt: "2026-01-08T11:22:31Z",
	}
}

func fixedClock(s *Service) {
	s.now = func() time.Time { return time.Date(2026, 7, 27, 9, 14, 2, 0, time.UTC) }
}

func exportBundle(t *testing.T, svc *Service, sections []string) ([]byte, Manifest, *zip.Reader) {
	t.Helper()
	archive, err := svc.Export(context.Background(), testOrg(), sections)
	if err != nil {
		t.Fatalf("Export() = %v, want nil", err)
	}
	defer func() { _ = archive.Close() }()

	raw, err := io.ReadAll(archive.Reader())
	if err != nil {
		t.Fatalf("reading bundle: %v", err)
	}
	if int64(len(raw)) != archive.Size {
		t.Errorf("read %d bytes, Size = %d", len(raw), archive.Size)
	}

	zr, err := zip.NewReader(bytes.NewReader(raw), int64(len(raw)))
	if err != nil {
		t.Fatalf("opening bundle as zip: %v", err)
	}
	if len(zr.File) == 0 || zr.File[0].Name != manifestPath {
		t.Fatalf("first entry = %q, want %q", firstName(zr), manifestPath)
	}

	var manifest Manifest
	if err := json.Unmarshal(readEntry(t, zr, manifestPath), &manifest); err != nil {
		t.Fatalf("decoding manifest: %v", err)
	}
	return raw, manifest, zr
}

func firstName(zr *zip.Reader) string {
	if len(zr.File) == 0 {
		return ""
	}
	return zr.File[0].Name
}

func readEntry(t *testing.T, zr *zip.Reader, name string) []byte {
	t.Helper()
	for _, f := range zr.File {
		if f.Name != name {
			continue
		}
		rc, err := f.Open()
		if err != nil {
			t.Fatalf("opening %s: %v", name, err)
		}
		defer func() { _ = rc.Close() }()
		data, err := io.ReadAll(rc)
		if err != nil {
			t.Fatalf("reading %s: %v", name, err)
		}
		return data
	}
	t.Fatalf("entry %q not in bundle", name)
	return nil
}

func TestExportProducesAConformantManifest(t *testing.T) {
	audit := &fakeRecorder{}
	svc := NewService(audit, allWriters())
	fixedClock(svc)

	_, manifest, _ := exportBundle(t, svc, nil)

	if manifest.SchemaVersion != SchemaVersion {
		t.Errorf("schemaVersion = %q, want %q", manifest.SchemaVersion, SchemaVersion)
	}
	if manifest.BundleID == uuid.Nil {
		t.Error("bundleId is the nil uuid")
	}
	if manifest.GeneratedAt != "2026-07-27T09:14:02Z" {
		t.Errorf("generatedAt = %q, want an RFC 3339 UTC instant", manifest.GeneratedAt)
	}
	if manifest.Producer.Name != producerName {
		t.Errorf("producer.name = %q, want %q", manifest.Producer.Name, producerName)
	}
	if manifest.Organization != testOrg() {
		t.Errorf("organization = %+v, want the exported org", manifest.Organization)
	}

	var keys []string
	for _, section := range manifest.Sections {
		keys = append(keys, section.Key)
		if section.Files == nil || section.Omitted == nil {
			t.Errorf("section %q has a null files/omitted list, want []", section.Key)
		}
	}
	if !slices.Equal(keys, SectionOrder) {
		t.Errorf("section keys = %v, want %v", keys, SectionOrder)
	}

	if len(audit.calls) != 1 {
		t.Fatalf("recorded %d exports, want 1", len(audit.calls))
	}
	if audit.calls[0].bundleID != manifest.BundleID {
		t.Errorf("audited bundleId = %s, want the manifest's %s", audit.calls[0].bundleID, manifest.BundleID)
	}
	if audit.calls[0].orgID != testOrg().ID {
		t.Errorf("audited orgId = %s, want %s", audit.calls[0].orgID, testOrg().ID)
	}
}

// An unrequested section is absent, not present-and-empty: zero counts are a
// claim that the organization holds none of that data.
func TestExportSectionFilterOmitsUnrequestedSections(t *testing.T) {
	audit := &fakeRecorder{}
	svc := NewService(audit, []SectionWriter{
		stubWriter{key: SectionOwnerIdentification, write: func(s *SectionBundle) error {
			t.Error("owner identification was written for an attestations-only export")
			return nil
		}},
		stubWriter{key: SectionAttestations, write: func(s *SectionBundle) error {
			s.Count("issued", 2)
			return s.AddJSON("issued.json", []string{"a", "b"})
		}},
		stubWriter{key: SectionQerds},
		stubWriter{key: SectionAuditRecords},
	})
	fixedClock(svc)

	_, manifest, zr := exportBundle(t, svc, []string{SectionAttestations})

	if len(manifest.Sections) != 1 || manifest.Sections[0].Key != SectionAttestations {
		t.Fatalf("sections = %+v, want only %s", manifest.Sections, SectionAttestations)
	}
	if got := manifest.Sections[0].Counts["issued"]; got != 2 {
		t.Errorf("issued count = %d, want 2", got)
	}
	if got := len(zr.File); got != 2 {
		t.Errorf("bundle holds %d entries, want the manifest plus one section file", got)
	}
	if !slices.Equal(audit.calls[0].sections, []string{SectionAttestations}) {
		t.Errorf("audited sections = %v, want [%s]", audit.calls[0].sections, SectionAttestations)
	}
}

// A section with no registered writer is refused by name rather than shipped
// empty, which would claim the organization holds none of it.
func TestExportRefusesASectionItCannotWrite(t *testing.T) {
	svc := NewService(&fakeRecorder{}, []SectionWriter{stubWriter{key: SectionAttestations}})

	_, err := svc.Export(context.Background(), testOrg(), []string{SectionQerds})
	if err == nil {
		t.Fatal("Export() = nil, want an error for an unavailable section")
	}
	var apiErr *respond.APIError
	if !errors.As(err, &apiErr) || apiErr.Code != "section_unavailable" {
		t.Fatalf("err = %v, want a section_unavailable APIError", err)
	}
}

// A full export carries what this producer can write, and nothing it cannot.
func TestExportCarriesOnlyRegisteredSections(t *testing.T) {
	svc := NewService(&fakeRecorder{}, []SectionWriter{stubWriter{key: SectionAttestations}})
	fixedClock(svc)

	_, manifest, _ := exportBundle(t, svc, nil)

	if len(manifest.Sections) != 1 || manifest.Sections[0].Key != SectionAttestations {
		t.Errorf("sections = %+v, want only the registered one", manifest.Sections)
	}
}

func TestExportRejectsAnUnknownSection(t *testing.T) {
	svc := NewService(&fakeRecorder{}, allWriters())

	_, err := svc.Export(context.Background(), testOrg(), []string{"attestation"})
	if err == nil {
		t.Fatal("Export() = nil, want an error for an unknown section")
	}
	var apiErr *respond.APIError
	if !errors.As(err, &apiErr) || apiErr.Status != http.StatusBadRequest {
		t.Fatalf("err = %v, want a 400 APIError", err)
	}
	if apiErr.Code != "invalid_section" {
		t.Errorf("code = %q, want invalid_section", apiErr.Code)
	}
}

func TestExportChecksumsCoverTheExtractedBytes(t *testing.T) {
	payload := []byte("raw-sd-jwt-vc-token")
	svc := NewService(&fakeRecorder{}, []SectionWriter{
		stubWriter{key: SectionOwnerIdentification, write: func(s *SectionBundle) error {
			return s.AddBytes("credentials/8c1d.sdjwt", "application/dc+sd-jwt", payload)
		}},
		stubWriter{key: SectionAttestations},
		stubWriter{key: SectionQerds},
		stubWriter{key: SectionAuditRecords},
	})
	fixedClock(svc)

	_, manifest, zr := exportBundle(t, svc, nil)

	var entry FileEntry
	for _, section := range manifest.Sections {
		for _, f := range section.Files {
			if f.Path == "owner-identification/credentials/8c1d.sdjwt" {
				entry = f
			}
		}
	}
	if entry.Path == "" {
		t.Fatal("the credential file is missing from the manifest")
	}
	if entry.MediaType != "application/dc+sd-jwt" {
		t.Errorf("mediaType = %q, want application/dc+sd-jwt", entry.MediaType)
	}
	if entry.SizeBytes != int64(len(payload)) {
		t.Errorf("sizeBytes = %d, want %d", entry.SizeBytes, len(payload))
	}
	sum := sha256.Sum256(payload)
	if entry.Checksum.Algorithm != checksumAlgorithm || entry.Checksum.Value != hex.EncodeToString(sum[:]) {
		t.Errorf("checksum = %+v, want a sha-256 over the extracted bytes", entry.Checksum)
	}
	if got := readEntry(t, zr, entry.Path); !bytes.Equal(got, payload) {
		t.Errorf("stored bytes = %q, want %q", got, payload)
	}
}

func TestExportIsDeterministic(t *testing.T) {
	writers := func() []SectionWriter {
		return []SectionWriter{
			stubWriter{key: SectionOwnerIdentification, write: func(s *SectionBundle) error {
				if err := s.AddJSON("members.json", []string{"sam"}); err != nil {
					return err
				}
				return s.AddJSON("departments.json", []string{"legal"})
			}},
			stubWriter{key: SectionAttestations},
			stubWriter{key: SectionQerds},
			stubWriter{key: SectionAuditRecords},
		}
	}

	svcA := NewService(&fakeRecorder{}, writers())
	fixedClock(svcA)
	svcB := NewService(&fakeRecorder{}, writers())
	fixedClock(svcB)

	_, first, _ := exportBundle(t, svcA, nil)
	_, second, _ := exportBundle(t, svcB, nil)

	if first.BundleID == second.BundleID {
		t.Error("two exports share a bundleId, want one per run")
	}
	first.BundleID, second.BundleID = uuid.Nil, uuid.Nil

	a, err := json.Marshal(first)
	if err != nil {
		t.Fatalf("marshalling: %v", err)
	}
	b, err := json.Marshal(second)
	if err != nil {
		t.Fatalf("marshalling: %v", err)
	}
	if !bytes.Equal(a, b) {
		t.Errorf("manifests differ beyond bundleId:\n%s\n%s", a, b)
	}

	paths := []string{
		first.Sections[0].Files[0].Path,
		first.Sections[0].Files[1].Path,
	}
	if !slices.IsSorted(paths) {
		t.Errorf("files = %v, want them sorted by path", paths)
	}
}

func TestExportFailsWhenASectionFails(t *testing.T) {
	svc := NewService(&fakeRecorder{}, []SectionWriter{
		stubWriter{key: SectionOwnerIdentification},
		stubWriter{key: SectionAttestations, failed: errors.New("store unreachable")},
		stubWriter{key: SectionQerds},
		stubWriter{key: SectionAuditRecords},
	})

	if _, err := svc.Export(context.Background(), testOrg(), nil); err == nil {
		t.Fatal("Export() = nil, want the section's error")
	}
}

func TestExportFailsWhenTheAuditWriteFails(t *testing.T) {
	svc := NewService(&fakeRecorder{err: errors.New("no database")}, allWriters())

	if _, err := svc.Export(context.Background(), testOrg(), nil); err == nil {
		t.Fatal("Export() = nil, want the audit error")
	}
}

func TestArchiveCloseRemovesTheStagingDirectory(t *testing.T) {
	svc := NewService(&fakeRecorder{}, allWriters())

	archive, err := svc.Export(context.Background(), testOrg(), nil)
	if err != nil {
		t.Fatalf("Export() = %v, want nil", err)
	}
	dir := archive.dir
	if _, err := os.Stat(dir); err != nil {
		t.Fatalf("staging directory missing before Close: %v", err)
	}
	if err := archive.Close(); err != nil {
		t.Fatalf("Close() = %v, want nil", err)
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Errorf("staging directory still present after Close (%v)", err)
	}
}

func TestParseSections(t *testing.T) {
	cases := map[string]struct {
		raw  string
		want []string
	}{
		"empty":     {"", nil},
		"blank":     {"   ", nil},
		"one":       {"attestations", []string{"attestations"}},
		"several":   {"qerds,auditRecords", []string{"qerds", "auditRecords"}},
		"spaced":    {" qerds , auditRecords ", []string{"qerds", "auditRecords"}},
		"trailing,": {"qerds,", []string{"qerds"}},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			if got := ParseSections(tc.raw); !slices.Equal(got, tc.want) {
				t.Errorf("ParseSections(%q) = %v, want %v", tc.raw, got, tc.want)
			}
		})
	}
}

func TestResolveSectionsIsCanonicallyOrdered(t *testing.T) {
	svc := NewService(&fakeRecorder{}, allWriters())

	got, err := svc.resolveSections([]string{SectionAuditRecords, SectionOwnerIdentification, SectionAuditRecords})
	if err != nil {
		t.Fatalf("resolveSections() = %v, want nil", err)
	}
	want := []string{SectionOwnerIdentification, SectionAuditRecords}
	if !slices.Equal(got, want) {
		t.Errorf("resolveSections() = %v, want %v", got, want)
	}
}
