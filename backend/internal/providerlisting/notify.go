package providerlisting

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/url"
	"strings"

	"github.com/google/uuid"

	"github.com/privacybydesign/yivi-businesswallet/backend/internal/email"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/organization"
)

type listingMailer interface {
	SendProviderDelisted(ctx context.Context, orgID uuid.UUID, recipients []string, n email.ProviderNotice) error
	SendProviderRelisted(ctx context.Context, orgID uuid.UUID, recipients []string, n email.ProviderNotice) error
	SendWalletCeased(ctx context.Context, orgID uuid.UUID, recipients []string, n email.ProviderNotice) error
}

type orgDirectory interface {
	GetByID(ctx context.Context, id uuid.UUID) (organization.Organization, error)
	ListAdminEmails(ctx context.Context, orgID uuid.UUID) ([]string, error)
}

// MailNotifier mails an org's admins, through Yivi's own SMTP server or, without
// one, the org's own SMTP settings. An org with neither sees only that its wallet
// is suspended.
type MailNotifier struct {
	mail       listingMailer
	orgs       orgDirectory
	appBaseURL string
	policyURL  string
}

// NewMailNotifier builds the notifier. policyURL may be empty until the
// revocation policy is published.
func NewMailNotifier(mail listingMailer, orgs orgDirectory, appBaseURL, policyURL string) *MailNotifier {
	return &MailNotifier{mail: mail, orgs: orgs, appBaseURL: strings.TrimRight(appBaseURL, "/"), policyURL: policyURL}
}

func (n *MailNotifier) ProviderDelisted(ctx context.Context, orgID uuid.UUID) error {
	return n.send(ctx, orgID, "suspended", n.mail.SendProviderDelisted)
}

func (n *MailNotifier) ProviderRelisted(ctx context.Context, orgID uuid.UUID) error {
	return n.send(ctx, orgID, "resumed", n.mail.SendProviderRelisted)
}

// WalletCeased tells an org's admins its wallet was revoked because its activity
// ended.
func (n *MailNotifier) WalletCeased(ctx context.Context, orgID uuid.UUID) error {
	return n.send(ctx, orgID, "revoked", n.mail.SendWalletCeased)
}

func (n *MailNotifier) send(ctx context.Context, orgID uuid.UUID, what string,
	send func(ctx context.Context, orgID uuid.UUID, recipients []string, n email.ProviderNotice) error,
) error {
	org, err := n.orgs.GetByID(ctx, orgID)
	if err != nil {
		return fmt.Errorf("providerlisting: org %s: %w", orgID, err)
	}
	admins, err := n.orgs.ListAdminEmails(ctx, orgID)
	if err != nil {
		return fmt.Errorf("providerlisting: admins of org %s: %w", orgID, err)
	}
	if len(admins) == 0 {
		return nil
	}
	err = send(ctx, orgID, admins, email.ProviderNotice{
		OrgName:   org.Name,
		WalletURL: n.appBaseURL + "/" + url.PathEscape(org.Slug),
		PolicyURL: n.policyURL,
	})
	if errors.Is(err, email.ErrNotConfigured) {
		slog.WarnContext(ctx, "providerlisting: neither we nor the org have SMTP settings; its admins are not mailed that the wallet was "+what,
			slog.String("org_id", orgID.String()))
		return nil
	}
	return err
}
