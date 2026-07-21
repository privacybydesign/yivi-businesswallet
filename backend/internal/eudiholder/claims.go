package eudiholder

import (
	"encoding/json"
	"sort"
)

// HeldAttribute is one disclosed claim of a held credential: its payload key, the
// issuer-metadata display label for it (empty when the credential carries no
// metadata label, so the caller falls back to the key), and the disclosed value.
// The value may be any JSON type (string, number, bool, nested object/array).
type HeldAttribute struct {
	Key   string `json:"key"`
	Label string `json:"label"`
	Value any    `json:"value"`
}

// HeldCredential is a held credential's display metadata and disclosed attributes,
// assembled from the holder engine for the detail view. IssuerName is the issuer's
// metadata display label (empty when the credential carried none, so the caller
// falls back to the issuer identifier); Attributes are display-ordered and labelled.
type HeldCredential struct {
	IssuerName string          `json:"issuerName"`
	Attributes []HeldAttribute `json:"attributes"`
}

// reservedClaims are the registered JWT / SD-JWT VC claims that carry protocol
// metadata rather than disclosed attributes. Claims decodes the verified SD-JWT
// payload for display, so these are filtered out — the caller wants the credential's
// attributes (e.g. company_name), not the envelope (iss, vct, cnf, the SD digests).
var reservedClaims = map[string]struct{}{
	"iss":     {},
	"sub":     {},
	"aud":     {},
	"exp":     {},
	"nbf":     {},
	"iat":     {},
	"jti":     {},
	"vct":     {},
	"cnf":     {},
	"status":  {},
	"_sd":     {},
	"_sd_alg": {},
}

// decodeClaims unmarshals a verified SD-JWT payload (as stored in the holder
// engine's ProcessedSdJwtPayload) and returns only its disclosed attributes,
// stripping the registered protocol claims. An empty or JSON-null payload yields
// an empty (non-nil) map so callers never dereference nil.
func decodeClaims(payload []byte) (map[string]any, error) {
	attributes := map[string]any{}
	if len(payload) == 0 {
		return attributes, nil
	}
	var all map[string]any
	if err := json.Unmarshal(payload, &all); err != nil {
		return nil, err
	}
	for key, value := range all {
		if _, reserved := reservedClaims[key]; reserved {
			continue
		}
		attributes[key] = value
	}
	return attributes, nil
}

// assembleAttributes turns a verified SD-JWT payload into a display-ordered,
// labelled attribute list. labels maps a payload key to its issuer-metadata
// display name; order lists keys in the metadata's declared order. Claims that
// have metadata appear first in that order; any remaining payload-only claims
// follow, sorted for stable output. A key with no label yields an empty Label so
// the caller can fall back to the key. labels/order may be nil (no metadata).
func assembleAttributes(payload []byte, labels map[string]string, order []string) ([]HeldAttribute, error) {
	values, err := decodeClaims(payload)
	if err != nil {
		return nil, err
	}

	attributes := make([]HeldAttribute, 0, len(values))
	emitted := make(map[string]struct{}, len(values))
	appendKey := func(key string) {
		if _, done := emitted[key]; done {
			return
		}
		value, ok := values[key]
		if !ok {
			return
		}
		emitted[key] = struct{}{}
		attributes = append(attributes, HeldAttribute{Key: key, Label: labels[key], Value: value})
	}

	for _, key := range order {
		appendKey(key)
	}

	rest := make([]string, 0, len(values))
	for key := range values {
		if _, done := emitted[key]; !done {
			rest = append(rest, key)
		}
	}
	sort.Strings(rest)
	for _, key := range rest {
		appendKey(key)
	}

	return attributes, nil
}
