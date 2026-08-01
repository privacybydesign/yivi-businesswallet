package provisioning

import (
	"context"
	"errors"
	"maps"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/privacybydesign/yivi-businesswallet/backend/internal/organization"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/provisioner"
)

// fakeSource is a Provisioner returning a fixed snapshot, so the reconciler can
// be driven without an HTTP stub.
type fakeSource struct {
	directory provisioner.Directory
	err       error
	calls     int
	lastCfg   provisioner.Config
}

func (f *fakeSource) ID() provisioner.SourceID { return provisioner.SourceEntra }

func (f *fakeSource) Fetch(_ context.Context, cfg provisioner.Config) (provisioner.Directory, error) {
	f.calls++
	f.lastCfg = cfg
	return f.directory, f.err
}

// fakeSettings is the configuration and ownership side, in memory.
type fakeSettings struct {
	source      provisioner.SourceID
	cfg         provisioner.Config
	configErr   error
	memberLinks map[string]Link
	deptLinks   map[string]uuid.UUID

	runs []recordedRun
}

type recordedRun struct {
	result Result
	err    error
}

func newFakeSettings() *fakeSettings {
	return &fakeSettings{
		source:      provisioner.SourceEntra,
		cfg:         provisioner.Config{TenantID: "t", ClientID: "c", ClientSecret: "s"},
		memberLinks: map[string]Link{},
		deptLinks:   map[string]uuid.UUID{},
	}
}

func (f *fakeSettings) SourceConfig(context.Context, uuid.UUID) (provisioner.SourceID, provisioner.Config, error) {
	if f.configErr != nil {
		return "", provisioner.Config{}, f.configErr
	}
	return f.source, f.cfg, nil
}

func (f *fakeSettings) RecordRun(_ context.Context, _ uuid.UUID, _ provisioner.SourceID, result Result, runErr error) error {
	f.runs = append(f.runs, recordedRun{result: result, err: runErr})
	return nil
}

func (f *fakeSettings) MemberLinks(context.Context, uuid.UUID, provisioner.SourceID) (map[string]Link, error) {
	out := map[string]Link{}
	maps.Copy(out, f.memberLinks)
	return out, nil
}

func (f *fakeSettings) LinkMember(_ context.Context, _ uuid.UUID, _ provisioner.SourceID, externalID, email string) error {
	f.memberLinks[externalID] = Link{ExternalID: externalID, Email: email}
	return nil
}

func (f *fakeSettings) UnlinkMember(_ context.Context, _ uuid.UUID, _ provisioner.SourceID, externalID string) error {
	delete(f.memberLinks, externalID)
	return nil
}

func (f *fakeSettings) DepartmentLinks(context.Context, uuid.UUID, provisioner.SourceID) (map[string]uuid.UUID, error) {
	out := map[string]uuid.UUID{}
	maps.Copy(out, f.deptLinks)
	return out, nil
}

func (f *fakeSettings) LinkDepartment(_ context.Context, _ uuid.UUID, _ provisioner.SourceID, externalID string, deptID uuid.UUID) error {
	f.deptLinks[externalID] = deptID
	return nil
}

// fakeMembers is the organisation store the reconciler drives, in memory. It
// keeps the two shapes a member can have — a pending invitation and an accepted
// membership — because the whole point of the reconciler is that it handles both.
type fakeMembers struct {
	departments []organization.Department
	entries     map[string]organization.MemberEntry // keyed by lowercased e-mail
	admins      int                                 // active admins, for the last-admin rule
	updateErr   error
}

func newFakeMembers() *fakeMembers {
	return &fakeMembers{entries: map[string]organization.MemberEntry{}}
}

func (f *fakeMembers) ListDepartments(context.Context, uuid.UUID) ([]organization.Department, error) {
	return f.departments, nil
}

