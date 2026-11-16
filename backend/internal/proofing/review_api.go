package proofing

import (
	"context"
	"fmt"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/google/uuid"

	"github.com/privacybydesign/yivi-businesswallet/backend/internal/proofingprovider"
)

// maxReviewerNameLength bounds the reviewer an API key or review link names.
const maxReviewerNameLength = 100

// apiReviewer checks the name of the person deciding in an org's own system:
// required, at most maxReviewerNameLength characters, and printable.
func apiReviewer(raw string) (string, error) {
	name := strings.TrimSpace(raw)
	switch {
	case name == "":
		return "", fmt.Errorf("%w: name the reviewer, the person deciding", ErrInvalidInput)
	case utf8.RuneCountInString(name) > maxReviewerNameLength:
		return "", fmt.Errorf("%w: the reviewer is at most %d characters", ErrInvalidInput, maxReviewerNameLength)
	case strings.IndexFunc(name, func(r rune) bool { return !unicode.IsPrint(r) }) >= 0:
		return "", fmt.Errorf("%w: the reviewer has a character that does not print", ErrInvalidInput)
	}
	return name, nil
}

// ReviewView is what a reviewer outside the wallet decides on: the request
// with its reviews, the reviewers' decisions, and, once the check settled,
// its identity, which carries the images only while the identity waits.
type ReviewView struct {
	Request   Request
	Identity  *proofingprovider.Identity
	Decisions []Decision
	Diplomas  []Diploma
	// AlwaysReview is a request in review whose flow version sends every
	// session to review.
	AlwaysReview bool
}

// ExternalReview is ReviewView of one of the org's external requests, for its
// API key. Reading the identity is audited as a result read.
func (s *Service) ExternalReview(ctx context.Context, orgID, id uuid.UUID) (ReviewView, error) {
	req, err := s.ExternalRequest(ctx, orgID, id)
	if err != nil {
		return ReviewView{}, err
	}
	return s.reviewView(ctx, req)
}

func (s *Service) reviewView(ctx context.Context, req Request) (ReviewView, error) {
	out := ReviewView{Request: req}
	if req.PurgedAt == nil && req.session != nil && req.Status.Settled() {
		_, identity, err := s.requestResult(ctx, req)
		if err != nil {
			return ReviewView{}, err
		}
		if req.Status != StatusNeedsReview {
			identity.Photo, identity.Selfie, identity.ReferencePhoto = nil, nil, nil
			identity.DocumentImage, identity.DocumentImageBack = nil, nil
		}
		out.Identity = &identity
	}
	decisions, err := s.RequestDecisions(ctx, req.OrganizationID, req.ID)
	if err != nil {
		return ReviewView{}, err
	}
	out.Decisions = decisions
	diplomas, err := s.RequestDiplomas(ctx, []uuid.UUID{req.ID})
	if err != nil {
		return ReviewView{}, err
	}
	out.Diplomas = diplomas[req.ID]
	always, err := s.AlwaysReviewed(ctx, []Request{req}, s.now())
	if err != nil {
		return ReviewView{}, err
	}
	out.AlwaysReview = always[req.ID]
	return out, nil
}

// DecideExternalReview decides kind on one of the org's external requests
// for its API key, as reviewer, the person deciding in the org's own system.
func (s *Service) DecideExternalReview(ctx context.Context, orgID, id uuid.UUID, kind ReviewKind, reviewer string, in ReviewItemInput) (Request, error) {
	name, err := apiReviewer(reviewer)
	if err != nil {
		return Request{}, err
	}
	if _, err := s.requests.GetExternal(ctx, orgID, id); err != nil {
		return Request{}, err
	}
	in.APIReviewer, in.ReviewerID, in.Scope = name, uuid.Nil, ReviewScopeAll
	return s.DecideReviewItem(ctx, orgID, id, kind, name, in)
}

// CheckExternalSealsAgain is CheckSealsAgain on one of the org's external
// requests, for its API key.
func (s *Service) CheckExternalSealsAgain(ctx context.Context, orgID, id uuid.UUID, kind ReviewKind) (Request, error) {
	if _, err := s.requests.GetExternal(ctx, orgID, id); err != nil {
		return Request{}, err
	}
	return s.CheckSealsAgain(ctx, orgID, id, kind, StepReviewInput{Scope: ReviewScopeAll})
}

// stepReviewOrder is the steps a reviewer decides, in the order the review
// walks them, after the identity: the Verzekeringsbericht, the diplomas, the
// address.
var stepReviewOrder = []ReviewKind{ReviewKindEmployment, ReviewKindDiplomas, ReviewKindAddress}

// reviewParts are req's review items in the order the review walks them, each
// with where it stands: its own review (identity or data_request) while it
// waits or once a reviewer decided it, then each step it has an item of.
func reviewParts(req Request) []reviewPart {
	var out []reviewPart
	own := req.reviewKind()
	switch {
	case req.Status == StatusNeedsReview && req.PurgedAt == nil:
		out = append(out, reviewPart{Kind: own, State: ReviewPending})
	case own == ReviewKindIdentity && req.IdentityReviewedAt != nil:
		state := ReviewRejected
		if req.Status == StatusApproved {
			state = ReviewConfirmed
		}
		out = append(out, reviewPart{Kind: own, State: state, DecidedAt: req.IdentityReviewedAt})
	}
	for _, kind := range stepReviewOrder {
		if state := req.stepReview(kind); state != "" {
			out = append(out, reviewPart{Kind: kind, State: state, DecidedAt: req.stepReviewedAt(kind)})
		}
	}
	return out
}

// reviewPart is one review item of a request and where it stands.
type reviewPart struct {
	Kind      ReviewKind
	State     ReviewState
	DecidedAt *time.Time
}

// stepReviewedAt is when r's review item of kind was decided or lapsed.
func (r Request) stepReviewedAt(kind ReviewKind) *time.Time {
	switch kind {
	case ReviewKindAddress:
		return r.AddressReviewedAt
	case ReviewKindEmployment:
		return r.EmploymentReviewedAt
	case ReviewKindDiplomas:
		return r.DiplomasReviewedAt
	}
	return nil
}
