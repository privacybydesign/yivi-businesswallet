package organization

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/privacybydesign/yivi-businesswallet/backend/internal/auth"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/user"
)

type fakeRepo struct {
	org           Organization
	getBySlugErr  error
	membership    Membership
	membershipErr error
	avatar        user.Avatar
	avatarErr     error
	authority     Authority
	authorityErr  error
}

func (f fakeRepo) List(context.Context) ([]Organization, error) { return nil, nil }

func (f fakeRepo) GetByID(context.Context, uuid.UUID) (Organization, error) {
	return Organization{}, nil
}

func (f fakeRepo) GetBySlug(context.Context, string) (Organization, error) {
	return f.org, f.getBySlugErr
}

func (f fakeRepo) Update(context.Context, uuid.UUID, string) (Organization, error) {
	return Organization{}, nil
}
func (f fakeRepo) Delete(context.Context, uuid.UUID) error                        { return nil }
func (f fakeRepo) ListForUser(context.Context, uuid.UUID) ([]Organization, error) { return nil, nil }
func (f fakeRepo) GetMembership(context.Context, uuid.UUID, uuid.UUID) (Membership, error) {
	return f.membership, f.membershipErr
}

func (f fakeRepo) GetMember(context.Context, uuid.UUID, uuid.UUID) (Member, error) {
	return Member{}, nil
}

func (f fakeRepo) GetMemberAvatar(context.Context, uuid.UUID, uuid.UUID) (user.Avatar, error) {
	return f.avatar, f.avatarErr
}

func (f fakeRepo) ListMemberEntries(context.Context, uuid.UUID, MemberListParams) ([]MemberEntry, int, error) {
	return nil, 0, nil
}
func (f fakeRepo) RevokeInvitation(context.Context, uuid.UUID, uuid.UUID) error { return nil }
func (f fakeRepo) ResendInvitation(context.Context, uuid.UUID, uuid.UUID) (Invitation, error) {
	return Invitation{}, nil
}

func (f fakeRepo) UpdateMembership(context.Context, uuid.UUID, uuid.UUID, *string, *string, *uuid.UUID) (Member, error) {
	return Member{}, nil
}
func (f fakeRepo) RemoveMembership(context.Context, uuid.UUID, uuid.UUID) error     { return nil }
func (f fakeRepo) ListDepartments(context.Context, uuid.UUID) ([]Department, error) { return nil, nil }
func (f fakeRepo) CreateDepartment(context.Context, uuid.UUID, string) (Department, error) {
	return Department{}, nil
}

func (f fakeRepo) UpdateDepartment(context.Context, uuid.UUID, uuid.UUID, string) (Department, error) {
	return Department{}, nil
}
func (f fakeRepo) DeleteDepartment(context.Context, uuid.UUID, uuid.UUID) error { return nil }

func (f fakeRepo) ResolveAuthority(context.Context, uuid.UUID, uuid.UUID) (Authority, error) {
	return f.authority, f.authorityErr
}

func (f fakeRepo) ListMandates(context.Context, uuid.UUID) ([]Mandate, error) { return nil, nil }

func (f fakeRepo) GrantMandate(context.Context, uuid.UUID, uuid.UUID, MandateGrant) (Mandate, error) {
	return Mandate{}, nil
}

func (f fakeRepo) RevokeMandate(context.Context, uuid.UUID, uuid.UUID, uuid.UUID, *time.Time, string) ([]Mandate, error) {
	return nil, nil
}

// authorizeWith runs the Authorize middleware and returns the response together
// with the basis of authority it stashed, which the mandate cases assert on.
func authorizeWith(repo repository, admins auth.PlatformAdmins, email user.Email) (*httptest.ResponseRecorder, Authority) {
	h := &Handler{store: repo, admins: admins}
	var (
		gotRole      string
		gotAuthority Authority
	)
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotRole = roleFromContext(r.Context())
		gotAuthority = AuthorityFromContext(r.Context())
		_ = OrgFromContext(r.Context())
		w.Header().Set("X-Role", gotRole)
		w.WriteHeader(http.StatusOK)
	})

	req := httptest.NewRequest(http.MethodGet, "/orgs/acme", nil)
	req.SetPathValue("slug", "acme")
	req = req.WithContext(auth.ContextWithUser(req.Context(), user.User{ID: uuid.New(), Email: email}))
	rec := httptest.NewRecorder()

	h.Authorize(next).ServeHTTP(rec, req)
	return rec, gotAuthority
}