func (f *fakeMembers) CreateDepartment(_ context.Context, orgID uuid.UUID, name string) (organization.Department, error) {
	for _, d := range f.departments {
		if strings.EqualFold(d.Name, name) {
			return organization.Department{}, organization.ErrDepartmentNameTaken
		}
	}
	d := organization.Department{ID: uuid.New(), OrganizationID: orgID, Name: name}
	f.departments = append(f.departments, d)
	return d, nil
}

func (f *fakeMembers) MemberEntryByEmail(_ context.Context, _ uuid.UUID, email string) (organization.MemberEntry, error) {
	entry, ok := f.entries[strings.ToLower(email)]
	if !ok {
		return organization.MemberEntry{}, organization.ErrNotMember
	}
	return entry, nil
}

func (f *fakeMembers) CreateInvitation(_ context.Context, in organization.Invitation) (organization.Invitation, error) {
	key := strings.ToLower(in.Email)
	if _, ok := f.entries[key]; ok {
		return organization.Invitation{}, organization.ErrAlreadyInvited
	}
	in.ID = uuid.New()
	in.Token = "token-" + in.ID.String()
	invitationID := in.ID
	f.entries[key] = organization.MemberEntry{
		Status:       organization.StatusInvited,
		InvitationID: &invitationID,
		Email:        in.Email,
		GivenNames:   in.GivenNames,
		LastName:     in.LastName,
		Role:         in.Role,
		JobTitle:     in.JobTitle,
		DepartmentID: in.DepartmentID,
	}
	return in, nil
}

func (f *fakeMembers) UpdateInvitation(_ context.Context, _, invitationID uuid.UUID, role string, jobTitle *string, departmentID *uuid.UUID) error {
	for key, entry := range f.entries {
		if entry.InvitationID != nil && *entry.InvitationID == invitationID {
			entry.Role, entry.JobTitle, entry.DepartmentID = role, jobTitle, departmentID
			f.entries[key] = entry
			return nil
		}
	}
	return organization.ErrInvitationNotFound
}

func (f *fakeMembers) RevokeInvitation(_ context.Context, _, invitationID uuid.UUID) error {
	for key, entry := range f.entries {
		if entry.InvitationID != nil && *entry.InvitationID == invitationID {
			delete(f.entries, key)
			return nil
		}
	}
	return organization.ErrInvitationNotFound
}

func (f *fakeMembers) UpdateMembership(_ context.Context, _, userID uuid.UUID, role, jobTitle *string, departmentID *uuid.UUID) (organization.Member, error) {
	if f.updateErr != nil {
		return organization.Member{}, f.updateErr
	}
	for key, entry := range f.entries {
		if entry.UserID == nil || *entry.UserID != userID {
			continue
		}
		if role != nil && *role == organization.RoleMember && entry.Role == organization.RoleAdmin && f.admins <= 1 {
			return organization.Member{}, organization.ErrLastAdmin
		}
		if role != nil {
			if entry.Role == organization.RoleAdmin && *role != organization.RoleAdmin {
				f.admins--
			}
			if entry.Role != organization.RoleAdmin && *role == organization.RoleAdmin {
				f.admins++
			}
			entry.Role = *role
		}
		entry.JobTitle, entry.DepartmentID = jobTitle, departmentID
		f.entries[key] = entry
		return organization.Member{}, nil
	}
	return organization.Member{}, organization.ErrNotMember
}

func (f *fakeMembers) RemoveMembership(_ context.Context, _, userID uuid.UUID) error {
	for key, entry := range f.entries {
		if entry.UserID == nil || *entry.UserID != userID {
			continue
		}
		if entry.Role == organization.RoleAdmin && f.admins <= 1 {
			return organization.ErrLastAdmin
		}
		if entry.Role == organization.RoleAdmin {
			f.admins--
		}
		delete(f.entries, key)
		return nil
	}
	return organization.ErrNotMember
}

type fakeOrgs struct{}

func (fakeOrgs) GetByID(_ context.Context, id uuid.UUID) (organization.Organization, error) {
	return organization.Organization{ID: id, Name: "Acme", Slug: "acme"}, nil
}

