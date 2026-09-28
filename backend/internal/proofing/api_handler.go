package proofing

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/privacybydesign/yivi-businesswallet/backend/internal/auth"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/respond"
)

// The public proofing API: a customer's backend, authenticated by one of the
// customer's API keys (Authorization: Bearer yp_live_…), lists the customer's
// flows, creates sessions for its subjects and reads their outcome. It acts
// exactly as a member sending for the customer would, on the customer's
// assigned flows, and is refused while the customer is paused.

type apiKeyResponse struct {
	ID         uuid.UUID  `json:"id"`
	Name       string     `json:"name"`
	Prefix     string     `json:"prefix"`
	CreatedAt  time.Time  `json:"createdAt"`
	LastUsedAt *time.Time `json:"lastUsedAt,omitempty"`
	RevokedAt  *time.Time `json:"revokedAt,omitempty"`
}

func newAPIKeyResponse(k APIKey) apiKeyResponse {
	return apiKeyResponse{
		ID: k.ID, Name: k.Name, Prefix: k.Prefix,
		CreatedAt: k.CreatedAt, LastUsedAt: k.LastUsedAt, RevokedAt: k.RevokedAt,
	}
}

// createdAPIKeyResponse carries the key's secret, the one time it is readable.
type createdAPIKeyResponse struct {
	apiKeyResponse
	Secret string `json:"secret"`
}

func (h *Handler) listAPIKeys(w http.ResponseWriter, r *http.Request) error {
	id, err := customerIDFromPath(r)
	if err != nil {
		return err
	}
	keys, err := h.service.APIKeys(r.Context(), orgFromRequest(r).ID, id)
	if err != nil {
		return mapError(err)
	}
	out := make([]apiKeyResponse, 0, len(keys))
	for _, k := range keys {
		out = append(out, newAPIKeyResponse(k))
	}
	respond.JSON(w, r, http.StatusOK, out)
	return nil
}

type createAPIKeyRequest struct {
	Name string `json:"name"`
}

func (h *Handler) createAPIKey(w http.ResponseWriter, r *http.Request) error {
	id, err := customerIDFromPath(r)
	if err != nil {
		return err
	}
	var body createAPIKeyRequest
	if err := decode(r, &body); err != nil {
		return err
	}
	caller := auth.UserFromContext(r.Context())
	key, secret, err := h.service.CreateAPIKey(r.Context(), orgFromRequest(r).ID, id, caller.ID, body.Name)
	if err != nil {
		return mapError(err)
	}
	respond.JSON(w, r, http.StatusCreated, createdAPIKeyResponse{apiKeyResponse: newAPIKeyResponse(key), Secret: secret})
	return nil
}

func (h *Handler) revokeAPIKey(w http.ResponseWriter, r *http.Request) error {
	id, err := customerIDFromPath(r)
	if err != nil {
		return err
	}
	keyID, err := uuid.Parse(r.PathValue("keyID"))
	if err != nil {
		return &respond.APIError{Status: http.StatusBadRequest, Code: "invalid_id", Message: "invalid API key id"}
	}
	key, err := h.service.RevokeAPIKey(r.Context(), orgFromRequest(r).ID, id, keyID)
	if err != nil {
		return mapError(err)
	}
	respond.JSON(w, r, http.StatusOK, newAPIKeyResponse(key))
	return nil
}

type apiCallerKey struct{}

func callerFromContext(ctx context.Context) APIKeyCaller {
	c, _ := ctx.Value(apiCallerKey{}).(APIKeyCaller)
	return c
}

const bearerPrefix = "Bearer "

// requireAPIKey authenticates the customer key in the Authorization header.
func (h *Handler) requireAPIKey(next respond.HandlerFunc) http.Handler {
	return respond.HandlerFunc(func(w http.ResponseWriter, r *http.Request) error {
		raw, ok := strings.CutPrefix(r.Header.Get("Authorization"), bearerPrefix)
		if !ok || raw == "" {
			w.Header().Set("WWW-Authenticate", `Bearer realm="proofing"`)
			return &respond.APIError{Status: http.StatusUnauthorized, Code: "unauthorized", Message: "send a customer API key as a Bearer token"}
		}
		caller, err := h.service.AuthenticateAPIKey(r.Context(), strings.TrimSpace(raw))
		if errors.Is(err, ErrAPIKeyInvalid) {
			w.Header().Set("WWW-Authenticate", `Bearer realm="proofing", error="invalid_token"`)
			return &respond.APIError{Status: http.StatusUnauthorized, Code: "invalid_api_key", Message: "this API key is unknown or revoked"}
		}
		if err != nil {
			return err
		}
		return next(w, r.WithContext(context.WithValue(r.Context(), apiCallerKey{}, caller)))
	})
}

func (h *Handler) registerPublicAPI(mux *http.ServeMux) {
	mux.Handle("GET /proofing/flows", h.requireAPIKey(h.apiListFlows))
	mux.Handle("POST /proofing/sessions", h.requireAPIKey(h.apiCreateSession))
	mux.Handle("GET /proofing/sessions/{sessionID}", h.requireAPIKey(h.apiGetSession))
}

