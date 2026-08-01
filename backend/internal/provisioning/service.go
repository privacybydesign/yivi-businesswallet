package provisioning

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/privacybydesign/yivi-businesswallet/backend/internal/organization"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/provisioner"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/user"
)

// recordRunTimeout bounds the write of a run's outcome. It is a single row on a
// context of its own, so it needs no more than this; the point of the bound is
// that a detached context cannot be cancelled by the sync that just ended.
const recordRunTimeout = 5 * time.Second

// settingsStore is the configuration and ownership side of the sync, implemented
// by *Store.
type settingsStore interface {
	SourceConfig(ctx context.Context, orgID uuid.UUID) (provisioner.SourceID, provisioner.Config, error)
	RecordRun(ctx context.Context, orgID uuid.UUID, source provisioner.SourceID, result Result, runErr error) error
	MemberLinks(ctx context.Context, orgID uuid.UUID, source provisioner.SourceID) (map[string]Link, error)
	LinkMember(ctx context.Context, orgID uuid.UUID, source provisioner.SourceID, externalID, email string) error
	UnlinkMember(ctx context.Context, orgID uuid.UUID, source provisioner.SourceID, externalID string) error
	DepartmentLinks(ctx context.Context, orgID uuid.UUID, source provisioner.SourceID) (map[string]uuid.UUID, error)
	LinkDepartment(ctx context.Context, orgID uuid.UUID, source provisioner.SourceID, externalID string, departmentID uuid.UUID) error
}

// memberDirectory is the slice of the organisation store the reconciler drives.
// Every mutation goes through it rather than through SQL of our own, so a
// provisioned change is audited exactly like the same change made by hand.
type memberDirectory interface {
	ListDepartments(ctx context.Context, orgID uuid.UUID) ([]organization.Department, error)
	CreateDepartment(ctx context.Context, orgID uuid.UUID, name string) (organization.Department, error)
	MemberEntryByEmail(ctx context.Context, orgID uuid.UUID, email string) (organization.MemberEntry, error)
	CreateInvitation(ctx context.Context, in organization.Invitation) (organization.Invitation, error)
	UpdateInvitation(ctx context.Context, orgID, invitationID uuid.UUID, role string, jobTitle *string, departmentID *uuid.UUID) error
	RevokeInvitation(ctx context.Context, orgID, invitationID uuid.UUID) error
	UpdateMembership(ctx context.Context, orgID, userID uuid.UUID, role *string, jobTitle *string, departmentID *uuid.UUID) (organization.Member, error)
	RemoveMembership(ctx context.Context, orgID, userID uuid.UUID) error
}

// orgReader resolves the organisation a sync runs for, for the invitation
// e-mail's "you were invited to X".
type orgReader interface {
	GetByID(ctx context.Context, id uuid.UUID) (organization.Organization, error)
}

// inviteMailer is the invitation e-mail seam, the same one the manual invite
// flow uses. It is optional: a deployment without it still provisions, the
// invited person just has to find the invitation in the app.
type inviteMailer interface {
	SendInvitation(ctx context.Context, orgID uuid.UUID, to, orgName, acceptURL string) error
}

// Service reconciles a directory snapshot into an organisation's departments and
// memberships.
type Service struct {
	settings   settingsStore
	orgs       orgReader
	members    memberDirectory
	mailer     inviteMailer
	appBaseURL string
	sources    map[provisioner.SourceID]provisioner.Provisioner
}

func NewService(settings settingsStore, orgs orgReader, members memberDirectory, mailer inviteMailer, appBaseURL string) *Service {
	return &Service{
		settings:   settings,
		orgs:       orgs,
		members:    members,
		mailer:     mailer,
		appBaseURL: appBaseURL,
		sources:    map[provisioner.SourceID]provisioner.Provisioner{},
	}
}

// Register adds a source driver. Registering the same id twice replaces the
// earlier one; call it before the first sync.
func (s *Service) Register(p provisioner.Provisioner) { s.sources[p.ID()] = p }

// Sync fetches the organisation's directory and reconciles it. The outcome —
// success or failure — is recorded on the settings row and in the audit log, so
// a scheduled run that nobody watched is still answerable afterwards.
func (s *Service) Sync(ctx context.Context, orgID uuid.UUID) (Result, error) {
	source, cfg, err := s.settings.SourceConfig(ctx, orgID)
	if err != nil {
		// Not configured or switched off is not a run: there is no settings row to
		// record against, and switching provisioning off should not keep writing
		// failures to the audit log.
		return Result{}, err
	}

	result, runErr := s.run(ctx, orgID, source, cfg)
	if err := s.recordRun(ctx, orgID, source, result, runErr); err != nil {
		slog.ErrorContext(ctx, "provisioning: recording run outcome",
			slog.String("organizationId", orgID.String()), slog.String("error", err.Error()))
	}
	return result, runErr
}

