package organization

import (
	"context"
	"fmt"

	"github.com/privacybydesign/yivi-businesswallet/backend/internal/database"
)

type Store struct {
	db database.DB
}

func NewStore(db database.DB) *Store {
	return &Store{db: db}
}

func (s *Store) GetByID(ctx context.Context, id int64) (Organization, error) {
	var org Organization
	err := s.db.QueryRow(ctx, "SELECT id, name FROM organizations WHERE id = $1", id).Scan(&org.ID, &org.Name)
	if err != nil {
		return Organization{}, fmt.Errorf("organization: get by id %d: %w", id, err)
	}
	return org, nil
}

func (s *Store) List(ctx context.Context) ([]Organization, error) {
	rows, err := s.db.Query(ctx, "SELECT id, name FROM organizations")
	if err != nil {
		return nil, fmt.Errorf("organization: list query: %w", err)
	}
	defer rows.Close()

	var orgs []Organization
	for rows.Next() {
		var org Organization
		if err := rows.Scan(&org.ID, &org.Name); err != nil {
			return nil, fmt.Errorf("organization: list scan: %w", err)
		}
		orgs = append(orgs, org)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("organization: list rows: %w", err)
	}

	return orgs, nil
}
