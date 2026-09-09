package openid4vppresenter

import (
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"

	"github.com/privacybydesign/yivi-businesswallet/backend/internal/auth"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/organization"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/respond"
)

// maxStartBody caps the start request body: four short URL-ish strings.
const maxStartBody = 16 << 10

// Handler serves the inbound-presentation API. Three shapes on purpose (see the
// design): start/status/orgs are ordinary /api/v1 routes, select composes the
// tenant seam (requireUser → authorize) like every org-scoped route, and the
// wallet-metadata document goes on the root mux via RegisterRoot. GET /openid4vp
// itself — the address a verifier redirects to — needs no backend route: the SPA
// fallback serves it and the frontend drives the flow from there.
type Handler struct {
	svc         *Service
	metadata    *MetadataHandler
	requireUser func(http.Handler) http.Handler
	authorize   func(http.Handler) http.Handler
}

func NewHandler(svc *Service, metadata *MetadataHandler, requireUser, authorize func(http.Handler) http.Handler) *Handler {
	return &Handler{svc: svc, metadata: metadata, requireUser: requireUser, authorize: authorize}
}

func (h *Handler) Register(mux *http.ServeMux) {
	mux.Handle("POST /openid4vp/start", respond.HandlerFunc(h.start))
	mux.Handle("GET /openid4vp/{id}/status", respond.HandlerFunc(h.status))
	mux.Handle("GET /openid4vp/{id}/orgs", h.requireUser(respond.HandlerFunc(h.orgs)))
	mux.Handle("POST /orgs/{slug}/openid4vp/{id}/select", h.requireUser(h.authorize(respond.HandlerFunc(h.selectOrg))))
}

// RegisterRoot mounts the wallet-metadata document outside /api/v1: it is
// fetched by verifier software, has no API version, and must not fall through
// to the SPA's index.html.
func (h *Handler) RegisterRoot(mux *http.ServeMux) {
	h.metadata.RegisterRoot(mux)
}

type startRequest struct {
	ClientID         string `json:"clientId"`
	RequestURI       string `json:"requestUri"`
	RequestURIMethod string `json:"requestUriMethod"`
	Request          string `json:"request"`
}

type startResponse struct {
	ID string `json:"id"`
}

func (h *Handler) start(w http.ResponseWriter, r *http.Request) error {
	var req startRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxStartBody)).Decode(&req); err != nil {
		return &respond.APIError{Status: http.StatusBadRequest, Code: "invalid_body", Message: "invalid JSON body"}
	}
	id, err := h.svc.Start(r.Context(), StartRequest(req))
	if err != nil {
		if errors.Is(err, ErrRequestURIUnreachable) {
			// The response says only that the fetch failed; the reason (which
			// never includes the URL, see do) belongs in the log.
			slog.WarnContext(r.Context(), "openid4vp request_uri fetch failed", slog.String("error", err.Error()))
		}
		return mapError(err)
	}
	respond.JSON(w, r, http.StatusCreated, startResponse{ID: id})
	return nil
}

type statusResponse struct {
	Status   string `json:"status"`
	Verifier string `json:"verifier"`
}

func (h *Handler) status(w http.ResponseWriter, r *http.Request) error {
	v, err := h.svc.Status(r.Context(), r.PathValue("id"))
	if err != nil {
		return mapError(err)
	}
	respond.JSON(w, r, http.StatusOK, statusResponse(v))
	return nil
}

// orgSummary is the picker's row: what identifies the organization to its
// member. The slug only ever appears here — after authentication — never on the
// public /openid4vp surface.
type orgSummary struct {
	Slug    string `json:"slug"`
	Name    string `json:"name"`
	LogoURI string `json:"logoUri,omitempty"`
}

func (h *Handler) orgs(w http.ResponseWriter, r *http.Request) error {
	u := auth.UserFromContext(r.Context())
	orgs, err := h.svc.Organizations(r.Context(), r.PathValue("id"), u.ID)
	if err != nil {
		return mapError(err)
	}
	out := make([]orgSummary, 0, len(orgs))
	for _, o := range orgs {
		out = append(out, orgSummary{Slug: o.Slug, Name: o.Name, LogoURI: o.LogoURI})
	}
	respond.JSON(w, r, http.StatusOK, out)
	return nil
}

type selectResponse struct {
	Status      string `json:"status"`
	RedirectURI string `json:"redirectUri,omitempty"`
}

func (h *Handler) selectOrg(w http.ResponseWriter, r *http.Request) error {
	u := auth.UserFromContext(r.Context())
	org := organization.OrgFromContext(r.Context())
	res, err := h.svc.Select(r.Context(), r.PathValue("id"), u.ID, org)
	if err != nil {
		return mapError(err)
	}
	respond.JSON(w, r, http.StatusOK, selectResponse(res))
	return nil
}

// mapError turns the service's sentinels into API errors. Validation failures
// carry their reason (a verifier integrator needs it); internal failures do not.
func mapError(err error) error {
	switch {
	case errors.Is(err, ErrInvalidRequest):
		return &respond.APIError{Status: http.StatusBadRequest, Code: ErrInvalidRequest.Error(), Message: err.Error()}
	case errors.Is(err, ErrInvalidRequestURIMethod):
		return &respond.APIError{Status: http.StatusBadRequest, Code: ErrInvalidRequestURIMethod.Error(), Message: err.Error()}
	case errors.Is(err, ErrInvalidRequestObject):
		return &respond.APIError{Status: http.StatusBadRequest, Code: ErrInvalidRequestObject.Error(), Message: err.Error()}
	case errors.Is(err, ErrRequestURIUnreachable):
		return &respond.APIError{Status: http.StatusBadGateway, Code: ErrRequestURIUnreachable.Error(), Message: "the request object could not be fetched"}
	case errors.Is(err, ErrValidationUnavailable):
		return &respond.APIError{Status: http.StatusNotImplemented, Code: ErrValidationUnavailable.Error(), Message: "this deployment cannot validate request objects yet"}
	case errors.Is(err, ErrNotFound):
		return &respond.APIError{Status: http.StatusNotFound, Code: "transaction_not_found", Message: "unknown presentation transaction"}
	case errors.Is(err, ErrNotPending):
		return &respond.APIError{Status: http.StatusConflict, Code: "transaction_not_pending", Message: "this presentation request has already been handled or has expired"}
	case errors.Is(err, ErrForbidden):
		return &respond.APIError{Status: http.StatusForbidden, Code: "forbidden", Message: "forbidden"}
	case errors.Is(err, ErrPresentationFailed):
		return &respond.APIError{Status: http.StatusBadGateway, Code: ErrPresentationFailed.Error(), Message: "the presentation could not be delivered to the verifier"}
	default:
		return fmt.Errorf("openid4vppresenter: %w", err)
	}
}
