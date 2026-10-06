package organization

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/privacybydesign/yivi-businesswallet/backend/internal/auth"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/respond"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/user"
)

type updateMemberTypeRequest struct {
	MemberType           string  `json:"memberType"`
	ExternalOrganisation *string `json:"externalOrganisation"`
}

func (h *Handler) updateMemberType(w http.ResponseWriter, r *http.Request) error {
	userID, err := uuid.Parse(r.PathValue("userId"))
	if err != nil {
		return badRequest("invalid_id", "invalid user id")
	}
	var req updateMemberTypeRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		return badRequest("invalid_body", "invalid request body")
	}
	if req.MemberType != MemberTypeEmployee && req.MemberType != MemberTypeExternal {
		return badRequest("invalid_input", `memberType must be "employee" or "external"`)
	}

	org := OrgFromContext(r.Context())
	member, err := h.store.UpdateMemberType(r.Context(), org.ID, userID, req.MemberType, normalize(req.ExternalOrganisation))
	switch {
	case errors.Is(err, ErrNotMember):
		return &respond.APIError{Status: http.StatusNotFound, Code: "member_not_found", Message: "member not found"}
	case err != nil:
		return fmt.Errorf("updating member type: %w", err)
	}
	member.AvatarURI = user.AvatarURL(MemberAvatarPath(org.Slug, member.UserID), member.HasAvatar, member.AvatarUpdatedAt)
	lookahead, err := h.identityLookaheadDays(r.Context(), org.ID)
	if err != nil {
		return fmt.Errorf("resolving identity lookahead: %w", err)
	}
	member = member.withIdentityStatus(time.Now(), lookahead)
	respond.JSON(w, r, http.StatusOK, member)
	return nil
}

type requestIdentificationRequest struct {
	Reason string `json:"reason"`
}

func (h *Handler) requestIdentification(w http.ResponseWriter, r *http.Request) error {
	userID, err := uuid.Parse(r.PathValue("userId"))
	if err != nil {
		return badRequest("invalid_id", "invalid user id")
	}
	var req requestIdentificationRequest
	if r.ContentLength != 0 {
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			return badRequest("invalid_body", "invalid request body")
		}
	}
	return h.requestIdentificationFor(w, r, []uuid.UUID{userID}, req.Reason)
}

type requestIdentificationBulkRequest struct {
	UserIDs []string `json:"userIds"`
	Reason  string   `json:"reason"`
}

func (h *Handler) requestIdentificationBulk(w http.ResponseWriter, r *http.Request) error {
	var req requestIdentificationBulkRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		return badRequest("invalid_body", "invalid request body")
	}
	if len(req.UserIDs) == 0 {
		return badRequest("invalid_input", "userIds must not be empty")
	}
	ids := make([]uuid.UUID, 0, len(req.UserIDs))
	for _, raw := range req.UserIDs {
		id, err := uuid.Parse(raw)
		if err != nil {
			return badRequest("invalid_input", "invalid user id in userIds")
		}
		ids = append(ids, id)
	}
	return h.requestIdentificationFor(w, r, ids, req.Reason)
}

// requestIdentificationFor is the admin on-demand request (#240 §4), single or
// bulk: it marks each member requested, mints their re-identification link, and
// e-mails it best-effort — a delivery failure never fails the request, since the
// state change (and the in-app banner it produces) already happened.
func (h *Handler) requestIdentificationFor(w http.ResponseWriter, r *http.Request, userIDs []uuid.UUID, reason string) error {
	org := OrgFromContext(r.Context())
	actor := auth.UserFromContext(r.Context())
	requested, err := h.store.RequestIdentification(r.Context(), org.ID, userIDs, actor.ID, strings.TrimSpace(reason))
	if err != nil {
		return fmt.Errorf("requesting identification: %w", err)
	}
	for _, m := range requested {
		h.sendReidentifyEmail(r.Context(), org, m, reason)
	}
	respond.JSON(w, r, http.StatusOK, requestIdentificationResponse{Requested: len(requested)})
	return nil
}

type requestIdentificationResponse struct {
	Requested int `json:"requested"`
}

func (h *Handler) sendReidentifyEmail(ctx context.Context, org Organization, m RequestedMember, reason string) {
	if h.mailer == nil {
		return
	}
	url := h.appBaseURL + "/reidentify/" + m.ReidentifyToken
	if err := h.mailer.SendIdentityRequested(ctx, org.ID, m.Email, org.Name, url, strings.TrimSpace(reason)); err != nil {
		slog.WarnContext(ctx, "identity-requested e-mail not sent",
			slog.String("email", m.Email), slog.Any("error", err))
	}
}

type mintReverifyTokenResponse struct {
	ReidentifyURL string `json:"reidentifyUrl"`
}

// mintOwnReverifyToken backs the in-app banner: a member due, overdue or
// requested for re-identification mints their own link rather than waiting on
// e-mail, using the exact mechanism a reminder or an admin request would.
func (h *Handler) mintOwnReverifyToken(w http.ResponseWriter, r *http.Request) error {
	org := OrgFromContext(r.Context())
	actor := auth.UserFromContext(r.Context())
	token, _, err := h.service.MintOwnReverifyToken(r.Context(), org.ID, actor.ID)
	if err != nil {
		return fmt.Errorf("minting reverify token: %w", err)
	}
	respond.JSON(w, r, http.StatusOK, mintReverifyTokenResponse{ReidentifyURL: h.appBaseURL + "/reidentify/" + token})
	return nil
}

func (h *Handler) getIdentitySettings(w http.ResponseWriter, r *http.Request) error {
	org := OrgFromContext(r.Context())
	settings, err := h.store.GetIdentitySettings(r.Context(), org.ID)
	if err != nil {
		return fmt.Errorf("getting identity settings: %w", err)
	}
	respond.JSON(w, r, http.StatusOK, settings)
	return nil
}

type identitySettingsRequest struct {
	EmployeeIntervalMonths      *int    `json:"employeeIntervalMonths"`
	ExternalIntervalMonths      *int    `json:"externalIntervalMonths"`
	ReminderDaysBefore          []int32 `json:"reminderDaysBefore"`
	OverdueReminderIntervalDays int     `json:"overdueReminderIntervalDays"`
	OverdueReminderMaxCount     int     `json:"overdueReminderMaxCount"`
	CredentialMaxAgeDays        *int    `json:"credentialMaxAgeDays"`
	OverdueConsequence          string  `json:"overdueConsequence"`
}

func (h *Handler) putIdentitySettings(w http.ResponseWriter, r *http.Request) error {
	var req identitySettingsRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		return badRequest("invalid_body", "invalid request body")
	}

	org := OrgFromContext(r.Context())
	settings, err := h.store.SaveIdentitySettings(r.Context(), org.ID, IdentitySettingsInput(req))
	if errors.Is(err, ErrIdentitySettingsInvalid) {
		return badRequest("invalid_input", err.Error())
	}
	if err != nil {
		return fmt.Errorf("saving identity settings: %w", err)
	}
	respond.JSON(w, r, http.StatusOK, settings)
	return nil
}
