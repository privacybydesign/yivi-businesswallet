package main

import (
	"context"
	"fmt"
	"net/http"

	"github.com/google/uuid"

	"github.com/privacybydesign/yivi-businesswallet/backend/internal/organization"
)

// orgNames names an org for the proofing engine: the "who is asking" line
// the Idem app shows the subject. The engine's tenant id is the org's id.
type orgNames struct {
	store *organization.Store
}

func (n orgNames) DisplayName(ctx context.Context, tenantID string) (string, error) {
	id, err := uuid.Parse(tenantID)
	if err != nil {
		return "", fmt.Errorf("proofing tenant %q is not an organization id: %w", tenantID, err)
	}
	org, err := n.store.GetByID(ctx, id)
	if err != nil {
		return "", fmt.Errorf("look up proofing tenant: %w", err)
	}
	return org.Name, nil
}

// noRoutes stands in for the proofing engine's app routes when the stub runs:
// no phone can reach a stub session.
type noRoutes struct{}

func (noRoutes) Register(*http.ServeMux) {}
