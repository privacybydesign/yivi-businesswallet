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
	gotName string
}

func (c *capturingRepo) Update(_ context.Context, id uuid.UUID, name string) (Organization, error) {
	c.gotName = name
	return Organization{ID: id, Name: name, Slug: "acme"}, nil
}

func (c *capturingRepo) Delete(context.Context, uuid.UUID) error { return nil }

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
