package apidocs

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/getkin/kin-openapi/openapi3"
	"gopkg.in/yaml.v3"
)

// The served docs show identity proofing as the one API an integrator uses:
// its tab holds only the public routes, the wallet's own proofing routes are
// left out, and every other tab stays.
func TestServedDocsShowOneProofingAPI(t *testing.T) {
	full, err := openapi3.NewLoader().LoadFromData(Spec())
	if err != nil {
		t.Fatalf("load the spec: %v", err)
	}
	doc, err := openapi3.NewLoader().LoadFromData(served)
	if err != nil {
		t.Fatalf("load the served docs: %v", err)
	}
	if err := doc.Validate(context.Background()); err != nil {
		t.Fatalf("the served docs are not a valid OpenAPI 3 document: %v", err)
	}

	if len(doc.Tags) != len(full.Tags)-1 || doc.Tags.Get(internalProofingTag) != nil {
		t.Errorf("served %d of %d tags; want all but %s", len(doc.Tags), len(full.Tags), internalProofingTag)
	}
	if publicProofingRoutes(t, doc) == 0 {
		t.Error("the identity proofing tab holds no route")
	}
	if _, ok := doc.Extensions["x-webhooks"]; !ok {
		t.Error("the webhook is missing")
	}
}

// publicProofingRoutes counts doc's routes in the identity proofing tab,
// refusing one of the wallet's own and one outside the public API.
func publicProofingRoutes(t *testing.T, doc *openapi3.T) int {
	t.Helper()
	public := 0
	for path, item := range doc.Paths.Map() {
		for method, op := range item.Operations() {
			switch {
			case slices.Contains(op.Tags, internalProofingTag):
				t.Errorf("%s %s is one of the wallet's own proofing routes", method, path)
			case slices.Contains(op.Tags, "identity-proofing"):
				public++
				if !strings.HasPrefix(path, "/api/v1/proofing/") {
					t.Errorf("%s %s is in the identity proofing tab but not the public API", method, path)
				}
			}
		}
	}
	return public
}

// Every text the served docs keep reads as in openapi.yaml.
func TestServedDocsKeepEveryText(t *testing.T) {
	var full, kept any
	if err := yaml.Unmarshal(Spec(), &full); err != nil {
		t.Fatalf("decode the spec: %v", err)
	}
	if err := yaml.Unmarshal(served, &kept); err != nil {
		t.Fatalf("decode the served docs: %v", err)
	}
	compareTexts(t, "", kept, full)
}

// compareTexts reports every string in kept that differs from the one at the
// same place in full.
func compareTexts(t *testing.T, at string, kept, full any) {
	t.Helper()
	switch k := kept.(type) {
	case map[string]any:
		f, _ := full.(map[string]any)
		for key, v := range k {
			compareTexts(t, at+"/"+key, v, f[key])
		}
	case []any:
		f, _ := full.([]any)
		for i, v := range k {
			if i < len(f) {
				compareTexts(t, fmt.Sprintf("%s/%d", at, i), v, f[i])
			}
		}
	case string:
		if k != full {
			t.Errorf("%s: served %q, openapi.yaml %q", at, k, full)
		}
	}
}
