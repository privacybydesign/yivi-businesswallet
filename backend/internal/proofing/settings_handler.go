package proofing

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/privacybydesign/yivi-businesswallet/backend/internal/respond"
)

// settingsResponse is what the org set once for all its flows: how long a
// session runs and how long its data is kept, its branding and the origins its
// hosted pages may redirect to and be embedded on.
type settingsResponse struct {
	// SessionTTLSeconds is how long a mailed session runs; DataRetentionDays how
	// long an approved external person's proofed name is kept.
	SessionTTLSeconds int `json:"sessionTtlSeconds"`
	DataRetentionDays int `json:"dataRetentionDays"`
	// ErasureHandling is how its "delete my data" requests are decided.
	ErasureHandling ErasureHandling  `json:"erasureHandling"`
	Branding        brandingResponse `json:"branding"`
	// LegalRetention is the least the org keeps an identity check's data, by
	// law; null when none.
	LegalRetention *legalRetentionBody `json:"legalRetention"`
	// AllowedRedirectOrigins are where a hosted page may send its subject back
	// to and be embedded on; never null, the admin UI edits it as a list.
	AllowedRedirectOrigins []string `json:"allowedRedirectOrigins"`
}

// legalRetentionBody is a legal retention on the wire: how many months, and
// the reason the person is shown in each language.
type legalRetentionBody struct {
	Months   int    `json:"months"`
	ReasonEN string `json:"reasonEn"`
	ReasonNL string `json:"reasonNl"`
}

func newLegalRetentionBody(l LegalRetention) *legalRetentionBody {
	if !l.set() {
		return nil
	}
	body := legalRetentionBody(l)
	return &body
}

func newSettingsResponse(slug string, s Settings) settingsResponse {
	return settingsResponse{
		SessionTTLSeconds:      int(s.Session.SessionTTL.Seconds()),
		DataRetentionDays:      s.Session.DataRetentionDays,
		ErasureHandling:        s.Session.ErasureHandling,
		Branding:               newBrandingResponse(slug, s),
		LegalRetention:         newLegalRetentionBody(s.LegalRetention),
		AllowedRedirectOrigins: append([]string{}, s.RedirectOrigins...),
	}
}

func (h *Handler) respondSettings(w http.ResponseWriter, r *http.Request, s Settings) error {
	respond.JSON(w, r, http.StatusOK, newSettingsResponse(r.PathValue("slug"), s))
	return nil
}

func (h *Handler) getSettings(w http.ResponseWriter, r *http.Request) error {
	s, err := h.service.Settings(r.Context(), orgFromRequest(r).ID)
	if err != nil {
		return mapError(err)
	}
	return h.respondSettings(w, r, s)
}

// updateSettingsRequest changes the org's session settings, legal retention
// and/or redirect origins; an absent field is left as it is.
type updateSettingsRequest struct {
	SessionTTLSeconds *int `json:"sessionTtlSeconds"`
	DataRetentionDays *int `json:"dataRetentionDays"`
	// ErasureHandling is review, automatic or keep (ErasureHandling).
	ErasureHandling *ErasureHandling `json:"erasureHandling"`
	// LegalRetention replaces the legal retention, and null removes it; absent
	// leaves it. Kept raw, since absent and null differ.
	LegalRetention json.RawMessage `json:"legalRetention"`
	// AllowedRedirectOrigins replaces the list; absent leaves it.
	AllowedRedirectOrigins *[]string `json:"allowedRedirectOrigins"`
}

func (h *Handler) updateSettings(w http.ResponseWriter, r *http.Request) error {
	var body updateSettingsRequest
	if err := decode(r, &body); err != nil {
		return err
	}
	if body.SessionTTLSeconds == nil && body.DataRetentionDays == nil && body.ErasureHandling == nil &&
		body.LegalRetention == nil && body.AllowedRedirectOrigins == nil {
		return &respond.APIError{Status: http.StatusBadRequest, Code: "invalid_body", Message: "nothing to update"}
	}

	change := SettingsChange{
		DataRetentionDays: body.DataRetentionDays, ErasureHandling: body.ErasureHandling,
		RedirectOrigins: body.AllowedRedirectOrigins,
	}
	if body.LegalRetention != nil {
		legal, err := decodeLegalRetention(body.LegalRetention)
		if err != nil {
			return err
		}
		change.LegalRetention = &legal
	}
	if body.SessionTTLSeconds != nil {
		ttl := time.Duration(*body.SessionTTLSeconds) * time.Second
		change.SessionTTL = &ttl
	}
	settings, err := h.service.UpdateSettings(r.Context(), orgFromRequest(r).ID, change)
	if err != nil {
		return mapError(err)
	}

	return h.respondSettings(w, r, settings)
}

// decodeLegalRetention reads a legalRetention field: null is the zero value,
// which removes it.
func decodeLegalRetention(raw json.RawMessage) (LegalRetention, error) {
	if bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		return LegalRetention{}, nil
	}
	var body legalRetentionBody
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&body); err != nil {
		return LegalRetention{}, &respond.APIError{Status: http.StatusBadRequest, Code: "invalid_body", Message: "invalid legal retention"}
	}
	legal := LegalRetention(body)
	if legal == (LegalRetention{}) {
		return legal, mapError(fmt.Errorf("%w: give the legal retention its months and reasons, or null to remove it", ErrInvalidInput))
	}
	return legal, nil
}
