package export

import (
	"context"

	"github.com/google/uuid"
)

// SectionWriter fills one data point of the bundle.
type SectionWriter interface {
	Key() string
	Write(ctx context.Context, orgID uuid.UUID, s *SectionBundle) error
}

// sectionDirs lives here rather than on the writer so a section cannot rename or
// escape its own directory.
var sectionDirs = map[string]string{
	SectionOwnerIdentification: "owner-identification",
	SectionAttestations:        "attestations",
	SectionQerds:               "qerds",
	SectionAuditRecords:        "audit-records",
}

type emptyWriter struct{ key string }

func (w emptyWriter) Key() string { return w.key }

func (w emptyWriter) Write(context.Context, uuid.UUID, *SectionBundle) error { return nil }

// DefaultWriters is the set every export runs, in manifest order. Each entry is
// replaced by the real writer as its section lands.
func DefaultWriters() []SectionWriter {
	return []SectionWriter{
		emptyWriter{key: SectionOwnerIdentification},
		emptyWriter{key: SectionAttestations},
		emptyWriter{key: SectionQerds},
		emptyWriter{key: SectionAuditRecords},
	}
}
