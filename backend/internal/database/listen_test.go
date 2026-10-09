package database

import (
	"context"
	"testing"
	"time"
)

func TestRunJobRecoversPanic(t *testing.T) {
	next, err := runJob(context.Background(), func(context.Context) (time.Time, error) {
		panic("boom")
	})
	if err == nil {
		t.Fatal("runJob error = nil, want the panic as an error")
	}
	if !next.IsZero() {
		t.Errorf("next = %s, want zero", next)
	}
}
