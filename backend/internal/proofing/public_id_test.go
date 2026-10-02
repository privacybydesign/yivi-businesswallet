package proofing

import (
	"strings"
	"testing"

	"github.com/google/uuid"
)

func TestPublicSessionIDRoundTrips(t *testing.T) {
	id := uuid.New()
	public := PublicSessionID(id)
	if !strings.HasPrefix(public, "ps_") || strings.Contains(public, id.String()) {
		t.Fatalf("PublicSessionID = %q; want an opaque ps_ id", public)
	}
	if got, ok := parsePublicSessionID(public); !ok || got != id {
		t.Errorf("parse(%q) = %v, %v; want %v", public, got, ok, id)
	}
	for _, bad := range []string{id.String(), "ps_", "ps_!!", "ps_" + strings.Repeat("a", 10), "xs_" + public[3:]} {
		if _, ok := parsePublicSessionID(bad); ok {
			t.Errorf("parse(%q) accepted", bad)
		}
	}
}