// recordRun writes the outcome on a context detached from the sync's own. A run
// that ran out of time is the one an admin most needs to see, and on the sync's
// context the deadline that stopped the run would stop the write that says so —
// leaving the settings row showing the previous run's status for good.
func (s *Service) recordRun(ctx context.Context, orgID uuid.UUID, source provisioner.SourceID, result Result, runErr error) error {
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), recordRunTimeout)
	defer cancel()
	return s.settings.RecordRun(ctx, orgID, source, result, runErr)
}

func (s *Service) run(ctx context.Context, orgID uuid.UUID, source provisioner.SourceID, cfg provisioner.Config) (Result, error) {
	driver, ok := s.sources[source]
	if !ok {
		return Result{}, fmt.Errorf("%w %q", ErrUnknownSource, source)
	}

	directory, err := fetchDirectory(ctx, driver, cfg)
	if err != nil {
		// Whatever went wrong here is the source's side of the seam, which is what
		// separates it from a fault of ours for the caller and for lastRunError.
		return Result{}, &SourceError{Err: err}
	}
	// An expired secret, a mistyped group id and a revoked permission can all come
	// back as a successful, empty read. Obeying it would deprovision the whole
	// organisation in one pass, so an empty directory is refused instead.
	if len(directory.People) == 0 {
		return Result{}, ErrEmptyDirectory
	}

	return s.reconcile(ctx, orgID, source, directory)
}

// fetchDirectory runs the driver's Fetch and stops waiting for it once ctx is
// done.
//
// The deadline around a sync cannot be left to the driver. context.WithTimeout
// only arms a timer; nothing in the runtime interrupts a running callee, and
// Provisioner is an interface written for other sources to implement — one that
// blocks on a call ignoring the context would hold this goroutine forever, and
// with it the rest of the scheduler's pass (organisations are synced one after
// another) and the ticker loop that started the pass. Waiting on a channel
// instead costs one leaked goroutine per wedged driver, which is bounded by how
// many of them wedge.
func fetchDirectory(ctx context.Context, driver provisioner.Provisioner, cfg provisioner.Config) (provisioner.Directory, error) {
	type fetched struct {
		directory provisioner.Directory
		err       error
	}
	// Buffered, so the goroutine can finish after we have stopped listening.
	done := make(chan fetched, 1)
	go func() {
		// The recover has to be here rather than in the caller: a deferred recover on
		// this side of the call no longer sees a panic raised on another stack, and a
		// panicking goroutine with nobody to catch it takes the process down.
		defer func() {
			if r := recover(); r != nil {
				done <- fetched{err: fmt.Errorf("provisioning: source driver panicked: %v", r)}
			}
		}()
		directory, err := driver.Fetch(ctx, cfg)
		done <- fetched{directory: directory, err: err}
	}()

	select {
	case f := <-done:
		return f.directory, f.err
	case <-ctx.Done():
		return provisioner.Directory{}, ctx.Err()
	}
}

func (s *Service) reconcile(ctx context.Context, orgID uuid.UUID, source provisioner.SourceID, directory provisioner.Directory) (Result, error) {
	// Empty rather than nil, so a clean run answers "skipped": [] instead of null.
	result := Result{Skipped: []Skip{}}

	departments, err := s.syncDepartments(ctx, orgID, source, directory, &result)
	if err != nil {
		return result, err
	}

	links, err := s.settings.MemberLinks(ctx, orgID, source)
	if err != nil {
		return result, err
	}

	seen := make(map[string]bool, len(directory.People))
	for _, person := range directory.People {
		if person.ExternalID != "" {
			seen[person.ExternalID] = true
		}
	}

	// Everyone we own who was not in the snapshot has left the source. They are
	// handled before anybody is invited, because a person can be replaced in the
	// source by a new account on the same mailbox and only one link may hold an
	// address: the departed account has to let go of it first. Sorted so a run over
	// the same directory does the same thing in the same order.
	gone := make([]string, 0, len(links))
	for externalID := range links {
		if !seen[externalID] {
			gone = append(gone, externalID)
		}
	}
	sort.Strings(gone)
	for _, externalID := range gone {
		unlinked, err := s.deprovision(ctx, orgID, source, links[externalID], &result)
		if err != nil {
			return result, err
		}
		if unlinked {
			delete(links, externalID)
		}
	}

	// Which source account holds each address we own. provisioned_members allows
	// one link per address per source, so an invitation for an address another link
	// still holds could not be recorded — and by the time the write failed the
	// invitation would already exist, with nothing owning it. It is reported
	// instead, and clears itself once the holding account is gone from the source.
	claimed := make(map[string]string, len(links))
	for externalID, link := range links {
		claimed[strings.ToLower(link.Email)] = externalID
	}

	for _, person := range directory.People {
		if person.ExternalID == "" {
			continue
		}
		if err := s.syncPerson(ctx, orgID, source, person, departments, links, claimed, &result); err != nil {
			return result, err
		}
	}

	return result, nil
}