// harness wires a Service over the in-memory doubles.
type harness struct {
	orgID    uuid.UUID
	settings *fakeSettings
	members  *fakeMembers
	source   *fakeSource
	service  *Service
}

func newHarness(t *testing.T, people ...provisioner.Person) *harness {
	t.Helper()
	h := &harness{
		orgID:    uuid.New(),
		settings: newFakeSettings(),
		members:  newFakeMembers(),
		source:   &fakeSource{directory: provisioner.Directory{People: people}},
	}
	// No mailer: the invitation e-mail is best-effort and orthogonal to what the
	// reconciler decides.
	h.service = NewService(h.settings, fakeOrgs{}, h.members, nil, "https://wallet.example")
	h.service.Register(h.source)
	return h
}

func (h *harness) sync(t *testing.T) Result {
	t.Helper()
	result, err := h.service.Sync(context.Background(), h.orgID)
	if err != nil {
		t.Fatalf("Sync: %v", err)
	}
	return result
}

func (h *harness) entry(t *testing.T, email string) organization.MemberEntry {
	t.Helper()
	entry, ok := h.members.entries[strings.ToLower(email)]
	if !ok {
		t.Fatalf("no member entry for %s", email)
	}
	return entry
}

func person(id, email, given, last string) provisioner.Person {
	return provisioner.Person{ExternalID: id, Email: email, GivenNames: given, LastName: last, Active: true}
}

func TestSyncProvisionsDepartmentsAndPeople(t *testing.T) {
	ada := person("u1", "ada@example.org", "Ada", "Lovelace")
	ada.Department = "Research"
	ada.JobTitle = "Engineer"
	ada.Admin = true
	bob := person("u2", "bob@example.org", "Bob", "Baker")
	bob.Department = "research" // same department, different capitalisation
	h := newHarness(t, ada, bob)

	result := h.sync(t)

	if result.DepartmentsCreated != 1 {
		t.Errorf("departmentsCreated = %d, want 1 (the two spellings are one department)", result.DepartmentsCreated)
	}
	if result.MembersInvited != 2 {
		t.Errorf("membersInvited = %d, want 2", result.MembersInvited)
	}

	adaEntry := h.entry(t, "ada@example.org")
	// Provisioning creates the membership shell, not the person: they still prove
	// who they are with their wallet before the membership exists.
	if adaEntry.Status != organization.StatusInvited {
		t.Errorf("status = %q, want an invitation", adaEntry.Status)
	}
	if adaEntry.Role != organization.RoleAdmin {
		t.Errorf("role = %q, want admin (she is in an admin group)", adaEntry.Role)
	}
	if adaEntry.JobTitle == nil || *adaEntry.JobTitle != "Engineer" {
		t.Errorf("jobTitle = %v, want Engineer", adaEntry.JobTitle)
	}
	bobEntry := h.entry(t, "bob@example.org")
	if bobEntry.Role != organization.RoleMember {
		t.Errorf("role = %q, want member by default", bobEntry.Role)
	}
	if adaEntry.DepartmentID == nil || bobEntry.DepartmentID == nil || *adaEntry.DepartmentID != *bobEntry.DepartmentID {
		t.Error("both people should land in the same department")
	}
}

func TestSyncIsIdempotent(t *testing.T) {
	ada := person("u1", "ada@example.org", "Ada", "Lovelace")
	ada.Department = "Research"
	h := newHarness(t, ada)

	h.sync(t)
	second := h.sync(t)

	if second.DepartmentsCreated != 0 || second.MembersInvited != 0 ||
		second.MembersUpdated != 0 || second.MembersRemoved != 0 || len(second.Skipped) != 0 {
		t.Errorf("second run = %+v, want no changes at all", second)
	}
	if len(h.members.entries) != 1 {
		t.Errorf("entries = %d, want the one invitation", len(h.members.entries))
	}
	if len(h.members.departments) != 1 {
		t.Errorf("departments = %d, want no duplicate", len(h.members.departments))
	}
}

