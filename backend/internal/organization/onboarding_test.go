package organization

import (
	"context"
	"testing"

	"github.com/google/uuid"
)

type captureIssuer struct {
	called bool
	member OnboardingMember
}

func (c *captureIssuer) IssueOnboarding(_ context.Context, m OnboardingMember) {
	c.called = true
	c.member = m
}

func TestIssueOnboardingBuildsMemberFromInvitation(t *testing.T) {
	jobTitle := "Engineer"
	department := "Platform"
	orgID := uuid.New()
	inv := Invitation{
		OrganizationID:   orgID,
		OrganizationName: "Acme",
		GivenNames:       "Alice",
		LastName:         "Jones",
		Email:            "invited@acme.test",
		Role:             "admin",
		JobTitle:         &jobTitle,
		DepartmentName:   &department,
	}
	cap := &captureIssuer{}
	s := &Service{onboarding: cap}

	userID := uuid.New()
	s.issueOnboarding(context.Background(), inv, userID, "disclosed@acme.test", "+31600000000")

	if !cap.called {
		t.Fatal("onboarding issuer was not called")
	}
	got := cap.member
	// The disclosed e-mail/phone (proven at accept) win over the invitation's.
	if got.Email != "disclosed@acme.test" || got.Phone != "+31600000000" {
		t.Errorf("email/phone = %q/%q, want the disclosed values", got.Email, got.Phone)
	}
	if got.UserID != userID || got.OrganizationID != orgID {
		t.Error("ids not threaded through from the accept")
	}
	if got.Role != "admin" || got.JobTitle != "Engineer" || got.DepartmentName != "Platform" {
		t.Errorf("role/jobTitle/department = %q/%q/%q", got.Role, got.JobTitle, got.DepartmentName)
	}
	if got.GivenNames != "Alice" || got.LastName != "Jones" {
		t.Errorf("name = %q %q", got.GivenNames, got.LastName)
	}
}

func TestIssueOnboardingNoIssuerIsNoop(t *testing.T) {
	// With no issuer wired, the accept path must not panic.
	s := &Service{}
	s.issueOnboarding(context.Background(), Invitation{}, uuid.New(), "a@b.test", "")
}
