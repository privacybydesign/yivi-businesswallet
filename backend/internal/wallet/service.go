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
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/qerds"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/qerdsprovider"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/registryprovider"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/user"
)

// kvkSenderAddress is the QERDS "from" shown on the deposited attestation message.
const kvkSenderAddress = "registratie@kvk.nl"

// instanceStore is the persistence surface the service coordinates.
type instanceStore interface {
	ActiveWalletExists(ctx context.Context, kvkNumber string) (bool, error)
	CreateInstance(ctx context.Context, requestorUserID uuid.UUID, kvkNumber, digitalAddress string) (Instance, error)
	GetInstanceByID(ctx context.Context, id uuid.UUID) (Instance, error)
	GetInstanceByOrg(ctx context.Context, orgID uuid.UUID) (Instance, error)
	ListRepresentations(ctx context.Context, orgID uuid.UUID) ([]Representation, error)
	SetStatus(ctx context.Context, orgID uuid.UUID, status string) (Instance, error)
	ActivateFromAttestation(ctx context.Context, id uuid.UUID, att registryprovider.RegistrationAttestation) (Instance, error)
	RejectInstance(ctx context.Context, id uuid.UUID, reason string) (Instance, error)
	ClaimRepresentation(ctx context.Context, orgID, repID, userID uuid.UUID) error
}

// registry is the KVK authentic-source seam (see internal/registryprovider). The
// stub consults synchronously; a real KVK integration would deliver the
// attestation over QERDS and this method would await it.
type registry interface {
	Consult(ctx context.Context, kvkNumber string) (registryprovider.RegistrationAttestation, error)
}

// identityDiscloser resolves an OpenID4VP identity disclosure (used by the public
// register flow, which authenticates the person via their wallet).
type identityDiscloser interface {
	StartIdentitySession(ctx context.Context) (auth.Session, error)
	DiscloseIdentity(ctx context.Context, sessionID string) (auth.DisclosedIdentity, error)
}

// userDirectory finds or creates the registering user (self-service registration
// must work for someone with no account yet).
type userDirectory interface {
	FindByEmail(ctx context.Context, email user.Email) (user.User, error)
	Create(ctx context.Context, u user.User) (user.User, error)
}

// qerdsInbox deposits the KVK attestation into the new org's QERDS inbox as the
// evidenced record of the registration.
type qerdsInbox interface {
	CreateInbound(ctx context.Context, orgID uuid.UUID, in qerdsprovider.InboundMessage) (qerds.Message, bool, error)
}

// Service coordinates opening a wallet, processing the KVK attestation and
// managing representations across the instance store, registry, disclosure,
// user directory and QERDS inbox.
type Service struct {
	instances     instanceStore
	registry      registry
	discloser     identityDiscloser
	users         userDirectory
	inbox         qerdsInbox
	addressDomain string
}

