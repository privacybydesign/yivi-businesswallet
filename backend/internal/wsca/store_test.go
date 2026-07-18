package wsca_test

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"

	"github.com/privacybydesign/yivi-businesswallet/backend/internal/wsca"
)

// TestStoreNotConfigured proves that with no KEK (nil cipher) every secret
// operation reports ErrNotConfigured before touching the database.
func TestStoreNotConfigured(t *testing.T) {
	t.Parallel()
	s := wsca.NewStore(nil, nil)
	ctx := context.Background()
	org := uuid.New()

	if s.Configured() {
		t.Fatal("expected Configured() == false with a nil cipher")
	}
	if _, err := s.Activate(ctx, org, "secret", "acct", "cert"); !errors.Is(err, wsca.ErrNotConfigured) {
		t.Errorf("Activate err = %v, want ErrNotConfigured", err)
	}
	if _, err := s.Secret(ctx, org); !errors.Is(err, wsca.ErrNotConfigured) {
		t.Errorf("Secret err = %v, want ErrNotConfigured", err)
	}
	if _, err := s.Rotate(ctx, org, "new", "cert2"); !errors.Is(err, wsca.ErrNotConfigured) {
		t.Errorf("Rotate err = %v, want ErrNotConfigured", err)
	}
}
