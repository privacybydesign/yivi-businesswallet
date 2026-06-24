package organization

import (
	"context"

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
	return org, err
}

func (s *Store) List(ctx context.Context) ([]Organization, error) {
	rows, err := s.db.Query(ctx, "SELECT id, name FROM organizations")
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var orgs []Organization
	for rows.Next() {
		var org Organization
		if err := rows.Scan(&org.ID, &org.Name); err != nil {
			return nil, err
		}
		orgs = append(orgs, org)
	}

	return orgs, rows.Err()
}
