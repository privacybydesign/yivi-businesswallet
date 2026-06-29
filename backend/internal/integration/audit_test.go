//go:build integration

package integration

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/privacybydesign/yivi-businesswallet/backend/internal/audit"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/organization"
)

func TestOrgUpdateRecordsAuditEvent(t *testing.T) {
	env := setup(t)
	orgID := env.createOrg("Acme", "acme")
	me := env.login("boss@example.test")
	env.addMembership(me.ID, orgID, organization.RoleAdmin)

	resp := env.do(http.MethodPatch, "/api/v1/orgs/acme", strings.NewReader(`{"name":"Acme Renamed"}`))
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("PATCH /orgs/acme = %d, want 204", resp.StatusCode)
	}

	var (
		actorID uuid.UUID
		orgCol  uuid.UUID
		oldName string
		newName string
	)
	err := env.pool.QueryRow(context.Background(),
		`SELECT actor_user_id, organization_id, metadata->>'oldName', metadata->>'newName'
		 FROM audit_events WHERE action = $1 AND organization_id = $2`,
		audit.OrganizationUpdated, orgID,
	).Scan(&actorID, &orgCol, &oldName, &newName)
	if err != nil {
		t.Fatalf("query audit_events: %v", err)
	}
	if actorID != me.ID {
		t.Errorf("actor_user_id = %s, want %s", actorID, me.ID)
	}
	if oldName != "Acme" || newName != "Acme Renamed" {
		t.Errorf("name change = %q -> %q, want Acme -> Acme Renamed", oldName, newName)
	}
}
