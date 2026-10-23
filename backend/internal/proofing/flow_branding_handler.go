package proofing

import (
	"fmt"
	"net/http"
	"net/url"
	"strconv"

	"github.com/privacybydesign/yivi-businesswallet/backend/internal/respond"
)

// flowBrandingResponse is a flow's own branding as the app edits it, not the
// look its mail and page wear: Custom false is the org's, and the fields are
// empty. LogoURI is "" without an own logo.
type flowBrandingResponse struct {
	Custom         bool   `json:"custom"`
	DisplayName    string `json:"displayName"`
	PrimaryColor   string `json:"primaryColor"`
	SupportContact string `json:"supportContact"`
	PrivacyURL     string `json:"privacyUrl"`
	LogoURI        string `json:"logoUri"`
}

func newFlowBrandingResponse(slug, flowID string, f FlowBranding) flowBrandingResponse {
	b := flowBrandingResponse{
		Custom: f.Custom, DisplayName: f.DisplayName, PrimaryColor: f.PrimaryColor,
		SupportContact: f.SupportContact, PrivacyURL: f.PrivacyURL,
	}
	if f.HasLogo {
		b.LogoURI = fmt.Sprintf("/api/v1/orgs/%s/identity-proofing/flows/%s/branding/logo?v=%d",
			url.PathEscape(slug), url.PathEscape(flowID), f.UpdatedAt.Unix())
	}
	return b
}

func (h *Handler) getFlowBranding(w http.ResponseWriter, r *http.Request) error {
	flowID := r.PathValue("flowID")
	f, err := h.service.FlowBranding(r.Context(), orgFromRequest(r), flowID)
	if err != nil {
		return mapError(err)
	}
	respond.JSON(w, r, http.StatusOK, newFlowBrandingResponse(r.PathValue("slug"), flowID, f))
	return nil
}

// saveFlowBranding takes the multipart form of the org's branding without
// "powered by", and "custom" (true or false) for which branding the flow uses.
// A flow that is not custom drops its logo, so the upload is not read.
func (h *Handler) saveFlowBranding(w http.ResponseWriter, r *http.Request) error {
	if err := parseBrandingForm(w, r); err != nil {
		return err
	}
	custom, err := strconv.ParseBool(r.FormValue("custom"))
	if err != nil {
		return &respond.APIError{Status: http.StatusBadRequest, Code: "invalid_body", Message: "custom is required: true or false"}
	}

	var logo LogoChange
	if custom {
		if logo, err = parseLogo(r); err != nil {
			return err
		}
	}

	flowID := r.PathValue("flowID")
	f, err := h.service.SaveFlowBranding(r.Context(), orgFromRequest(r), flowID, FlowBranding{
		Custom: custom,
		Branding: Branding{
			DisplayName:    r.FormValue("displayName"),
			PrimaryColor:   r.FormValue("primaryColor"),
			SupportContact: r.FormValue("supportContact"),
			PrivacyURL:     r.FormValue("privacyUrl"),
		},
	}, logo)
	if err != nil {
		return mapError(err)
	}
	respond.JSON(w, r, http.StatusOK, newFlowBrandingResponse(r.PathValue("slug"), flowID, f))
	return nil
}

func (h *Handler) serveFlowLogo(w http.ResponseWriter, r *http.Request) error {
	logo, err := h.service.FlowBrandingLogo(r.Context(), orgFromRequest(r), r.PathValue("flowID"))
	if err != nil {
		return mapError(err)
	}
	writeLogo(w, r, logo)
	return nil
}
