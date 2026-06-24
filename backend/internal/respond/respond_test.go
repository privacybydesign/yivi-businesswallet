package respond_test

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/privacybydesign/yivi-businesswallet/backend/internal/respond"
)

func TestJSON_Escaping(t *testing.T) {
	type payload struct {
		Name string `json:"name"`
	}

	tests := []struct {
		name  string
		input string
	}{
		{"double quote", `He said "hello"`},
		{"backslash", `path\to\file`},
		{"angle brackets", `<script>alert("xss")</script>`},
		{"newline", "line1\nline2"},
		{"tab", "col1\tcol2"},
		{"unicode", "café ☕"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodGet, "/", nil)

			respond.JSON(rec, req, http.StatusOK, payload{Name: tc.input})

			if rec.Code != http.StatusOK {
				t.Fatalf("expected status 200, got %d", rec.Code)
			}

			var got payload
			if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
				t.Fatalf("response is not valid JSON: %v\nbody: %s", err, rec.Body.String())
			}
			if got.Name != tc.input {
				t.Errorf("round-trip failed: got %q, want %q", got.Name, tc.input)
			}
		})
	}
}

func TestJSON_MarshalFailure(t *testing.T) {
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", nil)

	respond.JSON(rec, req, http.StatusOK, make(chan int))

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("expected status 500 on marshal failure, got %d", rec.Code)
	}

	if rec.Body.Len() != 0 {
		t.Fatalf("expected empty body on marshal failure, got: %s", rec.Body.String())
	}
}

func TestJSON_ContentType(t *testing.T) {
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", nil)

	respond.JSON(rec, req, http.StatusOK, map[string]string{"ok": "true"})

	ct := rec.Header().Get("Content-Type")
	if ct != "application/json" {
		t.Fatalf("expected Content-Type application/json, got %q", ct)
	}
}

func TestJSON_StatusCodes(t *testing.T) {
	codes := []int{
		http.StatusOK,
		http.StatusCreated,
		http.StatusBadRequest,
		http.StatusNotFound,
		http.StatusInternalServerError,
	}

	for _, code := range codes {
		t.Run(fmt.Sprintf("status_%d", code), func(t *testing.T) {
			rec := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodGet, "/", nil)

			respond.JSON(rec, req, code, map[string]string{"status": "ok"})

			if rec.Code != code {
				t.Fatalf("expected status %d, got %d", code, rec.Code)
			}
		})
	}
}

func TestError_Shape(t *testing.T) {
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", nil)

	respond.Error(rec, req, http.StatusNotFound, "org_not_found", "organization not found")

	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected status 404, got %d", rec.Code)
	}

	var body map[string]string
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("response is not valid JSON: %v\nbody: %s", err, rec.Body.String())
	}

	if body["error"] != "organization not found" {
		t.Errorf("expected error=%q, got %q", "organization not found", body["error"])
	}
	if body["code"] != "org_not_found" {
		t.Errorf("expected code=%q, got %q", "org_not_found", body["code"])
	}

	// Status must NOT appear in the body — it's the HTTP status line only.
	if _, ok := body["status"]; ok {
		t.Error("status should not be in the response body")
	}
}

func TestHandlerFunc_APIError(t *testing.T) {
	h := respond.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) error {
		return &respond.APIError{
			Status:  http.StatusConflict,
			Code:    "name_taken",
			Message: "that name is already in use",
		}
	})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusConflict {
		t.Fatalf("expected status 409, got %d", rec.Code)
	}

	var body map[string]string
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("response is not valid JSON: %v", err)
	}
	if body["code"] != "name_taken" {
		t.Errorf("expected code=name_taken, got %q", body["code"])
	}
	if body["error"] != "that name is already in use" {
		t.Errorf("expected error=%q, got %q", "that name is already in use", body["error"])
	}
}

func TestHandlerFunc_WrappedAPIError(t *testing.T) {
	inner := &respond.APIError{
		Status:  http.StatusBadRequest,
		Code:    "invalid_input",
		Message: "bad request",
	}

	h := respond.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) error {
		return fmt.Errorf("parsing request: %w", inner)
	})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected status 400 through wrap chain, got %d", rec.Code)
	}
}

func TestHandlerFunc_InternalError(t *testing.T) {
	h := respond.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) error {
		return errors.New("sql: connection refused")
	})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("expected status 500, got %d", rec.Code)
	}

	var body map[string]string
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("response is not valid JSON: %v", err)
	}

	if body["error"] == "sql: connection refused" {
		t.Fatal("internal error message leaked to client")
	}
	if body["code"] != "internal_error" {
		t.Errorf("expected code=internal_error, got %q", body["code"])
	}
	if body["error"] != "internal server error" {
		t.Errorf("expected error=%q, got %q", "internal server error", body["error"])
	}
}

func TestHandlerFunc_NilError(t *testing.T) {
	called := false
	h := respond.HandlerFunc(func(w http.ResponseWriter, r *http.Request) error {
		called = true
		respond.JSON(w, r, http.StatusOK, map[string]string{"ok": "true"})
		return nil
	})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	h.ServeHTTP(rec, req)

	if !called {
		t.Fatal("handler was not called")
	}
	if rec.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", rec.Code)
	}
}
