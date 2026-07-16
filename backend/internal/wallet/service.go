package wallet

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/privacybydesign/yivi-businesswallet/backend/internal/auth"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/organization"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/qerds"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/qerdsprovider"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/registryprovider"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/user"
)

// kvkSenderAddress is the QERDS "from" shown on the deposited attestation message.
const kvkSenderAddress = "registratie@kvk.nl"

// walletStore is the persistence surface the service coordinates.
type walletStore interface {
	RegisterOrganization(ctx context.Context, requestorUserID uuid.UUID, slug, digitalAddress string, att registryprovider.RegistrationAttestation) (organization.Organization, error)
	SetStatus(ctx context.Context, orgID uuid.UUID, status string) (organization.Organization, error)
	ListRepresentations(ctx context.Context, orgID uuid.UUID) ([]Representation, error)
	ClaimRepresentation(ctx context.Context, orgID, repID, userID uuid.UUID) error
}

// registry is the KVK authentic-source seam (see internal/registryprovider).
type registry interface {
	Consult(ctx context.Context, kvkNumber string) (registryprovider.RegistrationAttestation, error)
}

// identityDiscloser resolves an OpenID4VP identity disclosure (public register
// flow authenticates the person via their wallet).
type identityDiscloser interface {
	StartIdentitySession(ctx context.Context) (auth.Session, error)
	DiscloseIdentity(ctx context.Context, sessionID string) (auth.DisclosedIdentity, error)
}

// userDirectory finds or creates the registering user.
type userDirectory interface {
	FindByEmail(ctx context.Context, email user.Email) (user.User, error)
	Create(ctx context.Context, u user.User) (user.User, error)
}

// qerdsInbox deposits the KVK attestation into the new org's QERDS inbox.
type qerdsInbox interface {
	CreateInbound(ctx context.Context, orgID uuid.UUID, in qerdsprovider.InboundMessage) (qerds.Message, bool, error)
}

// Service coordinates wallet registration and lifecycle across the wallet store,
// registry, disclosure, user directory and QERDS inbox.
type Service struct {
	store         walletStore
	registry      registry
	discloser     identityDiscloser
	users         userDirectory
	inbox         qerdsInbox
	addressDomain string
}

func NewService(
	store walletStore,
	reg registry,
	discloser identityDiscloser,
	users userDirectory,
	inbox qerdsInbox,
	addressDomain string,
) *Service {
	return &Service{
		store:         store,
		registry:      reg,
		discloser:     discloser,
		users:         users,
		inbox:         inbox,
		addressDomain: addressDomain,
	}
}

// StartRegisterSession begins the identity disclosure that authenticates a
// self-service registrant (public, no existing account required).
func (s *Service) StartRegisterSession(ctx context.Context) (auth.Session, error) {
	return s.discloser.StartIdentitySession(ctx)
}

// RegistrationOutcome carries the registration result plus the (possibly newly
// created) registrant's user id, so the handler can log them in.
type RegistrationOutcome struct {
	UserID uuid.UUID
	Result RegistrationResult
}

// Register authenticates the person via their disclosed identity (creating an
// account if new), then registers the wallet for the KVK number under the slug.
func (s *Service) Register(ctx context.Context, disclosureToken, kvkNumber, slug string) (RegistrationOutcome, error) {
	disclosed, err := s.discloser.DiscloseIdentity(ctx, disclosureToken)
	if err != nil {
		return RegistrationOutcome{}, err
	}
	u, err := s.findOrCreateUser(ctx, disclosed)
	if err != nil {
		return RegistrationOutcome{}, err
	}
	result, err := s.OpenWallet(ctx, u.ID, kvkNumber, slug)
	if err != nil {
		return RegistrationOutcome{}, err
	}
	return RegistrationOutcome{UserID: u.ID, Result: result}, nil
}

func (s *Service) findOrCreateUser(ctx context.Context, disclosed auth.DisclosedIdentity) (user.User, error) {
	u, err := s.users.FindByEmail(ctx, disclosed.Email)
	if err == nil {
		return u, nil
	}
	if !errors.Is(err, user.ErrNotFound) {
		return user.User{}, fmt.Errorf("wallet: find user: %w", err)
	}
	cleaned := disclosed.Name.Clean()
	created, err := s.users.Create(ctx, user.User{
		Email:      disclosed.Email,
		GivenNames: cleaned.GivenNames,
		LastName:   cleaned.LastName,
	})
	if err != nil {
		return user.User{}, fmt.Errorf("wallet: create user: %w", err)
	}
	return created, nil
}

