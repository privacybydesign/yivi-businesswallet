package organization

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/privacybydesign/yivi-businesswallet/backend/internal/auth"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/identity"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/user"
)

// reverifyStub is the store as the re-identification flow uses it: the embedded
// (nil) interface satisfies the rest of invitationStore at compile time, so the
// stub only spells out the five methods this flow actually calls.
type reverifyStub struct {
	invitationStore
	ctx       ReverifyContext
	lookupErr error
	settings  IdentitySettings

	completed        bool
	completedName    identity.Name
	completedPhone   string
	completedBirth   string
	rejectionReasons []string
}

func (s *reverifyStub) ReverifyTokenLookup(context.Context, string) (ReverifyContext, error) {
	return s.ctx, s.lookupErr
}

func (s *reverifyStub) GetIdentitySettings(context.Context, uuid.UUID) (IdentitySettings, error) {
	return s.settings, nil
}

func (s *reverifyStub) RecordReverifyRejected(_ context.Context, _, _ uuid.UUID, _, reason string) error {
	s.rejectionReasons = append(s.rejectionReasons, reason)
	return nil
}

func (s *reverifyStub) CompleteReverification(_ context.Context, _, _ uuid.UUID, disclosed identity.Name, phone, dateOfBirth string) error {
	s.completed = true
	s.completedName, s.completedPhone, s.completedBirth = disclosed, phone, dateOfBirth
	return nil
}

type disclosureStub struct {
	disclosed auth.DisclosedIdentity
	err       error
}

func (d disclosureStub) StartIdentitySession(context.Context) (auth.Session, error) {
	return auth.Session{ID: "session-id"}, nil
}

func (d disclosureStub) DiscloseIdentity(context.Context, string) (auth.DisclosedIdentity, error) {
	return d.disclosed, d.err
}

type nameUpgradeStub struct {
	userStore
	upgradedTo identity.Name
}

func (u *nameUpgradeStub) UpdateName(_ context.Context, _ uuid.UUID, givenNames, lastName string) error {
	u.upgradedTo = identity.Name{GivenNames: givenNames, LastName: lastName}
	return nil
}

func storedMember() ReverifyContext {
	return ReverifyContext{
		OrganizationID:   uuid.New(),
		OrganizationName: "Acme BV",
		OrganizationSlug: "acme",
		UserID:           uuid.New(),
		Email:            "alice@example.test",
		StoredName:       identity.Name{GivenNames: "Alice", LastName: "Anderson"},
		MemberType:       MemberTypeEmployee,
	}
}

func daysAgo(days int) time.Time { return time.Now().AddDate(0, 0, -days) }

func TestCompleteReverificationHappyPath(t *testing.T) {
	store := &reverifyStub{ctx: storedMember()}
	svc := NewService(&nameUpgradeStub{}, store, disclosureStub{disclosed: auth.DisclosedIdentity{
		Email:       user.Email("alice@example.test"),
		Name:        identity.Name{GivenNames: "Alice", LastName: "Anderson"},
		Phone:       "+31612345678",
		DateOfBirth: "1990-01-02",
	}})

	outcome, err := svc.CompleteReverification(context.Background(), "token", "disclosure")
	if err != nil {
		t.Fatalf("CompleteReverification: %v", err)
	}
	if outcome.OrganizationSlug != "acme" || outcome.OrganizationName != "Acme BV" {
		t.Errorf("outcome = %+v, want the org that asked", outcome)
	}
	if !store.completed {
		t.Fatal("the membership was not marked re-identified")
	}
	if store.completedPhone != "+31612345678" || store.completedBirth != "1990-01-02" {
		t.Errorf("phone/dob passed through = %q/%q", store.completedPhone, store.completedBirth)
	}
	if len(store.rejectionReasons) != 0 {
		t.Errorf("rejections recorded on the happy path: %v", store.rejectionReasons)
	}
}

// The e-mail is what binds a re-identification link to a person: a valid link
// plus somebody else's wallet must not re-identify the member it was sent to.
func TestCompleteReverificationRejectsAnotherPersonsDisclosure(t *testing.T) {
	store := &reverifyStub{ctx: storedMember()}
	svc := NewService(&nameUpgradeStub{}, store, disclosureStub{disclosed: auth.DisclosedIdentity{
		Email: user.Email("bob@example.test"),
		Name:  identity.Name{GivenNames: "Bob", LastName: "Brown"},
	}})

	if _, err := svc.CompleteReverification(context.Background(), "token", "disclosure"); !errors.Is(err, ErrReverifyEmailMismatch) {
		t.Fatalf("CompleteReverification = %v, want ErrReverifyEmailMismatch", err)
	}
	if store.completed {
		t.Error("the membership was re-identified despite an e-mail mismatch")
	}
	if len(store.rejectionReasons) != 1 || store.rejectionReasons[0] != "email_mismatch" {
		t.Errorf("rejections = %v, want one email_mismatch", store.rejectionReasons)
	}
}

// A name that no longer matches the identity on file is the takeover /
// legal-name-change case: it is refused (and audited) rather than silently
// rewriting who the member is.
func TestCompleteReverificationRejectsAMismatchedName(t *testing.T) {
	store := &reverifyStub{ctx: storedMember()}
	svc := NewService(&nameUpgradeStub{}, store, disclosureStub{disclosed: auth.DisclosedIdentity{
		Email: user.Email("alice@example.test"),
		Name:  identity.Name{GivenNames: "Alice", LastName: "Different"},
	}})

	if _, err := svc.CompleteReverification(context.Background(), "token", "disclosure"); !errors.Is(err, ErrReverifyNameMismatch) {
		t.Fatalf("CompleteReverification = %v, want ErrReverifyNameMismatch", err)
	}
	if store.completed {
		t.Error("the membership was re-identified despite a name mismatch")
	}
	if len(store.rejectionReasons) != 1 || store.rejectionReasons[0] != "name_mismatch" {
		t.Errorf("rejections = %v, want one name_mismatch", store.rejectionReasons)
	}
}