// syncDepartments mirrors the departments the snapshot's people belong to and
// returns the department id per source department.
//
// Departments are not renamed or deleted here. The source's department is a name
// with no stable identifier behind it (see .ai/features/provisioning.md), so a
// rename in the source is indistinguishable from a new department — and deleting
// one we no longer see would take its manually-added members with it, which the
// store refuses anyway (ErrDepartmentInUse).
func (s *Service) syncDepartments(ctx context.Context, orgID uuid.UUID, source provisioner.SourceID, directory provisioner.Directory, result *Result) (map[string]uuid.UUID, error) {
	wanted := []string{}
	seen := map[string]bool{}
	for _, person := range directory.People {
		// Only the people this run can act on. Departments are never deleted here, so
		// mirroring the department of somebody the run is about to deprovision, or
		// cannot invite at all, would leave an empty department in the organisation's
		// list for good. A person skipped as conflict or removed_locally is not
		// knowable until the membership lookup, and still mirrors theirs.
		if !person.Active {
			continue
		}
		if _, _, _, ok := invitable(person); !ok {
			continue
		}
		name := strings.TrimSpace(person.Department)
		if name == "" || seen[departmentKey(name)] {
			continue
		}
		seen[departmentKey(name)] = true
		wanted = append(wanted, name)
	}
	if len(wanted) == 0 {
		return map[string]uuid.UUID{}, nil
	}

	linked, err := s.settings.DepartmentLinks(ctx, orgID, source)
	if err != nil {
		return nil, err
	}
	existing, err := s.members.ListDepartments(ctx, orgID)
	if err != nil {
		return nil, err
	}
	byName := make(map[string]uuid.UUID, len(existing))
	for _, d := range existing {
		byName[departmentKey(d.Name)] = d.ID
	}

	out := make(map[string]uuid.UUID, len(wanted))
	for _, name := range wanted {
		key := departmentKey(name)
		// A link survives a local rename, so it wins over the name match: the admin
		// renamed our department, they did not ask for a second one.
		if id, ok := linked[key]; ok {
			out[key] = id
			continue
		}
		// A department of that name already exists but is not ours — adopt it rather
		// than fail on the unique name. Adopting only records the link; the
		// department itself is left exactly as it was.
		if id, ok := byName[key]; ok {
			if err := s.settings.LinkDepartment(ctx, orgID, source, key, id); err != nil {
				return nil, err
			}
			out[key] = id
			continue
		}
		created, err := s.members.CreateDepartment(ctx, orgID, name)
		if err != nil {
			return nil, err
		}
		if err := s.settings.LinkDepartment(ctx, orgID, source, key, created.ID); err != nil {
			return nil, err
		}
		out[key] = created.ID
		result.DepartmentsCreated++
	}
	return out, nil
}

