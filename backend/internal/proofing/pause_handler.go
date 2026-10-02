package proofing

import (
	"net/http"
	"time"

	"github.com/google/uuid"

	"github.com/privacybydesign/yivi-businesswallet/backend/internal/auth"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/organization"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/respond"
)

// SetPlatformAdmins names who may pause any org's proofing; none when unset.
func (h *Handler) SetPlatformAdmins(admins auth.PlatformAdmins) { h.platformAdmins = admins }

// active refuses an org route while the org's proofing is paused.
func (h *Handler) active(next http.Handler) http.Handler {
	return respond.HandlerFunc(func(w http.ResponseWriter, r *http.Request) error {
		if err := h.service.checkActive(r.Context(), orgFromRequest(r).ID); err != nil {
			return mapError(err)
		}
		next.ServeHTTP(w, r)
		return nil
	})
}

// registerPause mounts the pause routes, which stay reachable while paused:
// the org's own switch, and the platform admin's over every org.
func (h *Handler) registerPause(mux *http.ServeMux) {
	member := func(next http.Handler) http.Handler { return h.requireUser(h.authorize(next)) }
	admin := func(next http.Handler) http.Handler {
		return h.requireUser(h.authorize(organization.RequireOrgAdmin(next)))
	}
	platform := func(next http.Handler) http.Handler {
		return h.requireUser(auth.RequirePlatformAdmin(h.platformAdmins)(next))
	}
	mux.Handle("GET /orgs/{slug}/identity-proofing/pause", member(respond.HandlerFunc(h.getPause)))
	mux.Handle("PUT /orgs/{slug}/identity-proofing/pause", admin(respond.HandlerFunc(h.setOrgPause)))
	mux.Handle("GET /admin/identity-proofing/pauses", platform(respond.HandlerFunc(h.listPauses)))
	mux.Handle("PUT /admin/organizations/{id}/identity-proofing/pause", platform(respond.HandlerFunc(h.setPlatformPause)))
}

type pauseResponse struct {
	OrganizationID uuid.UUID `json:"organizationId"`
	// Paused is whether proofing is stopped; PlatformPausedAt and OrgPausedAt
	// say by whom (the org's admin cannot lift a platform pause).
	Paused           bool       `json:"paused"`
	PlatformPausedAt *time.Time `json:"platformPausedAt,omitempty"`
	OrgPausedAt      *time.Time `json:"orgPausedAt,omitempty"`
}

func newPauseResponse(p OrgPause) pauseResponse {
	return pauseResponse{
		OrganizationID: p.OrganizationID, Paused: p.Paused(),
		PlatformPausedAt: p.PlatformPausedAt, OrgPausedAt: p.OrgPausedAt,
	}
}

type setPauseRequest struct {
	Paused *bool `json:"paused"`
}

func decodePause(r *http.Request) (bool, error) {
	var body setPauseRequest
	if err := decode(r, &body); err != nil {
		return false, err
	}
	if body.Paused == nil {
		return false, &respond.APIError{Status: http.StatusBadRequest, Code: "invalid_body", Message: "paused is required"}
	}
	return *body.Paused, nil
}

func (h *Handler) getPause(w http.ResponseWriter, r *http.Request) error {
	p, err := h.service.ProofingPause(r.Context(), orgFromRequest(r).ID)
	if err != nil {
		return mapError(err)
	}
	respond.JSON(w, r, http.StatusOK, newPauseResponse(p))
	return nil
}

func (h *Handler) setOrgPause(w http.ResponseWriter, r *http.Request) error {
	paused, err := decodePause(r)
	if err != nil {
		return err
	}
	p, err := h.service.SetProofingPaused(r.Context(), orgFromRequest(r).ID, PauseOrganization, paused)
	if err != nil {
		return mapError(err)
	}
	respond.JSON(w, r, http.StatusOK, newPauseResponse(p))
	return nil
}

type pauseListResponse struct {
	Pauses []pauseResponse `json:"pauses"`
}

func (h *Handler) listPauses(w http.ResponseWriter, r *http.Request) error {
	pauses, err := h.service.ProofingPauses(r.Context())
	if err != nil {
		return mapError(err)
	}
	out := pauseListResponse{Pauses: make([]pauseResponse, 0, len(pauses))}
	for _, p := range pauses {
		out.Pauses = append(out.Pauses, newPauseResponse(p))
	}
	respond.JSON(w, r, http.StatusOK, out)
	return nil
}

func (h *Handler) setPlatformPause(w http.ResponseWriter, r *http.Request) error {
	orgID, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		return &respond.APIError{Status: http.StatusBadRequest, Code: "invalid_id", Message: "invalid organisation id"}
	}
	paused, err := decodePause(r)
	if err != nil {
		return err
	}
	p, err := h.service.SetProofingPaused(r.Context(), orgID, PausePlatform, paused)
	if err != nil {
		return mapError(err)
	}
	respond.JSON(w, r, http.StatusOK, newPauseResponse(p))
	return nil
}
