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
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/eudiholder"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/euid"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/organization"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/qerds"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/qerdsprovider"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/registryprovider"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/user"
)

// kvkSenderAddress is the QERDS "from" shown on the deposited attestation message.
const kvkSenderAddress = "registratie@kvk.nl"

// discardTimeout bounds dropping the holder of a wallet that was not opened.
const discardTimeout = 10 * time.Second

// walletStore is the persistence surface the service coordinates.
type walletStore interface {
	RegisterOrganization(ctx context.Context, reg Registration) (organization.Organization, error)
	SetStatus(ctx context.Context, orgID uuid.UUID, status string) (organization.Organization, error)
	ListRepresentations(ctx context.Context, orgID uuid.UUID) ([]Representation, error)
	ClaimRepresentation(ctx context.Context, orgID, repID, userID uuid.UUID) error
}

// registry is the KVK authentic-source seam (see internal/registryprovider). It
// receives the KVK number plus the requester's identification data and decides
// whether the requester is a listed representative.
type registry interface {
	Consult(ctx context.Context, req registryprovider.ConsultRequest) (registryprovider.RegistrationAttestation, error)
}

// ownerIDIssuer gets the org's EBWOID (Art 8 owner identification data) into
// its holder engine (see internal/registryprovider).
type ownerIDIssuer interface {
	Obtain(ctx context.Context, h registryprovider.EBWOIDHolder, orgID uuid.UUID, att registryprovider.RegistrationAttestation) (registryprovider.HeldEBWOID, error)
}

// ownerIDHolder is the org's holder engine the EBWOID is stored in and read from.
type ownerIDHolder interface {
	registryprovider.EBWOIDHolder
	Claims(ctx context.Context, orgID uuid.UUID, ref, vct, lang string) (eudiholder.HeldCredential, error)
	DropOrganization(ctx context.Context, orgID uuid.UUID) error
}

// OwnerID is where a new wallet's EBWOID comes from: the issuer that obtains it
// and the org's holder engine it is stored in and read from.
type OwnerID struct {
	Issuer ownerIDIssuer
	Holder ownerIDHolder
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
	ownerID       ownerIDIssuer
	holder        ownerIDHolder
	discloser     identityDiscloser
	users         userDirectory
	inbox         qerdsInbox
	addressDomain string
}

