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
// reads the disclosure, and applies it under ApplyDisclosedIdentity's rules.
func (s *Service) CompleteReverification(ctx context.Context, rawToken, disclosureToken string) (ReverifyOutcome, error) {
	rc, err := s.store.ReverifyTokenLookup(ctx, rawToken)
	if err != nil {
		return ReverifyOutcome{}, err
	}

	disclosed, err := s.discloser.DiscloseIdentity(ctx, disclosureToken)
	if err != nil {
		return ReverifyOutcome{}, ErrDisclosureFailed
	}

	if _, err := s.ApplyDisclosedIdentity(ctx, rc.OrganizationID, rc.UserID, rc.Email, rc.StoredName, disclosed); err != nil {
		return ReverifyOutcome{}, err
	}
	return ReverifyOutcome{OrganizationName: rc.OrganizationName, OrganizationSlug: rc.OrganizationSlug}, nil
}

// CompleteOwnIdentification records an identity disclosure for a member
// resolved from a VOG link rather than a re-identification token. It is the
// entry point for a member who has never identified (no date of birth on
// file) and is asked for a VOG: the PDF path needs a stored identity to match
// against. Same rules as a re-identification.
func (s *Service) CompleteOwnIdentification(ctx context.Context, orgID, userID uuid.UUID, disclosureToken string) error {
	mc, err := s.store.ScreeningMatchContext(ctx, orgID, userID)
	if err != nil {
		return err
	}
	disclosed, err := s.discloser.DiscloseIdentity(ctx, disclosureToken)
	if err != nil {
		return ErrDisclosureFailed
	}
	_, err = s.ApplyDisclosedIdentity(ctx, orgID, userID, mc.Email, mc.Name, disclosed)
	return err
}

// ApplyDisclosedIdentity is the one place a completed identity disclosure is
// matched against a member and written to their membership, shared by the
// token-based re-identification, the in-app identification and the combined
// identity+VOG disclosure (ScreeningService): the email must match; the name
// is reconciled against the stored one the same way invite-accept reconciles a
// disclosure (Review rejects, Upgrade and Populate - a member with no name on
// file yet - write the disclosed name); the org's credential-freshness policy
// is enforced; then the identification is recorded (Store.CompleteReverification).
// It returns the name now on file. A rejection at any step is audited and
// returns without changing the membership.
func (s *Service) ApplyDisclosedIdentity(ctx context.Context, orgID, userID uuid.UUID, email string, storedName identity.Name, disclosed auth.DisclosedIdentity) (identity.Name, error) {
	if !strings.EqualFold(string(disclosed.Email), email) {
		_ = s.store.RecordReverifyRejected(ctx, orgID, userID, email, "email_mismatch")
		return identity.Name{}, ErrReverifyEmailMismatch
	}

	name := storedName
	var stored *identity.Name
	if storedName != (identity.Name{}) {
		stored = &storedName
	}
	switch identity.Reconcile(disclosed.Name, stored) {
	case identity.Review:
		_ = s.store.RecordReverifyRejected(ctx, orgID, userID, email, "name_mismatch")
		return identity.Name{}, ErrReverifyNameMismatch
	case identity.Upgrade, identity.Populate:
		name = disclosed.Name.Clean()
		if err := s.users.UpdateName(ctx, userID, name.GivenNames, name.LastName); err != nil {
			return identity.Name{}, err
		}
	case identity.Proceed:
	}

	settings, err := s.store.GetIdentitySettings(ctx, orgID)
	if err != nil {
		return identity.Name{}, err
	}
	if settings.CredentialMaxAgeDays != nil && !disclosed.CredentialIssuedAt.IsZero() {
		maxAge := time.Duration(*settings.CredentialMaxAgeDays) * 24 * time.Hour
		if time.Since(disclosed.CredentialIssuedAt) > maxAge {
			_ = s.store.RecordReverifyRejected(ctx, orgID, userID, email, "credential_too_old")
			return identity.Name{}, ErrCredentialTooOld
		}
	}

	if err := s.store.CompleteReverification(ctx, orgID, userID, disclosed.Name, disclosed.Phone, disclosed.DateOfBirth); err != nil {
		return identity.Name{}, err
	}
	return name, nil
}
