package proofing

import (
	"net/http"

	"github.com/privacybydesign/yivi-businesswallet/backend/internal/email"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/respond"
)

// flowHostedResponse is a flow's hosted page settings; locales empty is every
// supported language.
type flowHostedResponse struct {
	Enabled    bool           `json:"enabled"`
	Locales    []email.Locale `json:"locales"`
	Completion Completion     `json:"completion"`
}

func newFlowHostedResponse(f FlowHosted) flowHostedResponse {
	return flowHostedResponse{Enabled: f.Enabled, Locales: append([]email.Locale{}, f.Locales...), Completion: f.Completion}
}

func (h *Handler) getFlowHosted(w http.ResponseWriter, r *http.Request) error {
	f, err := h.service.FlowHosted(r.Context(), orgFromRequest(r), r.PathValue("flowID"))
	if err != nil {
		return mapError(err)
	}
	respond.JSON(w, r, http.StatusOK, newFlowHostedResponse(f))
	return nil
}

type saveFlowHostedRequest struct {
	Enabled    *bool          `json:"enabled"`
	Locales    []email.Locale `json:"locales"`
	Completion Completion     `json:"completion"`
}

func (h *Handler) saveFlowHosted(w http.ResponseWriter, r *http.Request) error {
	var body saveFlowHostedRequest
	if err := decode(r, &body); err != nil {
		return err
	}
	if body.Enabled == nil {
		return &respond.APIError{Status: http.StatusBadRequest, Code: "invalid_body", Message: "enabled is required"}
	}
	f, err := h.service.SaveFlowHosted(r.Context(), orgFromRequest(r), r.PathValue("flowID"),
		FlowHosted{Enabled: *body.Enabled, Locales: body.Locales, Completion: body.Completion})
	if err != nil {
		return mapError(err)
	}
	respond.JSON(w, r, http.StatusOK, newFlowHostedResponse(f))
	return nil
}
