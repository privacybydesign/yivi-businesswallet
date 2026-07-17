package eudiholder

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/url"
	"path/filepath"
	"strings"
	"sync"

	"github.com/google/uuid"
	irmastorage "github.com/privacybydesign/irmago/eudi/storage"
	"github.com/privacybydesign/irmago/eudi/storage/db/models"
	"github.com/privacybydesign/irmago/eudi/storage/filesystem"
	"gorm.io/datatypes"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

const (
	// schemaPrefix namespaces each organization's irmago holder tables in its own
	// Postgres schema. irmago's holder models carry no tenant column, so isolation
	// is per-schema, not per-row (see .ai/features/attestations.md §6.5).
	schemaPrefix = "holder_"
	// probeSchema is a reserved schema the boot Ping opens and migrates to prove
	// the whole path (schema create + connect + AutoMigrate) before serving.
	probeSchema = schemaPrefix + "probe"
	// engineMaxOpenConns bounds each per-org engine's pool so many orgs don't
	// exhaust Postgres connections (the per-org-engine isolation trade-off).
	engineMaxOpenConns = 4
	// fsKeyLabel domain-separates the per-org filesystem key derivation.
	fsKeyLabel = "ybw-eudiholder-fs-key-v1:"
	// emptyJSONPayload is the not-null default for a credential with no processed
	// payload yet (the seed uses it; real receive supplies the verified payload).
	emptyJSONPayload = "{}"
)

// Engine is the irmago-backed holder engine (ATTESTATION_HOLDER=irmago). It
// instantiates one irmago EUDI storage engine per organization, each isolated in
// its own Postgres schema (holder_<orghex>) on the shared database, opened
// lazily and cached.
//
// At-rest encryption: sqlcipher's per-field encryption of the raw credential is
// not available on Postgres, and irmago owns the write path, so the raw
// credential rests under the database's own posture (volume / TDE) — documented
// and accepted, not silently dropped (§6.5). Per-org app-level envelope
// encryption is deferred. The per-org filesystem key below protects irmago's
// on-disk trust material (logos/certs), which this phase does not yet write.
type Engine struct {
	dsn        string
	storageDir string
	masterKey  [32]byte

	mu      sync.Mutex
	engines map[uuid.UUID]irmastorage.Storage
}

// NewEngine builds the irmago-backed holder. dsn is the shared-database URL
// (search_path is set per org), storageDir is the base directory for irmago's
// per-org filesystem storage, and masterKey seeds per-org key derivation.
func NewEngine(dsn, storageDir string, masterKey [32]byte) *Engine {
	return &Engine{
		dsn:        dsn,
		storageDir: storageDir,
		masterKey:  masterKey,
		engines:    make(map[uuid.UUID]irmastorage.Storage),
	}
}

// Ping opens and migrates the reserved probe schema, then closes it, proving the
// per-org open path works at boot. Fatal on failure at startup.
func (e *Engine) Ping(ctx context.Context) error {
	st, err := e.open(ctx, probeSchema, e.deriveKey([]byte(probeSchema)))
	if err != nil {
		return fmt.Errorf("eudiholder: ping: %w", err)
	}
	return st.Close()
}

// Store persists the credential as a single-instance CredentialBatch in the
// org's holder engine and returns the created instance id.
func (e *Engine) Store(ctx context.Context, orgID uuid.UUID, cred Credential) (string, error) {
	eng, err := e.engineFor(ctx, orgID)
	if err != nil {
		return "", err
	}

	payload := cred.ProcessedPayload
	if len(payload) == 0 {
		payload = []byte(emptyJSONPayload)
	}
	batch := &models.CredentialBatch{
		IssuerURL:                cred.IssuerURL,
		VerifiableCredentialType: cred.VCT,
		Format:                   models.CredentialFormatSdJwtVc,
		Hash:                     cred.Hash,
		ProcessedSdJwtPayload:    datatypes.JSON(payload),
		IssuedAt:                 datatypes.NewNull(cred.IssuedAt),
		BatchSize:                1,
		RemainingCount:           1,
		CredentialIssuer:         cred.CredentialIssuer,
		Instances:                []models.IssuedCredentialInstance{{RawCredential: cred.RawToken}},
	}
	if cred.ExpiresAt != nil {
		batch.ExpiresAt = datatypes.NewNull(*cred.ExpiresAt)
	}

	if err := eng.Db().WithContext(ctx).Create(batch).Error; err != nil {
		return "", fmt.Errorf("eudiholder: store credential org %s: %w", orgID, err)
	}
	return batch.Instances[0].ID.String(), nil
}