// OpenWallet registers a business wallet for the KVK number under the chosen slug:
// it validates the slug, consults KVK and — if the requester is a listed
// representative — creates the organization (with the register's legal name), makes
// them the first owner, and deposits the attestation in the org's QERDS inbox.
func (s *Service) OpenWallet(ctx context.Context, requestorUserID uuid.UUID, kvkNumber, slug string) (RegistrationResult, error) {
	slug = strings.ToLower(strings.TrimSpace(slug))
	if err := organization.ValidateSlug(slug); err != nil {
		return RegistrationResult{}, err
	}

	att, err := s.registry.Consult(ctx, kvkNumber)
	if err != nil {
		return RegistrationResult{}, fmt.Errorf("wallet: consult registry: %w", err)
	}
	if !att.RequesterIsRepresentative {
		return RegistrationResult{}, ErrNotRepresentative
	}

	address := fmt.Sprintf("kvk-%s@%s", kvkNumber, s.addressDomain)
	org, err := s.store.RegisterOrganization(ctx, requestorUserID, slug, address, att)
	if err != nil {
		return RegistrationResult{}, err
	}

	s.depositAttestation(ctx, org.ID, org.DigitalAddress, att)

	res := RegistrationResult{Organization: org}
	if att.RequesterRepresentativeIndex < len(att.Representatives) {
		rep := att.Representatives[att.RequesterRepresentativeIndex]
		res.RepresentationKind = rep.Kind
		res.RepresentationAuthority = rep.Authority
	}
	return res, nil
}

// depositAttestation records the KVK attestation as an inbound QERDS message in
// the org's inbox, with delivery evidence. Best-effort: a failure here does not
// undo the (committed) registration.
func (s *Service) depositAttestation(ctx context.Context, orgID uuid.UUID, recipient string, att registryprovider.RegistrationAttestation) {
	body := formatAttestation(att)
	ref := "kvk-attestation-" + orgID.String()
	now := time.Now().UTC()
	msg := qerdsprovider.InboundMessage{
		ProviderRef: ref,
		Sender:      qerdsprovider.Address(kvkSenderAddress),
		Recipient:   qerdsprovider.Address(recipient),
		Subject:     "KVK registration attestation — " + att.LegalName,
		Body:        body,
		Evidence: []qerdsprovider.Evidence{{
			Type:               qerdsprovider.EvidenceDelivery,
			ProviderRef:        ref,
			QualifiedTimestamp: now,
			Raw:                []byte(body),
		}},
	}
	if _, _, err := s.inbox.CreateInbound(ctx, orgID, msg); err != nil {
		slog.ErrorContext(ctx, "wallet: deposit attestation to qerds inbox failed",
			slog.String("orgId", orgID.String()), slog.String("error", err.Error()))
	}
}

func formatAttestation(att registryprovider.RegistrationAttestation) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Registration attestation for %s.\nKVK number: %s\nEUID: %s\n\nAuthorised representatives:\n",
		att.LegalName, att.KVKNumber, att.EUID)
	for _, r := range att.Representatives {
		fmt.Fprintf(&b, "- %s %s — %s (%s)\n", r.GivenNames, r.FamilyName, r.Kind, r.Authority)
	}
	return b.String()
}

// Representations returns the org's mandate list.
func (s *Service) Representations(ctx context.Context, orgID uuid.UUID) ([]Representation, error) {
	return s.store.ListRepresentations(ctx, orgID)
}

// ClaimRepresentation lets a co-representative claim their owner seat.
func (s *Service) ClaimRepresentation(ctx context.Context, orgID, repID, userID uuid.UUID) error {
	return s.store.ClaimRepresentation(ctx, orgID, repID, userID)
}

// Suspend suspends an org's wallet (Art 6(2)).
func (s *Service) Suspend(ctx context.Context, orgID uuid.UUID) (organization.Organization, error) {
	return s.store.SetStatus(ctx, orgID, organization.StatusSuspended)
}

// Revoke revokes an org's wallet (Art 6(2)).
func (s *Service) Revoke(ctx context.Context, orgID uuid.UUID) (organization.Organization, error) {
	return s.store.SetStatus(ctx, orgID, organization.StatusRevoked)
}
