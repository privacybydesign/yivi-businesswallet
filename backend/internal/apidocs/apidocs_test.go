package apidocs

import (
	"context"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/getkin/kin-openapi/openapi3"
	"gopkg.in/yaml.v3"
)

// TestSpecIsValidOpenAPI keeps the committed document a valid OpenAPI 3 spec so
// a broken edit fails CI rather than silently rendering an empty page.
func TestSpecIsValidOpenAPI(t *testing.T) {
	loader := openapi3.NewLoader()
	doc, err := loader.LoadFromData(Spec())
	if err != nil {
		t.Fatalf("load openapi.yaml: %v", err)
	}
	if err := doc.Validate(context.Background()); err != nil {
		t.Fatalf("openapi.yaml is not a valid OpenAPI 3 document: %v", err)
	}
}

func TestRegisterServesUIAndSpec(t *testing.T) {
	mux := http.NewServeMux()
	Register(mux)

	cases := []struct {
		path        string
		contentType string
		wantBody    string
	}{
		{"/api/docs", "text/html; charset=utf-8", "<redoc"},
		{"/api/docs/openapi.yaml", "application/yaml; charset=utf-8", "openapi:"},
	}
	for _, tc := range cases {
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, tc.path, nil))

		if rec.Code != http.StatusOK {
			t.Errorf("GET %s: status = %d, want 200", tc.path, rec.Code)
		}
		if got := rec.Header().Get("Content-Type"); got != tc.contentType {
			t.Errorf("GET %s: Content-Type = %q, want %q", tc.path, got, tc.contentType)
		}
		if !strings.Contains(rec.Body.String(), tc.wantBody) {
			t.Errorf("GET %s: body does not contain %q", tc.path, tc.wantBody)
		}
	}
}

// TestSpecCoversAllRoutes is the drift guard required by the issue: every route
// registered by a feature handler must be documented in openapi.yaml, and the
// spec must not document /api/v1 routes that no longer exist. It parses the
// route string literals out of the handler source and compares them to the
// operations declared in the embedded spec.
func TestSpecCoversAllRoutes(t *testing.T) {
	registered := registeredRoutes(t)
	documented := documentedRoutes(t)

	for r := range registered {
		if !documented[r] {
			t.Errorf("route %q is registered but missing from openapi.yaml", r)
		}
	}
	for d := range documented {
		if !registered[d] {
			t.Errorf("route %q is documented in openapi.yaml but not registered by any handler", d)
		}
	}
	if len(registered) == 0 {
		t.Fatal("found no registered routes; the source scan is broken")
	}
}

// registeredRoutes walks the internal packages and extracts every
// `mux.Handle("METHOD /path", ...)` route as "METHOD /api/v1/path". Docs routes
// (already carrying the /api prefix) are skipped — they live on the root mux,
// not the versioned API.
func registeredRoutes(t *testing.T) map[string]bool {
	t.Helper()
	routes := map[string]bool{}
	fset := token.NewFileSet()

	err := filepath.WalkDir("..", func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		file, perr := parser.ParseFile(fset, path, nil, 0)
		if perr != nil {
			return perr
		}
		ast.Inspect(file, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok || len(call.Args) == 0 {
				return true
			}
			sel, ok := call.Fun.(*ast.SelectorExpr)
			if !ok || (sel.Sel.Name != "Handle" && sel.Sel.Name != "HandleFunc") {
				return true
			}
			lit, ok := call.Args[0].(*ast.BasicLit)
			if !ok || lit.Kind != token.STRING {
				return true
			}
			pattern := strings.Trim(lit.Value, "`\"")
			method, urlPath, ok := splitPattern(pattern)
			if !ok || strings.HasPrefix(urlPath, "/api/") {
				return true
			}
			routes[method+" /api/v1"+urlPath] = true
			return true
		})
		return nil
	})
	if err != nil {
		t.Fatalf("walk internal source: %v", err)
	}
	return routes
}

func documentedRoutes(t *testing.T) map[string]bool {
	t.Helper()
	var doc struct {
		Paths map[string]map[string]yaml.Node `yaml:"paths"`
	}
	if err := yaml.Unmarshal(Spec(), &doc); err != nil {
		t.Fatalf("parse openapi.yaml: %v", err)
	}
	methods := map[string]bool{"get": true, "post": true, "put": true, "patch": true, "delete": true}
	routes := map[string]bool{}
	for urlPath, ops := range doc.Paths {
		if !strings.HasPrefix(urlPath, "/api/v1/") {
			continue
		}
		for method := range ops {
			if methods[method] {
				routes[strings.ToUpper(method)+" "+urlPath] = true
			}
		}
	}
	return routes
}

// splitPattern parses a Go 1.22 "METHOD /path" mux pattern. It returns ok=false
// for patterns without an explicit method (those are not API routes we document).
func splitPattern(pattern string) (method, urlPath string, ok bool) {
	parts := strings.SplitN(pattern, " ", 2)
	if len(parts) != 2 {
		return "", "", false
	}
	method, urlPath = parts[0], parts[1]
	switch method {
	case http.MethodGet, http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete:
		return method, urlPath, true
	default:
		return "", "", false
	}
}

func TestDocumentedRoutesSorted(t *testing.T) {
	// Guards against an accidental empty parse: the spec must document a
	// realistic number of operations.
	got := documentedRoutes(t)
	if len(got) < 50 {
		keys := make([]string, 0, len(got))
		for k := range got {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		t.Fatalf("expected the spec to document 50+ operations, got %d: %v", len(got), keys)
	}
}
