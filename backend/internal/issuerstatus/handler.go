package issuerstatus

import (
	"context"
	"crypto/subtle"
	"errors"
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"github.com/privacybydesign/yivi-businesswallet/backend/internal/openid4vciissuer"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/respond"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/statuslist"
)

const (
	// apiKeyHeader is how the EU reference issuer authenticates to its status
	// list service (its revocation.api_key).
	apiKeyHeader   = "X-Api-Key"
	maxTakeBody    = 4 << 10
	expiryDateForm = "2006-01-02"
)

type lists interface {
	Take(ctx context.Context, issuanceID string, expiresOn *time.Time) (map[string]any, error)
	StatusList(ctx context.Context, list int64) (string, error)
}

type Handler struct {
	lists  lists
	apiKey string
}

func NewHandler(svc *Service, apiKey string) *Handler {
	return &Handler{lists: svc, apiKey: apiKey}
}

func (h *Handler) Register(mux *http.ServeMux) {
	mux.HandleFunc("POST /attestations/status-lists/take", h.take)
	mux.HandleFunc("GET /attestations/status-lists/{list}", h.statusList)
}

// take answers the EU reference issuer's reservation (its revocation.take_url):
// a form with the offer's issuer_state and the credential's expiry_date.
func (h *Handler) take(w http.ResponseWriter, r *http.Request) {
	if subtle.ConstantTimeCompare([]byte(r.Header.Get(apiKeyHeader)), []byte(h.apiKey)) != 1 {
		http.Error(w, http.StatusText(http.StatusUnauthorized), http.StatusUnauthorized)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxTakeBody)
	if err := r.ParseForm(); err != nil {
		http.Error(w, "the request is not a form", http.StatusBadRequest)
		return
	}
	issuerState := r.PostForm.Get("issuer_state")
	if issuerState == "" {
		// An issuer without the issuer_state patch: the entry could not be tied to
		// a credential, so it could never be revoked.
		slog.ErrorContext(r.Context(), "issuerstatus: status list reservation without issuer_state; the credential is issued without a status")
		http.Error(w, "issuer_state is required", http.StatusBadRequest)
		return
	}
	var expiresOn *time.Time
	if raw := r.PostForm.Get("expiry_date"); raw != "" {
		t, err := time.Parse(expiryDateForm, raw)
		if err != nil {
			http.Error(w, "expiry_date is not a date", http.StatusBadRequest)
			return
		}
		expiresOn = &t
	}
	ref, err := h.lists.Take(r.Context(), openid4vciissuer.EudiIssuanceID(issuerState), expiresOn)
	if errors.Is(err, ErrUnknownOffer) {
		http.Error(w, "no open offer for that issuer_state", http.StatusNotFound)
		return
	}
	if err != nil {
		slog.ErrorContext(r.Context(), "issuerstatus: reserve entry", slog.Any("error", err))
		http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
		return
	}
	respond.JSON(w, r, http.StatusOK, ref)
}

// statusList serves a Status List Token, unauthenticated (CS-10 ISS-RV-04).
func (h *Handler) statusList(w http.ResponseWriter, r *http.Request) {
	list, err := strconv.ParseInt(r.PathValue("list"), 10, 64)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	token, err := h.lists.StatusList(r.Context(), list)
	if errors.Is(err, ErrUnknownList) {
		http.NotFound(w, r)
		return
	}
	if err != nil {
		slog.ErrorContext(r.Context(), "issuerstatus: status list", slog.Int64("list", list), slog.Any("error", err))
		http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", statuslist.ContentType)
	if _, err := w.Write([]byte(token)); err != nil {
		slog.ErrorContext(r.Context(), "issuerstatus: write status list", slog.Any("error", err))
	}
}
