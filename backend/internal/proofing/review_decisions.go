package proofing

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/privacybydesign/yivi-businesswallet/backend/internal/audit"
)

// DecisionKind is what a reviewer decided on: the session's review or its
// address's.
type DecisionKind string

const (
	DecisionReview  DecisionKind = "review"
	DecisionAddress DecisionKind = "address"
)

// Decision is a reviewer's decision on a request, as its audit event keeps
// it: what (review_decided or address_reviewed), the outcome, why, by whom and
// when. A purge erases the reason from the event, so Reason is empty then.
type Decision struct {
	Kind DecisionKind
	// Outcome is approved or rejected for a review, confirmed or rejected for
	// an address.
	Outcome   string
	Reason    string
	ErrorCode string
	// DecidedBy is the reviewer's name; empty when the wallet decided on its
	// own (a paused org's open reviews).
	DecidedBy string
	DecidedAt time.Time
}

// decisionFields is the part of a decision event's metadata ("after") a
// Decision reads.
type decisionFields struct {
	Decision      string `json:"decision"`
	AddressReview string `json:"addressReview"`
	Reason        string `json:"reason"`
	ErrorCode     string `json:"errorCode"`
}

// RequestDecisions is every reviewer decision on request id, oldest first.
// The caller has already reached the request.
func (s *Service) RequestDecisions(ctx context.Context, orgID, id uuid.UUID) ([]Decision, error) {
	page, err := s.events.ListForTarget(ctx, orgID, audit.TargetIdentityProofingRequest, id.String(), nil, audit.MaxListLimit)
	if err != nil {
		return nil, err
	}
	slices.Reverse(page.Events)
	out := []Decision{}
	for _, e := range page.Events {
		var kind DecisionKind
		switch e.Action {
		case audit.IdentityProofingReviewDecided:
			kind = DecisionReview
		case audit.IdentityProofingAddressReviewed:
			kind = DecisionAddress
		default:
			continue
		}
		var meta struct {
			After decisionFields `json:"after"`
		}
		if err := json.Unmarshal(e.Metadata, &meta); err != nil {
			return nil, fmt.Errorf("proofing: read decision %s: %w", e.ID, err)
		}
		outcome := meta.After.Decision
		if kind == DecisionAddress {
			outcome = meta.After.AddressReview
		}
		out = append(out, Decision{
			Kind: kind, Outcome: outcome, Reason: meta.After.Reason, ErrorCode: meta.After.ErrorCode,
			DecidedBy: actorName(e.Actor), DecidedAt: e.OccurredAt,
		})
	}
	return out, nil
}

// actorName is a member's name as the wallet shows it: the preferred name,
// else the given names and last name; empty for no member.
func actorName(a *audit.EventActor) string {
	if a == nil {
		return ""
	}
	if a.PreferredName != nil && *a.PreferredName != "" {
		return *a.PreferredName
	}
	return strings.TrimSpace(a.GivenNames + " " + a.LastName)
}
