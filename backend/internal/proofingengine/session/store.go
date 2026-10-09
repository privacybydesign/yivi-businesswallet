package session

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/privacybydesign/yivi-businesswallet/backend/internal/crypto"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/database"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/proofingprovider"
)

// ErrNotFound is an unknown session, or one under another tenant: scoping
// never tells the two apart.
var ErrNotFound = errors.New("session not found")

// errMisplaced is sealed data that is not its row's session: a row's data
// copied into another row (by someone with write access to the database) must
// not open there, or one org's evidence would read as another's.
var errMisplaced = errors.New("session: sealed data belongs to another row")

// Interface is what the engine needs from a session repository.
type Interface interface {
	Create(sess Session) (Session, error)
	Get(tenantID, id string) (Session, error)
	Authenticate(token string) (Session, error)
	AuthenticateSession(tenantID, id, token string) (Session, error)
	FindByHandover(tokenHash string) (Session, error)
	Update(tenantID, id string, fn func(*Session) error) (Session, error)
	Delete(tenantID, id string) error
	Purge(retention time.Duration, now time.Time) ([]Session, error)
	// SetOnExpire installs the hook a lazy expiry (a read that finds the
	// session past its deadline) reports through.
	SetOnExpire(fn func(Session))
}

// PostgresStore keeps sessions in identity_proofing_sessions: the lookup
// columns in the clear, the session itself (evidence included) as JSON sealed
// under the deployment's encryption key. A session's TenantID is its
// organization's id. Calls take no context; each runs under storeTimeout, so
// a stuck database never pins a request.
type PostgresStore struct {
	db       database.DB
	cipher   *crypto.Cipher
	onExpire func(Session)
}

// storeTimeout bounds each store call.
const storeTimeout = 10 * time.Second

// terminalStatusList is every Status.Terminal() value, for SQL IN (...).
const terminalStatusList = "'approved','rejected','expired','cancelled'"

// NewPostgresStore returns the session store on db, sealing with cipher. A
// nil cipher refuses every session with proofingprovider.ErrNoEncryptionKey.
func NewPostgresStore(db database.DB, cipher *crypto.Cipher) *PostgresStore {
	return &PostgresStore{db: db, cipher: cipher}
}

func (s *PostgresStore) SetOnExpire(fn func(Session)) { s.onExpire = fn }

// stored is the sealed form: Session plus its token, which Session never
// marshals on its own.
type stored struct {
	Session
	Token string `json:"token"`
}

func (s *PostgresStore) seal(sess Session) ([]byte, error) {
	if s.cipher == nil {
		return nil, proofingprovider.ErrNoEncryptionKey
	}
	plain, err := json.Marshal(stored{Session: sess.ForStorage(), Token: sess.Token})
	if err != nil {
		return nil, fmt.Errorf("session: encode: %w", err)
	}
	sealed, err := s.cipher.Encrypt(plain)
	if err != nil {
		return nil, fmt.Errorf("session: seal: %w", err)
	}
	return sealed, nil
}

// open unseals the data of the row with id and tenantID, refusing data sealed
// for another row.
func (s *PostgresStore) open(id, tenantID string, data []byte) (Session, error) {
	if s.cipher == nil {
		return Session{}, proofingprovider.ErrNoEncryptionKey
	}
	plain, err := s.cipher.Decrypt(data)
	if err != nil {
		return Session{}, fmt.Errorf("session: open: %w", err)
	}
	var st stored
	if err := json.Unmarshal(plain, &st); err != nil {
		return Session{}, fmt.Errorf("session: decode: %w", err)
	}
	if st.ID != id || st.TenantID != tenantID {
		return Session{}, errMisplaced
	}
	st.Session.Token = st.Token
	return st.Session, nil
}

func grantHash(g *HandoverGrant) string {
	if g == nil {
		return ""
	}
	return g.TokenHash
}

func ctx() (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.Background(), storeTimeout)
}

// Create persists a new session, assigning its id, token, AA challenge and
// timestamps. TenantID must be set; Status defaults to StatusCreated.
func (s *PostgresStore) Create(sess Session) (Session, error) {
	if sess.TenantID == "" {
		return Session{}, errors.New("session: tenantId is required")
	}
	if sess.Status == "" {
		sess.Status = StatusCreated
	}
	if !sess.Status.Valid() {
		return Session{}, fmt.Errorf("session: unknown status %q", sess.Status)
	}
	if sess.Flow == "" {
		sess.Flow = "default"
	}
	now := time.Now().UTC()
	sess.ID, sess.Token, sess.AAChallenge, sess.TenantReference = newID(), newToken(), newAAChallenge(), newTenantReference()
	sess.CreatedAt, sess.UpdatedAt = now, now
	data, err := s.seal(sess)
	if err != nil {
		return Session{}, err
	}
	c, cancel := ctx()
	defer cancel()
	_, err = s.db.Exec(c, `
		INSERT INTO identity_proofing_sessions (id, organization_id, token_hash, status, web_grant_hash, native_grant_hash,
			expires_at, completed_at, retention_override_seconds, data, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $11)`,
		sess.ID, sess.TenantID, HashAccessToken(sess.Token), string(sess.Status),
		grantHash(sess.Access.WebGrant), grantHash(sess.Access.NativeGrant), sess.ExpiresAt, sess.CompletedAt,
		int64(sess.RetentionOverride/time.Second), data, now)
	if err != nil {
		return Session{}, fmt.Errorf("session: create: %w", err)
	}
	return sess, nil
}

