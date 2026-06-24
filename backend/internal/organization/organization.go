package organization

import (
	"errors"

	"github.com/google/uuid"
)

var ErrNotFound = errors.New("organization not found")

type Organization struct {
	ID   uuid.UUID `json:"id"`
	Name string    `json:"name"`
}