func (s *Service) syncPerson(
	ctx context.Context,
	orgID uuid.UUID,
	source provisioner.SourceID,
	person provisioner.Person,
	departments map[string]uuid.UUID,
	links map[string]Link,
	claimed map[string]string,
	result *Result,
) error {
	link, owned := links[person.ExternalID]

	// A disabled account is a decision somebody made about this person, so it
	// deprovisions exactly like disappearing from the source does.
	if !person.Active {
		if !owned {
			return nil
		}
		_, err := s.deprovision(ctx, orgID, source, link, result)
		return err
	}

	email, givenNames, lastName, ok := invitable(person)
	if !ok {
		result.Skipped = append(result.Skipped, Skip{Email: person.Email, Reason: SkipIncomplete})
		return nil
	}

	role := organization.RoleMember
	if person.Admin {
		role = organization.RoleAdmin
	}
	jobTitle := optional(person.JobTitle)
	var departmentID *uuid.UUID
	if id, ok := departments[departmentKey(person.Department)]; ok {
		departmentID = &id
	}

	// Look the person up by the address we provisioned them under, not by the one
	// the source reports today: the invitation and the membership are keyed by
	// e-mail, and the wallet disclosure on accept is matched against it. An address
	// change in the source is therefore not followed — see the feature doc.
	lookup := string(email)
	if owned {
		lookup = link.Email
	}
	entry, err := s.members.MemberEntryByEmail(ctx, orgID, lookup)
	switch {
	case errors.Is(err, organization.ErrNotMember):
		if owned {
			// We own this person but their invitation or membership is gone — an admin
			// revoked it, or they declined. Re-inviting would restart that argument on
			// every run and mail them each time, so the run reports it and leaves it
			// alone. Removing them from the source is what stops it.
			result.Skipped = append(result.Skipped, Skip{Email: link.Email, Reason: SkipRemovedLocally})
			return nil
		}
		if holder, taken := claimed[string(email)]; taken && holder != person.ExternalID {
			// Another source account still owns this address here, with nothing behind
			// it any more — the usual way there is a person who declined their
			// invitation and was later replaced in the source. Inviting them would
			// create an invitation the link table cannot record.
			result.Skipped = append(result.Skipped, Skip{Email: string(email), Reason: SkipConflict})
			return nil
		}
		return s.invite(ctx, orgID, source, person.ExternalID, email, givenNames, lastName, role, jobTitle, departmentID, result)
	case err != nil:
		return err
	}

	if !owned {
		// The address already belongs to a membership or invitation this sync did not
		// create. Taking it over would let a directory change somebody's role in an
		// organisation they were admitted to by hand, so it is reported instead.
		result.Skipped = append(result.Skipped, Skip{Email: string(email), Reason: SkipConflict})
		return nil
	}

	if sameAttributes(entry, role, jobTitle, departmentID) {
		return nil
	}
	return s.update(ctx, orgID, entry, role, jobTitle, departmentID, result)
}

func (s *Service) invite(
	ctx context.Context,
	orgID uuid.UUID,
	source provisioner.SourceID,
	externalID string,
	email user.Email,
	givenNames, lastName, role string,
	jobTitle *string,
	departmentID *uuid.UUID,
	result *Result,
) error {
	// InvitedBy is left nil: nobody typed this invitation, the directory did. The
	// audit event's actor is empty for the same reason on a scheduled run.
	invitation, err := s.members.CreateInvitation(ctx, organization.Invitation{
		OrganizationID: orgID,
		Email:          string(email),
		Role:           role,
		JobTitle:       jobTitle,
		DepartmentID:   departmentID,
		GivenNames:     givenNames,
		LastName:       lastName,
	})
	if errors.Is(err, organization.ErrAlreadyInvited) {
		// Raced with a hand-made invitation between the lookup and the insert.
		result.Skipped = append(result.Skipped, Skip{Email: string(email), Reason: SkipConflict})
		return nil
	}
	if err != nil {
		return err
	}
	if err := s.settings.LinkMember(ctx, orgID, source, externalID, string(email)); err != nil {
		return err
	}
	result.MembersInvited++
	s.sendInvitation(ctx, orgID, invitation)
	return nil
}

func (s *Service) update(
	ctx context.Context,
	orgID uuid.UUID,
	entry organization.MemberEntry,
	role string,
	jobTitle *string,
	departmentID *uuid.UUID,
	result *Result,
) error {
	switch entry.Status {
	case organization.StatusActive:
		_, err := s.members.UpdateMembership(ctx, orgID, *entry.UserID, &role, jobTitle, departmentID)
		if errors.Is(err, organization.ErrLastAdmin) {
			// Demoting the organisation's last admin is refused, and rightly so: a
			// directory should not be able to lock everyone out of the wallet.
			result.Skipped = append(result.Skipped, Skip{Email: entry.Email, Reason: SkipLastAdmin})
			return nil
		}
		if err != nil {
			return err
		}
	case organization.StatusInvited:
		if err := s.members.UpdateInvitation(ctx, orgID, *entry.InvitationID, role, jobTitle, departmentID); err != nil {
			return err
		}
	default:
		return fmt.Errorf("provisioning: unexpected member status %q", entry.Status)
	}
	result.MembersUpdated++
	return nil
}