func NewService(
	instances instanceStore,
	reg registry,
	discloser identityDiscloser,
	users userDirectory,
	inbox qerdsInbox,
	addressDomain string,
) *Service {
	return &Service{
		instances:     instances,
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

// RegistrationOutcome carries the enrollment result plus the (possibly newly
// created) registrant's user id, so the handler can log them in.
type RegistrationOutcome struct {
	UserID uuid.UUID
	Result EnrollmentResult
}

// Register authenticates the person via their disclosed identity (creating an
// account if new), then opens and bootstraps the wallet for the KVK number.
func (s *Service) Register(ctx context.Context, disclosureToken, kvkNumber string) (RegistrationOutcome, error) {
	disclosed, err := s.discloser.DiscloseIdentity(ctx, disclosureToken)
	if err != nil {
		return RegistrationOutcome{}, err
	}

	u, err := s.findOrCreateUser(ctx, disclosed)
	if err != nil {
		return RegistrationOutcome{}, err
	}

	result, err := s.OpenWallet(ctx, u.ID, kvkNumber)
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

// OpenWallet opens a wallet for the given KVK number on behalf of the requester:
// it consults KVK, provisions the wallet's digital address, and — if the
// requester is a listed representative — activates the wallet (creating the org
// and making them the first owner) and deposits the attestation in the org's
// QERDS inbox. See §6.
func (s *Service) OpenWallet(ctx context.Context, requestorUserID uuid.UUID, kvkNumber string) (EnrollmentResult, error) {
	// One wallet per company: a second representative joins the existing org via a
	// claim rather than registering a duplicate.
	exists, err := s.instances.ActiveWalletExists(ctx, kvkNumber)
	if err != nil {
		return EnrollmentResult{}, err
	}
	if exists {
		return EnrollmentResult{}, ErrAlreadyRegistered
	}

	att, err := s.registry.Consult(ctx, kvkNumber)
	if err != nil {
		return EnrollmentResult{}, fmt.Errorf("wallet: consult registry: %w", err)
	}

	// TODO(wallet-bootstrap): allocate the address via the qerds slice rather
	// than derive it, so the same value becomes a qerds_addresses row on activation.
	address := fmt.Sprintf("kvk-%s@%s", kvkNumber, s.addressDomain)
	in, err := s.instances.CreateInstance(ctx, requestorUserID, kvkNumber, address)
	if err != nil {
		return EnrollmentResult{}, err
	}

	instance, err := s.HandleAttestation(ctx, in.ID, att)
	if err != nil {
		return EnrollmentResult{}, err
	}

	if instance.Status == StatusActive && instance.OrganizationID != nil {
		s.depositAttestation(ctx, *instance.OrganizationID, instance.DigitalAddress, att)
	}

	res := EnrollmentResult{Instance: instance}
	if att.RequesterIsRepresentative && att.RequesterRepresentativeIndex < len(att.Representatives) {
		rep := att.Representatives[att.RequesterRepresentativeIndex]
		res.RepresentationKind = rep.Kind
		res.RepresentationAuthority = rep.Authority
	}
	return res, nil
}

// depositAttestation records the KVK attestation as an inbound QERDS message in
// the org's inbox, with delivery evidence — the registered-delivery record of the
// bootstrap. Best-effort: a failure here does not undo the (committed) activation.
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

// GetInstance loads an instance by id (central poll path).
func (s *Service) GetInstance(ctx context.Context, id uuid.UUID) (Instance, error) {
	return s.instances.GetInstanceByID(ctx, id)
}

// WalletForOrg loads the instance backing an organization.
func (s *Service) WalletForOrg(ctx context.Context, orgID uuid.UUID) (Instance, error) {
	return s.instances.GetInstanceByOrg(ctx, orgID)
}

// Representations returns the org's mandate list.
func (s *Service) Representations(ctx context.Context, orgID uuid.UUID) ([]Representation, error) {
	return s.instances.ListRepresentations(ctx, orgID)
}

// HandleAttestation applies a KVK registration attestation: on confirmation it
// activates the wallet, otherwise it rejects the instance. See §6.2.
func (s *Service) HandleAttestation(ctx context.Context, instanceID uuid.UUID, att registryprovider.RegistrationAttestation) (Instance, error) {
	if !att.RequesterIsRepresentative {
		return s.instances.RejectInstance(ctx, instanceID, RejectNotRepresentative)
	}
	return s.instances.ActivateFromAttestation(ctx, instanceID, att)
}

// ClaimRepresentation lets a co-representative claim their owner seat.
func (s *Service) ClaimRepresentation(ctx context.Context, orgID, repID, userID uuid.UUID) error {
	return s.instances.ClaimRepresentation(ctx, orgID, repID, userID)
}

// Suspend suspends an org's wallet (Art 6(2)).
func (s *Service) Suspend(ctx context.Context, orgID uuid.UUID) (Instance, error) {
	return s.instances.SetStatus(ctx, orgID, StatusSuspended)
}

// Revoke revokes an org's wallet (Art 6(2)).
func (s *Service) Revoke(ctx context.Context, orgID uuid.UUID) (Instance, error) {
	return s.instances.SetStatus(ctx, orgID, StatusRevoked)
}