// Delete removes the credential instance from the org's engine. A ref that is not
// a valid instance id, or that matches no row, is a no-op (the index owns the
// audit trail; the engine holds the live material).
func (e *Engine) Delete(ctx context.Context, orgID uuid.UUID, ref string) error {
	id, err := uuid.Parse(ref)
	if err != nil {
		return nil
	}
	eng, err := e.engineFor(ctx, orgID)
	if err != nil {
		return err
	}
	if err := eng.Db().WithContext(ctx).
		Where("id = ?", id).Delete(&models.IssuedCredentialInstance{}).Error; err != nil {
		return fmt.Errorf("eudiholder: delete credential %s org %s: %w", ref, orgID, err)
	}
	return nil
}

// Close releases every per-org engine.
func (e *Engine) Close() error {
	e.mu.Lock()
	defer e.mu.Unlock()
	var errs []error
	for orgID, st := range e.engines {
		if err := st.Close(); err != nil {
			errs = append(errs, fmt.Errorf("close engine org %s: %w", orgID, err))
		}
	}
	e.engines = make(map[uuid.UUID]irmastorage.Storage)
	if len(errs) > 0 {
		return fmt.Errorf("eudiholder: close: %w", errs[0])
	}
	return nil
}

func (e *Engine) engineFor(ctx context.Context, orgID uuid.UUID) (irmastorage.Storage, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if st, ok := e.engines[orgID]; ok {
		return st, nil
	}
	st, err := e.open(ctx, e.schemaFor(orgID), e.deriveKey(orgID[:]))
	if err != nil {
		return nil, err
	}
	e.engines[orgID] = st
	return st, nil
}

// open ensures the org's schema exists, then opens the irmago engine bound to it
// (AutoMigrate creates irmago's tables inside that schema).
func (e *Engine) open(ctx context.Context, schema string, key [32]byte) (irmastorage.Storage, error) {
	if err := e.ensureSchema(ctx, schema); err != nil {
		return nil, err
	}
	dsn, err := dsnWithSearchPath(e.dsn, schema)
	if err != nil {
		return nil, err
	}
	fs := filesystem.NewFileSystemStorage(key, filepath.Join(e.storageDir, schema))
	st, err := irmastorage.NewStorageWithDialector(postgres.Open(dsn), fs)
	if err != nil {
		return nil, fmt.Errorf("eudiholder: open engine schema %s: %w", schema, err)
	}
	if sqlDB, err := st.Db().DB(); err == nil {
		sqlDB.SetMaxOpenConns(engineMaxOpenConns)
	}
	return st, nil
}

// ensureSchema creates the org's Postgres schema on a short-lived connection to
// the default schema (irmago's AutoMigrate can only create tables in a schema
// that already exists).
func (e *Engine) ensureSchema(ctx context.Context, schema string) error {
	db, err := gorm.Open(postgres.Open(e.dsn), &gorm.Config{})
	if err != nil {
		return fmt.Errorf("eudiholder: open for schema create: %w", err)
	}
	defer func() {
		if sqlDB, err := db.DB(); err == nil {
			_ = sqlDB.Close()
		}
	}()
	if err := db.WithContext(ctx).Exec(`CREATE SCHEMA IF NOT EXISTS "` + schema + `"`).Error; err != nil {
		return fmt.Errorf("eudiholder: create schema %s: %w", schema, err)
	}
	return nil
}

// schemaFor is the per-org schema name: holder_<orgid-hex-without-hyphens>, a
// valid unquoted identifier.
func (e *Engine) schemaFor(orgID uuid.UUID) string {
	return schemaPrefix + hex.EncodeToString(orgID[:])
}

// deriveKey derives a per-label 32-byte key from the master key (HMAC-SHA256),
// so each org's filesystem storage uses a distinct key.
func (e *Engine) deriveKey(label []byte) [32]byte {
	mac := hmac.New(sha256.New, e.masterKey[:])
	mac.Write([]byte(fsKeyLabel))
	mac.Write(label)
	var key [32]byte
	copy(key[:], mac.Sum(nil))
	return key
}

// dsnWithSearchPath returns the base URL DSN with search_path set to schema; pgx
// forwards it as a startup runtime parameter, applied per pooled connection.
func dsnWithSearchPath(base, schema string) (string, error) {
	u, err := url.Parse(base)
	if err != nil {
		return "", fmt.Errorf("eudiholder: parse dsn: %w", err)
	}
	q := u.Query()
	q.Set("search_path", schema)
	u.RawQuery = q.Encode()
	return u.String(), nil
}

// ParseMasterKey decodes a hex-encoded 32-byte holder master key.
func ParseMasterKey(s string) ([32]byte, error) {
	var key [32]byte
	raw, err := hex.DecodeString(strings.TrimSpace(s))
	if err != nil {
		return key, fmt.Errorf("eudiholder: master key must be hex: %w", err)
	}
	if len(raw) != len(key) {
		return key, fmt.Errorf("eudiholder: master key must be %d bytes (%d hex chars), got %d", len(key), 2*len(key), len(raw))
	}
	copy(key[:], raw)
	return key, nil
}
