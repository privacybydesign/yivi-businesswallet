package organization

import (
	"fmt"
	"net/http"
	"strconv"

	"github.com/google/uuid"

	"github.com/privacybydesign/yivi-businesswallet/backend/internal/audit"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/respond"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/user"
)

func (h *Handler) auditEvents(w http.ResponseWriter, r *http.Request) error {
	q := r.URL.Query()

	limit := 0
	if raw := q.Get("limit"); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n < 0 {
			return badRequest("invalid_limit", "limit must be a non-negative integer")
		}
		limit = n
	}

	var after *audit.Cursor
	if raw := q.Get("cursor"); raw != "" {
		c, err := audit.DecodeCursor(raw)
		if err != nil {
			return badRequest("invalid_cursor", "invalid cursor")
		}
		after = &c
	}

	org := OrgFromContext(r.Context())
	page, err := h.reader.ListForOrganization(r.Context(), org.ID, after, limit)
	if err != nil {
		return fmt.Errorf("listing audit events: %w", err)
	}
	addActorAvatarURIs(org.Slug, page.Events)
	respond.JSON(w, r, http.StatusOK, page)
	return nil
}

// addActorAvatarURIs points each event's actor at the org-scoped avatar endpoint,
// so the log shows who acted with the same photo the member list does. The reader
// is org-agnostic and cannot build that path itself.
func addActorAvatarURIs(slug string, events []audit.Event) {
	for i := range events {
		actor := events[i].Actor
		if actor == nil {
			continue
		}
		actor.AvatarURI = user.AvatarURL(MemberAvatarPath(slug, actor.UserID), actor.HasAvatar, actor.AvatarUpdatedAt)
	}
}

func (h *Handler) memberAuditEvents(w http.ResponseWriter, r *http.Request) error {
	userID, err := uuid.Parse(r.PathValue("userId"))
	if err != nil {
		return badRequest("invalid_user_id", "user id must be a UUID")
	}

	q := r.URL.Query()

	limit := 0
	if raw := q.Get("limit"); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n < 0 {
			return badRequest("invalid_limit", "limit must be a non-negative integer")
		}
		limit = n
	}

	var after *audit.Cursor
	if raw := q.Get("cursor"); raw != "" {
		c, err := audit.DecodeCursor(raw)
		if err != nil {
			return badRequest("invalid_cursor", "invalid cursor")
		}
		after = &c
	}

	org := OrgFromContext(r.Context())
	page, err := h.reader.ListForMember(r.Context(), org.ID, userID, after, limit)
	if err != nil {
		return fmt.Errorf("listing member audit events: %w", err)
	}
	addActorAvatarURIs(org.Slug, page.Events)
	respond.JSON(w, r, http.StatusOK, page)
	return nil
}
