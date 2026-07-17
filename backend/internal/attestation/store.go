package attestation

import (
	"encoding/json"
	"fmt"

	"github.com/privacybydesign/yivi-businesswallet/backend/internal/audit"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/database"
)

const (
	uniqueViolation     = "23505"
	foreignKeyViolation = "23503"
)

// Store is the pgx-backed persistence for the attestation slice: schemas,
// templates, key material and the issuance ledger. Mutations audit in-tx.
type Store struct {
	db    database.DB
	audit audit.Recorder
}

func NewStore(db database.DB, recorder audit.Recorder) *Store {
	return &Store{db: db, audit: recorder}
}

// marshalJSON encodes a value for a jsonb column (pgx accepts the raw bytes).
func marshalJSON(v any) ([]byte, error) {
	b, err := json.Marshal(v)
	if err != nil {
		return nil, fmt.Errorf("attestation: marshal jsonb: %w", err)
	}
	return b, nil
}

// unmarshalAttributes decodes a schema attribute list from jsonb bytes.
func unmarshalAttributes(raw []byte) ([]AttributeDef, error) {
	attrs := []AttributeDef{}
	if len(raw) == 0 {
		return attrs, nil
	}
	if err := json.Unmarshal(raw, &attrs); err != nil {
		return nil, fmt.Errorf("attestation: decode attributes: %w", err)
	}
	return attrs, nil
}

// unmarshalNames decodes a schema's localized credential-display list from jsonb.
func unmarshalNames(raw []byte) ([]LocalizedName, error) {
	names := []LocalizedName{}
	if len(raw) == 0 {
		return names, nil
	}
	if err := json.Unmarshal(raw, &names); err != nil {
		return nil, fmt.Errorf("attestation: decode display: %w", err)
	}
	return names, nil
}

// unmarshalStringMap decodes a {key: value} jsonb object.
func unmarshalStringMap(raw []byte) (map[string]string, error) {
	if len(raw) == 0 {
		return map[string]string{}, nil
	}
	m := map[string]string{}
	if err := json.Unmarshal(raw, &m); err != nil {
		return nil, fmt.Errorf("attestation: decode string map: %w", err)
	}
	return m, nil
}
