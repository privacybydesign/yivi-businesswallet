package proofing

import (
	"fmt"
	"net/http"
	"time"

	"github.com/privacybydesign/yivi-businesswallet/backend/internal/proofingprovider"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/respond"
)

// dataExportFilename is the name a "see my data" export downloads as.
const dataExportFilename = "my-data.json"

type flowKindBody struct {
	Kind FlowKind `json:"kind"`
}

func (h *Handler) getFlowKind(w http.ResponseWriter, r *http.Request) error {
	kind, err := h.service.FlowKind(r.Context(), orgFromRequest(r), r.PathValue("flowID"))
	if err != nil {
		return mapError(err)
	}
	respond.JSON(w, r, http.StatusOK, flowKindBody{Kind: kind})
	return nil
}

func (h *Handler) saveFlowKind(w http.ResponseWriter, r *http.Request) error {
	var body flowKindBody
	if err := decode(r, &body); err != nil {
		return err
	}
	kind, err := h.service.SaveFlowKind(r.Context(), orgFromRequest(r), r.PathValue("flowID"), body.Kind)
	if err != nil {
		return mapError(err)
	}
	respond.JSON(w, r, http.StatusOK, flowKindBody{Kind: kind})
	return nil
}

// dataMatchesResponse is what a data request's review shows next to the
// person's identity: what they ask for, and the customer's sessions of them.
type dataMatchesResponse struct {
	FlowKind FlowKind            `json:"flowKind"`
	Matches  []dataMatchResponse `json:"matches"`
}

type dataMatchResponse struct {
	RequestID      string     `json:"requestId"`
	SessionID      string     `json:"sessionId"`
	FlowName       string     `json:"flowName"`
	Status         Status     `json:"status"`
	Method         string     `json:"method,omitempty"`
	AssuranceLevel string     `json:"assuranceLevel,omitempty"`
	EIDASLevel     string     `json:"eidasLevel,omitempty"`
	CreatedAt      time.Time  `json:"createdAt"`
	CompletedAt    *time.Time `json:"completedAt,omitempty"`
	PurgedAt       *time.Time `json:"purgedAt,omitempty"`
	Level          MatchLevel `json:"level"`
	// Approved is the reviewer's choice; absent until the decision.
	Approved *bool `json:"approved,omitempty"`
}

func (h *Handler) dataMatches(w http.ResponseWriter, r *http.Request) error {
	id, err := parseRequestID(r.PathValue("requestID"))
	if err != nil {
		return &respond.APIError{Status: http.StatusBadRequest, Code: "invalid_id", Message: "invalid request id"}
	}
	req, matches, err := h.service.DataMatches(r.Context(), orgFromRequest(r).ID, id)
	if err != nil {
		return mapError(err)
	}
	out := dataMatchesResponse{FlowKind: req.FlowKind, Matches: make([]dataMatchResponse, 0, len(matches))}
	for _, m := range matches {
		out.Matches = append(out.Matches, dataMatchResponse{
			RequestID: m.RequestID.String(), SessionID: PublicSessionID(m.RequestID), FlowName: m.FlowName,
			Status: m.Status, Method: m.Method, AssuranceLevel: m.AssuranceLevel, EIDASLevel: m.EIDASLevel,
			CreatedAt: m.CreatedAt, CompletedAt: m.CompletedAt, PurgedAt: m.PurgedAt, Level: m.Level, Approved: m.Approved,
		})
	}
	respond.JSON(w, r, http.StatusOK, out)
	return nil
}

type dataExportResponse struct {
	RequestID  string                      `json:"sessionId"`
	ExportedAt time.Time                   `json:"exportedAt"`
	Sessions   []dataExportSessionResponse `json:"sessions"`
}

type dataExportSessionResponse struct {
	SessionID      string     `json:"sessionId"`
	Flow           string     `json:"flow"`
	Status         Status     `json:"status"`
	Method         string     `json:"method,omitempty"`
	AssuranceLevel string     `json:"assuranceLevel,omitempty"`
	EIDASLevel     string     `json:"eidasLevel,omitempty"`
	CreatedAt      time.Time  `json:"createdAt"`
	CompletedAt    *time.Time `json:"completedAt,omitempty"`
	// PurgeAt is when the wallet erases the session's personal data on its
	// own; PurgedAt when it did.
	PurgeAt  *time.Time `json:"purgeAt,omitempty"`
	PurgedAt *time.Time `json:"purgedAt,omitempty"`
	// Contact is the name and address the customer gave for the person.
	Contact struct {
		Name  string `json:"name,omitempty"`
		Email string `json:"email,omitempty"`
	} `json:"contact"`
	Identity *dataExportIdentity `json:"identity,omitempty"`
	Diplomas []diplomaResponse   `json:"diplomas"`
}

