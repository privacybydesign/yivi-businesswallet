package consent

import "strings"

// PendingItem is the selector-relevant view of an inbound request the matcher
// evaluates. It is separate from ApprovalRequest so Match stays pure — no ids, no
// timestamps, no I/O.
type PendingItem struct {
	Kind         Kind
	Counterparty string
	Requested    []string
}

// Match returns the first policy whose selector matches the item, in the order
// given, or nil when none match. It is the policy engine's evaluation core:
// first-match-wins, pure, and covered by a table-driven test.
//
// The store passes policies already filtered to the item's org + kind and to the
// non-revoked set, ordered by (priority, created_at) — so "first match" is the
// admin's explicit author order, and a revoked policy is never seen here.
// Validity windows (valid_from/valid_until) are deliberately NOT applied: v1
// enforces org-wide with no window; #27 turns windows on behind this same seam.
func Match(policies []Policy, item PendingItem) *Policy {
	for i := range policies {
		if selectorMatches(policies[i], item) {
			return &policies[i]
		}
	}
	return nil
}

func selectorMatches(p Policy, item PendingItem) bool {
	if p.Kind != item.Kind {
		return false
	}
	if !counterpartyMatches(p.CounterpartyPattern, item.Counterparty) {
		return false
	}
	return containsAll(item.Requested, p.RequiredAttributes)
}

// counterpartyMatches supports three forms, kept deliberately small until the
// selector vocabulary settles (the design defers richer patterns and value
// constraints): "*" matches any counterparty; a single trailing "*" is a prefix
// match ("acme.*" matches "acme.example"); anything else is an exact match.
func counterpartyMatches(pattern, counterparty string) bool {
	if pattern == "*" {
		return true
	}
	if prefix, ok := strings.CutSuffix(pattern, "*"); ok {
		return strings.HasPrefix(counterparty, prefix)
	}
	return pattern == counterparty
}

// containsAll reports whether every element of want is present in have. An empty
// want (no attribute constraint) always matches.
func containsAll(have, want []string) bool {
	if len(want) == 0 {
		return true
	}
	set := make(map[string]struct{}, len(have))
	for _, h := range have {
		set[h] = struct{}{}
	}
	for _, w := range want {
		if _, ok := set[w]; !ok {
			return false
		}
	}
	return true
}

// resolveApprovedSubset computes the attribute subset an auto_approve policy
// grants for a given request: the full requested set when the policy does not
// narrow (empty ApproveSubset), else the intersection with the requested set
// (preserving the request's order) so a policy can never approve an attribute the
// counterparty did not ask for.
func resolveApprovedSubset(p Policy, requested []string) []string {
	if len(p.ApproveSubset) == 0 {
		return append([]string(nil), requested...)
	}
	allow := make(map[string]struct{}, len(p.ApproveSubset))
	for _, a := range p.ApproveSubset {
		allow[a] = struct{}{}
	}
	subset := make([]string, 0, len(requested))
	for _, r := range requested {
		if _, ok := allow[r]; ok {
			subset = append(subset, r)
		}
	}
	return subset
}

// isSubset reports whether every element of subset is present in requested.
func isSubset(subset, requested []string) bool {
	return containsAll(requested, subset)
}
