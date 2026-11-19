package lifecycle

import (
	"errors"
	"testing"
)

func TestTransitions(t *testing.T) {
	allowed := map[[2]string]bool{
		{Active, Suspended}:  true,
		{Active, Revoked}:    true,
		{Suspended, Revoked}: true,
	}
	for _, from := range []string{Active, Suspended, Revoked, "unknown"} {
		for _, to := range []string{Active, Suspended, Revoked} {
			want := allowed[[2]string{from, to}]
			if got := CanTransition(from, to); got != want {
				t.Errorf("CanTransition(%s, %s) = %v, want %v", from, to, got, want)
			}
			if err := Transition(from, to); errors.Is(err, ErrTransition) == want {
				t.Errorf("Transition(%s, %s) = %v", from, to, err)
			}
		}
	}
}