func (s *PostgresStore) get(query string, args ...any) (Session, error) {
	c, cancel := ctx()
	defer cancel()
	var id, tenantID string
	var data []byte
	if err := s.db.QueryRow(c, `SELECT id, organization_id::text, data FROM identity_proofing_sessions WHERE `+query, args...).
		Scan(&id, &tenantID, &data); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return Session{}, ErrNotFound
		}
		return Session{}, fmt.Errorf("session: read: %w", err)
	}
	sess, err := s.open(id, tenantID, data)
	if err != nil {
		return Session{}, err
	}
	return s.expireIfDue(sess), nil
}

func (s *PostgresStore) Get(tenantID, id string) (Session, error) {
	return s.get(`id = $1 AND organization_id::text = $2`, id, tenantID)
}

// Authenticate resolves the app-facing session token.
func (s *PostgresStore) Authenticate(token string) (Session, error) {
	if token == "" {
		return Session{}, ErrNotFound
	}
	return s.get(`token_hash = $1`, HashAccessToken(token))
}

// AuthenticateSession is Get plus the check that token is the session's.
func (s *PostgresStore) AuthenticateSession(tenantID, id, token string) (Session, error) {
	if token == "" {
		return Session{}, ErrNotFound
	}
	sess, err := s.Get(tenantID, id)
	if err != nil {
		return Session{}, err
	}
	if subtle.ConstantTimeCompare([]byte(sess.Token), []byte(token)) != 1 {
		return Session{}, ErrNotFound
	}
	return sess, nil
}

// FindByHandover looks up the session whose web or native grant hashes to tokenHash.
func (s *PostgresStore) FindByHandover(tokenHash string) (Session, error) {
	if tokenHash == "" {
		return Session{}, ErrNotFound
	}
	return s.get(`web_grant_hash = $1 OR native_grant_hash = $1`, tokenHash)
}

// expireIfDue moves a session found past its deadline to expired, once:
// only the read that wins the update reports it through onExpire.
func (s *PostgresStore) expireIfDue(sess Session) Session {
	now := time.Now().UTC()
	if !sess.IsExpired(now) {
		return sess
	}
	updated, err := s.Update(sess.TenantID, sess.ID, func(*Session) error { return nil })
	if err != nil {
		// The read still answers; the next read or the purge job expires it.
		slog.Warn("identity proofing: expire session on read", slog.String("session_id", sess.ID), slog.Any("error", err))
		return sess
	}
	return updated
}

// Update runs fn on the locked row and writes the result back. A session
// that is past its deadline is expired first (and reported), so fn never
// sees a stale open session.
func (s *PostgresStore) Update(tenantID, id string, fn func(*Session) error) (Session, error) {
	c, cancel := ctx()
	defer cancel()
	var updated, expired Session
	var expiredNow bool
	var fnErr error
	err := database.InTx(c, s.db, func(q database.Querier) error {
		var data []byte
		if err := q.QueryRow(c, `SELECT data FROM identity_proofing_sessions
			WHERE id = $1 AND organization_id::text = $2 FOR UPDATE`, id, tenantID).Scan(&data); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return ErrNotFound
			}
			return fmt.Errorf("session: read for update: %w", err)
		}
		sess, err := s.open(id, tenantID, data)
		if err != nil {
			return err
		}
		now := time.Now().UTC()
		if sess.IsExpired(now) {
			if err := sess.SetStatus(StatusExpired, now); err != nil {
				return err
			}
			expiredNow, expired = true, sess
		}
		updated = sess
		if fnErr = fn(&updated); fnErr != nil {
			if !expiredNow {
				return fnErr
			}
			// Keep the expiry even though fn refused.
			updated = expired
		}
		updated.UpdatedAt = now
		sealed, err := s.seal(updated)
		if err != nil {
			return err
		}
		_, err = q.Exec(c, `
			UPDATE identity_proofing_sessions SET status = $1, web_grant_hash = $2, native_grant_hash = $3,
				expires_at = $4, completed_at = $5, data = $6, updated_at = $7
			WHERE id = $8`,
			string(updated.Status), grantHash(updated.Access.WebGrant), grantHash(updated.Access.NativeGrant),
			updated.ExpiresAt, updated.CompletedAt, sealed, updated.UpdatedAt, id)
		if err != nil {
			return fmt.Errorf("session: update: %w", err)
		}
		return nil
	})
	if err != nil {
		return Session{}, err
	}
	if expiredNow && s.onExpire != nil {
		s.onExpire(expired)
	}
	if fnErr != nil {
		return Session{}, fnErr
	}
	return updated, nil
}

