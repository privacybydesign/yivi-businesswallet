package eudiholder

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"reflect"
	"testing"
)

func segment(t *testing.T, v any) string {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return base64.RawURLEncoding.EncodeToString(b)
}

func digestOf(disclosure string) string {
	sum := sha256.Sum256([]byte(disclosure))
	return base64.RawURLEncoding.EncodeToString(sum[:])
}

func TestResolveSdJwtPutsNestedAndArrayDisclosuresBack(t *testing.T) {
	city := segment(t, []any{"s1", "city", "Utrecht"})
	first := segment(t, []any{"s2", map[string]any{"name": "A"}})
	reps := segment(t, []any{"s3", "reps", []any{map[string]any{"...": digestOf(first)}, map[string]any{"name": "B"}}})
	payload := map[string]any{
		"vct":     "uri:eu.eudi.eucc.1",
		"_sd_alg": "sha-256",
		"_sd":     []any{digestOf(reps)},
		"address": map[string]any{"_sd": []any{digestOf(city)}, "street": "Main"},
	}
	raw := segment(t, map[string]any{"alg": "ES256"}) + "." + segment(t, payload) + ".sig~" + city + "~" + first + "~" + reps + "~"

	got, err := resolveSdJwt(raw)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]any{
		"vct":     "uri:eu.eudi.eucc.1",
		"address": map[string]any{"street": "Main", "city": "Utrecht"},
		"reps":    []any{map[string]any{"name": "A"}, map[string]any{"name": "B"}},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("resolved payload\n got %v\nwant %v", got, want)
	}
}

func TestResolveSdJwtRejectsAnythingElse(t *testing.T) {
	if _, err := resolveSdJwt("not-a-jwt"); err == nil {
		t.Error("want an error for a value that is not an SD-JWT")
	}
}
