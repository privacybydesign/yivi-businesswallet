package attestation

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/privacybydesign/yivi-businesswallet/backend/internal/organization"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/respond"
)

// Credential-image upload constraints, mirroring the issuer-logo pipeline
// (internal/issuersettings). The image is embedded as a data: URI in the
// generated issuer config a wallet shows, so an optimised PNG/SVG of a few KiB is
// expected; 512 KiB leaves ample room without bloating the bundle.
const (
	// MaxLogoBytes caps an uploaded credential image.
	MaxLogoBytes = 512 << 10
	// multipartMemory is how much of the form is buffered in RAM before spilling
	// to temp files during parsing.
	multipartMemory = 1 << 20
	// bodySlack allows for multipart boundaries on top of the image payload cap.
	bodySlack = 1 << 20
	// logoFormField is the multipart file field carrying the image.
	logoFormField = "logo"
)

// putSchemaLogo applies the credential-image change carried by the multipart
// form to an existing schema: an uploaded "logo" file replaces the image and
// "removeLogo=true" clears it.
func (h *Handler) putSchemaLogo(w http.ResponseWriter, r *http.Request) error {
	id, err := parseID(r, "id", "schema")
	if err != nil {
		return err
	}

	r.Body = http.MaxBytesReader(w, r.Body, MaxLogoBytes+bodySlack)
	if err := r.ParseMultipartForm(multipartMemory); err != nil {
		if _, ok := errors.AsType[*http.MaxBytesError](err); ok {
			return apiError(http.StatusRequestEntityTooLarge, "payload_too_large", "the image is too large")
		}
		return badRequest("invalid_body", "invalid multipart form")
	}

	logo, err := parseLogoUpdate(r)
	if err != nil {
		return err
	}

	org := organization.OrgFromContext(r.Context())
	sc, err := h.schemas.SetSchemaLogo(r.Context(), org.ID, id, logo)
	if errors.Is(err, ErrSchemaNotFound) {
		return notFound("schema_not_found", "schema not found")
	}
	if err != nil {
		return fmt.Errorf("updating attestation schema logo: %w", err)
	}
	sc.LogoURI = schemaLogoURL(org.Slug, sc)
	respond.JSON(w, r, http.StatusOK, sc)
	return nil
}

// serveSchemaLogo streams a schema's stored image bytes with a locked-down
// response (see setLogoResponseHeaders). It backs the admin builder preview; the
// wallet-facing bundle embeds the image as a data: URI instead.
func (h *Handler) serveSchemaLogo(w http.ResponseWriter, r *http.Request) error {
	id, err := parseID(r, "id", "schema")
	if err != nil {
		return err
	}
	org := organization.OrgFromContext(r.Context())
	logo, err := h.schemas.GetSchemaLogo(r.Context(), org.ID, id)
	switch {
	case errors.Is(err, ErrSchemaNotFound):
		return notFound("schema_not_found", "schema not found")
	case errors.Is(err, ErrNoSchemaLogo):
		return notFound("not_found", "no image set")
	case err != nil:
		return fmt.Errorf("getting attestation schema logo: %w", err)
	}

	setLogoResponseHeaders(w.Header(), logo.ContentType)
	w.WriteHeader(http.StatusOK)
	if _, err := w.Write(logo.Bytes); err != nil {
		// The status and headers are already committed, so an error here can only
		// be logged, not turned into an API error response.
		slog.ErrorContext(r.Context(), "attestation: write schema logo body", slog.String("error", err.Error()))
	}
	return nil
}

// setLogoResponseHeaders locks the image response down. The image is
// admin-uploaded content served same-origin, so nosniff keeps the declared type
// authoritative and the sandbox + null-source CSP stop an uploaded SVG from
// running script if the URL is opened directly.
func setLogoResponseHeaders(h http.Header, contentType string) {
	h.Set("Content-Type", contentType)
	h.Set("X-Content-Type-Options", "nosniff")
	h.Set("Content-Security-Policy", "default-src 'none'; style-src 'unsafe-inline'; sandbox")
	h.Set("Cache-Control", "private, max-age=300")
}

// parseLogoUpdate reads the image intent from the multipart form. A "logo" file
// part replaces the image (validated for size and type); "removeLogo=true" clears
// it; otherwise the request is rejected (the upload endpoint carries an intent).
func parseLogoUpdate(r *http.Request) (LogoUpdate, error) {
	file, _, err := r.FormFile(logoFormField)
	if errors.Is(err, http.ErrMissingFile) {
		if r.FormValue("removeLogo") == "true" {
			return LogoUpdate{Replace: true}, nil
		}
		return LogoUpdate{}, badRequest("invalid_input", "no image uploaded")
	}
	if err != nil {
		return LogoUpdate{}, badRequest("invalid_body", "invalid image upload")
	}
	defer func() { _ = file.Close() }()

	data, err := io.ReadAll(io.LimitReader(file, MaxLogoBytes+1))
	if err != nil {
		return LogoUpdate{}, fmt.Errorf("reading uploaded image: %w", err)
	}
	if len(data) == 0 {
		return LogoUpdate{}, badRequest("invalid_input", "the image file is empty")
	}
	if len(data) > MaxLogoBytes {
		return LogoUpdate{}, apiError(http.StatusRequestEntityTooLarge, "payload_too_large", "the image is too large")
	}
	contentType, ok := detectLogoType(data)
	if !ok {
		return LogoUpdate{}, badRequest("invalid_input", "the image must be a PNG, JPEG, GIF, WebP or SVG image")
	}
	return LogoUpdate{Replace: true, Logo: Logo{Bytes: data, ContentType: contentType}}, nil
}

// detectLogoType sniffs the actual bytes (not the client-declared type) and
// returns the canonical MIME type for a supported image, or ok=false otherwise.
// Raster formats are recognised by http.DetectContentType; SVG (XML, which the
// sniffer reports as text) is matched separately.
func detectLogoType(data []byte) (string, bool) {
	switch sniff := http.DetectContentType(data); {
	case strings.HasPrefix(sniff, "image/png"):
		return "image/png", true
	case strings.HasPrefix(sniff, "image/jpeg"):
		return "image/jpeg", true
	case strings.HasPrefix(sniff, "image/gif"):
		return "image/gif", true
	case strings.HasPrefix(sniff, "image/webp"):
		return "image/webp", true
	}
	if looksLikeSVG(data) {
		return "image/svg+xml", true
	}
	return "", false
}

// looksLikeSVG reports whether the bytes open an SVG document, allowing a leading
// XML declaration or doctype before the <svg> root.
func looksLikeSVG(data []byte) bool {
	const sniffLen = 1024
	head := data
	if len(head) > sniffLen {
		head = head[:sniffLen]
	}
	head = bytes.ToLower(bytes.TrimSpace(head))
	if bytes.HasPrefix(head, []byte("<svg")) {
		return true
	}
	return bytes.HasPrefix(head, []byte("<?xml")) && bytes.Contains(head, []byte("<svg"))
}

// schemaLogoURL is the API path that serves a schema's image for the admin
// preview, or "" when none is stored. The updated-at timestamp is a cache-busting
// version so a replaced image is re-fetched rather than served stale.
func schemaLogoURL(slug string, sc Schema) string {
	if !sc.HasLogo {
		return ""
	}
	return fmt.Sprintf("/api/v1/orgs/%s/attestations/schemas/%s/logo?v=%s",
		url.PathEscape(slug), url.PathEscape(sc.ID.String()), strconv.FormatInt(sc.UpdatedAt.Unix(), 10))
}

func apiError(status int, code, msg string) error {
	return &respond.APIError{Status: status, Code: code, Message: msg}
}