func (s *PostgresStore) Delete(tenantID, id string) error {
	c, cancel := ctx()
	defer cancel()
	tag, err := s.db.Exec(c, `DELETE FROM identity_proofing_sessions WHERE id = $1 AND organization_id::text = $2`, id, tenantID)
	if err != nil {
		return fmt.Errorf("session: delete: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// purgeBatch bounds how many finished sessions one DELETE removes, so each
// batch runs under its own storeTimeout however many are due.
const purgeBatch = 100

// Purge expires every open session past its deadline (reporting each), then
// removes finished sessions older than their own retention override, or
// retention when they have none and it is positive, and returns them. Each
// step has its own storeTimeout: the listing, every expiry (Update) and every
// batch of removals, so a large backlog is worked off rather than cut short.
func (s *PostgresStore) Purge(retention time.Duration, now time.Time) ([]Session, error) {
	due, err := s.listDue(now)
	if err != nil {
		return nil, err
	}
	for _, k := range due {
		// Update expires (and reports) it; a concurrent writer may have won.
		if _, err := s.Update(k.tenant, k.id, func(*Session) error { return nil }); err != nil && !errors.Is(err, ErrNotFound) {
			slog.Warn("identity proofing: expire due session", slog.String("session_id", k.id), slog.Any("error", err))
		}
	}
	var removed []Session
	for {
		batch, err := s.removeExpired(retention, now)
		removed = append(removed, batch...)
		if err != nil || len(batch) < purgeBatch {
			return removed, err
		}
	}
}

type sessionKey struct{ id, tenant string }

// listDue is every open session past its deadline.
func (s *PostgresStore) listDue(now time.Time) ([]sessionKey, error) {
	c, cancel := ctx()
	defer cancel()
	rows, err := s.db.Query(c, `SELECT id, organization_id::text FROM identity_proofing_sessions
		WHERE status NOT IN (`+terminalStatusList+`, 'needs_review') AND expires_at < $1`, now)
	if err != nil {
		return nil, fmt.Errorf("session: list due: %w", err)
	}
	defer rows.Close()
	var due []sessionKey
	for rows.Next() {
		var k sessionKey
		if err := rows.Scan(&k.id, &k.tenant); err != nil {
			return nil, fmt.Errorf("session: list due: %w", err)
		}
		due = append(due, k)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("session: list due: %w", err)
	}
	return due, nil
}

// removeExpired deletes up to purgeBatch finished sessions past their
// retention and returns them. A session's own (its flow's) retention applies
// whatever the default; a default of 0 keeps the rest until they are deleted.
func (s *PostgresStore) removeExpired(retention time.Duration, now time.Time) ([]Session, error) {
	c, cancel := ctx()
	defer cancel()
	rows, err := s.db.Query(c, `
		DELETE FROM identity_proofing_sessions WHERE id IN (
			SELECT id FROM identity_proofing_sessions
			WHERE status IN (`+terminalStatusList+`)
				AND (retention_override_seconds > 0 OR $2 > 0)
				AND COALESCE(completed_at, updated_at) <= $1::timestamptz - make_interval(secs =>
					CASE WHEN retention_override_seconds > 0 THEN retention_override_seconds ELSE $2 END)
			LIMIT $3)
		RETURNING id, organization_id::text, data`, now, int64(retention/time.Second), purgeBatch)
	if err != nil {
		return nil, fmt.Errorf("session: purge: %w", err)
	}
	defer rows.Close()
	var removed []Session
	for rows.Next() {
		var id, tenant string
		var data []byte
		if err := rows.Scan(&id, &tenant, &data); err != nil {
			return removed, fmt.Errorf("session: purge: %w", err)
		}
		sess, err := s.open(id, tenant, data)
		if err != nil {
			// Gone all the same: reported by id, so the purge is still recorded.
			slog.Warn("identity proofing: purged a session that does not open", slog.String("session_id", id), slog.Any("error", err))
			sess = Session{ID: id, TenantID: tenant}
		}
		removed = append(removed, sess)
	}
	if err := rows.Err(); err != nil {
		return removed, fmt.Errorf("session: purge: %w", err)
	}
	return removed, nil
}
