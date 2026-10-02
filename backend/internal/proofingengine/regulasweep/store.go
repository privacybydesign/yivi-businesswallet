package regulasweep

import (
	"context"
	"fmt"
	"time"

	"github.com/privacybydesign/yivi-businesswallet/backend/internal/database"
)

// maxErrorLength caps the last error kept per tag.
const maxErrorLength = 500

// Store is the Postgres Queue (identity_proofing_regula_sweeps).
type Store struct {
	db database.Querier
}

// NewStore returns a Queue on db.
func NewStore(db database.Querier) *Store { return &Store{db: db} }

func (s *Store) Add(ctx context.Context, tag string, dueAt time.Time) error {
	_, err := s.db.Exec(ctx, `
		INSERT INTO identity_proofing_regula_sweeps (tag, due_at) VALUES ($1, $2)
		ON CONFLICT (tag) DO UPDATE SET due_at = GREATEST(identity_proofing_regula_sweeps.due_at, EXCLUDED.due_at)
		WHERE identity_proofing_regula_sweeps.attempts = 0`, tag, dueAt)
	if err != nil {
		return fmt.Errorf("regulasweep: queue: %w", err)
	}
	return nil
}

// Due leases the due tags by moving them MinBackoff ahead, so two sweepers
// never take the same one.
func (s *Store) Due(ctx context.Context, now time.Time, limit int) ([]Entry, error) {
	rows, err := s.db.Query(ctx, `
		UPDATE identity_proofing_regula_sweeps SET due_at = $2
		WHERE tag IN (
			SELECT tag FROM identity_proofing_regula_sweeps WHERE due_at <= $1
			ORDER BY due_at LIMIT $3 FOR UPDATE SKIP LOCKED)
		RETURNING tag, due_at, attempts`, now, now.Add(MinBackoff), limit)
	if err != nil {
		return nil, fmt.Errorf("regulasweep: list due: %w", err)
	}
	defer rows.Close()
	var out []Entry
	for rows.Next() {
		var e Entry
		if err := rows.Scan(&e.Tag, &e.DueAt, &e.Attempts); err != nil {
			return nil, fmt.Errorf("regulasweep: list due: %w", err)
		}
		out = append(out, e)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("regulasweep: list due: %w", err)
	}
	return out, nil
}

func (s *Store) Done(ctx context.Context, tag string) error {
	if _, err := s.db.Exec(ctx, `DELETE FROM identity_proofing_regula_sweeps WHERE tag = $1`, tag); err != nil {
		return fmt.Errorf("regulasweep: finish: %w", err)
	}
	return nil
}

func (s *Store) Retry(ctx context.Context, tag string, next time.Time, lastError string) error {
	if len(lastError) > maxErrorLength {
		lastError = lastError[:maxErrorLength]
	}
	_, err := s.db.Exec(ctx, `
		UPDATE identity_proofing_regula_sweeps SET due_at = $2, attempts = attempts + 1, last_error = $3
		WHERE tag = $1`, tag, next, lastError)
	if err != nil {
		return fmt.Errorf("regulasweep: retry: %w", err)
	}
	return nil
}
