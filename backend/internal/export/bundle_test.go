package export

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func stagedSection(t *testing.T, budget int64) (*SectionBundle, string) {
	t.Helper()
	dir := t.TempDir()
	return newBundle(dir, budget).section(SectionQerds, sectionDirs[SectionQerds]), dir
}

func TestSectionBundleRejectsUnsafePaths(t *testing.T) {
	names := map[string]string{
		"traversal":        "../../etc/passwd",
		"nested traversal": "attachments/../../escape",
		"absolute":         "/etc/passwd",
		"backslash":        `attachments\evil`,
		"empty segment":    "attachments//blank",
		"empty":            "",
		"non-ascii":        "attachments/naïve",
	}
	for label, name := range names {
		t.Run(label, func(t *testing.T) {
			section, dir := stagedSection(t, unlimitedBudget)
			if err := section.AddBytes(name, "application/octet-stream", []byte("x")); err == nil {
				t.Fatalf("AddBytes(%q) = nil, want a rejected path", name)
			}
			if len(section.files) != 0 {
				t.Errorf("a rejected path still produced %d manifest entries", len(section.files))
			}
			if entries, err := os.ReadDir(dir); err == nil && len(entries) != 0 {
				t.Errorf("a rejected path wrote %d entries into the staging directory", len(entries))
			}
		})
	}
}

func TestSectionBundleStagesNestedPaths(t *testing.T) {
	section, dir := stagedSection(t, unlimitedBudget)

	const name = "attachments/0b9c/a7f2"
	payload := []byte("attachment bytes")
	if err := section.AddBytes(name, "application/octet-stream", payload); err != nil {
		t.Fatalf("AddBytes() = %v, want nil", err)
	}

	staged := filepath.Join(dir, "qerds", "attachments", "0b9c", "a7f2")
	got, err := os.ReadFile(staged)
	if err != nil {
		t.Fatalf("reading staged file: %v", err)
	}
	if string(got) != string(payload) {
		t.Errorf("staged %q, want %q", got, payload)
	}
	if len(section.files) != 1 || section.files[0].Path != "qerds/"+name {
		t.Errorf("manifest entry = %+v, want the section-prefixed path", section.files)
	}
}

func TestSectionBundleOmitsOverBudgetPayloads(t *testing.T) {
	section, dir := stagedSection(t, 8)

	if err := section.AddBytes("attachments/small", "application/octet-stream", []byte("1234")); err != nil {
		t.Fatalf("AddBytes(small) = %v, want nil", err)
	}
	big := []byte(strings.Repeat("x", 64))
	if err := section.AddBytes("attachments/big", "application/octet-stream", big); err != nil {
		t.Fatalf("AddBytes(big) = %v, want nil — an over-budget payload is omitted, not an error", err)
	}

	if len(section.files) != 1 || section.files[0].Path != "qerds/attachments/small" {
		t.Errorf("files = %+v, want only the payload that fit", section.files)
	}
	if len(section.omitted) != 1 {
		t.Fatalf("omitted %d payloads, want 1", len(section.omitted))
	}
	omission := section.omitted[0]
	if omission.Path != "qerds/attachments/big" || omission.Reason != ReasonSizeLimit {
		t.Errorf("omission = %+v, want the big payload at size_limit", omission)
	}
	if omission.SizeBytes != int64(len(big)) {
		t.Errorf("omitted sizeBytes = %d, want %d", omission.SizeBytes, len(big))
	}
	if omission.Checksum == nil || omission.Checksum.Algorithm != checksumAlgorithm {
		t.Errorf("omitted checksum = %+v, want a sha-256", omission.Checksum)
	}
	if _, err := os.Stat(filepath.Join(dir, "qerds", "attachments", "big")); !os.IsNotExist(err) {
		t.Errorf("the omitted payload was staged anyway (%v)", err)
	}
}

func TestSectionBundleAlwaysWritesIndexes(t *testing.T) {
	section, _ := stagedSection(t, 1)

	if err := section.AddJSON("messages.json", map[string]string{"id": "0b9c"}); err != nil {
		t.Fatalf("AddJSON() = %v, want nil even over budget", err)
	}
	if len(section.files) != 1 {
		t.Fatalf("files = %+v, want the index written", section.files)
	}
	if section.files[0].MediaType != jsonMediaType {
		t.Errorf("mediaType = %q, want %q", section.files[0].MediaType, jsonMediaType)
	}
	if len(section.omitted) != 0 {
		t.Errorf("omitted = %+v, want an index never to be omitted", section.omitted)
	}
}

func TestSectionManifestNormalisesEmptyLists(t *testing.T) {
	section, _ := stagedSection(t, unlimitedBudget)

	got := section.manifest(true)
	if got.Files == nil || got.Omitted == nil {
		t.Errorf("manifest = %+v, want empty slices rather than nulls", got)
	}
	if got.Counts == nil {
		t.Error("counts is nil, want an empty object")
	}
}
