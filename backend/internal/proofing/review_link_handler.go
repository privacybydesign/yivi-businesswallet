package proofing

import (
	"errors"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/privacybydesign/yivi-businesswallet/backend/internal/respond"
)

// reviewPagePrefix is the SPA route of a review link's page, /r/:token.
const reviewPagePrefix = "/r/"

type apiCreateReviewLinkRequest struct {
	Reviewer string `json:"reviewer"`
}

type apiReviewLinkResponse struct {
	URL       string    `json:"url"`
	ExpiresAt time.Time `json:"expiresAt"`
}

func (h *Handler) apiCreateReviewLink(w http.ResponseWriter, r *http.Request) error {
	id, err := apiSessionID(r)
	if err != nil {
		return err
	}
	var body apiCreateReviewLinkRequest
	if err := decode(r, &body); err != nil {
		return err
	}
	url, expires, err := h.service.CreateReviewLink(r.Context(), callerFromContext(r.Context()), id, body.Reviewer)
	if err != nil {
		return mapError(err)
	}
	w.Header().Set(headerCacheControl, cacheNoStore)
	respond.JSON(w, r, http.StatusCreated, apiReviewLinkResponse{URL: url, ExpiresAt: expires})
	return nil
}

func (h *Handler) registerReviewLinks(mux *http.ServeMux) {
	mux.Handle("GET /review/{token}", h.limitReviewLink(h.reviewLinkView))
	mux.Handle("POST /review/{token}/items/{kind}", h.limitReviewLink(h.reviewLinkDecide))
	mux.Handle("POST /review/{token}/items/{kind}/seal-check", h.limitReviewLink(h.reviewLinkCheckSeals))
}

// limitReviewLink counts a review link's call against its source, as a hosted
// link's, and an unknown link against its authFailureLimit.
func (h *Handler) limitReviewLink(next respond.HandlerFunc) respond.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) error {
		if err := rateLimited(w, h.hostedSources, requestSource(r)); err != nil {
			return err
		}
		err := next(w, r)
		var apiErr *respond.APIError
		if errors.As(err, &apiErr) && apiErr.Code == reviewLinkNotFoundCode {
			if limited := h.failureLimited(w, r); limited != nil {
				return limited
			}
		}
		return err
	}
}

const reviewLinkNotFoundCode = "review_link_not_found"

// reviewLinkResponse is what a review link's page shows: the session as the
// wallet's review pages read it, its result while its identity waits, and
// who the link is for.
type reviewLinkResponse struct {
	SessionID string               `json:"sessionId"`
	Request   requestResponse      `json:"request"`
	Result    *adminResultResponse `json:"result,omitempty"`
	Reviewer  string               `json:"reviewer"`
	ExpiresAt time.Time            `json:"expiresAt"`
	// EmbedOrigins may frame the page and receive its messages.
	EmbedOrigins []string `json:"embedOrigins"`
	Organization string   `json:"organization"`
}

func (h *Handler) reviewLinkView(w http.ResponseWriter, r *http.Request) error {
	view, link, settings, err := h.service.ReviewLinkReview(r.Context(), r.PathValue("token"))
	if err != nil {
		return mapError(err)
	}
	req, now := view.Request, time.Now()
	stepsOnly := !ReviewScopeReviews.reaches(req)
	out := reviewLinkResponse{
		SessionID: publicSessionID(req.ID), Request: requestResponseLimited(req, view.Diplomas, now, stepsOnly),
		Reviewer: link.Reviewer, ExpiresAt: link.ExpiresAt,
		EmbedOrigins: append([]string{}, settings.RedirectOrigins...), Organization: settings.SignedAs(),
	}
	out.Request.Decisions = []decisionResponse{}
	for _, d := range view.Decisions {
		if stepsOnly && req.IdentityReviewedAt == nil && d.Kind == DecisionReview {
			continue
		}
		out.Request.Decisions = append(out.Request.Decisions, decisionResponse(d))
	}
	out.Request.FlowAlwaysReview = view.AlwaysReview
	if view.Identity != nil && req.Status == StatusNeedsReview {
		out.Result = h.newAPIReviewResponse(view).Result
	}
	w.Header().Set(headerCacheControl, cacheNoStore)
	respond.JSON(w, r, http.StatusOK, out)
	return nil
}

func (h *Handler) reviewLinkDecide(w http.ResponseWriter, r *http.Request) error {
	kind, err := reviewKindFrom(r)
	if err != nil {
		return err
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxFormBodyBytes)
	var body reviewItemRequest
	if err := decode(r, &body); err != nil {
		return err
	}
	if body.Decision != "confirm" && body.Decision != "reject" {
		return &respond.APIError{Status: http.StatusBadRequest, Code: "invalid_input", Message: "decision is confirm or reject"}
	}
	if err := h.service.ReviewLinkDecide(r.Context(), r.PathValue("token"), kind, ReviewItemInput{
		Confirm: body.Decision == "confirm", Reason: body.Reason, ErrorCode: body.ErrorCode,
	}); err != nil {
		return mapError(err)
	}
	return h.reviewLinkView(w, r)
}

func (h *Handler) reviewLinkCheckSeals(w http.ResponseWriter, r *http.Request) error {
	kind, err := reviewKindFrom(r)
	if err != nil {
		return err
	}
	if err := h.service.ReviewLinkCheckSeals(r.Context(), r.PathValue("token"), kind); err != nil {
		return mapError(err)
	}
	return h.reviewLinkView(w, r)
}

// reviewFramePolicy is the CSP frame-ancestors of a review link's page: its
// org's allowed origins, else none.
func (h *Handler) reviewFramePolicy(r *http.Request, token string) string {
	if ok, _ := h.hostedSources.Allow(requestSource(r)); !ok {
		return frameNone
	}
	origins, err := h.service.ReviewLinkEmbedOrigins(r.Context(), token)
	if errors.Is(err, ErrReviewLinkNotFound) {
		h.authFailures.Allow(requestSource(r))
		return frameNone
	}
	if err != nil {
		slog.ErrorContext(r.Context(), "proofing: review page frame origins", slog.Any("error", err))
		return frameNone
	}
	if len(origins) == 0 {
		return frameNone
	}
	return "frame-ancestors " + strings.Join(origins, " ")
}
