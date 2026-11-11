package walletunit

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/privacybydesign/yivi-businesswallet/backend/internal/organization"
)

type fakeUnits struct {
	summary     Summary
	summaryErr  error
	revokeErr   error
	activateErr error
	revoked     string
}

func (f *fakeUnits) Summary(context.Context, uuid.UUID) (Summary, error) {
	return f.summary, f.summaryErr
}

func (f *fakeUnits) Revoke(_ context.Context, _ uuid.UUID, reason string) (Unit, error) {
	f.revoked = reason
	return Unit{Status: UnitRevoked}, f.revokeErr
}

func (f *fakeUnits) Activate(context.Context, uuid.UUID) (Unit, error) {
	return Unit{Status: UnitActive}, f.activateErr
}

type fakeLists struct {
	token string
	err   error
}

func (f fakeLists) StatusList(context.Context, int64) (string, error) { return f.token, f.err }

// serve routes req through Register with the org in context under role.
func serve(units unitStore, lists statusLists, role string, req *http.Request) *httptest.ResponseRecorder {
	withOrg := func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ctx := organization.ContextWithOrg(r.Context(), organization.Organization{ID: uuid.New(), Slug: "acme"})
			next.ServeHTTP(w, r.WithContext(organization.ContextWithRole(ctx, role)))
		})
	}
	pass := func(next http.Handler) http.Handler { return next }
	mux := http.NewServeMux()
	NewHandler(units, lists, pass, withOrg).Register(mux)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	return rec
}

func TestOrgRoutes(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		method   string
		path     string
		body     string
		role     string
		units    *fakeUnits
		wantCode int
	}{
		{"member cannot read", http.MethodGet, "/orgs/acme/wallet-unit", "", organization.RoleMember, &fakeUnits{}, http.StatusForbidden},
		{"member cannot revoke", http.MethodPost, "/orgs/acme/wallet-unit/revoke", `{"reason":"owner_request"}`, organization.RoleMember, &fakeUnits{}, http.StatusForbidden},
		{"no unit yet reads as not issued", http.MethodGet, "/orgs/acme/wallet-unit", "", organization.RoleAdmin, &fakeUnits{summaryErr: ErrNoActiveUnit}, http.StatusOK},
		{"unknown reason", http.MethodPost, "/orgs/acme/wallet-unit/revoke", `{"reason":"bored"}`, organization.RoleAdmin, &fakeUnits{revokeErr: ErrInvalidReason}, http.StatusBadRequest},
		{"malformed body", http.MethodPost, "/orgs/acme/wallet-unit/revoke", `{`, organization.RoleAdmin, &fakeUnits{}, http.StatusBadRequest},
		{"nothing to revoke", http.MethodPost, "/orgs/acme/wallet-unit/revoke", `{"reason":"owner_request"}`, organization.RoleAdmin, &fakeUnits{revokeErr: ErrNoActiveUnit}, http.StatusConflict},
		{"revoke", http.MethodPost, "/orgs/acme/wallet-unit/revoke", `{"reason":"key_compromise"}`, organization.RoleAdmin, &fakeUnits{}, http.StatusOK},
		{"activate while active", http.MethodPost, "/orgs/acme/wallet-unit/activate", "", organization.RoleAdmin, &fakeUnits{activateErr: ErrUnitActive}, http.StatusConflict},
		{"activate", http.MethodPost, "/orgs/acme/wallet-unit/activate", "", organization.RoleAdmin, &fakeUnits{}, http.StatusCreated},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			req := httptest.NewRequest(tc.method, tc.path, strings.NewReader(tc.body))
			rec := serve(tc.units, fakeLists{}, tc.role, req)
			if rec.Code != tc.wantCode {
				t.Fatalf("status = %d, want %d (body %s)", rec.Code, tc.wantCode, rec.Body.String())
			}
		})
	}
}

func TestStatusListRoute(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		path     string
		lists    fakeLists
		wantCode int
	}{
		{"served", "/wallet-unit/status-lists/0", fakeLists{token: "a.b.c"}, http.StatusOK},
		{"not a number", "/wallet-unit/status-lists/x", fakeLists{}, http.StatusNotFound},
		{"unknown list", "/wallet-unit/status-lists/99", fakeLists{err: ErrUnknownList}, http.StatusNotFound},
		{"store failure", "/wallet-unit/status-lists/0", fakeLists{err: errors.New("db down")}, http.StatusInternalServerError},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			rec := serve(&fakeUnits{}, tc.lists, "", httptest.NewRequest(http.MethodGet, tc.path, nil))
			if rec.Code != tc.wantCode {
				t.Fatalf("status = %d, want %d", rec.Code, tc.wantCode)
			}
			if tc.wantCode == http.StatusOK && rec.Header().Get("Content-Type") != statusListContentType {
				t.Errorf("Content-Type = %q", rec.Header().Get("Content-Type"))
			}
		})
	}
}
