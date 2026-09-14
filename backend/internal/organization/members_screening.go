package organization

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"

	"github.com/google/uuid"

	"github.com/privacybydesign/yivi-businesswallet/backend/internal/auth"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/respond"
)

func (h *Handler) getScreeningSettings(w http.ResponseWriter, r *http.Request) error {
	org := OrgFromContext(r.Context())
	settings, err := h.store.GetScreeningSettings(r.Context(), org.ID)
	if err != nil {
		return fmt.Errorf("getting screening settings: %w", err)
	}
	respond.JSON(w, r, http.StatusOK, settings)
	return nil
}

type screeningSettingsRequest struct {
	RequiredFor                   string   `json:"requiredFor"`
	RequiredCodes                 []string `json:"requiredCodes"`
	MaxAgeAtUploadDays            *int     `json:"maxAgeAtUploadDays"`
	EmployeeRecheckIntervalMonths *int     `json:"employeeRecheckIntervalMonths"`
	ExternalRecheckIntervalMonths *int     `json:"externalRecheckIntervalMonths"`
	RecheckAnchor                 string   `json:"recheckAnchor"`
	ReminderDaysBefore            []int32  `json:"reminderDaysBefore"`
	OverdueReminderIntervalDays   int      `json:"overdueReminderIntervalDays"`
	OverdueReminderMaxCount       int      `json:"overdueReminderMaxCount"`
	OverdueConsequence            string   `json:"overdueConsequence"`
	AcceptYiviCredential          bool     `json:"acceptYiviCredential"`
}

func (h *Handler) putScreeningSettings(w http.ResponseWriter, r *http.Request) error {
	var req screeningSettingsRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		return badRequest("invalid_body", "invalid request body")
	}

	org := OrgFromContext(r.Context())
	settings, err := h.store.SaveScreeningSettings(r.Context(), org.ID, ScreeningSettingsInput(req))
	if errors.Is(err, ErrScreeningSettingsInvalid) {
		return badRequest("invalid_input", err.Error())
	}
	if err != nil {
		return fmt.Errorf("saving screening settings: %w", err)
	}
	respond.JSON(w, r, http.StatusOK, settings)
	return nil
}

func (h *Handler) screeningHistory(w http.ResponseWriter, r *http.Request) error {
	userID, err := uuid.Parse(r.PathValue("userId"))
	if err != nil {
		return badRequest("invalid_id", "invalid user id")
	}
	org := OrgFromContext(r.Context())
	history, err := h.store.ListScreeningHistory(r.Context(), org.ID, userID)
	if err != nil {
		return fmt.Errorf("listing screening history: %w", err)
	}
	respond.JSON(w, r, http.StatusOK, struct {
		History []ScreeningRecord `json:"history"`
	}{History: history})
	return nil
}

type requestVogRequest struct {
	Reason string `json:"reason"`
}

func (h *Handler) requestVog(w http.ResponseWriter, r *http.Request) error {
	userID, err := uuid.Parse(r.PathValue("userId"))
	if err != nil {
		return badRequest("invalid_id", "invalid user id")
	}
	var req requestVogRequest
	if r.ContentLength != 0 {
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			return badRequest("invalid_body", "invalid request body")
		}
	}
	return h.requestVogFor(w, r, []uuid.UUID{userID}, req.Reason)
}

type requestVogBulkRequest struct {
	UserIDs []string `json:"userIds"`
	Reason  string   `json:"reason"`
}

func (h *Handler) requestVogBulk(w http.ResponseWriter, r *http.Request) error {
	var req requestVogBulkRequest
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
	return h.requestVogFor(w, r, ids, req.Reason)
}

// requestVogFor is the admin on-demand "request VOG" (#242 §5), single or
// bulk: it marks each member requested and e-mails them best-effort, linking
// into the app rather than a bearer-token page - unlike re-identification, a
// member being screened already has an account and can simply sign in.
func (h *Handler) requestVogFor(w http.ResponseWriter, r *http.Request, userIDs []uuid.UUID, reason string) error {
	org := OrgFromContext(r.Context())
	actor := auth.UserFromContext(r.Context())
	requested, err := h.store.RequestVog(r.Context(), org.ID, userIDs, actor.ID, strings.TrimSpace(reason))
	if err != nil {
		return fmt.Errorf("requesting vog: %w", err)
	}
	for _, m := range requested {
		h.sendVogRequestedEmail(r.Context(), org, m, reason)
	}
	respond.JSON(w, r, http.StatusOK, requestVogResponse{Requested: len(requested)})
	return nil
}

type requestVogResponse struct {
	Requested int `json:"requested"`
}

func (h *Handler) sendVogRequestedEmail(ctx context.Context, org Organization, m RequestedVogMember, reason string) {
	if h.mailer == nil {
		return
	}
	url := h.appBaseURL + "/" + org.Slug + "/vog"
	if err := h.mailer.SendVogRequested(ctx, org.ID, m.Email, org.Name, url, strings.TrimSpace(reason)); err != nil {
		slog.WarnContext(ctx, "vog-requested e-mail not sent",
			slog.String("email", m.Email), slog.Any("error", err))
	}
}

// uploadMemberVog is the admin upload-on-behalf path (#242 §5): allowed,
// because date of birth is persisted on the membership, the match is exactly
// the self-service full name + date-of-birth match.
func (h *Handler) uploadMemberVog(w http.ResponseWriter, r *http.Request) error {
	userID, err := uuid.Parse(r.PathValue("userId"))
	if err != nil {
		return badRequest("invalid_id", "invalid user id")
	}
	actor := auth.UserFromContext(r.Context())
	return h.uploadVogFor(w, r, userID, CheckedByAdmin, &actor.ID)
}
