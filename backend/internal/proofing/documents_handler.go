package proofing

import (
	"net/http"

	"github.com/privacybydesign/yivi-businesswallet/backend/internal/respond"
)

// documentVerdictResponse is what became of one file of a document upload.
type documentVerdictResponse struct {
	FileName    string           `json:"fileName"`
	Kind        DocumentKind     `json:"kind,omitempty"`
	Accepted    bool             `json:"accepted"`
	Diploma     *diplomaResponse `json:"diploma,omitempty"`
	Employment  *Employment      `json:"employment,omitempty"`
	Reason      string           `json:"reason,omitempty"`
	FailedCheck string           `json:"failedCheck,omitempty"`
}

func writeDocumentVerdicts(w http.ResponseWriter, r *http.Request, verdicts []DocumentVerdict) {
	out := make([]documentVerdictResponse, 0, len(verdicts))
	for _, v := range verdicts {
		resp := documentVerdictResponse{
			FileName: v.FileName, Kind: v.Kind, Employment: v.Employment, Reason: v.Reason, FailedCheck: v.FailedCheck,
			Accepted: v.Diploma != nil || v.Employment != nil,
		}
		if v.Diploma != nil {
			d := newDiplomaResponse(*v.Diploma)
			resp.Diploma = &d
		}
		out = append(out, resp)
	}
	respond.JSON(w, r, http.StatusOK, out)
}

// addDocuments checks the documents uploaded on a request's on-screen page,
// each read as one diploma extract is.
func (h *Handler) addDocuments(w http.ResponseWriter, r *http.Request) error {
	id, requestedBy, err := sentRequestTarget(r)
	if err != nil {
		return err
	}
	files, err := readDiplomaFiles(w, r, MaxDocumentsPerUpload)
	if err != nil {
		return err
	}
	verdicts, err := h.service.AddDocuments(r.Context(), orgFromRequest(r).ID, id, requestedBy, files)
	if err != nil {
		return mapError(err)
	}
	writeDocumentVerdicts(w, r, verdicts)
	return nil
}

// hostedAddDocuments checks the documents a hosted link's subject uploaded.
// The link is checked before the body is read, as for diplomas: the route is
// anonymous. It reads no more files than the request still takes.
func (h *Handler) hostedAddDocuments(w http.ResponseWriter, r *http.Request) error {
	slots, err := h.service.HostedDocumentsOpen(r.Context(), r.PathValue("token"))
	if err != nil {
		return mapError(err)
	}
	files, err := readDiplomaFiles(w, r, slots)
	if err != nil {
		return err
	}
	verdicts, err := h.service.HostedAddDocuments(r.Context(), r.PathValue("token"), files)
	if err != nil {
		return mapError(err)
	}
	writeDocumentVerdicts(w, r, verdicts)
	return nil
}
