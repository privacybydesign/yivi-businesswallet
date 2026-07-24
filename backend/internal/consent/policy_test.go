package consent

import (
	"reflect"
	"testing"

	"github.com/google/uuid"
)

func pol(id string, kind Kind, pattern string, required []string, effect Effect, fourEyes bool) Policy {
	return Policy{
		ID:                  uuid.MustParse(id),
		Kind:                kind,
		CounterpartyPattern: pattern,
		RequiredAttributes:  required,
		Effect:              effect,
		FourEyes:            fourEyes,
	}
}

const (
	idA = "11111111-1111-1111-1111-111111111111"
	idB = "22222222-2222-2222-2222-222222222222"
	idC = "33333333-3333-3333-3333-333333333333"
)

func TestMatchFirstWins(t *testing.T) {
	// Both policies match; the first in the slice (the store orders by priority,
	// then age) wins — author order is explicit, not incidental.
	first := pol(idA, KindPresentation, "*", nil, EffectAutoDecline, false)
	second := pol(idB, KindPresentation, "*", nil, EffectAutoApprove, false)
	got := Match([]Policy{first, second}, PendingItem{Kind: KindPresentation, Counterparty: "acme", Requested: []string{"email"}})
	if got == nil || got.ID != first.ID {
		t.Fatalf("Match = %v, want first policy %s", got, first.ID)
	}
}

func TestMatchNoMatchStaysNil(t *testing.T) {
	// Absence of a matching policy must never mean auto-yes: no match => nil, and
	// the store leaves the item pending for a human.
	p := pol(idA, KindIssuance, "trusted-issuer", nil, EffectAutoApprove, false)
	got := Match([]Policy{p}, PendingItem{Kind: KindIssuance, Counterparty: "other-issuer", Requested: []string{"diploma"}})
	if got != nil {
		t.Fatalf("Match = %v, want nil (no match)", got)
	}
}

func TestSelectorMatches(t *testing.T) {
	base := PendingItem{Kind: KindPresentation, Counterparty: "acme.example", Requested: []string{"email", "name", "age"}}
	tests := []struct {
		name string
		p    Policy
		item PendingItem
		want bool
	}{
		{"kind mismatch", pol(idA, KindIssuance, "*", nil, EffectAutoApprove, false), base, false},
		{"wildcard counterparty", pol(idA, KindPresentation, "*", nil, EffectAutoApprove, false), base, true},
		{"exact counterparty", pol(idA, KindPresentation, "acme.example", nil, EffectAutoApprove, false), base, true},
		{"exact counterparty mismatch", pol(idA, KindPresentation, "acme.other", nil, EffectAutoApprove, false), base, false},
		{"prefix counterparty", pol(idA, KindPresentation, "acme.*", nil, EffectAutoApprove, false), base, true},
		{"prefix counterparty mismatch", pol(idA, KindPresentation, "bank.*", nil, EffectAutoApprove, false), base, false},
		{"required attrs present", pol(idA, KindPresentation, "*", []string{"email", "name"}, EffectAutoApprove, false), base, true},
		{"required attrs missing one", pol(idA, KindPresentation, "*", []string{"email", "bsn"}, EffectAutoApprove, false), base, false},
		{"empty required matches any", pol(idA, KindPresentation, "*", []string{}, EffectAutoApprove, false), base, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := selectorMatches(tt.p, tt.item); got != tt.want {
				t.Errorf("selectorMatches = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestResolveApprovedSubset(t *testing.T) {
	requested := []string{"email", "name", "age"}
	tests := []struct {
		name   string
		subset []string
		want   []string
	}{
		{"empty means all requested", nil, requested},
		{"narrows to a subset", []string{"email"}, []string{"email"}},
		{"preserves requested order", []string{"age", "email"}, []string{"email", "age"}},
		{"drops attrs not requested", []string{"email", "bsn"}, []string{"email"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := Policy{ApproveSubset: tt.subset}
			if got := resolveApprovedSubset(p, requested); !reflect.DeepEqual(got, tt.want) {
				t.Errorf("resolveApprovedSubset = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestIsSubset(t *testing.T) {
	requested := []string{"email", "name"}
	if !isSubset([]string{"email"}, requested) {
		t.Error("email should be a subset of {email,name}")
	}
	if isSubset([]string{"email", "bsn"}, requested) {
		t.Error("{email,bsn} must not be a subset of {email,name}")
	}
	if !isSubset(nil, requested) {
		t.Error("empty set is a subset of anything")
	}
}
