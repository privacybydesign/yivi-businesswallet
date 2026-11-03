package proofing

import (
	"context"
	"fmt"
	"net/http"
	"slices"

	"github.com/google/uuid"

	"github.com/privacybydesign/yivi-businesswallet/backend/internal/auth"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/organization"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/respond"
)

// overviewRows is how many rows each overview list shows; All people has the rest.
const overviewRows = 5

// Overview is the org's external requests as the overview and the dashboard
// show them: exact counts, and each list's first rows.
type Overview struct {
	Unfinished int
	Held       int
	// Waiting is the held requests, then the unfinished ones.
	Waiting  []Request
	Finished []Request
}

// OverviewScope is whose requests an overview reads: RequestedBy's, or the
// whole org's; Held adds those in review, for a reader who cannot decide them.
type OverviewScope struct {
	RequestedBy *uuid.UUID
	Held        bool
}

var (
	unfinishedStatuses = []string{string(StatusPending), string(StatusInProgress)}
	heldStatuses       = []string{string(StatusNeedsReview)}
	// A withdrawn request is not news.
	finishedStatuses = []string{string(StatusApproved), string(StatusRejected), string(StatusExpired)}
)

// overviewWhere takes $1 the org, $2 the sender or NULL, $3 the external kind.
const overviewWhere = `r.organization_id = $1 AND ($2::uuid IS NULL OR r.requested_by = $2)
		AND r.subject_kind = $3`

// Overview matches on the effective status, as the per-status lists do.
func (s *RequestStore) Overview(ctx context.Context, orgID uuid.UUID, scope OverviewScope) (Overview, error) {
	var o Overview
	err := s.db.QueryRow(ctx, `SELECT
			count(*) FILTER (WHERE `+effectiveStatusColumn+` = ANY($4)),
			count(*) FILTER (WHERE `+effectiveStatusColumn+` = ANY($5) AND r.purged_at IS NULL AND $6)
		FROM identity_proofing_requests r WHERE `+overviewWhere,
		orgID, scope.RequestedBy, string(SubjectExternal), unfinishedStatuses, heldStatuses, scope.Held,
	).Scan(&o.Unfinished, &o.Held)
	if err != nil {
		return Overview{}, fmt.Errorf("proofing: overview counts org %s: %w", orgID, err)
	}
	if o.Held > 0 {
		if o.Waiting, err = s.overviewList(ctx, orgID, scope, heldStatuses, true,
			`COALESCE(r.completed_at, r.created_at), r.created_at`); err != nil {
			return Overview{}, err
		}
	}
	if o.Unfinished > 0 && len(o.Waiting) < overviewRows {
		unfinished, err := s.overviewList(ctx, orgID, scope, unfinishedStatuses, false, `r.created_at`)
		if err != nil {
			return Overview{}, err
		}
		o.Waiting = append(o.Waiting, unfinished[:min(len(unfinished), overviewRows-len(o.Waiting))]...)
	}
	o.Finished, err = s.overviewList(ctx, orgID, scope, finishedStatuses, false,
		`COALESCE(r.completed_at, r.created_at) DESC, r.created_at DESC`)
	if err != nil {
		return Overview{}, err
	}
	return o, nil
}

// overviewList reads the first overviewRows in statuses; unpurged leaves out
// the erased ones.
func (s *RequestStore) overviewList(ctx context.Context, orgID uuid.UUID, scope OverviewScope,
	statuses []string, unpurged bool, order string,
) ([]Request, error) {
	rows, err := s.db.Query(ctx, `SELECT `+requestColumns+requestFrom+`
		WHERE `+overviewWhere+` AND `+effectiveStatusColumn+` = ANY($4)
			AND (NOT $5 OR r.purged_at IS NULL)
		ORDER BY `+order+` LIMIT $6`,
		orgID, scope.RequestedBy, string(SubjectExternal), statuses, unpurged, overviewRows)
	if err != nil {
		return nil, fmt.Errorf("proofing: overview list org %s: %w", orgID, err)
	}
	defer rows.Close()
	out := []Request{}
	for rows.Next() {
		req, err := s.scanRequest(rows)
		if err != nil {
			return nil, fmt.Errorf("proofing: scan overview request org %s: %w", orgID, err)
		}
		out = append(out, req)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("proofing: overview list org %s: %w", orgID, err)
	}
	return out, nil
}

func (s *Service) Overview(ctx context.Context, orgID uuid.UUID, scope OverviewScope) (Overview, error) {
	return s.requests.Overview(ctx, orgID, scope)
}

type overviewResponse struct {
	Unfinished int               `json:"unfinished"`
	Held       int               `json:"held"`
	Waiting    []requestResponse `json:"waiting"`
	Finished   []requestResponse `json:"finished"`
}

// overview is an admin's of the whole org, a member's of what they sent.
func (h *Handler) overview(w http.ResponseWriter, r *http.Request) error {
	ctx := r.Context()
	scope := OverviewScope{Held: !organization.MayReview(ctx)}
	if !organization.ActsAsAdmin(ctx) {
		id := auth.UserFromContext(ctx).ID
		scope.RequestedBy = &id
	}
	o, err := h.service.Overview(ctx, organization.OrgFromContext(ctx).ID, scope)
	if err != nil {
		return mapError(err)
	}
	listed, err := h.listedResponses(ctx, slices.Concat(o.Waiting, o.Finished))
	if err != nil {
		return err
	}
	respond.JSON(w, r, http.StatusOK, overviewResponse{
		Unfinished: o.Unfinished, Held: o.Held,
		Waiting: listed[:len(o.Waiting)], Finished: listed[len(o.Waiting):],
	})
	return nil
}
