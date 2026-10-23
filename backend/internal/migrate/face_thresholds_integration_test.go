//go:build integration

package migrate_test

import (
	"context"
	"testing"

	"github.com/jackc/pgx/v5"

	"github.com/privacybydesign/yivi-businesswallet/backend/internal/migrate"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/testdb"
)

// weakFaceThresholdsVersion is 20261021116000_drop_weak_identity_proofing_face_match_thresholds.sql.
// The test runs up to it, so a later migration does not change what it reads.
const weakFaceThresholdsVersion int64 = 20261021116000

// TestUpDropsWeakFaceMatchThresholds stores flow versions with face match
// thresholds under, at and over 0.75, and none, before the migration that
// lets a flow's threshold replace the deployment's: afterwards only the ones
// under 0.75 are gone, from inactive versions too, and every other setting of
// a version stays.
func TestUpDropsWeakFaceMatchThresholds(t *testing.T) {
	ctx := context.Background()
	dsn := testdb.Bare(t)
	if err := migrate.UpTo(ctx, dsn, weakFaceThresholdsVersion-1); err != nil {
		t.Fatalf("migrate up to before the face match thresholds: %v", err)
	}
	conn, err := pgx.Connect(ctx, dsn)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer func() { _ = conn.Close(ctx) }()

	var orgID string
	if err := conn.QueryRow(ctx, `INSERT INTO organizations (name, slug, kvk_number, euid, digital_address)
		VALUES ('Acme', 'acme', '12345678', 'NLNHR.12345678', 'acme@qerds.test') RETURNING id`).Scan(&orgID); err != nil {
		t.Fatalf("seed org: %v", err)
	}
	versions := map[string]string{
		"weak":       `{"Name": "weak", "CheckThresholds": {"face.match": 0.6, "other.check": 0.5}}`,
		"weak, old":  `{"Name": "weak, old", "CheckThresholds": {"face.match": 0}}`,
		"at floor":   `{"Name": "at floor", "CheckThresholds": {"face.match": 0.75}}`,
		"strict":     `{"Name": "strict", "CheckThresholds": {"face.match": 0.9}}`,
		"none":       `{"Name": "none", "CheckThresholds": null}`,
		"no setting": `{"Name": "no setting"}`,
	}
	for flowID, def := range versions {
		active := flowID != "weak, old"
		if _, err := conn.Exec(ctx, `INSERT INTO identity_proofing_flow_versions (organization_id, flow_id, version, definition, active)
			VALUES ($1, $2, 1, $3, $4)`, orgID, flowID, def, active); err != nil {
			t.Fatalf("seed %s: %v", flowID, err)
		}
	}

	if err := migrate.UpTo(ctx, dsn, weakFaceThresholdsVersion); err != nil {
		t.Fatalf("migrate up: %v", err)
	}

	for flowID, want := range map[string]string{
		"weak":       `{"Name": "weak", "CheckThresholds": {"other.check": 0.5}}`,
		"weak, old":  `{"Name": "weak, old", "CheckThresholds": {}}`,
		"at floor":   versions["at floor"],
		"strict":     versions["strict"],
		"none":       versions["none"],
		"no setting": versions["no setting"],
	} {
		var same bool
		if err := conn.QueryRow(ctx, `SELECT definition = $2::jsonb FROM identity_proofing_flow_versions
			WHERE organization_id = $1 AND flow_id = $3`, orgID, want, flowID).Scan(&same); err != nil || !same {
			var got string
			_ = conn.QueryRow(ctx, `SELECT definition::text FROM identity_proofing_flow_versions
				WHERE organization_id = $1 AND flow_id = $2`, orgID, flowID).Scan(&got)
			t.Errorf("%s = %s, %v; want %s", flowID, got, err, want)
		}
	}

	if err := migrate.Down(ctx, dsn); err != nil {
		t.Fatalf("migrate down: %v", err)
	}
	if v, err := migrate.Version(ctx, dsn); err != nil || v >= weakFaceThresholdsVersion {
		t.Fatalf("version after down = %d, %v; want before %d", v, err, weakFaceThresholdsVersion)
	}
}
