package proofing

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/privacybydesign/yivi-businesswallet/backend/internal/respond"
)

// diplomaFormField is the multipart field each extract is uploaded in.
const diplomaFormField = "file"

// maxDiplomaUploadBytes bounds one upload request: every extract at its
// largest, plus the multipart framing.
const maxDiplomaUploadBytes = MaxDiplomasPerRequest*MaxDiplomaFileBytes + 1<<20

type flowDiplomasBody struct {
	DiplomaMode DiplomaMode `json:"diplomaMode"`
}

func (h *Handler) getFlowDiplomas(w http.ResponseWriter, r *http.Request) error {
	mode, err := h.service.FlowDiplomas(r.Context(), orgFromRequest(r), r.PathValue("flowID"))
	if err != nil {
		return mapError(err)
	}
	respond.JSON(w, r, http.StatusOK, flowDiplomasBody{DiplomaMode: mode})
	return nil
}

func (h *Handler) saveFlowDiplomas(w http.ResponseWriter, r *http.Request) error {
	var body flowDiplomasBody
	if err := decode(r, &body); err != nil {
		return err
	}
	mode, err := h.service.SaveFlowDiplomas(r.Context(), orgFromRequest(r), r.PathValue("flowID"), body.DiplomaMode)
	if err != nil {
		return mapError(err)
	}
	respond.JSON(w, r, http.StatusOK, flowDiplomasBody{DiplomaMode: mode})
	return nil
}

// diplomaResponse is an extract a request holds, as DUO printed it.
type diplomaResponse struct {
	DocumentType   string     `json:"documentType"`
	Qualification  string     `json:"qualification"`
	Profiles       []string   `json:"profiles"`
	Institution    string     `json:"institution"`
	PlaceOfIssue   string     `json:"placeOfIssue"`
	DateAwarded    string     `json:"dateAwarded"`
	NLQFLevel      string     `json:"nlqfLevel,omitempty"`
	EQFLevel       string     `json:"eqfLevel,omitempty"`
	DocumentNumber string     `json:"documentNumber"`
	SignedAt       *time.Time `json:"signedAt,omitempty"`
	AddedAt        time.Time  `json:"addedAt"`
}

func newDiplomaResponse(d Diploma) diplomaResponse {
	return diplomaResponse{
		DocumentType: d.DocumentType, Qualification: d.Qualification, Profiles: d.Profiles,
		Institution: d.Institution, PlaceOfIssue: d.PlaceOfIssue, DateAwarded: d.DateAwarded.Format(time.DateOnly),
		NLQFLevel: d.NLQFLevel, EQFLevel: d.EQFLevel, DocumentNumber: d.DocumentNumber,
		SignedAt: d.SignedAt, AddedAt: d.CreatedAt,
	}
}

func newDiplomaResponses(diplomas []Diploma) []diplomaResponse {
	out := make([]diplomaResponse, 0, len(diplomas))
	for _, d := range diplomas {
		out = append(out, newDiplomaResponse(d))
	}
	return out
}

// diplomaModeOf is the request's mode, off for a row from before diplomas.
func diplomaModeOf(req Request) DiplomaMode {
	if req.Diplomas == "" {
		return DiplomasOff
	}
	return req.Diplomas
}

// diplomasUntil is when the subject can last add an extract: an hour after
// the approval of a request that asks for them, nil otherwise.
func diplomasUntil(req Request) *time.Time {
	if !req.Diplomas.asked() || req.Status != StatusApproved || req.CompletedAt == nil {
		return nil
	}
	until := req.CompletedAt.Add(DiplomaUploadWindow)
	return &until
}

// diplomaVerdictResponse is what became of one uploaded file.
type diplomaVerdictResponse struct {
	FileName    string           `json:"fileName"`
	Accepted    bool             `json:"accepted"`
	Diploma     *diplomaResponse `json:"diploma,omitempty"`
	Reason      string           `json:"reason,omitempty"`
	FailedCheck string           `json:"failedCheck,omitempty"`
}

