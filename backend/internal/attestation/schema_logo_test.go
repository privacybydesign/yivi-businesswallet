package attestation

import (
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestDetectLogoType(t *testing.T) {
	cases := []struct {
		name string
		data []byte
		want string
		ok   bool
	}{
		{"png", []byte{0x89, 0x50, 0x4e, 0x47, 0x0d, 0x0a, 0x1a, 0x0a}, "image/png", true},
		{"gif", []byte("GIF89a and more bytes to sniff"), "image/gif", true},
		{"svg", []byte(`<svg xmlns="http://www.w3.org/2000/svg"></svg>`), "image/svg+xml", true},
		{"svg with xml prolog", []byte(`<?xml version="1.0"?><svg></svg>`), "image/svg+xml", true},
		{"plain text", []byte("just some text, not an image"), "", false},
		{"empty", nil, "", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := detectLogoType(tc.data)
			if ok != tc.ok || got != tc.want {
				t.Fatalf("detectLogoType = (%q, %v), want (%q, %v)", got, ok, tc.want, tc.ok)
			}
		})
	}
}

func TestSchemaLogoURL(t *testing.T) {
	id := uuid.MustParse("11111111-1111-1111-1111-111111111111")
	// No image -> no URL.
	if got := schemaLogoURL("acme", Schema{ID: id, HasLogo: false}); got != "" {
		t.Fatalf("schemaLogoURL without image = %q, want empty", got)
	}
	// With an image -> a versioned admin-preview path (updatedAt cache-buster).
	updatedAt := time.Unix(1_700_000_000, 0)
	got := schemaLogoURL("acme", Schema{ID: id, HasLogo: true, UpdatedAt: updatedAt})
	want := "/api/v1/orgs/acme/attestations/schemas/11111111-1111-1111-1111-111111111111/logo?v=1700000000"
	if got != want {
		t.Fatalf("schemaLogoURL = %q, want %q", got, want)
	}
}