func TestSyncAppliesAttributeChangesToAPendingInvitation(t *testing.T) {
	ada := person("u1", "ada@example.org", "Ada", "Lovelace")
	h := newHarness(t, ada)
	h.sync(t)
	invitationID := *h.entry(t, "ada@example.org").InvitationID

	ada.Admin = true
	ada.Department = "Research"
	h.source.directory = provisioner.Directory{People: []provisioner.Person{ada}}
	result := h.sync(t)

	if result.MembersUpdated != 1 {
		t.Fatalf("membersUpdated = %d, want 1", result.MembersUpdated)
	}
	entry := h.entry(t, "ada@example.org")
	if entry.Role != organization.RoleAdmin || entry.DepartmentID == nil {
		t.Errorf("entry = %+v, want the promoted role and the new department", entry)
	}
	// The invitation is rewritten in place, not revoked and reissued: the person
	// may already have the link they were mailed.
	if *entry.InvitationID != invitationID {
		t.Error("the invitation was replaced; the accept link the person was sent is now dead")
	}
}

func TestSyncAppliesAttributeChangesToAnAcceptedMembership(t *testing.T) {
	ada := person("u1", "ada@example.org", "Ada", "Lovelace")
	h := newHarness(t, ada)
	h.sync(t)
	accept(h, "ada@example.org")

	ada.JobTitle = "Engineer"
	h.source.directory = provisioner.Directory{People: []provisioner.Person{ada}}
	result := h.sync(t)

	if result.MembersUpdated != 1 {
		t.Fatalf("membersUpdated = %d, want 1", result.MembersUpdated)
	}
	entry := h.entry(t, "ada@example.org")
	if entry.JobTitle == nil || *entry.JobTitle != "Engineer" {
		t.Errorf("jobTitle = %v, want Engineer", entry.JobTitle)
	}
}

func TestSyncDeprovisionsSomeoneRemovedFromTheSource(t *testing.T) {
	ada := person("u1", "ada@example.org", "Ada", "Lovelace")
	bob := person("u2", "bob@example.org", "Bob", "Baker")
	h := newHarness(t, ada, bob)
	h.sync(t)
	accept(h, "bob@example.org")

	h.source.directory = provisioner.Directory{People: []provisioner.Person{ada}}
	result := h.sync(t)

	if result.MembersRemoved != 1 {
		t.Fatalf("membersRemoved = %d, want 1", result.MembersRemoved)
	}
	if _, ok := h.members.entries["bob@example.org"]; ok {
		t.Error("bob left the directory but keeps his membership")
	}
	if _, ok := h.settings.memberLinks["u2"]; ok {
		t.Error("the ownership link should go with the membership")
	}
}

func TestSyncDeprovisionsADisabledAccount(t *testing.T) {
	ada := person("u1", "ada@example.org", "Ada", "Lovelace")
	h := newHarness(t, ada)
	h.sync(t)

	ada.Active = false
	h.source.directory = provisioner.Directory{People: []provisioner.Person{ada}}
	result := h.sync(t)

	if result.MembersRemoved != 1 {
		t.Fatalf("membersRemoved = %d, want 1", result.MembersRemoved)
	}
	if _, ok := h.members.entries["ada@example.org"]; ok {
		t.Error("a disabled account keeps its pending invitation")
	}
}

func TestSyncLeavesAManualMembershipAlone(t *testing.T) {
	ada := person("u1", "ada@example.org", "Ada", "Lovelace")
	ada.Admin = true
	h := newHarness(t, ada)

	// Somebody was admitted by hand before provisioning was switched on.
	userID := uuid.New()
	h.members.entries["ada@example.org"] = organization.MemberEntry{
		Status: organization.StatusActive, UserID: &userID,
		Email: "ada@example.org", Role: organization.RoleMember,
	}
	h.members.admins = 1

	result := h.sync(t)

	if result.MembersInvited != 0 || result.MembersUpdated != 0 {
		t.Errorf("result = %+v, want nothing done to a membership this sync does not own", result)
	}
	if got := h.entry(t, "ada@example.org").Role; got != organization.RoleMember {
		t.Errorf("role = %q; a directory must not be able to promote a hand-made membership", got)
	}
	if len(result.Skipped) != 1 || result.Skipped[0].Reason != SkipConflict {
		t.Errorf("skipped = %+v, want one conflict so an admin can decide", result.Skipped)
	}
	if _, ok := h.settings.memberLinks["u1"]; ok {
		t.Error("a conflict must not be recorded as owned")
	}
}

