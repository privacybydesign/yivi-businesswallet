package user

import (
	"errors"
	"time"

	"github.com/google/uuid"
)

var (
	ErrNotFound   = errors.New("user: not found")
	ErrEmailTaken = errors.New("user: email already taken")
)

type User struct {
	ID            uuid.UUID `json:"id"`
	Email         Email     `json:"email"`
	PreferredName *string   `json:"preferredName"`
	GivenNames    string    `json:"givenNames"`
	LastName      string    `json:"lastName"`
	// HasAvatar reports whether a portrait photo is stored; AvatarUpdatedAt is when
	// it was last replaced. The bytes are never carried here — they are served from
	// their own endpoint, and these two fields are all a caller needs to build the
	// URL for it (see AvatarURL).
	HasAvatar       bool       `json:"-"`
	AvatarUpdatedAt *time.Time `json:"-"`
}
