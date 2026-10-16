package main

import (
	"context"
	"fmt"
	"net/http"

	"github.com/google/uuid"
	"github.com/klippa-app/go-pdfium"
	"github.com/klippa-app/go-pdfium/webassembly"
	"github.com/tetratelabs/wazero"

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

// The diploma parser's own PDFium pool, apart from the VOG parser's, so a
// burst of crafted diploma uploads cannot starve VOG uploads.
const (
	diplomaPDFiumMinIdle  = 1
	diplomaPDFiumMaxIdle  = 2
	diplomaPDFiumMaxTotal = 2
)

// newDiplomaPDFiumPool builds the diploma parser's PDFium WebAssembly pool.
// Its runtime closes on context done, so the parser's Kill interrupts a parse
// in flight.
func newDiplomaPDFiumPool() (pdfium.Pool, error) {
	pool, err := webassembly.Init(webassembly.Config{
		MinIdle:       diplomaPDFiumMinIdle,
		MaxIdle:       diplomaPDFiumMaxIdle,
		MaxTotal:      diplomaPDFiumMaxTotal,
		RuntimeConfig: wazero.NewRuntimeConfig().WithCloseOnContextDone(true),
	})
	if err != nil {
		return nil, fmt.Errorf("diploma: initialise pdfium: %w", err)
	}
	return pool, nil
}
