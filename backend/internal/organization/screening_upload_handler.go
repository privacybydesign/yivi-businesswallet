package organization

import (
	"errors"
	"fmt"
	"net/http"

	"github.com/google/uuid"

	"github.com/privacybydesign/yivi-businesswallet/backend/internal/respond"
)

const (
	// MaxVogUploadBytes caps an uploaded VOG PDF. A scanned VOG is a handful of
	// pages; a few MiB is generous without inviting an oversized upload.
	MaxVogUploadBytes  = 8 << 20
	vogMultipartMemory = 1 << 20
	vogBodySlack       = 1 << 20
	vogFormField       = "file"
)

// uploadSelfVog is the member's own VOG upload (#242 §2): the primary path,
// self-service, reached through the VOG link rather than a signed-in session.
func (h *Handler) uploadSelfVog(w http.ResponseWriter, r *http.Request) error {
	tc, err := h.vogTokenContext(r)
	if err != nil {
		return err
	}
	return h.uploadVogFor(w, r, tc.OrganizationID, tc.UserID, CheckedBySelf, nil)
}

// uploadVogFor drives one upload attempt: parse the multipart body, run it
// through the screening service, and map the outcome to a response. Both the
// self-service and admin-on-behalf routes share this - the only difference is
// whose membership orgID/userID name and who checkedBy/checkedByUserID say ran
// it.
func (h *Handler) uploadVogFor(w http.ResponseWriter, r *http.Request, orgID, userID uuid.UUID, checkedBy string, checkedByUserID *uuid.UUID) error {
	r.Body = http.MaxBytesReader(w, r.Body, MaxVogUploadBytes+vogBodySlack)
	if err := r.ParseMultipartForm(vogMultipartMemory); err != nil {
		if _, ok := errors.AsType[*http.MaxBytesError](err); ok {
			return &respond.APIError{Status: http.StatusRequestEntityTooLarge, Code: "payload_too_large", Message: "the document is too large"}
		}
		return badRequest("invalid_body", "invalid multipart form")
	}

	file, _, err := r.FormFile(vogFormField)
	if err != nil {
		return badRequest("invalid_input", `a "file" form field carrying the PDF is required`)
	}
	defer func() { _ = file.Close() }()

	pdf := make([]byte, 0, 64<<10)
	buf := make([]byte, 32<<10)
	for {
		n, readErr := file.Read(buf)
		pdf = append(pdf, buf[:n]...)
		if readErr != nil {
			break
		}
	}

	outcome, err := h.screening.UploadVog(r.Context(), orgID, userID, checkedBy, checkedByUserID, pdf)
	if err := mapScreeningError(err); err != nil {
		return err
	}
	respondVogOutcome(w, r, outcome)
	return nil
}

// respondVogOutcome writes one screening attempt's outcome, shared by the PDF
// and both credential paths.
func respondVogOutcome(w http.ResponseWriter, r *http.Request, outcome ScreeningOutcome) {
	resp := uploadVogResponse{Result: string(outcome.Result), MissingCodes: outcome.MissingCodes, RejectionReason: outcome.RejectionReason}
	respond.JSON(w, r, vogUploadStatus(outcome.RejectionReason), resp)
}

// uploadVogResponse is the outcome the member/admin sees after an upload -
// never the disclosed document data, only the decision (#242's
// data-minimisation design).
type uploadVogResponse struct {
	Result          string   `json:"result"`
	MissingCodes    []string `json:"missingCodes,omitempty"`
	RejectionReason string   `json:"rejectionReason,omitempty"`
}

// vogUploadStatus is 200 for a passing check, 422 for any recorded rejection -
// the request itself succeeded either way; the document just did not pass.
func vogUploadStatus(rejectionReason string) int {
	if rejectionReason == "" {
		return http.StatusOK
	}
	return http.StatusUnprocessableEntity
}

func mapScreeningError(err error) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, ErrVogTokenNotFound):
		return &respond.APIError{Status: http.StatusNotFound, Code: "vog_link_not_found", Message: "this VOG link is invalid or has expired"}
	case errors.Is(err, ErrNotMember):
		return &respond.APIError{Status: http.StatusNotFound, Code: "member_not_found", Message: "member not found"}
	case errors.Is(err, ErrVogNoDateOfBirth):
		return &respond.APIError{Status: http.StatusConflict, Code: "no_date_of_birth", Message: "this member has no date of birth on file yet; re-identify first"}
	default:
		return fmt.Errorf("screening vog: %w", err)
	}
}