func TestSyncRefusesAnEmptyDirectory(t *testing.T) {
	ada := person("u1", "ada@example.org", "Ada", "Lovelace")
	h := newHarness(t, ada)
	h.sync(t)

	// An expired secret, a mistyped group id and a revoked permission all look
	// exactly like this.
	h.source.directory = provisioner.Directory{}
	_, err := h.service.Sync(context.Background(), h.orgID)

	if !errors.Is(err, ErrEmptyDirectory) {
		t.Fatalf("err = %v, want ErrEmptyDirectory", err)
	}
	if _, ok := h.members.entries["ada@example.org"]; !ok {
		t.Error("an empty read deprovisioned the organisation")
	}
	if last := h.settings.runs[len(h.settings.runs)-1]; last.err == nil {
		t.Error("the refused run should be recorded as a failure")
	}
}

func TestSyncSkipsARecordItCannotInvite(t *testing.T) {
	cases := map[string]provisioner.Person{
		"no e-mail":     {ExternalID: "u1", GivenNames: "Ada", LastName: "Lovelace", Active: true},
		"bad e-mail":    {ExternalID: "u2", Email: "not-an-address", GivenNames: "Ada", LastName: "Lovelace", Active: true},
		"no given name": {ExternalID: "u3", Email: "ada@example.org", LastName: "Lovelace", Active: true},
		"no last name":  {ExternalID: "u4", Email: "ada@example.org", GivenNames: "Ada", Active: true},
	}
	for name, p := range cases {
		t.Run(name, func(t *testing.T) {
			h := newHarness(t, p)
			result := h.sync(t)
			if result.MembersInvited != 0 {
				t.Errorf("membersInvited = %d, want 0", result.MembersInvited)
			}
			if len(result.Skipped) != 1 || result.Skipped[0].Reason != SkipIncomplete {
				t.Errorf("skipped = %+v, want one incomplete record", result.Skipped)
			}
		})
	}
}

func TestSyncKeepsTheLastAdmin(t *testing.T) {
	ada := person("u1", "ada@example.org", "Ada", "Lovelace")
	ada.Admin = true
	h := newHarness(t, ada)
	h.sync(t)
	accept(h, "ada@example.org")
	h.members.admins = 1

	h.source.directory = provisioner.Directory{People: []provisioner.Person{
		person("u2", "bob@example.org", "Bob", "Baker"),
	}}
	result := h.sync(t)

	if result.MembersRemoved != 0 {
		t.Errorf("membersRemoved = %d; a directory must not be able to lock everyone out", result.MembersRemoved)
	}
	if _, ok := h.settings.memberLinks["u1"]; !ok {
		t.Error("the person is still ours; the link must survive so the next run retries")
	}
	if !skippedFor(result, SkipLastAdmin) {
		t.Errorf("skipped = %+v, want the last-admin refusal reported", result.Skipped)
	}
}

func TestSyncDoesNotReinviteSomeoneRemovedHere(t *testing.T) {
	ada := person("u1", "ada@example.org", "Ada", "Lovelace")
	h := newHarness(t, ada)
	h.sync(t)

	// An admin revoked the provisioned invitation, or the person declined it.
	delete(h.members.entries, "ada@example.org")
	result := h.sync(t)

	if result.MembersInvited != 0 {
		t.Errorf("membersInvited = %d; re-inviting restarts that argument (and mails them) every run", result.MembersInvited)
	}
	if !skippedFor(result, SkipRemovedLocally) {
		t.Errorf("skipped = %+v, want the removal reported so it is not silent", result.Skipped)
	}
	if _, ok := h.settings.memberLinks["u1"]; !ok {
		t.Error("dropping the link would make the next run invite them again")
	}
}

