package session

import "testing"

func TestNewToken_HashMatchesRaw(t *testing.T) {
	raw, hash, err := newToken()
	if err != nil {
		t.Fatalf("newToken: %v", err)
	}
	if raw == "" {
		t.Fatal("raw token must not be empty")
	}
	if hashToken(raw) != hash {
		t.Fatal("hash returned by newToken must equal hashToken(raw)")
	}
}

func TestNewToken_Unique(t *testing.T) {
	const n = 1000
	seen := make(map[string]struct{}, n)
	for range n {
		raw, _, err := newToken()
		if err != nil {
			t.Fatalf("newToken: %v", err)
		}
		if _, dup := seen[raw]; dup {
			t.Fatalf("duplicate token generated: %q", raw)
		}
		seen[raw] = struct{}{}
	}
}

func TestHashToken_Deterministic(t *testing.T) {
	first := hashToken("abc")
	second := hashToken("abc")
	if first != second {
		t.Fatal("hashToken must be deterministic")
	}
	if hashToken("abc") == hashToken("abd") {
		t.Fatal("different inputs must hash differently")
	}
}
