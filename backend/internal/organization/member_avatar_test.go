package organization

import (
	"bytes"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/privacybydesign/yivi-businesswallet/backend/internal/audit"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/respond"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/user"
)

func avatarRequest(userID string) *http.Request {
	req := httptest.NewRequest(http.MethodGet, "/orgs/acme/members/"+userID+"/avatar", nil)
	req.SetPathValue("userId", userID)
	return req.WithContext(contextWithOrg(req.Context(), Organization{ID: uuid.New(), Slug: "acme"}))
}

func TestMemberAvatarStreamsTheStoredPhoto(t *testing.T) {
	stored := user.Avatar{Bytes: []byte{0xFF, 0xD8, 0xFF}, ContentType: user.AvatarContentType}
	h := &Handler{store: fakeRepo{avatar: stored}}
	rec := httptest.NewRecorder()

	if err := h.memberAvatar(rec, avatarRequest(uuid.New().String())); err != nil {
		t.Fatalf("memberAvatar: %v", err)
	}

	if rec.Code != http.StatusOK {
		t.Errorf("status = %d, want 200", rec.Code)
	}
	if !bytes.Equal(rec.Body.Bytes(), stored.Bytes) {
		t.Error("body does not match the stored avatar")
	}
}

func TestMemberAvatarErrors(t *testing.T) {
	cases := []struct {
		name       string
		userID     string
		repo       fakeRepo
		wantStatus int
	}{
		{"no avatar set", uuid.New().String(), fakeRepo{avatarErr: user.ErrNoAvatar}, http.StatusNotFound},
		{"not a uuid", "not-a-uuid", fakeRepo{}, http.StatusBadRequest},
	}
	for _, tc := range cases {
		h := &Handler{store: tc.repo}
		err := h.memberAvatar(httptest.NewRecorder(), avatarRequest(tc.userID))

		var apiErr *respond.APIError
		if !errors.As(err, &apiErr) {
			t.Fatalf("%s: error = %v, want *respond.APIError", tc.name, err)
		}
		if apiErr.Status != tc.wantStatus {
			t.Errorf("%s: status = %d, want %d", tc.name, apiErr.Status, tc.wantStatus)
		}
	}
}

func TestMemberAvatarPathEscapesTheSlug(t *testing.T) {
	id := uuid.MustParse("11111111-2222-3333-4444-555555555555")
	want := "/api/v1/orgs/a%2Fb/members/11111111-2222-3333-4444-555555555555/avatar"
	if got := MemberAvatarPath("a/b", id); got != want {
		t.Errorf("MemberAvatarPath = %q, want %q", got, want)
	}
}

func TestEntryAvatarURI(t *testing.T) {
	at := time.Unix(1700000000, 0)
	userID := uuid.MustParse("11111111-2222-3333-4444-555555555555")
	invitationID := uuid.New()

	cases := []struct {
		name  string
		entry MemberEntry
		want  string
	}{
		{
			name:  "invited entry has no user yet",
			entry: MemberEntry{Status: StatusInvited, InvitationID: &invitationID},
			want:  "",
		},
		{
			name:  "active member without a photo",
			entry: MemberEntry{Status: StatusActive, UserID: &userID},
			want:  "",
		},
		{
			name:  "active member with a photo",
			entry: MemberEntry{Status: StatusActive, UserID: &userID, HasAvatar: true, AvatarUpdatedAt: &at},
			want:  "/api/v1/orgs/acme/members/11111111-2222-3333-4444-555555555555/avatar?v=1700000000",
		},
	}
	for _, tc := range cases {
		if got := entryAvatarURI("acme", tc.entry); got != tc.want {
			t.Errorf("%s: entryAvatarURI = %q, want %q", tc.name, got, tc.want)
		}
	}
}

func TestAddActorAvatarURIs(t *testing.T) {
	at := time.Unix(1700000000, 0)
	actorID := uuid.MustParse("11111111-2222-3333-4444-555555555555")
	events := []audit.Event{
		{Actor: nil},
		{Actor: &audit.EventActor{UserID: actorID}},
		{Actor: &audit.EventActor{UserID: actorID, HasAvatar: true, AvatarUpdatedAt: &at}},
	}

	addActorAvatarURIs("acme", events)

	if events[1].Actor.AvatarURI != "" {
		t.Errorf("actor without a photo got %q, want empty", events[1].Actor.AvatarURI)
	}
	want := "/api/v1/orgs/acme/members/11111111-2222-3333-4444-555555555555/avatar?v=1700000000"
	if events[2].Actor.AvatarURI != want {
		t.Errorf("actor avatarUri = %q, want %q", events[2].Actor.AvatarURI, want)
	}
}
