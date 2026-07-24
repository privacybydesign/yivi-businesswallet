package attestation

import (
	"context"
	"log/slog"
	"strings"

	"github.com/google/uuid"

	"github.com/privacybydesign/yivi-businesswallet/backend/internal/organization"
)

// onboardingTemplateReader is the persistence the onboarding issuer needs: the
// org's configured onboarding set and each template's attribute-source bindings.
type onboardingTemplateReader interface {
	OnboardingTemplateIDs(ctx context.Context, orgID uuid.UUID) ([]uuid.UUID, error)
	GetTemplate(ctx context.Context, orgID, id uuid.UUID) (Template, error)
}

// OnboardingIssuer issues an organization's configured onboarding attestations to
// a member who has just accepted an invitation. It implements
// organization.OnboardingIssuer and is wired into the organization service after
// both services are constructed. Issuance reuses the normal Issue path (offer +
// e-mail claim link, audited), so an auto-issued credential is indistinguishable
// in the ledger from a manually issued one apart from having no issuing admin.
type OnboardingIssuer struct {
	store onboardingTemplateReader
	svc   *Service
}

func NewOnboardingIssuer(store onboardingTemplateReader, svc *Service) *OnboardingIssuer {
	return &OnboardingIssuer{store: store, svc: svc}
}

// IssueOnboarding issues each configured onboarding template to the member. It is
// best-effort: a failure on one template is logged and the rest still run, and no
// error is returned (the accept has already committed). A member with no
// configured onboarding set is a no-op.
func (i *OnboardingIssuer) IssueOnboarding(ctx context.Context, m organization.OnboardingMember) {
	ids, err := i.store.OnboardingTemplateIDs(ctx, m.OrganizationID)
	if err != nil {
		slog.ErrorContext(ctx, "attestation: onboarding template ids",
			slog.String("orgId", m.OrganizationID.String()), slog.String("error", err.Error()))
		return
	}
	if len(ids) == 0 {
		return
	}

	sources := onboardingSourceValues(m)
	userID := m.UserID
	for _, id := range ids {
		template, err := i.store.GetTemplate(ctx, m.OrganizationID, id)
		if err != nil {
			slog.ErrorContext(ctx, "attestation: onboarding load template",
				slog.String("orgId", m.OrganizationID.String()),
				slog.String("templateId", id.String()), slog.String("error", err.Error()))
			continue
		}
		_, err = i.svc.Issue(ctx, m.OrganizationID, nil, m.OrganizationName, IssueInput{
			TemplateID:     id,
			Recipient:      Recipient{Kind: RecipientMember, UserID: &userID, Ref: m.Email},
			Attributes:     resolveOnboardingAttributes(template, sources),
			DeliveryMethod: DeliveryMethodEmail,
		})
		if err != nil {
			slog.WarnContext(ctx, "attestation: onboarding issue failed",
				slog.String("orgId", m.OrganizationID.String()),
				slog.String("templateId", id.String()),
				slog.String("recipient", m.Email), slog.String("error", err.Error()))
		}
	}
}

// resolveOnboardingAttributes builds the attribute values for an auto-issued
// onboarding credential: the template's static defaults, overridden by any
// attribute-source binding that resolves to a non-empty value for this member. An
// unresolved binding falls back to the default (or is simply absent), mirroring
// the wizard's precedence without inventing values.
func resolveOnboardingAttributes(template Template, sources map[string]string) map[string]string {
	attrs := make(map[string]string, len(template.DefaultAttributes)+len(template.AttributeSources))
	for key, value := range template.DefaultAttributes {
		attrs[key] = value
	}
	for key, token := range template.AttributeSources {
		if value := sources[token]; value != "" {
			attrs[key] = value
		}
	}
	return attrs
}

// onboardingSourceValues maps the member's onboarding context onto the member.*
// attribute-source tokens. The org.* tokens never apply (organization-subject
// templates are rejected from the onboarding set), and member.preferredName has
// no value known at accept time, so both resolve to "".
func onboardingSourceValues(m organization.OnboardingMember) map[string]string {
	fullName := strings.TrimSpace(strings.TrimSpace(m.GivenNames) + " " + strings.TrimSpace(m.LastName))
	return map[string]string{
		SourceMemberGivenNames: m.GivenNames,
		SourceMemberLastName:   m.LastName,
		SourceMemberFullName:   fullName,
		SourceMemberEmail:      m.Email,
		SourceMemberPhone:      m.Phone,
		SourceMemberRole:       m.Role,
		SourceMemberJobTitle:   m.JobTitle,
		SourceMemberDepartment: m.DepartmentName,
	}
}
