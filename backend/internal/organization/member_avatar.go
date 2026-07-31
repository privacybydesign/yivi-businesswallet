package organization

import (
	"fmt"
	"net/url"
	"time"

	"github.com/google/uuid"

	"github.com/privacybydesign/yivi-businesswallet/backend/internal/audit"
)

// memberAvatarURL is the API path serving a member's avatar photo, or "" when
// they have none. The path is org-scoped because that is what authorises the
// read: only an administrator of an organisation the member belongs to may fetch
// it. updatedAt is a cache-busting version so a replaced photo is re-fetched
// rather than served stale from the browser cache.
func memberAvatarURL(slug string, userID uuid.UUID, updatedAt *time.Time) string {
	if updatedAt == nil {
		return ""
	}
	return fmt.Sprintf("/api/v1/orgs/%s/members/%s/avatar?v=%d",
		url.PathEscape(slug), url.PathEscape(userID.String()), updatedAt.Unix())
}

// fillMemberAvatarURI sets a member's avatar path from the stored version.
func fillMemberAvatarURI(m *Member, slug string) {
	m.AvatarURI = memberAvatarURL(slug, m.UserID, m.AvatarUpdatedAt)
}

// fillEntryAvatarURIs sets each active entry's avatar path. An invited entry has
// no user account yet, so it keeps the empty URI and renders as initials.
func fillEntryAvatarURIs(entries []MemberEntry, slug string) {
	for i := range entries {
		if entries[i].UserID == nil {
			continue
		}
		entries[i].AvatarURI = memberAvatarURL(slug, *entries[i].UserID, entries[i].AvatarUpdatedAt)
	}
}

// fillActorAvatarURIs sets each audit actor's avatar path. The audit reader only
// knows user ids, so the organisation the events are listed under — and with it
// the path that authorises the read — is filled in here.
func fillActorAvatarURIs(page *audit.Page, slug string) {
	for i := range page.Events {
		actor := page.Events[i].Actor
		if actor == nil {
			continue
		}
		actor.AvatarURI = memberAvatarURL(slug, actor.UserID, actor.AvatarUpdatedAt)
	}
}
