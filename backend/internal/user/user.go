package user

import (
	"errors"

	"github.com/google/uuid"
)

var ErrNotFound = errors.New("user: not found")

type User struct {
	ID    uuid.UUID `json:"id"`
	Email string    `json:"email"`
}
