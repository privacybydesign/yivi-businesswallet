package main

import (
	"context"
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
	out := make([]signing.OrgMember, 0, len(members))
	for _, mem := range members {
		out = append(out, signing.OrgMember{
			UserID: mem.UserID,
			Name:   memberDisplayName(mem),
			Email:  mem.Email,
		})
	}
	return out, nil
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