// An id-card disclosure of the same name in a richer form (diacritics the
// passport could not carry) upgrades the stored profile, as on accept.
func TestCompleteReverificationUpgradesARicherName(t *testing.T) {
	ctx := storedMember()
	ctx.StoredName = identity.Name{GivenNames: "Jose", LastName: "Munoz"}
	store := &reverifyStub{ctx: ctx}
	users := &nameUpgradeStub{}
	svc := NewService(users, store, disclosureStub{disclosed: auth.DisclosedIdentity{
		Email: user.Email("alice@example.test"),
		Name:  identity.Name{GivenNames: "José", LastName: "Muñoz"},
	}})

	if _, err := svc.CompleteReverification(context.Background(), "token", "disclosure"); err != nil {
		t.Fatalf("CompleteReverification: %v", err)
	}
	if users.upgradedTo.GivenNames != "José" || users.upgradedTo.LastName != "Muñoz" {
		t.Errorf("upgraded name = %+v, want the accented form", users.upgradedTo)
	}
	if !store.completed {
		t.Error("the membership was not marked re-identified after a name upgrade")
	}
}

// Credential freshness (#240 §5): with a limit configured, a credential the
// wallet obtained longer ago than the limit is refused with its own reason, so
// the member is told to refresh it rather than being let through.
func TestCompleteReverificationEnforcesCredentialFreshness(t *testing.T) {
	maxAge := 30
	store := &reverifyStub{ctx: storedMember(), settings: IdentitySettings{
		Configured: true, CredentialMaxAgeDays: &maxAge, OverdueConsequence: OverdueConsequenceFlag,
	}}
	svc := NewService(&nameUpgradeStub{}, store, disclosureStub{disclosed: auth.DisclosedIdentity{
		Email:              user.Email("alice@example.test"),
		Name:               identity.Name{GivenNames: "Alice", LastName: "Anderson"},
		CredentialIssuedAt: daysAgo(200),
	}})

	if _, err := svc.CompleteReverification(context.Background(), "token", "disclosure"); !errors.Is(err, ErrCredentialTooOld) {
		t.Fatalf("CompleteReverification = %v, want ErrCredentialTooOld", err)
	}
	if store.completed {
		t.Error("a stale credential was accepted")
	}
	if len(store.rejectionReasons) != 1 || store.rejectionReasons[0] != "credential_too_old" {
		t.Errorf("rejections = %v, want one credential_too_old", store.rejectionReasons)
	}
}

func TestCompleteReverificationAcceptsAFreshEnoughCredential(t *testing.T) {
	maxAge := 30
	store := &reverifyStub{ctx: storedMember(), settings: IdentitySettings{
		Configured: true, CredentialMaxAgeDays: &maxAge, OverdueConsequence: OverdueConsequenceFlag,
	}}
	svc := NewService(&nameUpgradeStub{}, store, disclosureStub{disclosed: auth.DisclosedIdentity{
		Email:              user.Email("alice@example.test"),
		Name:               identity.Name{GivenNames: "Alice", LastName: "Anderson"},
		CredentialIssuedAt: daysAgo(3),
	}})

	if _, err := svc.CompleteReverification(context.Background(), "token", "disclosure"); err != nil {
		t.Fatalf("CompleteReverification: %v", err)
	}
	if !store.completed {
		t.Error("a fresh credential was not accepted")
	}
}

// With freshness off (the default), an old credential is fine: the age limit is
// an opt-in policy, not a hidden default.
func TestCompleteReverificationIgnoresAgeWithoutAPolicy(t *testing.T) {
	store := &reverifyStub{ctx: storedMember()}
	svc := NewService(&nameUpgradeStub{}, store, disclosureStub{disclosed: auth.DisclosedIdentity{
		Email:              user.Email("alice@example.test"),
		Name:               identity.Name{GivenNames: "Alice", LastName: "Anderson"},
		CredentialIssuedAt: daysAgo(900),
	}})

	if _, err := svc.CompleteReverification(context.Background(), "token", "disclosure"); err != nil {
		t.Fatalf("CompleteReverification: %v", err)
	}
	if !store.completed {
		t.Error("an old credential was refused even though no freshness policy is set")
	}
}

func TestCompleteReverificationMapsAFailedDisclosure(t *testing.T) {
	store := &reverifyStub{ctx: storedMember()}
	svc := NewService(&nameUpgradeStub{}, store, disclosureStub{err: errors.New("session not finished")})

	if _, err := svc.CompleteReverification(context.Background(), "token", "disclosure"); !errors.Is(err, ErrDisclosureFailed) {
		t.Fatalf("CompleteReverification = %v, want ErrDisclosureFailed", err)
	}
}

func TestStartReverifySessionRefusesAnUnknownToken(t *testing.T) {
	store := &reverifyStub{lookupErr: ErrReverifyTokenNotFound}
	svc := NewService(&nameUpgradeStub{}, store, disclosureStub{})

	if _, err := svc.StartReverifySession(context.Background(), "nope"); !errors.Is(err, ErrReverifyTokenNotFound) {
		t.Errorf("StartReverifySession = %v, want ErrReverifyTokenNotFound", err)
	}
}
