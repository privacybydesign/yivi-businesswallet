package audit

import (
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestCursorRoundTrip(t *testing.T) {
	want := Cursor{
		OccurredAt: time.Date(2026, 6, 29, 14, 22, 5, 123456789, time.UTC),
		ID:         uuid.MustParse("018f9c2a-0000-7000-8000-000000000001"),
	}

	got, err := DecodeCursor(EncodeCursor(want))
	if err != nil {
		t.Fatalf("DecodeCursor: %v", err)
	}
	if !got.OccurredAt.Equal(want.OccurredAt) {
		t.Errorf("OccurredAt = %s, want %s", got.OccurredAt, want.OccurredAt)
	}
	if got.ID != want.ID {
		t.Errorf("ID = %s, want %s", got.ID, want.ID)
	}
}

func TestDecodeCursorRejectsGarbage(t *testing.T) {
	cases := []string{"", "not-base64!!", "bm90LWEtY3Vyc29y", "MjAyNi0wNi0yOXwx"}
	for _, c := range cases {
		if _, err := DecodeCursor(c); err == nil {
			t.Errorf("DecodeCursor(%q) = nil error, want error", c)
		}
	}
}
