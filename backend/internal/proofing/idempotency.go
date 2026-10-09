package proofing

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/privacybydesign/yivi-businesswallet/backend/internal/crypto"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/database"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/respond"
)

const (
	idempotencyHeader = "Idempotency-Key"
	// maxIdempotencyKeyLength bounds the key a customer sends.
	maxIdempotencyKeyLength = 255
	// maxIdempotentBody bounds the request body hashed; the handlers' own
	// decoders cap it further.
	maxIdempotentBody = 1 << 20
	// idempotencyKeyTTL is how long an answer is kept for a retry. A claim
	// whose call was lost (its replica died mid-call, or its answer could not
	// be stored) is kept as long: nothing tells whether that call created its
	// session, so the key is never run again before Prune drops it.
	idempotencyKeyTTL = 24 * time.Hour
)

var (
	errIdempotencyMismatch = errors.New("proofing: idempotency key reused with another request")
	errIdempotencyInFlight = errors.New("proofing: idempotency key still in flight")
)

// storedAnswer is the first answer to a key, replayed to its retries.
type storedAnswer struct {
	Status int
	Body   []byte
}

// IdempotencyStore keeps customer-API answers by Idempotency-Key, sealed: an
// answer can carry a hosted link and the subject's name and e-mail.
type IdempotencyStore struct {
	db     database.DB
	cipher *crypto.Cipher
}

func NewIdempotencyStore(db database.DB, cipher *crypto.Cipher) *IdempotencyStore {
	return &IdempotencyStore{db: db, cipher: cipher}
}

// begin claims key for a call with hash. It answers nil to run the call, the
// stored answer to replay it, or errIdempotencyMismatch / errIdempotencyInFlight.
func (s *IdempotencyStore) begin(ctx context.Context, customerID uuid.UUID, key string, hash []byte) (*storedAnswer, error) {
	if s.cipher == nil {
		return nil, ErrNoEncryptionKey
	}
	tag, err := s.db.Exec(ctx, `INSERT INTO identity_proofing_idempotency_keys (organization_id, customer_id, key, request_hash)
		SELECT c.organization_id, c.id, $2, $3 FROM identity_proofing_customers c WHERE c.id = $1
		ON CONFLICT DO NOTHING`, customerID, key, hash)
	if err != nil {
		return nil, fmt.Errorf("proofing: claim idempotency key: %w", err)
	}
	if tag.RowsAffected() == 1 {
		return nil, nil
	}
	var stored []byte
	var status *int
	var body []byte
	err = s.db.QueryRow(ctx, `SELECT request_hash, status_code, response FROM identity_proofing_idempotency_keys
		WHERE customer_id = $1 AND key = $2`, customerID, key).Scan(&stored, &status, &body)
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		// Abandoned between the insert and this read: the caller retries.
		return nil, errIdempotencyInFlight
	case err != nil:
		return nil, fmt.Errorf("proofing: read idempotency key: %w", err)
	case !bytes.Equal(stored, hash):
		return nil, errIdempotencyMismatch
	case status == nil:
		return nil, errIdempotencyInFlight
	}
	plain, err := s.cipher.Decrypt(body)
	if err != nil {
		return nil, fmt.Errorf("proofing: open idempotent answer: %w", err)
	}
	return &storedAnswer{Status: *status, Body: plain}, nil
}

// finish stores a's answer to key, linked to the session the answer names so
// that purging that session drops it too.
func (s *IdempotencyStore) finish(ctx context.Context, customerID uuid.UUID, key string, a storedAnswer) error {
	sealed, err := s.cipher.Encrypt(a.Body)
	if err != nil {
		return fmt.Errorf("proofing: seal idempotent answer: %w", err)
	}
	if _, err := s.db.Exec(ctx, `UPDATE identity_proofing_idempotency_keys
		SET status_code = $3, response = $4, request_id = $5
		WHERE customer_id = $1 AND key = $2`, customerID, key, a.Status, sealed, answerRequestID(a.Body)); err != nil {
		return fmt.Errorf("proofing: store idempotent answer: %w", err)
	}
	return nil
}

// answerRequestID is the session an answer names by its top-level id (every
// idempotent route answers with one), or nil.
func answerRequestID(body []byte) *uuid.UUID {
	var named struct {
		ID string `json:"id"`
	}
	if json.Unmarshal(body, &named) != nil {
		return nil
	}
	id, ok := parsePublicSessionID(named.ID)
	if !ok {
		return nil
	}
	return &id
}

// abandon releases key after a call that failed, so a retry runs it again.
func (s *IdempotencyStore) abandon(ctx context.Context, customerID uuid.UUID, key string) error {
	if _, err := s.db.Exec(ctx, `DELETE FROM identity_proofing_idempotency_keys
		WHERE customer_id = $1 AND key = $2 AND status_code IS NULL`, customerID, key); err != nil {
		return fmt.Errorf("proofing: release idempotency key: %w", err)
	}
	return nil
}