// dataExportIdentity is what the check read off the person and their
// document. Images are named in ImagesHeld, not included.
type dataExportIdentity struct {
	GivenName   string                     `json:"givenName,omitempty"`
	FamilyName  string                     `json:"familyName,omitempty"`
	BirthDate   string                     `json:"birthDate,omitempty"`
	Nationality string                     `json:"nationality,omitempty"`
	Evidence    *proofingprovider.Evidence `json:"evidence,omitempty"`
	ImagesHeld  []string                   `json:"imagesHeld"`
}

// exportImages names the images a session holds, in a fixed order.
var exportImages = []struct {
	name  string
	image func(proofingprovider.Identity) *proofingprovider.Image
}{
	{"documentPortrait", func(id proofingprovider.Identity) *proofingprovider.Image { return id.Photo }},
	{"selfie", func(id proofingprovider.Identity) *proofingprovider.Image { return id.Selfie }},
	{"referencePhoto", func(id proofingprovider.Identity) *proofingprovider.Image { return id.ReferencePhoto }},
	{"documentFront", func(id proofingprovider.Identity) *proofingprovider.Image { return id.DocumentImage }},
	{"documentBack", func(id proofingprovider.Identity) *proofingprovider.Image { return id.DocumentImageBack }},
}

func newDataExportIdentity(id proofingprovider.Identity) *dataExportIdentity {
	out := &dataExportIdentity{
		GivenName: id.GivenName, FamilyName: id.FamilyName, BirthDate: id.BirthDate, Nationality: id.Nationality,
		Evidence: id.Evidence, ImagesHeld: []string{},
	}
	for _, e := range exportImages {
		if e.image(id) != nil {
			out.ImagesHeld = append(out.ImagesHeld, e.name)
		}
	}
	return out
}

// writeDataExport answers an export as a JSON attachment.
func writeDataExport(w http.ResponseWriter, r *http.Request, export DataExport, err error) error {
	if err != nil {
		return mapError(err)
	}
	out := dataExportResponse{
		RequestID: PublicSessionID(export.RequestID), ExportedAt: export.ExportedAt,
		Sessions: make([]dataExportSessionResponse, 0, len(export.Sessions)),
	}
	now := time.Now()
	for _, s := range export.Sessions {
		req := s.Request
		session := dataExportSessionResponse{
			SessionID: PublicSessionID(req.ID), Flow: req.FlowName, Status: req.EffectiveStatus(now),
			Method: string(req.Method), AssuranceLevel: req.AssuranceLevel, EIDASLevel: req.EIDASLevel,
			CreatedAt: req.CreatedAt, CompletedAt: req.CompletedAt, PurgeAt: req.PurgeAt, PurgedAt: req.PurgedAt,
			Diplomas: newDiplomaResponses(s.Diplomas),
		}
		session.Contact.Name, session.Contact.Email = req.SubjectName, req.SubjectEmail
		if s.Identity != nil {
			session.Identity = newDataExportIdentity(*s.Identity)
		}
		out.Sessions = append(out.Sessions, session)
	}
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", dataExportFilename))
	w.Header().Set("Cache-Control", "no-store")
	respond.JSON(w, r, http.StatusOK, out)
	return nil
}

func (h *Handler) adminDataExport(w http.ResponseWriter, r *http.Request) error {
	id, err := parseRequestID(r.PathValue("requestID"))
	if err != nil {
		return &respond.APIError{Status: http.StatusBadRequest, Code: "invalid_id", Message: "invalid request id"}
	}
	export, err := h.service.AdminDataExport(r.Context(), orgFromRequest(r).ID, id)
	return writeDataExport(w, r, export, err)
}

func (h *Handler) apiDataExport(w http.ResponseWriter, r *http.Request) error {
	id, err := apiSessionID(r)
	if err != nil {
		return err
	}
	caller := callerFromContext(r.Context())
	export, err := h.service.CustomerDataExport(r.Context(), caller.Org.ID, caller.CustomerID, id)
	return writeDataExport(w, r, export, err)
}

func (h *Handler) hostedDataExport(w http.ResponseWriter, r *http.Request) error {
	export, err := h.service.HostedDataExport(r.Context(), r.PathValue("token"))
	return writeDataExport(w, r, export, err)
}
