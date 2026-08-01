// Package provisioner is the client seam to an external identity directory an
// organisation provisions its people and departments from. It mirrors
// internal/qerdsprovider and internal/registryprovider: our backend is a client
// of somebody else's system, the concrete driver is swapped per organisation by
// configuration, and the domain slice (internal/provisioning) depends only on
// the value types and behaviours defined here.
//
// A Provisioner is deliberately read-only and stateless: it fetches a snapshot
// of the source and hands it back. Deciding what that snapshot means for our own
// model — which departments to create, who to invite, whose membership to take
// away — is internal/provisioning's job, so a second source (Google Workspace,
// Okta, a CSV upload) only has to implement Fetch. See
// .ai/features/provisioning.md.
package provisioner

import (
	"context"
	"errors"
)

// SourceID identifies one directory source. The ids are stored per organisation,
// so they are fixed values rather than whatever happens to be registered: a
// deployment that has not enabled a source yet keeps the organisation's saved
// configuration instead of silently rewriting it.
type SourceID string

// SourceEntra is Microsoft Entra ID (formerly Azure AD), read over Microsoft
// Graph. It is the first — currently only — implemented source.
const SourceEntra SourceID = "entra"

// ErrIncompleteConfig is returned when the organisation's configuration is
// missing something the driver cannot work without (a tenant, a client id, a
// secret). It is a configuration problem, not an outage: the caller reports it
// to the org admin rather than retrying.
var ErrIncompleteConfig = errors.New("provisioner: incomplete configuration")

// Config is the per-organisation credentials and scoping a Provisioner needs.
// The fields are the OAuth client-credentials shape every directory API in this
// family uses; a source that needs something else carries it in its own driver
// rather than widening this struct for everyone.
//
// ClientSecret is the decrypted secret and must not be logged or included in an
// error message.
type Config struct {
	// TenantID is the directory tenant (Entra: the tenant GUID or domain).
	TenantID string
	// ClientID and ClientSecret are the app registration the sync authenticates
	// as.
	ClientID     string
	ClientSecret string
	// GroupID scopes the sync to the members of one group. Empty means every
	// account in the directory, which is rarely what a tenant wants — a business
	// wallet usually maps to a department or a division, not to the whole company.
	GroupID string
	// AdminGroupIDs are the groups whose members are provisioned as organisation
	// admins. A person in none of them becomes a plain member. This is the role
	// mapping, and it is resolved by the driver because only the driver knows how
	// to ask the source about group membership.
	AdminGroupIDs []string
}

// Person is one account in the source directory, mapped onto the fields our
// membership model carries. A driver reports what the source said and does not
// filter: a person the source lists without an e-mail address or without a name
// is still returned, and internal/provisioning decides that such a record cannot
// become an invitation and reports it as skipped. Filtering here would make the
// admin's "why is this person missing?" unanswerable.
type Person struct {
	// ExternalID is the source's stable identifier for this person (Entra: the
	// directory object id). It is what a provisioned membership is linked by, so
	// renaming or re-addressing someone in the source does not orphan them.
	ExternalID string
	Email      string
	GivenNames string
	LastName   string
	JobTitle   string
	// Department is the name of the person's department in the source, "" when
	// they have none.
	Department string
	// Admin reports that the person is in one of Config.AdminGroupIDs.
	Admin bool
	// Active reports that the account is usable in the source. A disabled account
	// is reported with Active false rather than left out, so deprovisioning can
	// tell "disabled" apart from "no longer in scope" — both deprovision, but only
	// the first is a decision somebody made about this person.
	Active bool
}

// Directory is one snapshot of the source: every person in scope at the moment
// of the fetch. Departments are not a separate list — our model gives a
// membership exactly one department, so a department is whatever the people in
// the snapshot say theirs is.
type Directory struct {
	People []Person
}

// Provisioner reads a directory source. Fetch must respect the context deadline
// and must not modify anything in the source; provisioning is a one-way sync
// into us.
type Provisioner interface {
	ID() SourceID
	Fetch(ctx context.Context, cfg Config) (Directory, error)
}