func writeDiplomaVerdicts(w http.ResponseWriter, r *http.Request, verdicts []DiplomaVerdict) {
	out := make([]diplomaVerdictResponse, 0, len(verdicts))
	for _, v := range verdicts {
		resp := diplomaVerdictResponse{FileName: v.FileName, Reason: v.Reason, FailedCheck: v.FailedCheck}
		if v.Diploma != nil {
			d := newDiplomaResponse(*v.Diploma)
			resp.Accepted, resp.Diploma = true, &d
		}
		out = append(out, resp)
	}
	respond.JSON(w, r, http.StatusOK, out)
}

// readDiplomaFiles reads the extracts of a multipart upload, each at most
// MaxDiplomaFileBytes, part by part into memory: an upload never spills to a
// temporary file, as ParseMultipartForm's would.
func readDiplomaFiles(w http.ResponseWriter, r *http.Request) ([]DiplomaFile, error) {
	r.Body = http.MaxBytesReader(w, r.Body, maxDiplomaUploadBytes)
	reader, err := r.MultipartReader()
	if err != nil {
		return nil, diplomaReadError(err)
	}
	var files []DiplomaFile
	for {
		part, err := reader.NextPart()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, diplomaReadError(err)
		}
		if part.FormName() != diplomaFormField || part.FileName() == "" {
			continue
		}
		if len(files) == MaxDiplomasPerRequest {
			return nil, &respond.APIError{Status: http.StatusBadRequest, Code: "too_many_files", Message: fmt.Sprintf("upload at most %d extracts", MaxDiplomasPerRequest)}
		}
		b, err := io.ReadAll(io.LimitReader(part, MaxDiplomaFileBytes+1))
		if err != nil {
			return nil, diplomaReadError(err)
		}
		if len(b) > MaxDiplomaFileBytes {
			return nil, &respond.APIError{Status: http.StatusRequestEntityTooLarge, Code: "too_large", Message: fmt.Sprintf("an extract is at most %d bytes", MaxDiplomaFileBytes)}
		}
		files = append(files, DiplomaFile{Name: part.FileName(), Bytes: b})
	}
	if len(files) == 0 {
		return nil, &respond.APIError{Status: http.StatusBadRequest, Code: "invalid_body", Message: "no file uploaded"}
	}
	return files, nil
}

// diplomaReadError is the answer to an upload that does not read: too large
// past maxDiplomaUploadBytes, else not well-formed multipart/form-data.
func diplomaReadError(err error) error {
	var tooLarge *http.MaxBytesError
	if errors.As(err, &tooLarge) {
		return &respond.APIError{Status: http.StatusRequestEntityTooLarge, Code: "too_large", Message: "the upload is too large"}
	}
	return &respond.APIError{Status: http.StatusBadRequest, Code: "invalid_body", Message: "upload the extracts as multipart/form-data"}
}

// addDiplomas checks the extracts uploaded on a request's on-screen page.
func (h *Handler) addDiplomas(w http.ResponseWriter, r *http.Request) error {
	id, requestedBy, err := sentRequestTarget(r)
	if err != nil {
		return err
	}
	files, err := readDiplomaFiles(w, r)
	if err != nil {
		return err
	}
	verdicts, err := h.service.AddDiplomas(r.Context(), orgFromRequest(r).ID, id, requestedBy, files)
	if err != nil {
		return mapError(err)
	}
	writeDiplomaVerdicts(w, r, verdicts)
	return nil
}

// hostedAddDiplomas checks the extracts a hosted link's subject uploaded. The
// link is checked before the body is read: the route is anonymous, and its
// rate limit is keyed on the token, so a fresh token per call would otherwise
// buy a full upload's worth of memory each time.
func (h *Handler) hostedAddDiplomas(w http.ResponseWriter, r *http.Request) error {
	if err := h.service.HostedDiplomasOpen(r.Context(), r.PathValue("token")); err != nil {
		return mapError(err)
	}
	files, err := readDiplomaFiles(w, r)
	if err != nil {
		return err
	}
	verdicts, err := h.service.HostedAddDiplomas(r.Context(), r.PathValue("token"), files)
	if err != nil {
		return mapError(err)
	}
	writeDiplomaVerdicts(w, r, verdicts)
	return nil
}
