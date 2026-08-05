// Package provisioning syncs an organisation's people and departments in from an
// external identity directory, so a tenant maintains its member list where it
// already maintains it (HR/IT's identity provider) instead of re-keying it here.
//
// The moving parts:
//
//   - internal/provisioner — the source seam. A Provisioner fetches a snapshot
//     and nothing else; Microsoft Entra ID over Graph is the first one.
//   - Store — the per-org configuration, saved like every other settings slice
//     (Settings/SettingsInput, org-admin-gated Handler, audited on change), plus
//     the link tables recording which departments and members this sync owns.
//   - Service — the reconciler: snapshot in, invitations/memberships/departments
//     out, and a Result saying what happened.
//   - Scheduler — runs Service.Sync for every enabled organisation on a timer.
//
// Two rules shape all of it.
//
// Provisioning creates the membership shell, never the person. A provisioned
// account becomes an invitation, and the invited person still proves who they
// are with their wallet before the membership exists — the identity-binding path
// (organization/accept.go, admin_reviews.go) is untouched. Trusting a directory
// export to assert a legal identity would quietly demote the thing this product
// is for. The consequence is that "provisioned" means "invited", and an
// organisation's member list shows them with status invited until they accept.
//
// The sync only ever touches what it created. Every provisioned person and
// department is recorded in a link table, and a membership or department that
// has no link is left exactly as it is — the manual invitation flow and the
// directory sync coexist in one organisation. A source account whose e-mail
// already belongs to a hand-made membership is therefore not adopted and not
// modified; it is reported as skipped so an admin can decide.
//
// See .ai/features/provisioning.md.
package provisioning

import (
	"errors"
	"slices"
	"strings"
	"time"

	"github.com/privacybydesign/yivi-businesswallet/backend/internal/provisioner"
)

var (
	// ErrNotConfigured is returned when an organisation has never saved a
	// provisioning configuration.
	ErrNotConfigured = errors.New("provisioning: not configured for this organization")
	// ErrDisabled is returned when a sync is asked for on an organisation whose
	// provisioning is switched off.
	ErrDisabled = errors.New("provisioning: disabled for this organization")
	// ErrUnknownSource is returned when the saved source has no driver in this
	// deployment.
	ErrUnknownSource = errors.New("provisioning: unknown source")
	// ErrEmptyDirectory is returned when the source answers with no people at all.
	// It is refused rather than obeyed: an expired secret, a mistyped group id or
	// a permission that was revoked all look exactly like "everybody left", and
	// obeying it would deprovision the whole organisation in one pass.
	ErrEmptyDirectory = errors.New("provisioning: source returned no accounts")
	// ErrNoEncryptionKey is returned when saving a client secret but the deployment
	// has no PROVISIONING_ENCRYPTION_KEY configured, so the secret cannot be
	// encrypted at rest. It is surfaced to the admin as a configuration problem
	// rather than an internal error.
	ErrNoEncryptionKey = errors.New("provisioning: no encryption key configured; cannot store a client secret")
)

// SourceError wraps a failure that belongs to the directory source: it could not
// be reached, refused the credential, or answered with something the driver could
// not read. Everything else a sync can fail on is ours — a database error in the
// reconciler, say — and the two are answered differently, because telling an
// admin their source is at fault sends them to check credentials that are fine.
//
// It adds no text of its own: the driver's message is what lands in
// Settings.LastRunError for the admin to read.
type SourceError struct{ Err error }

func (e *SourceError) Error() string { return e.Err.Error() }

func (e *SourceError) Unwrap() error { return e.Err }

// Run statuses recorded on the settings row after a sync.
const (
	RunSucceeded = "succeeded"
	RunFailed    = "failed"
)

// Sources returns the source ids this deployment knows how to configure, in the
// order the settings screen lists them.
func Sources() []provisioner.SourceID {
	return []provisioner.SourceID{provisioner.SourceEntra}
}

// KnownSource reports whether id is a source this deployment can configure.
func KnownSource(id provisioner.SourceID) bool {
	return slices.Contains(Sources(), id)
}

// Settings is an organisation's provisioning configuration as it is served to an
// org admin. Configured is false when the organisation has never saved one.
//
// The client secret is never in here — only HasClientSecret, so the settings
// screen can show that one is stored and offer to replace it. A write-only
// secret is the same posture as the per-org SMTP password.
type Settings struct {
	Configured      bool                 `json:"configured"`
	Enabled         bool                 `json:"enabled"`
	Source          provisioner.SourceID `json:"source"`
	TenantID        string               `json:"tenantId"`
	ClientID        string               `json:"clientId"`
	HasClientSecret bool                 `json:"hasClientSecret"`
	GroupID         string               `json:"groupId"`
	AdminGroupIDs   []string             `json:"adminGroupIds"`
	LastRunAt       *time.Time           `json:"lastRunAt,omitempty"`
	LastRunStatus   string               `json:"lastRunStatus,omitempty"`
	// LastRunError is the failure of the last run, "" when it succeeded. It is the
	// error the driver reported (a status plus the source's own error code), which
	// is what tells an admin whether the secret expired or the permission is wrong.
	LastRunError string     `json:"lastRunError,omitempty"`
	UpdatedAt    *time.Time `json:"updatedAt,omitempty"`
}

