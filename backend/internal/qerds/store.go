package qerds

import (
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/audit"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/database"
)

const (
	uniqueViolation = "23505"
)

// Store is the pgx-backed persistence for the qerds slice: messages, their
// append-only evidence, and organization digital addresses.
type Store struct {
	db    database.DB
	audit audit.Recorder
}

func NewStore(db database.DB, recorder audit.Recorder) *Store {
	return &Store{db: db, audit: recorder}
}
