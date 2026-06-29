package organization

import (
	"fmt"
	"net/http"
	"strconv"

	"github.com/privacybydesign/yivi-businesswallet/backend/internal/audit"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/respond"
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
	respond.JSON(w, r, http.StatusOK, page)
	return nil
}
