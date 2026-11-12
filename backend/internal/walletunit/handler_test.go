package walletunit

import (
	"context"
	"encoding/json"
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
func serve(units unitStore, lists statusLister, role string, req *http.Request) *httptest.ResponseRecorder {
	return serveWith(units, lists, fakeKeys{}, role, req)
}

type fakeKeys struct {
	att *KeyAttestation
	err error
}

func (f fakeKeys) KeyAttestation(context.Context, uuid.UUID) (*KeyAttestation, error) {
	return f.att, f.err
}

func serveWith(units unitStore, lists statusLister, keys keyAttester, role string, req *http.Request) *httptest.ResponseRecorder {
	return serveAs(units, lists, keys, organization.Organization{ID: uuid.New(), Slug: "acme"}, role, req)
}

// serveOrg serves req as an admin of org.
func serveOrg(units unitStore, keys keyAttester, org organization.Organization, req *http.Request) *httptest.ResponseRecorder {
	return serveAs(units, fakeLists{}, keys, org, organization.RoleAdmin, req)
}

func serveAs(units unitStore, lists statusLister, keys keyAttester, org organization.Organization, role string, req *http.Request) *httptest.ResponseRecorder {
	withOrg := func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ctx := organization.ContextWithOrg(r.Context(), org)
			next.ServeHTTP(w, r.WithContext(organization.ContextWithRole(ctx, role)))
		})
	}
	pass := func(next http.Handler) http.Handler { return next }
	mux := http.NewServeMux()
	NewHandler(units, lists, keys, pass, withOrg).Register(mux)
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

func TestSummaryKeyAttestation(t *testing.T) {
	t.Parallel()
	ska := fakeKeys{att: &KeyAttestation{Levels: []string{"iso_18045_high"}}}
	active := Summary{Unit: Unit{Status: UnitActive}}
	revoked := Summary{Unit: Unit{Status: UnitRevoked}}
	attestable := organization.Organization{ID: uuid.New(), Slug: "acme", Status: organization.StatusActive, EUID: "NLNHR.12345678"}
	suspended, noEUID := attestable, attestable
	suspended.Status = organization.StatusSuspended
	noEUID.EUID = ""

	tests := []struct {
		name    string
		org     organization.Organization
		units   *fakeUnits
		keys    fakeKeys
		wantSKA bool
	}{
		{"active unit of an attestable org", attestable, &fakeUnits{summary: active}, ska, true},
		{"no SKA for the org's keys", attestable, &fakeUnits{summary: active}, fakeKeys{}, false},
		{"revoked unit", attestable, &fakeUnits{summary: revoked}, ska, false},
		{"no unit yet", attestable, &fakeUnits{summaryErr: ErrNoActiveUnit}, ska, false},
		{"suspended org", suspended, &fakeUnits{summary: active}, ska, false},
		{"org without an EUID", noEUID, &fakeUnits{summary: active}, ska, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			rec := serveOrg(tc.units, tc.keys, tc.org, httptest.NewRequest(http.MethodGet, "/orgs/acme/wallet-unit", nil))
			if rec.Code != http.StatusOK {
				t.Fatalf("status = %d", rec.Code)
			}
			var got map[string]any
			if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
				t.Fatal(err)
			}
			if has := got["keyAttestation"] != nil; has != tc.wantSKA {
				t.Errorf("keyAttestation = %v, want present %v", got["keyAttestation"], tc.wantSKA)
			}
		})
	}

	failing := fakeKeys{err: errors.New("db down")}
	if rec := serveOrg(&fakeUnits{summary: active}, failing, attestable, httptest.NewRequest(http.MethodGet, "/orgs/acme/wallet-unit", nil)); rec.Code != http.StatusInternalServerError {
		t.Errorf("status = %d on a lookup failure, want 500", rec.Code)
	}
}
