package organization

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"

	"github.com/privacybydesign/yivi-businesswallet/backend/internal/respond"
)

type capturingRepo struct {
	fakeRepo
	gotSlug string
	gotName string
}

func (c *capturingRepo) Create(_ context.Context, name, slug string) (Organization, error) {
	c.gotSlug = slug
	return Organization{Name: name, Slug: slug}, nil
}

func (c *capturingRepo) Update(_ context.Context, id uuid.UUID, name string) (Organization, error) {
	c.gotName = name
	return Organization{ID: id, Name: name, Slug: "acme"}, nil
}

func (c *capturingRepo) Delete(context.Context, uuid.UUID) error { return nil }

func TestCreateValidatesSlug(t *testing.T) {
	tests := []struct {
		name string
		slug string
		// wantCode is "" when the slug is accepted (201), otherwise the
		// expected 400 error code.
		wantCode string
	}{
		{name: "simple", slug: "acme", wantCode: ""},
		{name: "hyphenated", slug: "acme-corp", wantCode: ""},
		{name: "uppercase is normalized", slug: "Acme", wantCode: ""},
		{name: "reserved admin", slug: "admin", wantCode: "reserved_slug"},
		{name: "reserved login", slug: "login", wantCode: "reserved_slug"},
		{name: "spaces", slug: "acme corp", wantCode: "invalid_slug"},
		{name: "slash", slug: "acme/corp", wantCode: "invalid_slug"},
		{name: "trailing hyphen", slug: "acme-", wantCode: "invalid_slug"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			h := &Handler{store: fakeRepo{}}
			body, err := json.Marshal(createRequest{Name: "Acme", Slug: tc.slug})
			if err != nil {
				t.Fatalf("marshal: %v", err)
			}
			req := httptest.NewRequest(http.MethodPost, "/organizations", bytes.NewReader(body))
			rec := httptest.NewRecorder()

			gotErr := h.create(rec, req)

			if tc.wantCode == "" {
				if gotErr != nil {
					t.Fatalf("create: unexpected error %v", gotErr)
				}
				if rec.Code != http.StatusCreated {
					t.Errorf("status = %d, want 201", rec.Code)
				}
				return
			}

			var apiErr *respond.APIError
			if !errors.As(gotErr, &apiErr) {
				t.Fatalf("error = %v, want *respond.APIError", gotErr)
			}
			if apiErr.Status != http.StatusBadRequest || apiErr.Code != tc.wantCode {
				t.Errorf("got %d/%s, want 400/%s", apiErr.Status, apiErr.Code, tc.wantCode)
			}
		})
	}
}

func TestCreateNormalizesSlug(t *testing.T) {
	repo := &capturingRepo{}
	h := &Handler{store: repo}
	body, err := json.Marshal(createRequest{Name: "Acme", Slug: "  ACME-Corp  "})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	req := httptest.NewRequest(http.MethodPost, "/organizations", bytes.NewReader(body))

	if err := h.create(httptest.NewRecorder(), req); err != nil {
		t.Fatalf("create: %v", err)
	}
	if repo.gotSlug != "acme-corp" {
		t.Errorf("stored slug = %q, want %q", repo.gotSlug, "acme-corp")
	}
}

func updateRequestTo(h *Handler, name string) (*httptest.ResponseRecorder, error) {
	body, _ := json.Marshal(updateRequest{Name: name})
	req := httptest.NewRequest(http.MethodPatch, "/orgs/acme", bytes.NewReader(body))
	req = req.WithContext(contextWithOrg(req.Context(), Organization{ID: uuid.New(), Slug: "acme"}))
	rec := httptest.NewRecorder()
	return rec, h.update(rec, req)
}

func TestUpdateRenamesOrg(t *testing.T) {
	repo := &capturingRepo{}
	h := &Handler{store: repo}

	rec, err := updateRequestTo(h, "  Acme Renamed  ")
	if err != nil {
		t.Fatalf("update: %v", err)
	}
	if rec.Code != http.StatusNoContent {
		t.Errorf("status = %d, want 204", rec.Code)
	}
	if repo.gotName != "Acme Renamed" {
		t.Errorf("stored name = %q, want %q", repo.gotName, "Acme Renamed")
	}
}

func TestUpdateRejectsEmptyName(t *testing.T) {
	h := &Handler{store: &capturingRepo{}}

	_, err := updateRequestTo(h, "   ")

	var apiErr *respond.APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("error = %v, want *respond.APIError", err)
	}
	if apiErr.Status != http.StatusBadRequest || apiErr.Code != "invalid_input" {
		t.Errorf("got %d/%s, want 400/invalid_input", apiErr.Status, apiErr.Code)
	}
}