type apiFlowResponse struct {
	ID                     string   `json:"id"`
	Name                   string   `json:"name"`
	Version                int      `json:"version"`
	RequiredAssuranceLevel string   `json:"requiredAssuranceLevel,omitempty"`
	RequestedAttributes    []string `json:"requestedAttributes"`
	Default                bool     `json:"default"`
}

func (h *Handler) apiListFlows(w http.ResponseWriter, r *http.Request) error {
	caller := callerFromContext(r.Context())
	flows, err := h.service.CustomerFlows(r.Context(), caller.Org, caller.CustomerID, false)
	if err != nil {
		return mapError(err)
	}
	out := make([]apiFlowResponse, 0, len(flows))
	for _, f := range flows {
		attrs := f.RequestedAttributes
		if attrs == nil {
			attrs = []string{}
		}
		out = append(out, apiFlowResponse{
			ID: f.ID, Name: f.Name, Version: f.Version, RequiredAssuranceLevel: f.RequiredAssuranceLevel,
			RequestedAttributes: attrs, Default: f.Default,
		})
	}
	respond.JSON(w, r, http.StatusOK, out)
	return nil
}

// apiSessionResponse is a session as the customer's backend sees it: the
// outcome, and for an approved subject the name read off their document until
// the customer's data retention clears it. Never other document data.
type apiSessionResponse struct {
	ID             uuid.UUID  `json:"id"`
	Status         Status     `json:"status"`
	FlowID         string     `json:"flowId"`
	FlowName       string     `json:"flowName"`
	FlowVersion    int        `json:"flowVersion,omitempty"`
	Method         string     `json:"method,omitempty"`
	SubjectEmail   string     `json:"subjectEmail"`
	SubjectName    string     `json:"subjectName,omitempty"`
	ProofedName    string     `json:"proofedName,omitempty"`
	AssuranceLevel string     `json:"assuranceLevel,omitempty"`
	EIDASLevel     string     `json:"eidasLevel,omitempty"`
	ErrorCode      string     `json:"errorCode,omitempty"`
	ExpiresAt      time.Time  `json:"expiresAt"`
	CreatedAt      time.Time  `json:"createdAt"`
	CompletedAt    *time.Time `json:"completedAt,omitempty"`
}

func newAPISessionResponse(req Request, now time.Time) apiSessionResponse {
	return apiSessionResponse{
		ID: req.ID, Status: req.EffectiveStatus(now), FlowID: req.FlowID, FlowName: req.FlowName,
		FlowVersion: req.FlowVersion, Method: string(req.Method), SubjectEmail: req.SubjectEmail, SubjectName: req.SubjectName,
		ProofedName: req.ProofedName, AssuranceLevel: req.AssuranceLevel, EIDASLevel: req.EIDASLevel,
		ErrorCode: req.ErrorCode, ExpiresAt: req.LinkExpiresAt, CreatedAt: req.CreatedAt, CompletedAt: req.CompletedAt,
	}
}

// apiCreatedSessionResponse adds the session's vcmrtd deep link, for a caller
// that shows the QR code itself, and whether the mail went out.
type apiCreatedSessionResponse struct {
	apiSessionResponse
	DeepLink string `json:"deepLink"`
	MailSent bool   `json:"mailSent"`
}

// apiCreateSessionRequest names the subject by address and optional name; an
// absent flowId is the customer's default flow, and sendMail false leaves the
// mail out.
type apiCreateSessionRequest struct {
	Email    string `json:"email"`
	Name     string `json:"name"`
	FlowID   string `json:"flowId"`
	SendMail *bool  `json:"sendMail"`
}

func (h *Handler) apiCreateSession(w http.ResponseWriter, r *http.Request) error {
	var body apiCreateSessionRequest
	if err := decode(r, &body); err != nil {
		return err
	}
	caller := callerFromContext(r.Context())
	sent, err := h.service.CreateRequest(r.Context(), caller.Org,
		Requester{Name: caller.KeyName, APIKeyID: &caller.KeyID}, NewRequest{
			CustomerID: &caller.CustomerID, SubjectEmail: body.Email, SubjectName: body.Name,
			FlowID: body.FlowID, SkipMail: body.SendMail != nil && !*body.SendMail,
		})
	if err != nil {
		return mapError(err)
	}
	respond.JSON(w, r, http.StatusCreated, apiCreatedSessionResponse{
		apiSessionResponse: newAPISessionResponse(sent.Request, time.Now()),
		DeepLink:           sent.DeepLink, MailSent: sent.MailSent,
	})
	return nil
}

func (h *Handler) apiGetSession(w http.ResponseWriter, r *http.Request) error {
	id, err := uuid.Parse(r.PathValue("sessionID"))
	if err != nil {
		return &respond.APIError{Status: http.StatusNotFound, Code: "session_not_found", Message: "this session does not exist"}
	}
	caller := callerFromContext(r.Context())
	req, err := h.service.CustomerRequest(r.Context(), caller.Org.ID, caller.CustomerID, id)
	if err != nil {
		return mapError(err)
	}
	respond.JSON(w, r, http.StatusOK, newAPISessionResponse(req, time.Now()))
	return nil
}
