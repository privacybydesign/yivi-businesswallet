//go:build integration

package export_test

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/privacybydesign/yivi-businesswallet/backend/internal/audit"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/export"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/testdb"
)

func TestRecordExportWritesAReadableAuditEvent(t *testing.T) {
	pool, _ := testdb.Fresh(t)
	orgID := makeOrg(t, pool, "caesar")
	store := export.NewStore(pool, audit.NewDBRecorder())
	bundleID := uuid.New()
	ctx := context.Background()

	sections := []string{export.SectionAttestations, export.SectionQerds}
	if err := store.RecordExport(ctx, orgID, bundleID, sections); err != nil {
		t.Fatalf("RecordExport: %v", err)
	}

	page, err := audit.NewReader(pool).ListForOrganization(ctx, orgID, nil, 50)
	if err != nil {
		t.Fatalf("ListForOrganization: %v", err)
	}
	if len(page.Events) != 1 {
		t.Fatalf("read %d events, want 1", len(page.Events))
	}

	event := page.Events[0]
	if event.Action != audit.ExportRequested {
		t.Errorf("action = %q, want %q", event.Action, audit.ExportRequested)
	}
	if event.TargetType != audit.TargetExport {
		t.Errorf("targetType = %q, want %q", event.TargetType, audit.TargetExport)
	}
	// The bundle's own id is the target, so a bundle in someone's hands traces
	// back to the run that produced it.
	if event.TargetID != bundleID.String() {
		t.Errorf("targetId = %q, want the bundle id %q", event.TargetID, bundleID)
	}

	var metadata struct {
		After struct {
			BundleID      string   `json:"bundleId"`
			SchemaVersion string   `json:"schemaVersion"`
			Sections      []string `json:"sections"`
		} `json:"after"`
	}
	if err := json.Unmarshal(event.Metadata, &metadata); err != nil {
		t.Fatalf("decoding metadata %s: %v", event.Metadata, err)
	}
	if metadata.After.BundleID != bundleID.String() {
		t.Errorf("metadata bundleId = %q, want %q", metadata.After.BundleID, bundleID)
	}
	if metadata.After.SchemaVersion != export.SchemaVersion {
		t.Errorf("metadata schemaVersion = %q, want %q", metadata.After.SchemaVersion, export.SchemaVersion)
	}
	if len(metadata.After.Sections) != 2 ||
		metadata.After.Sections[0] != export.SectionAttestations ||
		metadata.After.Sections[1] != export.SectionQerds {
		t.Errorf("metadata sections = %v, want %v", metadata.After.Sections, sections)
	}
}

func makeOrg(t *testing.T, pool *pgxpool.Pool, slug string) uuid.UUID {
	t.Helper()
	var id uuid.UUID
	err := pool.QueryRow(context.Background(),
		`INSERT INTO organizations (name, slug, kvk_number, euid, digital_address)
		 VALUES ($1, $2, $3, $4, $5) RETURNING id`,
		slug, slug, "kvk-"+slug, "NL.KVK."+slug, slug+"@qerds.localhost").Scan(&id)
	if err != nil {
		t.Fatalf("create org %q: %v", slug, err)
	}
	return id
}
