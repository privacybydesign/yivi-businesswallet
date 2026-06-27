package user

import (
	"errors"

	"github.com/google/uuid"
)

var ErrNotFound = errors.New("user: not found")

type User struct {
	ID            uuid.UUID `json:"id"`
	Email         string    `json:"email"`
	PreferredName *string   `json:"preferredName"`
	GivenNames    string    `json:"givenNames"`
	NamePrefix    *string   `json:"namePrefix"`
	LastName      string    `json:"lastName"`
}
