package registryprovider

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"log/slog"
	"math/big"
	"net/http"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/jackc/pgx/v5"

	"github.com/privacybydesign/yivi-businesswallet/backend/internal/database"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/eudiholder"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/statuslist"
)

// EBWOIDStatusListPath is the public path of the stub KVK's EBWOID status list.
const EBWOIDStatusListPath = "/api/v1/registry/ebwoid-status-list"

const (
	ebwoidListSize = 1 << 17
	ebwoidListBits = 1
	// ebwoidIndexAttempts bounds the retries for a random index that is taken.
	ebwoidIndexAttempts = 8
	ebwoidTokenTTL      = 24 * time.Hour
	// ebwoidCacheTTL bounds how late a revocation reaches a holder (CS-10
	// ISS-RV-05: at most an hour).
	ebwoidCacheTTL = time.Hour
)

const ebwoidStatusInvalid byte = 1

// ErrEBWOIDIndexExhausted means no free random index was found; the list is close to full.
var ErrEBWOIDIndexExhausted = errors.New("registryprovider: no free EBWOID status list index")

// StatusSigner signs a Status List Token as the EBWOID issuer.
type StatusSigner interface {
	Sign(typ string, claims jwt.MapClaims) (string, error)
}

// EBWOIDStatusList is the stub KVK's Token Status List of the EBWOIDs it issued
// (rb-ebwoid). Revoking a company invalidates every EBWOID it was issued, the
// signal a wallet acts on when the company ends (Art 6(2)).
type EBWOIDStatusList struct {
	db      database.DB
	signer  StatusSigner
	baseURL string
	now     func() time.Time
}

// NewEBWOIDStatusList serves the list under baseURL, the public URL of this backend.
func NewEBWOIDStatusList(db database.DB, signer StatusSigner, baseURL string) *EBWOIDStatusList {
	return &EBWOIDStatusList{db: db, signer: signer, baseURL: baseURL, now: time.Now}
}

// Take reserves a random free entry for an EBWOID issued to the company euid.
// The index is random so it says nothing about when the company joined.
func (l *EBWOIDStatusList) Take(ctx context.Context, euid string) (eudiholder.StatusReference, error) {
	const insert = `INSERT INTO ebwoid_status_entries (status_index, euid) VALUES ($1, $2)
		ON CONFLICT (status_index) DO NOTHING`
	limit := big.NewInt(ebwoidListSize)
	for range ebwoidIndexAttempts {
		n, err := rand.Int(rand.Reader, limit)
		if err != nil {
			return eudiholder.StatusReference{}, err
		}
		tag, err := l.db.Exec(ctx, insert, n.Int64(), euid)
		if err != nil {
			return eudiholder.StatusReference{}, fmt.Errorf("registryprovider: reserve EBWOID status entry: %w", err)
		}
		if tag.RowsAffected() == 1 {
			return eudiholder.StatusReference{URI: l.uri(), Index: n.Uint64()}, nil
		}
	}
	return eudiholder.StatusReference{}, ErrEBWOIDIndexExhausted
}

// RevokeCompany revokes every EBWOID issued to euid, as KVK does when the company
// ends. It reports how many entries it revoked; revoking is final.
func (l *EBWOIDStatusList) RevokeCompany(ctx context.Context, euid string) (int64, error) {
	const update = `UPDATE ebwoid_status_entries SET revoked_at = now() WHERE euid = $1 AND revoked_at IS NULL`
	tag, err := l.db.Exec(ctx, update, euid)
	if err != nil {
		return 0, fmt.Errorf("registryprovider: revoke EBWOIDs of %s: %w", euid, err)
	}
	return tag.RowsAffected(), nil
}

// StatusList is the signed Status List Token.
func (l *EBWOIDStatusList) StatusList(ctx context.Context) (string, error) {
	rows, err := l.db.Query(ctx, `SELECT status_index FROM ebwoid_status_entries WHERE revoked_at IS NOT NULL`)
	if err != nil {
		return "", fmt.Errorf("registryprovider: EBWOID status list: %w", err)
	}
	revoked, err := pgx.CollectRows(rows, pgx.RowTo[int64])
	if err != nil {
		return "", fmt.Errorf("registryprovider: EBWOID status list rows: %w", err)
	}
	statuses := make(map[int64]byte, len(revoked))
	for _, idx := range revoked {
		statuses[idx] = ebwoidStatusInvalid
	}
	lst, err := statuslist.Encode(statuses, ebwoidListSize, ebwoidListBits)
	if err != nil {
		return "", err
	}
	now := l.now()
	return l.signer.Sign(statuslist.TokenTyp, jwt.MapClaims{
		"sub":         l.uri(),
		"iat":         now.Unix(),
		"exp":         now.Add(ebwoidTokenTTL).Unix(),
		"ttl":         int64(ebwoidCacheTTL / time.Second),
		"status_list": map[string]any{"bits": ebwoidListBits, "lst": lst},
	})
}

func (l *EBWOIDStatusList) uri() string {
	return l.baseURL + EBWOIDStatusListPath
}

// EBWOIDStatusHandler serves the stub KVK's EBWOID status list, unauthenticated
// (CS-10 ISS-RV-04).
type EBWOIDStatusHandler struct {
	list *EBWOIDStatusList
}

func NewEBWOIDStatusHandler(list *EBWOIDStatusList) *EBWOIDStatusHandler {
	return &EBWOIDStatusHandler{list: list}
}

func (h *EBWOIDStatusHandler) Register(mux *http.ServeMux) {
	mux.HandleFunc("GET /registry/ebwoid-status-list", h.statusList)
}

func (h *EBWOIDStatusHandler) statusList(w http.ResponseWriter, r *http.Request) {
	token, err := h.list.StatusList(r.Context())
	if err != nil {
		slog.ErrorContext(r.Context(), "registryprovider: EBWOID status list", slog.Any("error", err))
		http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", statuslist.ContentType)
	if _, err := w.Write([]byte(token)); err != nil {
		slog.ErrorContext(r.Context(), "registryprovider: write EBWOID status list", slog.Any("error", err))
	}
}
