package wallet

import (
	"context"
	"fmt"

	"github.com/google/uuid"

	"github.com/privacybydesign/yivi-businesswallet/backend/internal/registryprovider"
)

// instanceStore is the persistence surface the service coordinates.
type instanceStore interface {
	CreateInstance(ctx context.Context, requestorUserID uuid.UUID, kvkNumber, digitalAddress string) (Instance, error)
	MarkRequested(ctx context.Context, id uuid.UUID) error
	GetInstanceByID(ctx context.Context, id uuid.UUID) (Instance, error)
	GetInstanceByOrg(ctx context.Context, orgID uuid.UUID) (Instance, error)
	ListRepresentations(ctx context.Context, orgID uuid.UUID) ([]Representation, error)
	SetStatus(ctx context.Context, orgID uuid.UUID, status string) (Instance, error)
	ActivateFromAttestation(ctx context.Context, id uuid.UUID, att registryprovider.RegistrationAttestation) (Instance, error)
	RejectInstance(ctx context.Context, id uuid.UUID, reason string) (Instance, error)
	ClaimRepresentation(ctx context.Context, orgID, repID, userID uuid.UUID) error
}

// registry is the KVK authentic-source seam (see internal/registryprovider).
type registry interface {
	RequestRegistration(ctx context.Context, req registryprovider.RegistrationRequest) (registryprovider.RequestReceipt, error)
}

// Service coordinates opening a wallet, processing the KVK attestation and
// managing representations across the instance store and the registry seam.
type Service struct {
	instances     instanceStore
	registry      registry
	addressDomain string
}

func NewService(instances instanceStore, reg registry, addressDomain string) *Service {
	return &Service{instances: instances, registry: reg, addressDomain: addressDomain}
}

// OpenWallet opens a wallet for the given KVK number on behalf of the requester:
// it provisions the wallet's QERDS digital address and sends the {PID, KVK
// number} request to KVK. The attestation returns asynchronously and is applied
// by HandleAttestation. See §6.1.
func (s *Service) OpenWallet(ctx context.Context, requestorUserID uuid.UUID, kvkNumber string, pid registryprovider.PID) (Instance, error) {
	// TODO(wallet-bootstrap): allocate the address via the qerds slice rather
	// than derive it, so the same value becomes a qerds_addresses row on activation.
	address := fmt.Sprintf("kvk-%s@%s", kvkNumber, s.addressDomain)

	in, err := s.instances.CreateInstance(ctx, requestorUserID, kvkNumber, address)
	if err != nil {
		return Instance{}, err
	}

	if _, err := s.registry.RequestRegistration(ctx, registryprovider.RegistrationRequest{PID: pid, KVKNumber: kvkNumber}); err != nil {
		return Instance{}, fmt.Errorf("wallet: request registration: %w", err)
	}
	if err := s.instances.MarkRequested(ctx, in.ID); err != nil {
		return Instance{}, err
	}
	in.Status = StatusAwaitingAttestation
	return in, nil
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

// HandleAttestation applies a KVK registration attestation delivered over QERDS:
// on confirmation it activates the wallet. See §6.2. Invoked by the QERDS inbound
// dispatch once that wiring lands.
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