func NewService(
	store walletStore,
	reg registry,
	ownerID OwnerID,
	discloser identityDiscloser,
	users userDirectory,
	inbox qerdsInbox,
	addressDomain string,
) *Service {
	return &Service{
		store:         store,
		registry:      reg,
		ownerID:       ownerID.Issuer,
		holder:        ownerID.Holder,
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
	// The disclosed PID (verified name + date of birth) is what KVK matches
	// against its register — the person is identified by their wallet, not by
	// the account we just found or created.
	requester := Requester{
		GivenNames:  disclosed.Name.GivenNames,
		FamilyName:  disclosed.Name.LastName,
		DateOfBirth: disclosed.DateOfBirth,
	}
	result, err := s.OpenWallet(ctx, u.ID, requester, kvkNumber, slug)
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
// it validates the slug, consults KVK with the requester's identification data
// and — if KVK validates the requester as a listed representative — gets the
// org's EBWOID into its holder, creates the organization with the legal name and
// EUID from that verified credential, makes the requester the first owner, and
// deposits the attestation in the org's QERDS inbox. An unknown KVK number or a
// requester KVK does not list is bounced as not validated; no org is created.
func (s *Service) OpenWallet(ctx context.Context, requestorUserID uuid.UUID, requester Requester, kvkNumber, slug string) (RegistrationResult, error) {
	slug = strings.ToLower(strings.TrimSpace(slug))
	if err := organization.ValidateSlug(slug); err != nil {
		return RegistrationResult{}, err
	}

	att, err := s.registry.Consult(ctx, registryprovider.ConsultRequest{
		KVKNumber:   kvkNumber,
		GivenNames:  requester.GivenNames,
		FamilyName:  requester.FamilyName,
		DateOfBirth: requester.DateOfBirth,
	})
	if errors.Is(err, registryprovider.ErrUnknownKVK) {
		return RegistrationResult{}, ErrUnknownKVK
	}
	if err != nil {
		return RegistrationResult{}, fmt.Errorf("wallet: consult registry: %w", err)
	}
	if !att.RequesterIsRepresentative {
		return RegistrationResult{}, ErrNotRepresentative
	}
	registered, err := euid.Parse(att.EUID)
	if err != nil {
		return RegistrationResult{}, fmt.Errorf("wallet: registry EUID: %w", err)
	}
	att.EUID = registered.String()

	// The holder engine is per org, so the org id is fixed before the org row exists.
	orgID := uuid.New()
	ebwoid, err := s.obtainEBWOID(ctx, orgID, att, registered)
	if err != nil {
		return RegistrationResult{}, err
	}
	att.LegalName, att.EUID = ebwoid.legalName, ebwoid.euid

	// The org's own default (sending) address is slug-based. KVK is saved as a
	// recipient in the address book at the address it delivers the attestation
	// from (kvkSenderAddress) — so the contact matches the message's "from" and
	// replies reach KVK, rather than a synthetic org-derived address.
	digitalAddress := fmt.Sprintf("%s@%s", slug, s.addressDomain)
	org, err := s.store.RegisterOrganization(ctx, Registration{
		OrgID:             orgID,
		RequestorUserID:   requestorUserID,
		Slug:              slug,
		DigitalAddress:    digitalAddress,
		KVKContactAddress: kvkSenderAddress,
		Attestation:       att,
		EBWOID:            ebwoid.held,
	})
	if err != nil {
		s.discardHolder(ctx, orgID)
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

// heldEBWOID is a verified EBWOID in the org's holder, with the identity read from it.
type heldEBWOID struct {
	held      registryprovider.HeldEBWOID
	legalName string
	euid      string
}

// obtainEBWOID gets the EBWOID into orgID's holder and reads the owner identity
// from the verified credential. Its EUID must name the company the register
// validated, or a valid credential for another company would open this wallet.
func (s *Service) obtainEBWOID(ctx context.Context, orgID uuid.UUID, att registryprovider.RegistrationAttestation, registered euid.EUID) (heldEBWOID, error) {
	held, err := s.ownerID.Obtain(ctx, s.holder, orgID, att)
	if err != nil {
		s.discardHolder(ctx, orgID)
		return heldEBWOID{}, fmt.Errorf("wallet: obtain EBWOID: %w", err)
	}
	legalName, id, err := s.readEBWOID(ctx, orgID, held.Ref)
	if err == nil && !sameCompany(id, registered) {
		err = fmt.Errorf("%w: EBWOID %s, register %s", ErrEBWOIDMismatch, id, registered)
	}
	if err != nil {
		s.discardHolder(ctx, orgID)
		return heldEBWOID{}, err
	}
	return heldEBWOID{held: held, legalName: legalName, euid: id.String()}, nil
}

// readEBWOID reads the official name and EUID from the stored EBWOID's verified claims.
func (s *Service) readEBWOID(ctx context.Context, orgID uuid.UUID, ref string) (string, euid.EUID, error) {
	cred, err := s.holder.Claims(ctx, orgID, ref, registryprovider.EBWOIDVCT, "")
	if err != nil {
		return "", euid.EUID{}, fmt.Errorf("wallet: read EBWOID: %w", err)
	}
	var legalName, rawID string
	for _, a := range cred.Attributes {
		value, _ := a.Value.(string)
		switch a.Key {
		case registryprovider.EBWOIDClaimName:
			legalName = strings.TrimSpace(value)
		case registryprovider.EBWOIDClaimID:
			rawID = value
		}
	}
	if legalName == "" {
		return "", euid.EUID{}, errors.New("wallet: EBWOID has no name")
	}
	id, err := euid.Parse(rawID)
	if err != nil {
		return "", euid.EUID{}, fmt.Errorf("wallet: EBWOID id: %w", err)
	}
	return legalName, id, nil
}

// sameCompany compares two EUIDs without the EBWOID's optional third part.
func sameCompany(a, b euid.EUID) bool {
	return a.Country == b.Country && a.Register == b.Register && a.Number == b.Number
}

// discardHolder drops the holder of a wallet that was not opened, so no org-less
// EBWOID or holder schema stays behind. It runs detached from a possibly cancelled request.
func (s *Service) discardHolder(ctx context.Context, orgID uuid.UUID) {
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), discardTimeout)
	defer cancel()
	if err := s.holder.DropOrganization(ctx, orgID); err != nil {
		slog.ErrorContext(ctx, "wallet: dropping the holder of an unopened wallet failed",
			slog.String("orgId", orgID.String()), slog.String("error", err.Error()))
	}
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
