package user

import (
	"fmt"
	"time"
)

// AvatarURL is the API path the authenticated user's own avatar photo is served
// from, or "" when updatedAt is nil (no avatar stored). The timestamp is a
// cache-busting version so a replaced photo is re-fetched rather than served
// stale from the browser cache.
//
// It lives here, next to the AvatarUpdatedAt this is built from, so both the /me
// response and the avatar upload response name the same path without either slice
// importing the other.
func AvatarURL(updatedAt *time.Time) string {
	if updatedAt == nil {
		return ""
	}
	return fmt.Sprintf("/api/v1/me/avatar?v=%d", updatedAt.Unix())
}
