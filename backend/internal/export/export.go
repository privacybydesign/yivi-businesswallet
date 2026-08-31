// Package export produces the data-portability bundle of COM(2025) 838
// Art 5(1)(l): a ZIP whose first entry is manifest.json, with one directory per
// data point. The contract — layout, manifest fields, versioning, format profile
// and the secrets that never leave — is .ai/features/export.md.
package export

import (
	"runtime/debug"
	"time"

	"github.com/google/uuid"
)

// SchemaVersion is the bundle layout's version, MAJOR.MINOR. It tracks the
// layout, not the application.
const SchemaVersion = "1.0"

const producerName = "yivi-businesswallet"

// Section keys, in manifest and ZIP order.
const (
	SectionOwnerIdentification = "ownerIdentification"
	SectionAttestations        = "attestations"
	SectionQerds               = "qerds"
	SectionAuditRecords        = "auditRecords"
)

// SectionOrder is the canonical section order.
var SectionOrder = []string{
	SectionOwnerIdentification,
	SectionAttestations,
	SectionQerds,
	SectionAuditRecords,
}

// Omission reasons.
const (
	ReasonSizeLimit   = "size_limit"
	ReasonUnavailable = "unavailable"
)

const checksumAlgorithm = "sha-256"

// Manifest is the bundle's index. It does not list itself, so it carries no
// digest of its own.
type Manifest struct {
	SchemaVersion string       `json:"schemaVersion"`
	BundleID      uuid.UUID    `json:"bundleId"`
	GeneratedAt   string       `json:"generatedAt"`
	Producer      Producer     `json:"producer"`
	Organization  Organization `json:"organization"`
	Sections      []Section    `json:"sections"`
}

// Producer identifies what wrote the bundle. Version is diagnostic only;
// SchemaVersion is the compatibility signal.
type Producer struct {
	Name    string `json:"name"`
	Version string `json:"version"`
}

// Organization denormalises the org's identity so a reader can tell whose
// bundle this is without unpacking it.
type Organization struct {
	ID             uuid.UUID `json:"id"`
	Name           string    `json:"name"`
	Slug           string    `json:"slug"`
	KVKNumber      string    `json:"kvkNumber"`
	EUID           string    `json:"euid"`
	DigitalAddress string    `json:"digitalAddress"`
	Status         string    `json:"status"`
	BootstrappedAt string    `json:"bootstrappedAt"`
}

// Section is one data point's manifest entry. A section appears only when the
// export produced it, so zero counts mean the organization holds none of that
// data — a section nobody asked for is absent instead.
type Section struct {
	Key     string         `json:"key"`
	Counts  map[string]int `json:"counts"`
	Files   []FileEntry    `json:"files"`
	Omitted []Omission     `json:"omitted"`
}

// FileEntry describes one file the bundle carries. SizeBytes and Checksum are
// over the extracted bytes, so a consumer verifies after unzipping.
type FileEntry struct {
	Path      string   `json:"path"`
	MediaType string   `json:"mediaType"`
	SizeBytes int64    `json:"sizeBytes"`
	Checksum  Checksum `json:"checksum"`
}

// Omission is a payload the section's records reference but the bundle does not
// carry.
type Omission struct {
	Path      string    `json:"path"`
	Reason    string    `json:"reason"`
	SizeBytes int64     `json:"sizeBytes,omitempty"`
	Checksum  *Checksum `json:"checksum,omitempty"`
}

// Checksum is a digest over a file's extracted bytes.
type Checksum struct {
	Algorithm string `json:"algorithm"`
	Value     string `json:"value"`
}

func timestamp(t time.Time) string {
	return t.UTC().Format(time.RFC3339)
}

func producerVersion() string {
	info, ok := debug.ReadBuildInfo()
	if !ok {
		return "dev"
	}
	for _, setting := range info.Settings {
		if setting.Key == "vcs.revision" {
			return setting.Value
		}
	}
	return "dev"
}

func optionalTimestamp(t *time.Time) *string {
	if t == nil {
		return nil
	}
	s := timestamp(*t)
	return &s
}
