package organization

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"

	"github.com/privacybydesign/yivi-businesswallet/backend/internal/auth"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/user"
)

type fakeRepo struct {
	org           Organization
	getBySlugErr  error
	membership    Membership
	membershipErr error
}

func (f fakeRepo) List(context.Context) ([]Organization, error) { return nil, nil }
func (f fakeRepo) Create(context.Context, string, string) (Organization, error) {
	return Organization{}, nil
}

func (f fakeRepo) GetByID(context.Context, uuid.UUID) (Organization, error) {
	return Organization{}, nil
}

func (f fakeRepo) GetBySlug(context.Context, string) (Organization, error) {
	return f.org, f.getBySlugErr
}

func (f fakeRepo) Update(context.Context, uuid.UUID, string) (Organization, error) {
	return Organization{}, nil
}
func (f fakeRepo) ListForUser(context.Context, uuid.UUID) ([]Organization, error) { return nil, nil }
func (f fakeRepo) GetMembership(context.Context, uuid.UUID, uuid.UUID) (Membership, error) {
	return f.membership, f.membershipErr
}
func (f fakeRepo) ListMembers(context.Context, uuid.UUID) ([]Member, error) { return nil, nil }
func (f fakeRepo) ListInvitations(context.Context, uuid.UUID) ([]Invitation, error) {
	return nil, nil
}
func (f fakeRepo) RevokeInvitation(context.Context, uuid.UUID, uuid.UUID) error { return nil }
func (f fakeRepo) ResendInvitation(context.Context, uuid.UUID, uuid.UUID) error { return nil }

func (f fakeRepo) UpdateMembership(context.Context, uuid.UUID, uuid.UUID, *string, *string, *uuid.UUID) (Member, error) {
	return Member{}, nil
}
func (f fakeRepo) ListDepartments(context.Context, uuid.UUID) ([]Department, error) { return nil, nil }
func (f fakeRepo) CreateDepartment(context.Context, uuid.UUID, string) (Department, error) {
	return Department{}, nil
}

func (f fakeRepo) UpdateDepartment(context.Context, uuid.UUID, uuid.UUID, string) (Department, error) {
	return Department{}, nil
}
func (f fakeRepo) DeleteDepartment(context.Context, uuid.UUID, uuid.UUID) error { return nil }

func authorizeWith(repo repository, admins auth.PlatformAdmins, email user.Email) *httptest.ResponseRecorder {
	h := &Handler{store: repo, admins: admins}
	var gotRole string
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotRole = roleFromContext(r.Context())
		_ = OrgFromContext(r.Context())
		w.Header().Set("X-Role", gotRole)
		w.WriteHeader(http.StatusOK)
	})

	req := httptest.NewRequest(http.MethodGet, "/orgs/acme", nil)
	req.SetPathValue("slug", "acme")
	req = req.WithContext(auth.ContextWithUser(req.Context(), user.User{ID: uuid.New(), Email: email}))
	rec := httptest.NewRecorder()

	h.Authorize(next).ServeHTTP(rec, req)
	return rec
}

func TestAuthorize(t *testing.T) {
	org := Organization{ID: uuid.New(), Name: "Acme", Slug: "acme"}

	t.Run("member passes with their role", func(t *testing.T) {
		repo := fakeRepo{org: org, membership: Membership{Role: RoleMember}}
		rec := authorizeWith(repo, nil, "user@example.com")
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200", rec.Code)
		}
		if got := rec.Header().Get("X-Role"); got != RoleMember {
			t.Errorf("role = %q, want %q", got, RoleMember)
		}
	})

	t.Run("non-member forbidden", func(t *testing.T) {
		repo := fakeRepo{org: org, membershipErr: ErrNotMember}
		rec := authorizeWith(repo, nil, "user@example.com")
		if rec.Code != http.StatusForbidden {
			t.Fatalf("status = %d, want 403", rec.Code)
		}
	})

	t.Run("platform admin bypasses membership as admin", func(t *testing.T) {
		repo := fakeRepo{org: org, membershipErr: ErrNotMember}
		admins := auth.NewPlatformAdmins([]string{"boss@example.com"})
		rec := authorizeWith(repo, admins, "boss@example.com")
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200", rec.Code)
		}
		if got := rec.Header().Get("X-Role"); got != RoleAdmin {
			t.Errorf("role = %q, want %q", got, RoleAdmin)
		}
	})

	t.Run("unknown slug is 404", func(t *testing.T) {
		repo := fakeRepo{getBySlugErr: ErrNotFound}
		rec := authorizeWith(repo, nil, "user@example.com")
		if rec.Code != http.StatusNotFound {
			t.Fatalf("status = %d, want 404", rec.Code)
		}
	})

	t.Run("store error is 500", func(t *testing.T) {
		repo := fakeRepo{getBySlugErr: errors.New("boom")}
		rec := authorizeWith(repo, nil, "user@example.com")
		if rec.Code != http.StatusInternalServerError {
			t.Fatalf("status = %d, want 500", rec.Code)
		}
	})
}

func TestRequireOrgAdmin(t *testing.T) {
	next := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })

	tests := []struct {
		role string
		want int
	}{
		{RoleAdmin, http.StatusOK},
		{RoleMember, http.StatusForbidden},
		{"", http.StatusForbidden},
	}
	for _, tt := range tests {
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		req = req.WithContext(contextWithRole(req.Context(), tt.role))
		rec := httptest.NewRecorder()

		RequireOrgAdmin(next).ServeHTTP(rec, req)

		if rec.Code != tt.want {
			t.Errorf("role %q: status = %d, want %d", tt.role, rec.Code, tt.want)
		}
	}
}
