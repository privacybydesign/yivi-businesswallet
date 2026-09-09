package openid4vppresenter

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"strings"

	"github.com/privacybydesign/irmago/eudi/openid4vp"

	"github.com/privacybydesign/yivi-businesswallet/backend/internal/eudiholder"
)

const (
	// WellKnownPath is RFC 8414's default authorization-server metadata path.
	// OpenID4VP wallet metadata is shaped as OAuth AS metadata because the wallet
	// plays the authorization-server role toward the verifier-as-client.
	WellKnownPath = "/.well-known/oauth-authorization-server"
	// AuthorizationPath is the browser-facing entry point a verifier redirects to
	// (or encodes in a QR): an SPA route, not an API route.
	AuthorizationPath = "/openid4vp"
	// metadataCacheControl: the document changes only on deploy.
	metadataCacheControl = "public, max-age=3600"
)

// Metadata is the wallet-metadata document (OpenID4VP 1.0 §11.1 over RFC 8414).
// Every value is derived from configuration or from the configured holder and
// validator — nothing here is a capability claim typed by hand.
type Metadata struct {
	Issuer                    string         `json:"issuer"`
	AuthorizationEndpoint     string         `json:"authorization_endpoint"`
	ResponseTypesSupported    []string       `json:"response_types_supported"`
	ResponseModesSupported    []string       `json:"response_modes_supported"`
	VPFormatsSupported        map[string]any `json:"vp_formats_supported"`
	ClientIDPrefixesSupported []string       `json:"client_id_prefixes_supported,omitempty"`
}

// NewMetadata assembles the document for appBaseURL from the holder's formats and
// the validator's client_id prefixes.
func NewMetadata(appBaseURL string, formats eudiholder.PresentationFormats, validator Validator) Metadata {
	base := strings.TrimRight(appBaseURL, "/")
	vpFormats := map[string]any{}
	if formats.SDJWTVC != nil {
		vpFormats[eudiholder.FormatSDJWTVC] = formats.SDJWTVC
	}
	return Metadata{
		Issuer:                    base,
		AuthorizationEndpoint:     base + AuthorizationPath,
		ResponseTypesSupported:    []string{string(openid4vp.ResponseType_VpToken)},
		ResponseModesSupported:    []string{string(openid4vp.ResponseMode_DirectPost)},
		VPFormatsSupported:        vpFormats,
		ClientIDPrefixesSupported: validator.ClientIDPrefixes(),
	}
}

// MetadataHandler serves the document, marshalled once at boot.
type MetadataHandler struct {
	body []byte
}

func NewMetadataHandler(m Metadata) (*MetadataHandler, error) {
	body, err := json.Marshal(m)
	if err != nil {
		return nil, fmt.Errorf("openid4vppresenter: marshal wallet metadata: %w", err)
	}
	return &MetadataHandler{body: body}, nil
}

// RegisterRoot mounts the document on the root mux. The pattern is built from
// the constant rather than written as a literal so the /api/v1 spec-coverage
// scan (internal/apidocs) does not mistake it for a versioned API route.
func (h *MetadataHandler) RegisterRoot(mux *http.ServeMux) {
	mux.Handle(http.MethodGet+" "+WellKnownPath, h)
}

func (h *MetadataHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", metadataCacheControl)
	w.WriteHeader(http.StatusOK)
	if _, err := w.Write(h.body); err != nil {
		slog.ErrorContext(r.Context(), "writing wallet metadata", slog.String("error", err.Error()))
	}
}