// SettingsInput is a full replacement of an organisation's configuration.
//
// ClientSecret is a pointer because "leave the stored secret alone" and "clear
// it" are different requests: nil keeps what is stored, a pointer to "" removes
// it. Sending the secret back on every save would mean the screen has to hold it,
// which is exactly what HasClientSecret exists to avoid.
type SettingsInput struct {
	Enabled       bool
	Source        provisioner.SourceID
	TenantID      string
	ClientID      string
	ClientSecret  *string
	GroupID       string
	AdminGroupIDs []string
}

// Normalize trims and validates an input, returning a canonical copy. Storing
// the canonical form keeps a saved configuration comparable to the next one, so
// the audit diff of a change reads as the change the admin actually made.
func Normalize(in SettingsInput) (SettingsInput, error) {
	out := SettingsInput{
		Enabled:  in.Enabled,
		Source:   provisioner.SourceID(strings.TrimSpace(string(in.Source))),
		TenantID: strings.TrimSpace(in.TenantID),
		ClientID: strings.TrimSpace(in.ClientID),
		GroupID:  strings.TrimSpace(in.GroupID),
	}
	if !KnownSource(out.Source) {
		return SettingsInput{}, errors.New("unknown source " + string(out.Source))
	}
	if in.ClientSecret != nil {
		secret := strings.TrimSpace(*in.ClientSecret)
		out.ClientSecret = &secret
	}

	seen := map[string]bool{}
	for _, id := range in.AdminGroupIDs {
		id = strings.TrimSpace(id)
		if id == "" || seen[id] {
			continue
		}
		seen[id] = true
		out.AdminGroupIDs = append(out.AdminGroupIDs, id)
	}
	return out, nil
}

// Skip reasons reported by a sync.
const (
	// SkipIncomplete: the source record cannot become an invitation. Our
	// invitation carries an e-mail plus a given and family name, because the
	// person's wallet disclosure is matched against them on accept.
	SkipIncomplete = "incomplete"
	// SkipConflict: the e-mail already belongs to a membership or invitation this
	// sync does not own.
	SkipConflict = "conflict"
	// SkipLastAdmin: deprovisioning would have removed the organisation's last
	// admin, which the membership store refuses.
	SkipLastAdmin = "last_admin"
	// SkipRemovedLocally: the sync owns this person, but the invitation or
	// membership it created is gone — revoked by an admin here, or declined. It is
	// not re-created, so the run reports it instead of restarting that argument
	// (and mailing the person) on every pass.
	SkipRemovedLocally = "removed_locally"
)

// Skip is one source record the sync did not act on, and why.
type Skip struct {
	Email  string `json:"email"`
	Reason string `json:"reason"`
}

// Result is what one sync did. It is returned to the admin who triggered it and
// summarised into the audit event; the per-person detail is already in the audit
// log, recorded by the membership operations the sync went through.
type Result struct {
	DepartmentsCreated int    `json:"departmentsCreated"`
	MembersInvited     int    `json:"membersInvited"`
	MembersUpdated     int    `json:"membersUpdated"`
	MembersRemoved     int    `json:"membersRemoved"`
	Skipped            []Skip `json:"skipped"`
}

// auditSnapshot renders a Result as audit metadata: the counts and how many
// records were skipped for each reason. The skipped people's e-mail addresses
// stay out of it — an audit event is readable by every org admin and a skip says
// nothing about the organisation's own members, only about who is in the
// tenant's directory.
func (r Result) auditSnapshot(source provisioner.SourceID) map[string]any {
	reasons := map[string]int{}
	for _, s := range r.Skipped {
		reasons[s.Reason]++
	}
	snapshot := map[string]any{
		"source":             string(source),
		"departmentsCreated": r.DepartmentsCreated,
		"membersInvited":     r.MembersInvited,
		"membersUpdated":     r.MembersUpdated,
		"membersRemoved":     r.MembersRemoved,
		"skipped":            len(r.Skipped),
	}
	for reason, count := range reasons {
		snapshot["skipped_"+reason] = count
	}
	return snapshot
}

// Link is one provisioned person: the source's id for them and the e-mail their
// invitation or membership is keyed by. The e-mail is what the sync looks the
// person up with, because it is the one identifier our invitation and membership
// rows share.
type Link struct {
	ExternalID string
	Email      string
}
