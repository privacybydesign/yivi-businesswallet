// Package lifecycle holds the status moves of what an org can suspend and
// revoke: its wallet and its attestation keys. One table, so neither can be
// brought back from revoked by a plain status write.
package lifecycle

import (
	"errors"
	"slices"
)

const (
	Active    = "active"
	Suspended = "suspended"
	Revoked   = "revoked"
)

// ErrTransition is returned for a move the table does not allow.
var ErrTransition = errors.New("lifecycle: status transition not allowed")

// transitions lists, per status, the statuses it may move to. There is no way
// back: revoked is final, and nothing reactivates a suspended one yet.
var transitions = map[string][]string{
	Active:    {Suspended, Revoked},
	Suspended: {Revoked},
}

func CanTransition(from, to string) bool {
	return slices.Contains(transitions[from], to)
}

// Transition is CanTransition as an error, for a store to return.
func Transition(from, to string) error {
	if !CanTransition(from, to) {
		return ErrTransition
	}
	return nil
}
