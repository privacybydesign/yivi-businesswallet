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

// DefaultWriters is the set every export runs. A section is registered here
// only once it can actually be written: an unregistered one is absent from the
// bundle and refused by name, rather than appearing empty and claiming the
// organization holds none of it.
func DefaultWriters() []SectionWriter {
	return nil
}
