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
	// AvatarUpdatedAt is when the user last changed their avatar photo, or nil when
	// they have none. It carries no image data — it is the marker that one exists
	// and the version in AvatarURL — so it stays out of the JSON.
	AvatarUpdatedAt *time.Time `json:"-"`
}
