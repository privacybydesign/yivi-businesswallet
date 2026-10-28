package proofing

import (
	"net/http"
	"time"

	"github.com/privacybydesign/yivi-businesswallet/backend/internal/respond"
)

type flowEmploymentBody struct {
	EmploymentMode EmploymentMode `json:"employmentMode"`
}

func (h *Handler) getFlowEmployment(w http.ResponseWriter, r *http.Request) error {
	mode, err := h.service.FlowEmployment(r.Context(), orgFromRequest(r), r.PathValue("flowID"))
	if err != nil {
		return mapError(err)
	}
	respond.JSON(w, r, http.StatusOK, flowEmploymentBody{EmploymentMode: mode})
	return nil
}

func (h *Handler) saveFlowEmployment(w http.ResponseWriter, r *http.Request) error {
	var body flowEmploymentBody
	if err := decode(r, &body); err != nil {
		return err
	}
	mode, err := h.service.SaveFlowEmployment(r.Context(), orgFromRequest(r), r.PathValue("flowID"), body.EmploymentMode)
	if err != nil {
		return mapError(err)
	}
	respond.JSON(w, r, http.StatusOK, flowEmploymentBody{EmploymentMode: mode})
	return nil
}

// employmentModeOf is the request's mode, off for a row from before the step.
func employmentModeOf(req Request) EmploymentMode {
	if req.Employment == "" {
		return EmploymentOff
	}
	return req.Employment
}

// employmentUntil is when the subject can last upload their statement:
// EmploymentWindow after the identity check of a request that asks for it
// and holds none yet finished (stepsUntil), nil otherwise.
func employmentUntil(req Request) *time.Time {
	if !req.Employment.asked() || req.SubjectEmployment != nil {
		return nil
	}
	return req.stepsUntil(EmploymentWindow)
}

// employmentVerdictResponse is what became of an uploaded statement.
type employmentVerdictResponse struct {
	FileName    string      `json:"fileName"`
	Accepted    bool        `json:"accepted"`
	Employment  *Employment `json:"employment,omitempty"`
	Reason      string      `json:"reason,omitempty"`
	FailedCheck string      `json:"failedCheck,omitempty"`
}

func writeEmploymentVerdict(w http.ResponseWriter, r *http.Request, v EmploymentVerdict) {
	respond.JSON(w, r, http.StatusOK, employmentVerdictResponse{
		FileName: v.FileName, Accepted: v.Employment != nil, Employment: v.Employment, Reason: v.Reason, FailedCheck: v.FailedCheck,
	})
}

// employmentUploadSlots is how many files an upload takes: the one statement.
const employmentUploadSlots = 1

// addEmployment checks the statement uploaded on a request's on-screen page.
// It is read as one diploma extract is: one file, at most MaxDiplomaFileBytes.
func (h *Handler) addEmployment(w http.ResponseWriter, r *http.Request) error {
	id, requestedBy, err := sentRequestTarget(r)
	if err != nil {
		return err
	}
	files, err := readDiplomaFiles(w, r, employmentUploadSlots)
	if err != nil {
		return err
	}
	verdict, err := h.service.AddEmployment(r.Context(), orgFromRequest(r).ID, id, requestedBy, files[0])
	if err != nil {
		return mapError(err)
	}
	writeEmploymentVerdict(w, r, verdict)
	return nil
}

// hostedAddEmployment checks the statement a hosted link's subject uploaded.
// The link is checked before the body is read, as for diplomas: the route is
// anonymous.
func (h *Handler) hostedAddEmployment(w http.ResponseWriter, r *http.Request) error {
	if err := h.service.HostedEmploymentOpen(r.Context(), r.PathValue("token")); err != nil {
		return mapError(err)
	}
	files, err := readDiplomaFiles(w, r, employmentUploadSlots)
	if err != nil {
		return err
	}
	verdict, err := h.service.HostedAddEmployment(r.Context(), r.PathValue("token"), files[0])
	if err != nil {
		return mapError(err)
	}
	writeEmploymentVerdict(w, r, verdict)
	return nil
}
