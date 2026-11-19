package eudiholder

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/privacybydesign/irmago/eudi/storage/db/models"
	"gorm.io/gorm"
)

// Payload rebuilds a held credential's payload from the raw SD-JWT the engine
// stored at issuance. A ref the engine does not hold yields nil.
func (e *Engine) Payload(ctx context.Context, orgID uuid.UUID, ref string) (map[string]any, error) {
	id, err := uuid.Parse(ref)
	if err != nil {
		return nil, nil //nolint:nilerr // a non-uuid ref is not an engine instance
	}
	st, err := e.engineFor(ctx, orgID)
	if err != nil {
		return nil, err
	}
	var inst models.IssuedCredentialInstance
	err = st.Db().WithContext(ctx).Where("id = ?", id).First(&inst).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("eudiholder: payload %s org %s: %w", ref, orgID, err)
	}
	return resolveSdJwt(string(inst.RawCredential))
}

// resolveSdJwt decodes an SD-JWT's issuer-signed payload and puts every
// disclosure back in place. It does not verify anything: the engine verified the
// credential when it received it.
func resolveSdJwt(raw string) (map[string]any, error) {
	parts := strings.Split(raw, "~")
	jwtParts := strings.Split(parts[0], ".")
	if len(jwtParts) != 3 {
		return nil, errors.New("eudiholder: stored credential is not an SD-JWT")
	}
	var payload map[string]any
	if err := decodeSegment(jwtParts[1], &payload); err != nil {
		return nil, fmt.Errorf("eudiholder: stored credential payload: %w", err)
	}
	byDigest := map[string][]any{}
	for _, d := range parts[1:] {
		if d == "" || strings.Count(d, ".") == 2 {
			continue // empty trailer or a key-binding JWT
		}
		var disclosure []any
		if err := decodeSegment(d, &disclosure); err != nil {
			return nil, fmt.Errorf("eudiholder: stored credential disclosure: %w", err)
		}
		sum := sha256.Sum256([]byte(d))
		byDigest[base64.RawURLEncoding.EncodeToString(sum[:])] = disclosure
	}
	out, _ := expandDisclosures(payload, byDigest).(map[string]any)
	return out, nil
}

// expandDisclosures replaces every _sd digest and array "..." placeholder by its
// disclosed value, at any depth.
func expandDisclosures(v any, byDigest map[string][]any) any {
	switch o := v.(type) {
	case map[string]any:
		out := make(map[string]any, len(o))
		for k, val := range o {
			if k != "_sd" && k != "_sd_alg" {
				out[k] = expandDisclosures(val, byDigest)
			}
		}
		digests, _ := o["_sd"].([]any)
		for _, dg := range digests {
			if d, ok := byDigest[fmt.Sprint(dg)]; ok && len(d) == 3 {
				out[fmt.Sprint(d[1])] = expandDisclosures(d[2], byDigest)
			}
		}
		return out
	case []any:
		out := make([]any, 0, len(o))
		for _, el := range o {
			if m, ok := el.(map[string]any); ok && len(m) == 1 && m["..."] != nil {
				if d, ok := byDigest[fmt.Sprint(m["..."])]; ok && len(d) == 2 {
					out = append(out, expandDisclosures(d[1], byDigest))
				}
				continue
			}
			out = append(out, expandDisclosures(el, byDigest))
		}
		return out
	}
	return v
}

func decodeSegment(s string, into any) error {
	b, err := base64.RawURLEncoding.DecodeString(strings.TrimRight(s, "="))
	if err != nil {
		return err
	}
	return json.Unmarshal(b, into)
}
