package main

import (
	"context"
	"net/url"
	"strings"

	"github.com/google/uuid"

	"github.com/privacybydesign/yivi-businesswallet/backend/internal/email"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/organization"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/qerds"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/qerdsprovider"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/signing"
)

// signingMembers adapts organization.Store to signing.memberDirectory, mapping the
// full member record to the minimal (id, name, email) view the signing slice needs
// to validate and label selected signers.
type signingMembers struct{ store *organization.Store }

func (m signingMembers) ListMembers(ctx context.Context, orgID uuid.UUID) ([]signing.OrgMember, error) {
	members, err := m.store.ListMembers(ctx, orgID)
	if err != nil {
		return nil, err
	}
	// Empty (not an error) whenever the org's overdue consequence is "flag only"
	// (the default) or unconfigured — see organization.Store.IdentityBlockedSet.
	blocked, err := m.store.IdentityBlockedSet(ctx, orgID)
	if err != nil {
		return nil, err
	}
	out := make([]signing.OrgMember, 0, len(members))
	for _, mem := range members {
		out = append(out, signing.OrgMember{
			UserID:          mem.UserID,
			Name:            memberDisplayName(mem),
			Email:           mem.Email,
			IdentityBlocked: blocked[mem.UserID],
		})
	}
	return out, nil
}

// signingOrgs adapts organization.Store to signing.orgDirectory, so the external
// signing page — which is reached with nothing but an invitation token — can name the
// organisation that is asking for the signature.
type signingOrgs struct{ store *organization.Store }

func (o signingOrgs) OrgName(ctx context.Context, orgID uuid.UUID) (string, error) {
	org, err := o.store.GetByID(ctx, orgID)
	if err != nil {
		return "", err
	}
	return org.Name, nil
}

func memberDisplayName(m organization.Member) string {
	if m.PreferredName != nil && strings.TrimSpace(*m.PreferredName) != "" {
		return strings.TrimSpace(*m.PreferredName)
	}
	full := strings.TrimSpace(m.GivenNames + " " + m.LastName)
	if full != "" {
		return full
	}
	return m.Email
}

// signingDeliverer adapts the email and QERDS services to signing.documentDeliverer,
// so a completed co-signed document reaches its recipient over the chosen channel:
// email carries the signed PDF as an attachment; QERDS sends it as a registered
// delivery from the org's default address.
type signingDeliverer struct {
	email *email.Service
	qerds *qerds.Service
	orgs  *organization.Store
}

func (d signingDeliverer) DeliverEmail(ctx context.Context, orgID uuid.UUID, to, _ /*recipientName*/, coverMessage, filename string, pdf []byte) error {
	org, err := d.orgs.GetByID(ctx, orgID)
	if err != nil {
		return err
	}
	return d.email.SendSignedDocument(ctx, orgID, to, org.Name, coverMessage, filename, pdf)
}

func (d signingDeliverer) DeliverQERDS(ctx context.Context, orgID uuid.UUID, to, _ /*recipientName*/, subject, coverMessage, filename string, pdf []byte) error {
	// Empty "from" uses the org's default QERDS address.
	_, err := d.qerds.Send(ctx, orgID, "", to, subject, coverMessage, []qerdsprovider.Attachment{{
		Filename:    filename,
		ContentType: "application/pdf",
		Content:     pdf,
	}})
	return err
}

// signingNotifier adapts email.Service to signing.signerNotifier: it e-mails a
// selected member that a document awaits their signature, building the signing-page
// link from the org slug and the app base URL.
type signingNotifier struct {
	email      *email.Service
	orgs       *organization.Store
	appBaseURL string
}

func (n signingNotifier) NotifySignatureRequested(ctx context.Context, orgID uuid.UUID, signerEmail, documentName, slug string) error {
	org, err := n.orgs.GetByID(ctx, orgID)
	if err != nil {
		return err
	}
	signingURL := strings.TrimRight(n.appBaseURL, "/") + "/" + url.PathEscape(slug) + "/signing"
	return n.email.SendSignatureRequested(ctx, orgID, signerEmail, org.Name, documentName, signingURL)
}

// NotifyExternalSignatureRequested mails an external signee the same "a document is
// waiting for your signature" message a member gets, but pointed at their one-time
// invitation link — they have no org page to be sent to. The path comes from the
// signing slice so the mail links exactly where the ceremony returns to.
func (n signingNotifier) NotifyExternalSignatureRequested(ctx context.Context, orgID uuid.UUID, signeeEmail, documentName, token string) error {
	org, err := n.orgs.GetByID(ctx, orgID)
	if err != nil {
		return err
	}
	signingURL := strings.TrimRight(n.appBaseURL, "/") + signing.ExternalSignPath(token)
	return n.email.SendSignatureRequested(ctx, orgID, signeeEmail, org.Name, documentName, signingURL)
}

// NotifyRequesterDeclined mails the request's creator that a selected signer
// refused to sign, linking to the org's signing page. Unlike the two notify
// methods above, the caller has no org slug to build that link from (a decline
// reaches this from the external-signee route too, which has none), so it is
// resolved here from the org itself, the same way DeliverEmail resolves the org's
// display name.
func (n signingNotifier) NotifyRequesterDeclined(ctx context.Context, orgID uuid.UUID, requesterEmail, documentName, signerName, reason string) error {
	org, err := n.orgs.GetByID(ctx, orgID)
	if err != nil {
		return err
	}
	signingURL := strings.TrimRight(n.appBaseURL, "/") + "/" + url.PathEscape(org.Slug) + "/signing"
	return n.email.SendSignatureDeclined(ctx, orgID, requesterEmail, org.Name, documentName, signerName, reason, signingURL)
}
