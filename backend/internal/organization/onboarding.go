package organization

import (
	"context"

	"github.com/google/uuid"
)

// OnboardingMember is the accepted member's context handed to the onboarding
// auto-issuer. It carries the plain field values the attestation slice maps onto
// its attribute-source tokens (member.* / org.*), so the organization slice never
// depends on the attestation slice. A field with no known value is left empty and
// resolves to "" — a template binding an attribute to that source simply omits it.
type OnboardingMember struct {
	OrganizationID   uuid.UUID
	OrganizationName string
	UserID           uuid.UUID
	GivenNames       string
	LastName         string
	Email            string
	Phone            string
	Role             string
	JobTitle         string
	DepartmentName   string
}

// OnboardingIssuer auto-issues an organization's configured onboarding
// attestations to a member who has just accepted an invitation. It is
// best-effort and non-fatal: issuance runs after the accept has committed and a
// failure must never fail the accept (consistent with the existing offer
// delivery model). The concrete implementation lives in the attestation slice
// and is wired in after both services are constructed (SetOnboardingIssuer).
type OnboardingIssuer interface {
	IssueOnboarding(ctx context.Context, member OnboardingMember)
}

// SetOnboardingIssuer wires the onboarding auto-issuer after construction. It is
// optional: with none set, accepting an invitation issues nothing (the prior
// behaviour). The seam breaks the construction-order cycle between the
// organization and attestation services, mirroring how the inbound QERDS
// consumer is wired.
func (s *Service) SetOnboardingIssuer(issuer OnboardingIssuer) {
	s.onboarding = issuer
}

// issueOnboarding fires the configured onboarding attestations for a member who
// has just accepted, when an issuer is wired. It builds the neutral member
// context from the invitation and the disclosed identity.
func (s *Service) issueOnboarding(ctx context.Context, inv Invitation, userID uuid.UUID, email, phone string) {
	s.issueOnboardingMember(ctx, OnboardingMember{
		OrganizationID:   inv.OrganizationID,
		OrganizationName: inv.OrganizationName,
		UserID:           userID,
		GivenNames:       inv.GivenNames,
		LastName:         inv.LastName,
		Email:            email,
		Phone:            phone,
		Role:             inv.Role,
		JobTitle:         deref(inv.JobTitle),
		DepartmentName:   deref(inv.DepartmentName),
	})
}

// issueOnboardingMember fires the configured onboarding attestations for a
// prebuilt member context, when an issuer is wired. It backs both join paths:
// the happy accept (issueOnboarding) and the identity-review approval, which
// builds its own member from the held invitation and disclosed identity.
func (s *Service) issueOnboardingMember(ctx context.Context, member OnboardingMember) {
	if s.onboarding == nil {
		return
	}
	s.onboarding.IssueOnboarding(ctx, member)
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}
