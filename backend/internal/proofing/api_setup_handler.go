package proofing

import (
	"net/http"

	"github.com/privacybydesign/yivi-businesswallet/backend/internal/respond"
)

// The public API's setup routes: an org's software sets up its flows, their
// hosted page and steps, the org's settings and its webhook endpoint, as an
// admin does in the wallet. They share the admin's handlers, which read the
// key's org (orgFromRequest).
func (h *Handler) registerPublicSetupAPI(mux *http.ServeMux) {
	mux.Handle("POST /proofing/flows", h.requireAPIKey(ScopeFlowsWrite, h.idempotent(h.createFlow)))
	mux.Handle("GET /proofing/flows/{flowID}/versions", h.requireAPIKey(ScopeFlowsRead, h.listFlowVersions))
	mux.Handle("POST /proofing/flows/{flowID}/versions", h.requireAPIKey(ScopeFlowsWrite, h.idempotent(h.editFlow)))
	mux.Handle("POST /proofing/flows/{flowID}/versions/{version}/activate", h.requireAPIKey(ScopeFlowsWrite, h.activateFlowVersion))
	mux.Handle("PUT /proofing/flow-selection", h.requireAPIKey(ScopeFlowsWrite, h.apiConfigureFlows))
	mux.Handle("GET /proofing/flows/{flowID}/hosted", h.requireAPIKey(ScopeFlowsRead, h.getFlowHosted))
	mux.Handle("PUT /proofing/flows/{flowID}/hosted", h.requireAPIKey(ScopeFlowsWrite, h.saveFlowHosted))
	mux.Handle("GET /proofing/flows/{flowID}/address", h.requireAPIKey(ScopeFlowsRead, h.getFlowAddress))
	mux.Handle("PUT /proofing/flows/{flowID}/address", h.requireAPIKey(ScopeFlowsWrite, h.saveFlowAddress))
	mux.Handle("GET /proofing/flows/{flowID}/diplomas", h.requireAPIKey(ScopeFlowsRead, h.getFlowDiplomas))
	mux.Handle("PUT /proofing/flows/{flowID}/diplomas", h.requireAPIKey(ScopeFlowsWrite, h.saveFlowDiplomas))
	mux.Handle("GET /proofing/flows/{flowID}/employment", h.requireAPIKey(ScopeFlowsRead, h.getFlowEmployment))
	mux.Handle("PUT /proofing/flows/{flowID}/employment", h.requireAPIKey(ScopeFlowsWrite, h.saveFlowEmployment))

	mux.Handle("GET /proofing/settings", h.requireAPIKey(ScopeSettingsRead, h.apiGetSettings))
	mux.Handle("PATCH /proofing/settings", h.requireAPIKey(ScopeSettingsWrite, h.apiUpdateSettings))

	mux.Handle("GET /proofing/webhook", h.requireAPIKey(ScopeWebhooksWrite, h.getWebhook))
	mux.Handle("PUT /proofing/webhook", h.requireAPIKey(ScopeWebhooksWrite, h.saveWebhook))
	mux.Handle("DELETE /proofing/webhook", h.requireAPIKey(ScopeWebhooksWrite, h.removeWebhook))
	mux.Handle("POST /proofing/webhook/rotate-secret", h.requireAPIKey(ScopeWebhooksWrite, h.rotateWebhookSecret))
	mux.Handle("POST /proofing/webhook/test", h.requireAPIKey(ScopeWebhooksWrite, h.testWebhook))
	mux.Handle("GET /proofing/webhook/deliveries", h.requireAPIKey(ScopeWebhooksWrite, h.listWebhookDeliveries))
}

// apiConfigureFlows sets which flows the org offers its members and its
// default, the flow a session without a flowId runs, and answers the flows.
func (h *Handler) apiConfigureFlows(w http.ResponseWriter, r *http.Request) error {
	var body flowSelectionRequest
	if err := decode(r, &body); err != nil {
		return err
	}
	if err := h.service.ConfigureFlows(r.Context(), orgFromRequest(r), FlowSelection(body)); err != nil {
		return mapError(err)
	}
	return h.apiListFlows(w, r)
}

// apiSettingsResponse is the org's settings as its software sees them: the
// wallet's settingsResponse without the branding, which only the wallet sets.
type apiSettingsResponse struct {
	SessionTTLSeconds      int                 `json:"sessionTtlSeconds"`
	DataRetentionDays      int                 `json:"dataRetentionDays"`
	ErasureHandling        ErasureHandling     `json:"erasureHandling"`
	LegalRetention         *legalRetentionBody `json:"legalRetention"`
	AllowedRedirectOrigins []string            `json:"allowedRedirectOrigins"`
}

func newAPISettingsResponse(s Settings) apiSettingsResponse {
	full := newSettingsResponse("", s)
	return apiSettingsResponse{
		SessionTTLSeconds: full.SessionTTLSeconds, DataRetentionDays: full.DataRetentionDays,
		ErasureHandling: full.ErasureHandling, LegalRetention: full.LegalRetention,
		AllowedRedirectOrigins: full.AllowedRedirectOrigins,
	}
}

func (h *Handler) apiGetSettings(w http.ResponseWriter, r *http.Request) error {
	s, err := h.service.Settings(r.Context(), orgFromRequest(r).ID)
	if err != nil {
		return mapError(err)
	}
	respond.JSON(w, r, http.StatusOK, newAPISettingsResponse(s))
	return nil
}

func (h *Handler) apiUpdateSettings(w http.ResponseWriter, r *http.Request) error {
	s, err := h.changeSettings(r)
	if err != nil {
		return err
	}
	respond.JSON(w, r, http.StatusOK, newAPISettingsResponse(s))
	return nil
}