// Prune drops answers older than idempotencyKeyTTL.
func (s *IdempotencyStore) Prune(ctx context.Context) (int64, error) {
	tag, err := s.db.Exec(ctx, `DELETE FROM identity_proofing_idempotency_keys WHERE created_at < now() - $1::interval`,
		idempotencyKeyTTL.String())
	if err != nil {
		return 0, fmt.Errorf("proofing: prune idempotency keys: %w", err)
	}
	return tag.RowsAffected(), nil
}

// answerRecorder holds a handler's answer so it can be stored before it is sent.
type answerRecorder struct {
	header http.Header
	status int
	body   bytes.Buffer
}

func (a *answerRecorder) Header() http.Header         { return a.header }
func (a *answerRecorder) Write(b []byte) (int, error) { return a.body.Write(b) }
func (a *answerRecorder) WriteHeader(status int)      { a.status = status }

// idempotent runs a customer-API POST at most once per Idempotency-Key: a retry
// with the same key and body gets the first successful answer again. A call
// that errs or panics keeps no answer, so it can be retried. A call that may
// have had its effect but has no stored answer (its answer could not be
// stored, or its replica died) keeps the key in flight until it expires, so a
// retry can never run it twice. Without the header, or without a store, the
// call just runs.
func (h *Handler) idempotent(next respond.HandlerFunc) respond.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) error {
		key := r.Header.Get(idempotencyHeader)
		if key == "" || h.idempotency == nil {
			return next(w, r)
		}
		if len(key) > maxIdempotencyKeyLength {
			return &respond.APIError{Status: http.StatusBadRequest, Code: "invalid_input", Message: "the Idempotency-Key is too long"}
		}
		body, err := io.ReadAll(io.LimitReader(r.Body, maxIdempotentBody+1))
		if err != nil {
			return &respond.APIError{Status: http.StatusBadRequest, Code: "invalid_input", Message: "could not read the request body"}
		}
		// A body cut at the limit would hash, and then decode, as another one.
		if len(body) > maxIdempotentBody {
			return &respond.APIError{Status: http.StatusRequestEntityTooLarge, Code: "body_too_large", Message: "the request body is too large"}
		}
		r.Body = io.NopCloser(bytes.NewReader(body))
		sum := sha256.Sum256(append([]byte(r.Method+" "+r.URL.Path+"\n"), body...))
		customerID := callerFromContext(r.Context()).CustomerID
		replay, err := h.idempotency.begin(r.Context(), customerID, key, sum[:])
		switch {
		case errors.Is(err, errIdempotencyMismatch):
			return &respond.APIError{Status: http.StatusUnprocessableEntity, Code: "idempotency_key_reused", Message: "this Idempotency-Key was used for another request"}
		case errors.Is(err, errIdempotencyInFlight):
			return &respond.APIError{Status: http.StatusConflict, Code: "idempotency_in_flight", Message: "a request with this Idempotency-Key is still running or its outcome is unknown; look the session up or use a new key"}
		case err != nil:
			return err
		case replay != nil:
			w.Header().Set(headerContentType, contentTypeJSON)
			w.Header().Set(headerIdempotentReplayed, "true")
			w.WriteHeader(replay.Status)
			_, err := w.Write(replay.Body)
			return err
		}
		// A call that panics keeps no answer either: release the key and let
		// the recoverer answer the panic.
		defer func() {
			if p := recover(); p != nil {
				if err := h.idempotency.abandon(context.WithoutCancel(r.Context()), customerID, key); err != nil {
					slog.ErrorContext(r.Context(), "release idempotency key after a panic", "customer_id", customerID, "error", err)
				}
				panic(p)
			}
		}()
		rec := &answerRecorder{header: w.Header(), status: http.StatusOK}
		if err := next(rec, r); err != nil {
			// A context of its own: the key must be released even if the call was cancelled.
			if abandonErr := h.idempotency.abandon(context.WithoutCancel(r.Context()), customerID, key); abandonErr != nil {
				return errors.Join(err, abandonErr)
			}
			return err
		}
		// The call had its effect: an answer that cannot be stored keeps the key
		// in flight rather than freeing it, and the caller still gets its answer.
		if err := h.idempotency.finish(context.WithoutCancel(r.Context()), customerID, key,
			storedAnswer{Status: rec.status, Body: rec.body.Bytes()}); err != nil {
			slog.ErrorContext(r.Context(), "store idempotent answer", "customer_id", customerID, "error", err)
		}
		w.WriteHeader(rec.status)
		_, err = w.Write(rec.body.Bytes())
		return err
	}
}
