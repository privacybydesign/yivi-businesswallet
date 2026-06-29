package auth

import (
	"context"
	"crypto/sha256"
	"fmt"

	irma "github.com/privacybydesign/irmago/irma"
	irmaserver "github.com/privacybydesign/irmago/irma/server"

	"github.com/privacybydesign/yivi-businesswallet/backend/internal/session"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/user"
)

// irmaRequestor is the slice of irmarequestor.Client the service needs, defined
// in the consumer so the service is testable without a live daemon.
type irmaRequestor interface {
	StartSession(ctx context.Context, req *irma.DisclosureRequest) (*irmaserver.SessionPackage, error)
	Result(ctx context.Context, token irma.RequestorToken) (*irmaserver.SessionResult, error)
	Status(ctx context.Context, token irma.RequestorToken) (irma.ServerStatus, error)
}

type Service struct {
	irma      irmaRequestor
	users     *user.Store
	sessions  *session.Store
	emailAttr irma.AttributeTypeIdentifier
}

func NewService(
	requestor irmaRequestor,
	users *user.Store,
	sessions *session.Store,
	emailAttr irma.AttributeTypeIdentifier,
) *Service {
	return &Service{
		irma:      requestor,
		users:     users,
		sessions:  sessions,
		emailAttr: emailAttr,
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

	// TODO: users have to be invited by an organization
	u, err := s.users.FindOrCreateByEmail(ctx, email)
	if err != nil {
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
