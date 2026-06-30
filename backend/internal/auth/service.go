package auth

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"

	irma "github.com/privacybydesign/irmago/irma"
	irmaserver "github.com/privacybydesign/irmago/irma/server"

	"github.com/privacybydesign/yivi-businesswallet/backend/internal/identity"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/session"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/user"
)

type IdentityAttributes struct {
	GivenNames irma.AttributeTypeIdentifier
	FamilyName irma.AttributeTypeIdentifier
}

type DisclosedIdentity struct {
	Email user.Email
	Name  identity.Name
}

// irmaRequestor is the slice of irmarequestor.Client the service needs, defined
// in the consumer so the service is testable without a live daemon.
type irmaRequestor interface {
	StartSession(ctx context.Context, req *irma.DisclosureRequest) (*irmaserver.SessionPackage, error)
	Result(ctx context.Context, token irma.RequestorToken) (*irmaserver.SessionResult, error)
	Status(ctx context.Context, token irma.RequestorToken) (irma.ServerStatus, error)
}

type Service struct {
	irma          irmaRequestor
	users         *user.Store
	sessions      *session.Store
	emailAttr     irma.AttributeTypeIdentifier
	identityAttrs IdentityAttributes
}

func NewService(
	requestor irmaRequestor,
	users *user.Store,
	sessions *session.Store,
	emailAttr irma.AttributeTypeIdentifier,
	identityAttrs IdentityAttributes,
) *Service {
	return &Service{
		irma:          requestor,
		users:         users,
		sessions:      sessions,
		emailAttr:     emailAttr,
		identityAttrs: identityAttrs,
	}
}

func (s *Service) StartSession(ctx context.Context) (*irmaserver.SessionPackage, error) {
	req := irma.NewDisclosureRequest(s.emailAttr)

	pkg, err := s.irma.StartSession(ctx, req)
	if err != nil {
		return nil, fmt.Errorf("auth: start session: %w", err)
	}
	return pkg, nil
}

func (s *Service) StartIdentitySession(ctx context.Context) (*irmaserver.SessionPackage, error) {
	req := irma.NewDisclosureRequest(s.identityAttrs.GivenNames, s.identityAttrs.FamilyName, s.emailAttr)

	pkg, err := s.irma.StartSession(ctx, req)
	if err != nil {
		return nil, fmt.Errorf("auth: start identity session: %w", err)
	}
	return pkg, nil
}

func (s *Service) DiscloseIdentity(ctx context.Context, token irma.RequestorToken) (DisclosedIdentity, error) {
	res, err := s.irma.Result(ctx, token)
	if err != nil {
		return DisclosedIdentity{}, err
	}
	return extractIdentity(res, s.identityAttrs, s.emailAttr)
}

func (s *Service) Status(ctx context.Context, token irma.RequestorToken) (irma.ServerStatus, error) {
	return s.irma.Status(ctx, token)
}

func (s *Service) Authenticate(ctx context.Context, token irma.RequestorToken) (user.User, string, error) {
	res, err := s.irma.Result(ctx, token)
	if err != nil {
		return user.User{}, "", err
	}

	email, err := extractEmail(res, s.emailAttr)
	if err != nil {
		return user.User{}, "", err
	}

	u, err := s.users.FindByEmail(ctx, email)
	if err != nil {
		if errors.Is(err, user.ErrNotFound) {
			return user.User{}, "", errUserNotInvited
		}
		return user.User{}, "", err
	}

	// idempotencyKey = sha256(requestorToken) makes a replayed /claim rotate this user's session row rather than mint a second one.
	raw, err := s.sessions.Mint(ctx, u.ID, sha256.Sum256([]byte(token)))
	if err != nil {
		return user.User{}, "", err
	}
	return u, raw, nil
}

func (s *Service) Logout(ctx context.Context, rawToken string) error {
	return s.sessions.Delete(ctx, rawToken)
}
