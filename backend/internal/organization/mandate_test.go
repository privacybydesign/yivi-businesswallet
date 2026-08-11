package organization

import (
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
)

func ptr[T any](v T) *T { return &v }

func TestMandateStatus(t *testing.T) {
	now := time.Date(2026, 8, 11, 12, 0, 0, 0, time.UTC)
	hour := time.Hour

	tests := []struct {
		name string
		m    Mandate
		want string
	}{
		{
			name: "open-ended and in force is active",
			m:    Mandate{ValidFrom: now.Add(-hour)},
			want: MandateStatusActive,
		},
		{
			name: "window not open yet is pending",
			m:    Mandate{ValidFrom: now.Add(hour)},
			want: MandateStatusPending,
		},
		{
			name: "window closed is expired",
			m:    Mandate{ValidFrom: now.Add(-2 * hour), ValidUntil: ptr(now.Add(-hour))},
			want: MandateStatusExpired,
		},
		{
			name: "valid_until exactly now is expired",
			m:    Mandate{ValidFrom: now.Add(-hour), ValidUntil: ptr(now)},
			want: MandateStatusExpired,
		},
		{
			name: "revoked wins over expired",
			m: Mandate{
				ValidFrom:  now.Add(-2 * hour),
				ValidUntil: ptr(now.Add(-hour)),
				RevokedAt:  ptr(now.Add(-90 * time.Minute)),
			},
			want: MandateStatusRevoked,
		},
		{
			name: "revoked wins over active",
			m:    Mandate{ValidFrom: now.Add(-hour), RevokedAt: ptr(now.Add(-time.Minute))},
			want: MandateStatusRevoked,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := mandateStatus(tt.m, now); got != tt.want {
				t.Errorf("mandateStatus = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestClampToParent(t *testing.T) {
	now := time.Date(2026, 8, 11, 12, 0, 0, 0, time.UTC)
	day := 24 * time.Hour
	dept := uuid.New()
	otherDept := uuid.New()

	activeFull := Mandate{
		Type:      MandateFull,
		Scope:     MandateScopeOrganization,
		ValidFrom: now.Add(-day),
	}

	t.Run("administrative under full keeps its own window", func(t *testing.T) {
		req := MandateGrant{
			Type: MandateAdministrative, Scope: MandateScopeOrganization,
			ValidFrom: now, ValidUntil: ptr(now.Add(day)),
		}
		got, err := clampToParent(req, activeFull, now)
		if err != nil {
			t.Fatalf("clampToParent: %v", err)
		}
		if !got.ValidFrom.Equal(now) || !got.ValidUntil.Equal(now.Add(day)) {
			t.Errorf("window = %v..%v, want unchanged", got.ValidFrom, got.ValidUntil)
		}
	})

	t.Run("full under administrative is over-delegation", func(t *testing.T) {
		parent := Mandate{Type: MandateAdministrative, Scope: MandateScopeOrganization, ValidFrom: now.Add(-day)}
		req := MandateGrant{Type: MandateFull, Scope: MandateScopeOrganization, ValidFrom: now}
		if _, err := clampToParent(req, parent, now); !errors.Is(err, ErrOverDelegation) {
			t.Errorf("err = %v, want ErrOverDelegation", err)
		}
	})

	t.Run("unknown type cannot be delegated", func(t *testing.T) {
		req := MandateGrant{Type: "owner", Scope: MandateScopeOrganization, ValidFrom: now}
		if _, err := clampToParent(req, activeFull, now); !errors.Is(err, ErrOverDelegation) {
			t.Errorf("err = %v, want ErrOverDelegation", err)
		}
	})

	t.Run("window is clamped to the parent's end", func(t *testing.T) {
		parent := activeFull
		parent.ValidUntil = ptr(now.Add(day))
		req := MandateGrant{
			Type: MandateAdministrative, Scope: MandateScopeOrganization,
			ValidFrom: now, ValidUntil: ptr(now.Add(10 * day)),
		}
		got, err := clampToParent(req, parent, now)
		if err != nil {
			t.Fatalf("clampToParent: %v", err)
		}
		if !got.ValidUntil.Equal(now.Add(day)) {
			t.Errorf("validUntil = %v, want the parent's %v", got.ValidUntil, now.Add(day))
		}
	})

	t.Run("an open-ended delegation inherits the parent's end", func(t *testing.T) {
		parent := activeFull
		parent.ValidUntil = ptr(now.Add(day))
		req := MandateGrant{Type: MandateAdministrative, Scope: MandateScopeOrganization, ValidFrom: now}
		got, err := clampToParent(req, parent, now)
		if err != nil {
			t.Fatalf("clampToParent: %v", err)
		}
		if got.ValidUntil == nil || !got.ValidUntil.Equal(now.Add(day)) {
			t.Errorf("validUntil = %v, want the parent's %v", got.ValidUntil, now.Add(day))
		}
	})

	t.Run("start is clamped up to the parent's start", func(t *testing.T) {
		parent := activeFull
		parent.ValidFrom = now.Add(-day)
		req := MandateGrant{
			Type: MandateAdministrative, Scope: MandateScopeOrganization,
			ValidFrom: now.Add(-10 * day),
		}
		got, err := clampToParent(req, parent, now)
		if err != nil {
			t.Fatalf("clampToParent: %v", err)
		}
		if !got.ValidFrom.Equal(parent.ValidFrom) {
			t.Errorf("validFrom = %v, want the parent's %v", got.ValidFrom, parent.ValidFrom)
		}
	})

	t.Run("a start after the parent's end leaves nothing to cut", func(t *testing.T) {
		parent := activeFull
		parent.ValidUntil = ptr(now.Add(day))
		req := MandateGrant{
			Type: MandateAdministrative, Scope: MandateScopeOrganization,
			ValidFrom: now.Add(2 * day),
		}
		if _, err := clampToParent(req, parent, now); !errors.Is(err, ErrOverDelegation) {
			t.Errorf("err = %v, want ErrOverDelegation", err)
		}
	})

	t.Run("a department parent cannot be widened to the org", func(t *testing.T) {
		parent := Mandate{
			Type: MandateFull, Scope: MandateScopeDepartment,
			ScopeDepartmentID: &dept, ValidFrom: now.Add(-day),
		}
		req := MandateGrant{Type: MandateAdministrative, Scope: MandateScopeOrganization, ValidFrom: now}
		if _, err := clampToParent(req, parent, now); !errors.Is(err, ErrOverDelegation) {
			t.Errorf("err = %v, want ErrOverDelegation", err)
		}
	})

	t.Run("a department parent cannot be moved to another department", func(t *testing.T) {
		parent := Mandate{
			Type: MandateFull, Scope: MandateScopeDepartment,
			ScopeDepartmentID: &dept, ValidFrom: now.Add(-day),
		}
		req := MandateGrant{
			Type: MandateAdministrative, Scope: MandateScopeDepartment,
			ScopeDepartmentID: &otherDept, ValidFrom: now,
		}
		if _, err := clampToParent(req, parent, now); !errors.Is(err, ErrOverDelegation) {
			t.Errorf("err = %v, want ErrOverDelegation", err)
		}
	})

	t.Run("an org-wide parent may be narrowed to a department", func(t *testing.T) {
		req := MandateGrant{
			Type: MandateAdministrative, Scope: MandateScopeDepartment,
			ScopeDepartmentID: &dept, ValidFrom: now,
		}
		if _, err := clampToParent(req, activeFull, now); err != nil {
			t.Errorf("clampToParent: %v", err)
		}
	})

	t.Run("nothing may be cut from an inactive parent", func(t *testing.T) {
		for name, parent := range map[string]Mandate{
			"revoked": {Type: MandateFull, Scope: MandateScopeOrganization, ValidFrom: now.Add(-day), RevokedAt: ptr(now.Add(-time.Hour))},
			"expired": {Type: MandateFull, Scope: MandateScopeOrganization, ValidFrom: now.Add(-2 * day), ValidUntil: ptr(now.Add(-day))},
			"pending": {Type: MandateFull, Scope: MandateScopeOrganization, ValidFrom: now.Add(day)},
		} {
			req := MandateGrant{Type: MandateAdministrative, Scope: MandateScopeOrganization, ValidFrom: now}
			if _, err := clampToParent(req, parent, now); !errors.Is(err, ErrMandateInactive) {
				t.Errorf("%s parent: err = %v, want ErrMandateInactive", name, err)
			}
		}
	})
}

func TestAuthorityWithdrawn(t *testing.T) {
	tests := []struct {
		name string
		a    Authority
		want bool
	}{
		{
			name: "an org that never granted a mandate is never withdrawn",
			a:    Authority{Granted: 0},
			want: false,
		},
		{
			name: "an active org-wide mandate is not withdrawn",
			a:    Authority{Granted: 1, Mandated: true},
			want: false,
		},
		{
			// Mandated counts only active org-wide grants, so this is equally the
			// caller whose every grant was revoked or expired and the one left with
			// nothing but a department-scoped mandate. Which of the two it is, is
			// ResolveAuthority's job and is covered in the store's integration test.
			name: "no active org-wide grant left is withdrawn",
			a:    Authority{Granted: 2, Mandated: false},
			want: true,
		},
		{
			name: "a platform admin is never withdrawn by an org's register",
			a:    Authority{Granted: 1, Mandated: false, PlatformAdmin: true},
			want: false,
		},
		{
			// Their authority is the register's, not a delegation, so a mandate they
			// happened to also be granted running out cannot take it away — the
			// register cannot withdraw the root it hangs from.
			name: "a legal representative is never withdrawn by the register they control",
			a:    Authority{Granted: 1, Mandated: false, LegalRepresentative: true},
			want: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.a.Withdrawn(); got != tt.want {
				t.Errorf("Withdrawn() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestAuthorityMayGrantMandate(t *testing.T) {
	tests := []struct {
		name string
		a    Authority
		want bool
	}{
		{"legal representative may", Authority{LegalRepresentative: true}, true},
		{"full-mandate holder may", Authority{FullMandate: true, Mandated: true, Granted: 1}, true},
		{"an administrative mandate is not enough", Authority{Mandated: true, Granted: 1}, false},
		{"a platform admin is not the owner", Authority{PlatformAdmin: true}, false},
		{"nobody else may", Authority{}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.a.MayGrantMandate(); got != tt.want {
				t.Errorf("MayGrantMandate() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestValidateGrant(t *testing.T) {
	dept := uuid.New()
	now := time.Now()

	t.Run("an unknown tier is rejected", func(t *testing.T) {
		req := MandateGrant{Type: "owner", Scope: MandateScopeOrganization}
		if err := validateGrant(&req); !errors.Is(err, ErrMandateType) {
			t.Errorf("err = %v, want ErrMandateType", err)
		}
	})

	t.Run("a department scope needs a department", func(t *testing.T) {
		req := MandateGrant{Type: MandateFull, Scope: MandateScopeDepartment}
		if err := validateGrant(&req); !errors.Is(err, ErrMandateScope) {
			t.Errorf("err = %v, want ErrMandateScope", err)
		}
	})

	t.Run("an org scope may not carry a department", func(t *testing.T) {
		req := MandateGrant{Type: MandateFull, Scope: MandateScopeOrganization, ScopeDepartmentID: &dept}
		if err := validateGrant(&req); !errors.Is(err, ErrMandateScope) {
			t.Errorf("err = %v, want ErrMandateScope", err)
		}
	})

	t.Run("an unknown scope is rejected", func(t *testing.T) {
		req := MandateGrant{Type: MandateFull, Scope: "everything"}
		if err := validateGrant(&req); !errors.Is(err, ErrMandateScope) {
			t.Errorf("err = %v, want ErrMandateScope", err)
		}
	})

	t.Run("an empty window is rejected", func(t *testing.T) {
		req := MandateGrant{
			Type: MandateFull, Scope: MandateScopeOrganization,
			ValidFrom: now, ValidUntil: ptr(now),
		}
		if err := validateGrant(&req); !errors.Is(err, ErrMandateWindow) {
			t.Errorf("err = %v, want ErrMandateWindow", err)
		}
	})

	t.Run("a missing start defaults to now", func(t *testing.T) {
		req := MandateGrant{Type: MandateFull, Scope: MandateScopeOrganization}
		if err := validateGrant(&req); err != nil {
			t.Fatalf("validateGrant: %v", err)
		}
		if req.ValidFrom.IsZero() {
			t.Error("validFrom is still zero")
		}
	})
}