func TestAuthorize(t *testing.T) {
	org := Organization{ID: uuid.New(), Name: "Acme", Slug: "acme"}

	t.Run("member passes with their role", func(t *testing.T) {
		repo := fakeRepo{org: org, membership: Membership{Role: RoleMember}}
		rec, _ := authorizeWith(repo, nil, "user@example.com")
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200", rec.Code)
		}
		if got := rec.Header().Get("X-Role"); got != RoleMember {
			t.Errorf("role = %q, want %q", got, RoleMember)
		}
	})

	t.Run("non-member forbidden", func(t *testing.T) {
		repo := fakeRepo{org: org, membershipErr: ErrNotMember}
		rec, _ := authorizeWith(repo, nil, "user@example.com")
		if rec.Code != http.StatusForbidden {
			t.Fatalf("status = %d, want 403", rec.Code)
		}
	})

	t.Run("platform admin bypasses membership as admin", func(t *testing.T) {
		repo := fakeRepo{org: org, membershipErr: ErrNotMember}
		admins := auth.NewPlatformAdmins([]string{"boss@example.com"})
		rec, _ := authorizeWith(repo, admins, "boss@example.com")
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200", rec.Code)
		}
		if got := rec.Header().Get("X-Role"); got != RoleAdmin {
			t.Errorf("role = %q, want %q", got, RoleAdmin)
		}
	})

	t.Run("unknown slug is 404", func(t *testing.T) {
		repo := fakeRepo{getBySlugErr: ErrNotFound}
		rec, _ := authorizeWith(repo, nil, "user@example.com")
		if rec.Code != http.StatusNotFound {
			t.Fatalf("status = %d, want 404", rec.Code)
		}
	})

	t.Run("store error is 500", func(t *testing.T) {
		repo := fakeRepo{getBySlugErr: errors.New("boom")}
		rec, _ := authorizeWith(repo, nil, "user@example.com")
		if rec.Code != http.StatusInternalServerError {
			t.Fatalf("status = %d, want 500", rec.Code)
		}
	})

	t.Run("the basis of authority reaches the handler", func(t *testing.T) {
		repo := fakeRepo{
			org:        org,
			membership: Membership{Role: RoleAdmin},
			authority:  Authority{LegalRepresentative: true, FullMandate: true, Mandated: true, Granted: 1},
		}
		rec, got := authorizeWith(repo, nil, "boss@acme.example")
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200", rec.Code)
		}
		want := Authority{LegalRepresentative: true, FullMandate: true, Mandated: true, Granted: 1}
		if got != want {
			t.Errorf("authority = %+v, want %+v", got, want)
		}
	})

	t.Run("platform admin is marked in the authority", func(t *testing.T) {
		repo := fakeRepo{org: org, membershipErr: ErrNotMember}
		admins := auth.NewPlatformAdmins([]string{"boss@example.com"})
		_, got := authorizeWith(repo, admins, "boss@example.com")
		if !got.PlatformAdmin {
			t.Error("PlatformAdmin = false, want true")
		}
	})

	t.Run("an authority lookup failure is 500, never an open door", func(t *testing.T) {
		repo := fakeRepo{org: org, membership: Membership{Role: RoleAdmin}, authorityErr: errors.New("boom")}
		rec, _ := authorizeWith(repo, nil, "user@example.com")
		if rec.Code != http.StatusInternalServerError {
			t.Fatalf("status = %d, want 500", rec.Code)
		}
	})
}

func TestRequireOrgAdmin(t *testing.T) {
	next := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })

	tests := []struct {
		name      string
		role      string
		authority Authority
		want      int
	}{
		{"admin passes", RoleAdmin, Authority{}, http.StatusOK},
		{"member forbidden", RoleMember, Authority{}, http.StatusForbidden},
		{"no role forbidden", "", Authority{}, http.StatusForbidden},
		{
			name:      "admin with an active mandate passes",
			role:      RoleAdmin,
			authority: Authority{Granted: 1, Mandated: true},
			want:      http.StatusOK,
		},
		{
			// The membership row still says admin; the mandate behind it is gone, and
			// that has to be enough on its own (Annex §12(3)(b)).
			name:      "admin whose mandate is withdrawn is forbidden",
			role:      RoleAdmin,
			authority: Authority{Granted: 1},
			want:      http.StatusForbidden,
		},
		{
			name:      "a platform admin is not withdrawn by the org's register",
			role:      RoleAdmin,
			authority: Authority{Granted: 1, PlatformAdmin: true},
			want:      http.StatusOK,
		},
		{
			// RequireMandateAuthority already lets them grant and revoke; refusing
			// them the register they just wrote to would be the same authority
			// answering two ways.
			name:      "a legal representative is not withdrawn by the org's register",
			role:      RoleAdmin,
			authority: Authority{Granted: 1, LegalRepresentative: true},
			want:      http.StatusOK,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/", nil)
			req = req.WithContext(contextWithAuthority(contextWithRole(req.Context(), tt.role), tt.authority))
			rec := httptest.NewRecorder()

			RequireOrgAdmin(next).ServeHTTP(rec, req)

			if rec.Code != tt.want {
				t.Errorf("status = %d, want %d", rec.Code, tt.want)
			}
		})
	}
}

func TestRequireMandateAuthority(t *testing.T) {
	next := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })

	tests := []struct {
		name      string
		authority Authority
		want      int
	}{
		{"legal representative passes", Authority{LegalRepresentative: true}, http.StatusOK},
		{"full-mandate holder passes", Authority{FullMandate: true, Mandated: true, Granted: 1}, http.StatusOK},
		{"administrative mandate is not enough", Authority{Mandated: true, Granted: 1}, http.StatusForbidden},
		{"no basis of authority is forbidden", Authority{}, http.StatusForbidden},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, "/", nil)
			// Deliberately an admin: no functional role reaches this gate.
			req = req.WithContext(contextWithAuthority(contextWithRole(req.Context(), RoleAdmin), tt.authority))
			rec := httptest.NewRecorder()

			RequireMandateAuthority(next).ServeHTTP(rec, req)

			if rec.Code != tt.want {
				t.Errorf("status = %d, want %d", rec.Code, tt.want)
			}
		})
	}
}
