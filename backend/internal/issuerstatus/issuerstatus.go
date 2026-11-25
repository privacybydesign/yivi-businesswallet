// Package issuerstatus is the Token Status List (CS-10) of the credentials the EU
// reference issuer issues for this deployment. That issuer runs no status list of
// its own: it reserves an entry here for each credential it signs (Take), and an
// entry reads invalid once the credential's issued_attestations row is revoked.
// Revoking is therefore the ledger revoke alone, and like it, final.
package issuerstatus

import (
	"context"
	"errors"
	"strconv"
	"time"

	"github.com/golang-jwt/jwt/v5"

	"github.com/privacybydesign/yivi-businesswallet/backend/internal/statuslist"
)

const (
	// listSize entries per list at listBits each: 16 KiB uncompressed, mostly
	// zeros on the wire.
	listSize  = 1 << 17
	listBits  = 1
	listCount = 16
	// tokenTTL bounds how long a fetched list may be used; cacheTTL is how long a
	// relying party may cache it, which also bounds how late a revocation shows
	// (CS-10 ISS-RV-05: at most an hour).
	tokenTTL = 24 * time.Hour
	cacheTTL = time.Hour

	// ListPath is the public form of GET /attestations/status-lists/{list}.
	ListPath = "/api/v1/attestations/status-lists/"
)

const statusInvalid byte = 1

var (
	// ErrUnknownOffer means no open offer has the issuance id Take was given.
	ErrUnknownOffer = errors.New("issuerstatus: no open offer with that issuance id")
	// ErrUnknownList means the list number is outside the lists this deployment keeps.
	ErrUnknownList = errors.New("issuerstatus: unknown status list")
	// ErrIndexExhausted means no free random index was found; the lists are close to full.
	ErrIndexExhausted = errors.New("issuerstatus: no free status list index")
)

// Signer signs the Status List Tokens: the EU reference issuer's own key, so a
// relying party finds the same signer on the list as on the credential.
type Signer interface {
	Sign(typ string, claims jwt.MapClaims) (string, error)
}

type entryStore interface {
	take(ctx context.Context, issuanceID string, expiresOn *time.Time) (int64, error)
	taken(ctx context.Context, issuanceID string) (bool, error)
	revoked(ctx context.Context, list int64) ([]int64, error)
}

type Service struct {
	store   entryStore
	signer  Signer
	baseURL string
	now     func() time.Time
}

// NewService serves the lists under baseURL, the public URL of this backend.
func NewService(store *Store, signer Signer, baseURL string) *Service {
	return &Service{store: store, signer: signer, baseURL: baseURL, now: time.Now}
}

// Take reserves a random free entry for one credential of the offer issuanceID
// and returns the status claim the issuer embeds in it.
func (s *Service) Take(ctx context.Context, issuanceID string, expiresOn *time.Time) (map[string]any, error) {
	idx, err := s.store.take(ctx, issuanceID, expiresOn)
	if err != nil {
		return nil, err
	}
	return map[string]any{"status_list": map[string]any{
		"idx": idx % listSize,
		"uri": s.listURI(idx / listSize),
	}}, nil
}

// Taken reports whether the issuer reserved an entry for the offer, which it only
// does while signing a credential a wallet collected.
func (s *Service) Taken(ctx context.Context, issuanceID string) (bool, error) {
	return s.store.taken(ctx, issuanceID)
}

// StatusList is the signed Status List Token for list.
func (s *Service) StatusList(ctx context.Context, list int64) (string, error) {
	if list < 0 || list >= listCount {
		return "", ErrUnknownList
	}
	revoked, err := s.store.revoked(ctx, list)
	if err != nil {
		return "", err
	}
	statuses := make(map[int64]byte, len(revoked))
	for _, idx := range revoked {
		statuses[idx] = statusInvalid
	}
	lst, err := statuslist.Encode(statuses, listSize, listBits)
	if err != nil {
		return "", err
	}
	now := s.now()
	return s.signer.Sign(statuslist.TokenTyp, jwt.MapClaims{
		"sub":         s.listURI(list),
		"iat":         now.Unix(),
		"exp":         now.Add(tokenTTL).Unix(),
		"ttl":         int64(cacheTTL / time.Second),
		"status_list": map[string]any{"bits": listBits, "lst": lst},
	})
}

func (s *Service) listURI(list int64) string {
	return s.baseURL + ListPath + strconv.FormatInt(list, 10)
}
