package providerlisting

import (
	"context"
	"fmt"

	"github.com/privacybydesign/yivi-businesswallet/backend/internal/database"
)

// StubList is our own trusted list of wallet providers, for a deployment without
// WE BUILD access: it lists us until SetListed takes us off.
type StubList struct {
	db database.DB
}

func NewStubList(db database.DB) *StubList { return &StubList{db: db} }

func (*StubList) Ping(context.Context) error { return nil }

func (l *StubList) ProviderListed(ctx context.Context) (bool, error) {
	var listed bool
	if err := l.db.QueryRow(ctx, `SELECT listed FROM stub_trusted_list`).Scan(&listed); err != nil {
		return false, fmt.Errorf("providerlisting: read stub list: %w", err)
	}
	return listed, nil
}

// SetListed puts us on the stub list or takes us off it.
func (l *StubList) SetListed(ctx context.Context, listed bool) error {
	if _, err := l.db.Exec(ctx, `UPDATE stub_trusted_list SET listed = $1`, listed); err != nil {
		return fmt.Errorf("providerlisting: set stub list: %w", err)
	}
	return nil
}