func TestSyncAdoptsAnExistingDepartmentByName(t *testing.T) {
	ada := person("u1", "ada@example.org", "Ada", "Lovelace")
	ada.Department = "Research"
	h := newHarness(t, ada)
	existing := organization.Department{ID: uuid.New(), OrganizationID: h.orgID, Name: "research"}
	h.members.departments = []organization.Department{existing}

	result := h.sync(t)

	if result.DepartmentsCreated != 0 {
		t.Errorf("departmentsCreated = %d, want the existing department adopted", result.DepartmentsCreated)
	}
	if got := h.entry(t, "ada@example.org").DepartmentID; got == nil || *got != existing.ID {
		t.Errorf("departmentId = %v, want the existing department %s", got, existing.ID)
	}
}

func TestSyncFollowsALocallyRenamedDepartment(t *testing.T) {
	ada := person("u1", "ada@example.org", "Ada", "Lovelace")
	ada.Department = "Research"
	h := newHarness(t, ada)
	h.sync(t)
	created := h.members.departments[0]

	// An admin renames our department. The link, not the name, is what says which
	// department mirrors the source's.
	h.members.departments[0].Name = "R&D"
	result := h.sync(t)

	if result.DepartmentsCreated != 0 {
		t.Errorf("departmentsCreated = %d; a local rename must not spawn a second department", result.DepartmentsCreated)
	}
	if got := h.entry(t, "ada@example.org").DepartmentID; got == nil || *got != created.ID {
		t.Errorf("departmentId = %v, want the renamed department %s", got, created.ID)
	}
}

func TestSyncRecordsAFailedRun(t *testing.T) {
	h := newHarness(t, person("u1", "ada@example.org", "Ada", "Lovelace"))
	h.source.err = errors.New("status 403 (Authorization_RequestDenied)")

	if _, err := h.service.Sync(context.Background(), h.orgID); err == nil {
		t.Fatal("Sync succeeded on a source error")
	}
	if len(h.settings.runs) != 1 || h.settings.runs[0].err == nil {
		t.Fatalf("runs = %+v, want the failure recorded", h.settings.runs)
	}
}

func TestSyncDoesNotRecordARunWhenThereIsNothingConfigured(t *testing.T) {
	h := newHarness(t)
	h.settings.configErr = ErrDisabled

	if _, err := h.service.Sync(context.Background(), h.orgID); !errors.Is(err, ErrDisabled) {
		t.Fatalf("err = %v, want ErrDisabled", err)
	}
	if len(h.settings.runs) != 0 {
		t.Error("switching provisioning off should not keep writing failures to the audit log")
	}
	if h.source.calls != 0 {
		t.Error("a disabled organisation's directory must not be read")
	}
}

func TestSyncRejectsASourceThisDeploymentCannotDrive(t *testing.T) {
	h := newHarness(t, person("u1", "ada@example.org", "Ada", "Lovelace"))
	h.settings.source = provisioner.SourceID("okta")

	_, err := h.service.Sync(context.Background(), h.orgID)
	if !errors.Is(err, ErrUnknownSource) {
		t.Fatalf("err = %v, want ErrUnknownSource", err)
	}
}

// accept turns a pending invitation into an accepted membership, the way the
// wallet identity-disclosure flow does.
func accept(h *harness, email string) {
	key := strings.ToLower(email)
	entry := h.members.entries[key]
	userID := uuid.New()
	entry.Status = organization.StatusActive
	entry.UserID = &userID
	entry.InvitationID = nil
	entry.Verified = true
	h.members.entries[key] = entry
	if entry.Role == organization.RoleAdmin {
		h.members.admins++
	}
}

func skippedFor(result Result, reason string) bool {
	for _, s := range result.Skipped {
		if s.Reason == reason {
			return true
		}
	}
	return false
}