// deprovision takes away what the sync gave a person: a pending invitation is
// revoked, an accepted membership is removed. It reports whether the ownership
// link went with it — it stays when the removal was refused, so the person is
// still ours and the next run tries again.
func (s *Service) deprovision(ctx context.Context, orgID uuid.UUID, source provisioner.SourceID, link Link, result *Result) (bool, error) {
	entry, err := s.members.MemberEntryByEmail(ctx, orgID, link.Email)
	switch {
	case errors.Is(err, organization.ErrNotMember):
		// Already gone; drop the ownership record and say nothing.
		if err := s.settings.UnlinkMember(ctx, orgID, source, link.ExternalID); err != nil {
			return false, err
		}
		return true, nil
	case err != nil:
		return false, err
	}

	switch entry.Status {
	case organization.StatusActive:
		err = s.members.RemoveMembership(ctx, orgID, *entry.UserID)
		if errors.Is(err, organization.ErrLastAdmin) {
			// Keep the link: the person is still ours, they just cannot be the one
			// removal that leaves the organisation without an administrator. The run
			// reports it every pass until an admin resolves it.
			result.Skipped = append(result.Skipped, Skip{Email: link.Email, Reason: SkipLastAdmin})
			return false, nil
		}
	case organization.StatusInvited:
		err = s.members.RevokeInvitation(ctx, orgID, *entry.InvitationID)
	default:
		return false, fmt.Errorf("provisioning: unexpected member status %q", entry.Status)
	}
	if err != nil {
		return false, err
	}

	if err := s.settings.UnlinkMember(ctx, orgID, source, link.ExternalID); err != nil {
		return false, err
	}
	result.MembersRemoved++
	return true, nil
}

// sendInvitation delivers the invitation e-mail best-effort, like the manual
// invite flow: the invitation is already persisted and discoverable in-app, so a
// delivery failure (including an organisation with no SMTP configured) is
// logged, never fatal to the run.
func (s *Service) sendInvitation(ctx context.Context, orgID uuid.UUID, invitation organization.Invitation) {
	if s.mailer == nil {
		return
	}
	org, err := s.orgs.GetByID(ctx, orgID)
	if err != nil {
		slog.WarnContext(ctx, "provisioning: invitation e-mail not sent",
			slog.String("organizationId", orgID.String()), slog.String("error", err.Error()))
		return
	}
	acceptURL := s.appBaseURL + "/invite/" + invitation.Token
	if err := s.mailer.SendInvitation(ctx, orgID, invitation.Email, org.Name, acceptURL); err != nil {
		slog.WarnContext(ctx, "provisioning: invitation e-mail not sent",
			slog.String("email", invitation.Email), slog.String("error", err.Error()))
	}
}

// invitable reports whether a source record can become one of our invitations,
// and returns the fields it would carry. The invitation needs an address plus a
// given and a family name, because the person's wallet disclosure is matched
// against them on accept — a record missing any of the three cannot become one.
func invitable(person provisioner.Person) (email user.Email, givenNames, lastName string, ok bool) {
	email, err := user.ParseEmail(person.Email)
	givenNames = strings.TrimSpace(person.GivenNames)
	lastName = strings.TrimSpace(person.LastName)
	return email, givenNames, lastName, err == nil && givenNames != "" && lastName != ""
}

// sameAttributes reports whether a member entry already says what the source
// says, so an unchanged person costs no write and no audit event.
func sameAttributes(entry organization.MemberEntry, role string, jobTitle *string, departmentID *uuid.UUID) bool {
	if entry.Role != role {
		return false
	}
	if !equalStringPtr(entry.JobTitle, jobTitle) {
		return false
	}
	switch {
	case entry.DepartmentID == nil && departmentID == nil:
		return true
	case entry.DepartmentID == nil || departmentID == nil:
		return false
	default:
		return *entry.DepartmentID == *departmentID
	}
}

func equalStringPtr(a, b *string) bool {
	switch {
	case a == nil && b == nil:
		return true
	case a == nil || b == nil:
		return false
	default:
		return *a == *b
	}
}

// departmentKey folds a department name for matching. The source names a
// department, it does not identify one, so "Legal" and "legal" have to be the
// same department or every capitalisation change would create another.
func departmentKey(name string) string {
	return strings.ToLower(strings.TrimSpace(name))
}

// optional turns a source string into the nullable column our model stores: a
// blank job title is "not set", not the empty string.
func optional(value string) *string {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil
	}
	return &value
}
