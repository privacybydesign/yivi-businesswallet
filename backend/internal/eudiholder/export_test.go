package eudiholder

import (
	"context"

	"github.com/google/uuid"
	irmastorage "github.com/privacybydesign/irmago/eudi/storage"
)

// StorageForTest exposes the org's irmago storage so tests can seed what only
// the real receive flow writes otherwise (holder-binding keys).
func (e *Engine) StorageForTest(ctx context.Context, orgID uuid.UUID) (irmastorage.Storage, error) {
	return e.engineFor(ctx, orgID)
}
