package proofing

import (
	"net/http"
	"time"

	"github.com/privacybydesign/yivi-businesswallet/backend/internal/respond"
)

// The public API's review routes: an org's software reads what waits for a
// reviewer and decides it, without anyone opening the wallet.
func (h *Handler) registerPublicReviewAPI(mux *http.ServeMux) {
	mux.Handle("GET /proofing/sessions/{sessionID}/review", h.requireAPIKey(ScopeReviewsRead, h.apiGetReview))
	mux.Handle("POST /proofing/sessions/{sessionID}/review-items/{kind}", h.requireAPIKey(ScopeReviewsWrite, h.apiDecideReviewItem))
	mux.Handle("POST /proofing/sessions/{sessionID}/review-items/{kind}/seal-check", h.requireAPIKey(ScopeReviewsWrite, h.apiCheckSealsAgain))
	mux.Handle("POST /proofing/sessions/{sessionID}/review-links", h.requireAPIKey(ScopeReviewsWrite, h.apiCreateReviewLink))
}

// apiReviewItem is one part of a session a reviewer decides and where it
// stands.
type apiReviewItem struct {
	Kind      ReviewKind  `json:"kind"`
	State     ReviewState `json:"state"`
	DecidedAt *time.Time  `json:"decidedAt,omitempty"`
}

// apiReviewResponse is what a reviewer outside the wallet decides on.
type apiReviewResponse struct {
	ID       string   `json:"id"`
	Status   Status   `json:"status"`
	FlowKind FlowKind `json:"flowKind"`
	// Waiting are the parts that wait for a decision, in the order the review
	// walks them; Items every part, waiting or decided.
	Waiting []ReviewKind    `json:"waiting"`
	Items   []apiReviewItem `json:"items"`
	// StepReviewDueAt is when the first waiting step lapses undecided.
	StepReviewDueAt *time.Time `json:"stepReviewDueAt,omitempty"`
	// FlowAlwaysReview is a session in review because its flow sends every
	// session there, not because the checks doubted it.
	FlowAlwaysReview bool `json:"flowAlwaysReview"`
	// SealChecks are the files whose seal could not be checked at upload.
	SealChecks []SealCheck        `json:"sealChecks"`
	Decisions  []decisionResponse `json:"decisions"`
	// Result is the session's result once it settled: the identity, the
	// evidence, the steps' data, and the images only while the identity waits.
	Result *adminResultResponse `json:"result,omitempty"`
}

func (h *Handler) newAPIReviewResponse(v ReviewView) apiReviewResponse {
	req, now := v.Request, time.Now()
	out := apiReviewResponse{
		ID: publicSessionID(req.ID), Status: req.EffectiveStatus(now), FlowKind: req.FlowKind,
		Waiting: []ReviewKind{}, Items: []apiReviewItem{}, StepReviewDueAt: req.StepReviewDueAt,
		FlowAlwaysReview: v.AlwaysReview, SealChecks: append([]SealCheck{}, req.SealChecks...),
		Decisions: []decisionResponse{},
	}
	for _, p := range reviewParts(req) {
		out.Items = append(out.Items, apiReviewItem(p))
		if p.State == ReviewPending {
			out.Waiting = append(out.Waiting, p.Kind)
		}
	}
	for _, d := range v.Decisions {
		out.Decisions = append(out.Decisions, decisionResponse(d))
	}
	if v.Identity != nil {
		result := adminResultResponse{apiResultResponse: newAPIResultResponse(req, *v.Identity, now)}
		result.Diplomas = newDiplomaResponses(v.Diplomas)
		if req.Status == StatusNeedsReview {
			result.Identity = newAPIIdentity(*v.Identity)
		}
		result.Photo, result.Selfie = newAdminImage(v.Identity.Photo), newAdminImage(v.Identity.Selfie)
		result.ReferencePhoto = newAdminImage(v.Identity.ReferencePhoto)
		result.DocumentImage = newAdminImage(v.Identity.DocumentImage)
		result.DocumentImageBack = newAdminImage(v.Identity.DocumentImageBack)
		out.Result = &result
	}
	return out
}

func (h *Handler) apiGetReview(w http.ResponseWriter, r *http.Request) error {
	id, err := apiSessionID(r)
	if err != nil {
		return err
	}
	view, err := h.service.ExternalReview(r.Context(), callerFromContext(r.Context()).Org.ID, id)
	if err != nil {
		return mapError(err)
	}
	// It carries the person's identity and photos: no cache keeps them.
	w.Header().Set(headerCacheControl, cacheNoStore)
	respond.JSON(w, r, http.StatusOK, h.newAPIReviewResponse(view))
	return nil
}

// apiReviewDecisionRequest is a decision on one part: confirm or reject, a
// reason, the reviewer deciding, and for rejecting the identity optionally
// what was wrong (errorCode).
type apiReviewDecisionRequest struct {
	Decision  string `json:"decision"`
	Reason    string `json:"reason"`
	Reviewer  string `json:"reviewer"`
	ErrorCode string `json:"errorCode"`
}

func (h *Handler) apiDecideReviewItem(w http.ResponseWriter, r *http.Request) error {
	id, err := apiSessionID(r)
	if err != nil {
		return err
	}
	kind, err := reviewKindFrom(r)
	if err != nil {
		return err
	}
	var body apiReviewDecisionRequest
	if err := decode(r, &body); err != nil {
		return err
	}
	if body.Decision != "confirm" && body.Decision != "reject" {
		return &respond.APIError{Status: http.StatusBadRequest, Code: "invalid_input", Message: "decision is confirm or reject"}
	}
	orgID := callerFromContext(r.Context()).Org.ID
	if _, err := h.service.DecideExternalReview(r.Context(), orgID, id, kind, body.Reviewer, ReviewItemInput{
		Confirm: body.Decision == "confirm", Reason: body.Reason, ErrorCode: body.ErrorCode,
	}); err != nil {
		return mapError(err)
	}
	return h.apiGetReview(w, r)
}

func (h *Handler) apiCheckSealsAgain(w http.ResponseWriter, r *http.Request) error {
	id, err := apiSessionID(r)
	if err != nil {
		return err
	}
	kind, err := reviewKindFrom(r)
	if err != nil {
		return err
	}
	if _, err := h.service.CheckExternalSealsAgain(r.Context(), callerFromContext(r.Context()).Org.ID, id, kind); err != nil {
		return mapError(err)
	}
	return h.apiGetReview(w, r)
}

// reviewKindFrom is the review item kind a route names.
func reviewKindFrom(r *http.Request) (ReviewKind, error) {
	kind := ReviewKind(r.PathValue("kind"))
	if !kind.known() {
		return "", &respond.APIError{Status: http.StatusNotFound, Code: "not_found", Message: "no such review item kind"}
	}
	return kind, nil
}
