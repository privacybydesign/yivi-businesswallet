package attestation

import (
	"fmt"
	"regexp"
	"strings"
)

// IdentifierClaim is the WE BUILD shared identifier object every organization-subject
// credential carries, as rb-ownership's EntityIdentifier defines it: at least one of
// euid, lei, tax and other (YIV-530).
const IdentifierClaim = "identifier"

// identifierFields are the identifier object's members, in display order.
var identifierFields = []string{"euid", "lei", "tax", "other"}

// leiPattern is an ISO 17442 Legal Entity Identifier: 20 upper-case alphanumerics.
var leiPattern = regexp.MustCompile(`^[A-Z0-9]{20}$`)

// normalizeIdentifier checks an issuance's identifier object and drops its empty
// members, so a credential never carries a blank identifier.
func normalizeIdentifier(value any) (map[string]any, error) {
	in, ok := value.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("%w: %q must be an object", ErrInvalidAttribute, IdentifierClaim)
	}
	allowed := make(map[string]bool, len(identifierFields))
	for _, f := range identifierFields {
		allowed[f] = true
	}
	out := make(map[string]any, len(in))
	for key, raw := range in {
		if !allowed[key] {
			return nil, fmt.Errorf("%w: %q has no member %q", ErrInvalidAttribute, IdentifierClaim, key)
		}
		s, ok := raw.(string)
		if !ok {
			return nil, fmt.Errorf("%w: %s.%s must be text", ErrInvalidAttribute, IdentifierClaim, key)
		}
		if s = strings.TrimSpace(s); s != "" {
			out[key] = s
		}
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("%w: %q needs at least one of %s", ErrMissingAttribute, IdentifierClaim, strings.Join(identifierFields, ", "))
	}
	if lei, ok := out["lei"].(string); ok && !leiPattern.MatchString(lei) {
		return nil, fmt.Errorf("%w: %s.lei must be 20 letters or digits", ErrInvalidAttribute, IdentifierClaim)
	}
	return out, nil
}
