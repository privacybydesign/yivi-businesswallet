package organization

import (
	"context"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/privacybydesign/yivi-businesswallet/backend/internal/auth"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/identity"
)

// StartReverifySession begins an identity disclosure for a re-identification
// token, mirroring StartAcceptSession — the token is checked to exist and not be
// expired before a wallet session is opened for it.
func (s *Service) StartReverifySession(ctx context.Context, rawToken string) (auth.Session, error) {
	if _, err := s.store.ReverifyTokenLookup(ctx, rawToken); err != nil {
		return auth.Session{}, err
	}
	return s.discloser.StartIdentitySession(ctx)
}

// MintOwnReverifyToken issues a fresh re-identification token for the caller's
// own membership — the in-app banner's entry point, sharing the exact mechanism
// an admin's "request identification" or a reminder e-mail uses.
func (s *Service) MintOwnReverifyToken(ctx context.Context, orgID, userID uuid.UUID) (string, time.Time, error) {
	return s.store.EnsureReverifyToken(ctx, orgID, userID)
}

// ReverifyOutcome is the result of a completed re-identification, enough for the
// handler to greet the member and route them into the app.
type ReverifyOutcome struct {
	OrganizationName string
	OrganizationSlug string
}

// CompleteReverification finishes a re-identification: it resolves the token,
// disclosure-matches the member (email must match; name is reconciled against
// the stored identity the same way invite-accept reconciles a disclosure —
// Populate never applies here since a verified name already exists), optionally
// enforces the org's credential-freshness policy, and on success records the new
// identification. A rejection at any step is audited and returns without
// changing the membership.
func (s *Service) CompleteReverification(ctx context.Context, rawToken, disclosureToken string) (ReverifyOutcome, error) {
	rc, err := s.store.ReverifyTokenLookup(ctx, rawToken)
	if err != nil {
		return ReverifyOutcome{}, err
	}

	disclosed, err := s.discloser.DiscloseIdentity(ctx, disclosureToken)
	if err != nil {
		return ReverifyOutcome{}, ErrDisclosureFailed
	}

	if !strings.EqualFold(string(disclosed.Email), rc.Email) {
		_ = s.store.RecordReverifyRejected(ctx, rc.OrganizationID, rc.UserID, rc.Email, "email_mismatch")
		return ReverifyOutcome{}, ErrReverifyEmailMismatch
	}

	switch identity.Reconcile(disclosed.Name, &rc.StoredName) {
	case identity.Review:
		_ = s.store.RecordReverifyRejected(ctx, rc.OrganizationID, rc.UserID, rc.Email, "name_mismatch")
		return ReverifyOutcome{}, ErrReverifyNameMismatch
	case identity.Upgrade:
		cleaned := disclosed.Name.Clean()
		if err := s.users.UpdateName(ctx, rc.UserID, cleaned.GivenNames, cleaned.LastName); err != nil {
			return ReverifyOutcome{}, err
		}
	}

	settings, err := s.store.GetIdentitySettings(ctx, rc.OrganizationID)
	if err != nil {
		return ReverifyOutcome{}, err
	}
	if settings.CredentialMaxAgeDays != nil && !disclosed.CredentialIssuedAt.IsZero() {
		maxAge := time.Duration(*settings.CredentialMaxAgeDays) * 24 * time.Hour
		if time.Since(disclosed.CredentialIssuedAt) > maxAge {
			_ = s.store.RecordReverifyRejected(ctx, rc.OrganizationID, rc.UserID, rc.Email, "credential_too_old")
			return ReverifyOutcome{}, ErrCredentialTooOld
		}
	}

	if err := s.store.CompleteReverification(ctx, rc.OrganizationID, rc.UserID, disclosed.Name, disclosed.Phone, disclosed.DateOfBirth); err != nil {
		return ReverifyOutcome{}, err
	}
	return ReverifyOutcome{OrganizationName: rc.OrganizationName, OrganizationSlug: rc.OrganizationSlug}, nil
}
