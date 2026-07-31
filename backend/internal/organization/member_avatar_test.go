package organization

import (
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/privacybydesign/yivi-businesswallet/backend/internal/audit"
)

var (
	avatarUpdated = time.Unix(1_700_000_000, 0)
	avatarUserID  = uuid.MustParse("8f14e45f-ceea-467a-9575-1b1b1b1b1b1b")
)

func TestMemberAvatarURL(t *testing.T) {
	cases := map[string]struct {
		slug      string
		updatedAt *time.Time
		want      string
	}{
		"no avatar yields empty path": {"acme", nil, ""},
		"avatar is versioned": {
			"acme", &avatarUpdated,
			"/api/v1/orgs/acme/members/8f14e45f-ceea-467a-9575-1b1b1b1b1b1b/avatar?v=1700000000",
		},
		"slug is path-escaped": {
			"acme corp/eu", &avatarUpdated,
			"/api/v1/orgs/acme%20corp%2Feu/members/8f14e45f-ceea-467a-9575-1b1b1b1b1b1b/avatar?v=1700000000",
		},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			if got := memberAvatarURL(tc.slug, avatarUserID, tc.updatedAt); got != tc.want {
				t.Errorf("memberAvatarURL = %q, want %q", got, tc.want)
			}
		})
	}
}

// An invited entry has no user account yet, so it must not get an avatar path
// built from a nil user id.
func TestFillEntryAvatarURIsSkipsInvitations(t *testing.T) {
	entries := []MemberEntry{
		{Status: StatusActive, UserID: &avatarUserID, AvatarUpdatedAt: &avatarUpdated},
		{Status: StatusActive, UserID: &avatarUserID},
		{Status: StatusInvited, AvatarUpdatedAt: &avatarUpdated},
	}

	fillEntryAvatarURIs(entries, "acme")

	if entries[0].AvatarURI == "" {
		t.Error("active member with a photo got no avatar path")
	}
	if entries[1].AvatarURI != "" {
		t.Errorf("active member without a photo got %q, want empty", entries[1].AvatarURI)
	}
	if entries[2].AvatarURI != "" {
		t.Errorf("invited entry got %q, want empty", entries[2].AvatarURI)
	}
}

func TestFillActorAvatarURIsLeavesSystemEventsAlone(t *testing.T) {
	page := audit.Page{Events: []audit.Event{
		{Actor: &audit.EventActor{UserID: avatarUserID, AvatarUpdatedAt: &avatarUpdated}},
		{Actor: nil},
	}}

	fillActorAvatarURIs(&page, "acme")

	want := "/api/v1/orgs/acme/members/8f14e45f-ceea-467a-9575-1b1b1b1b1b1b/avatar?v=1700000000"
	if page.Events[0].Actor.AvatarURI != want {
		t.Errorf("actor avatarUri = %q, want %q", page.Events[0].Actor.AvatarURI, want)
	}
	if page.Events[1].Actor != nil {
		t.Error("system event gained an actor")
	}
}
