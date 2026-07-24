package organization

import "strings"

// This file is the source of truth for the RBAC permission matrix designed in
// .ai/plans/rbac-model.md. A permission is a "{resource}:{action}" string; each
// functional role (Axis B) maps to a fixed, code-defined set. Keeping the map in
// code (rather than DB-editable data) makes the role -> permission mapping
// auditable by construction — it ships in git, changes go through review, and
// TestRolePermissions pins every grant.
//
// v1 enforces role -> permission with org-wide scope and no validity window,
// which is behaviourally equal to the binary RequireOrgAdmin gate for admins
// plus the finer roles. Scope narrowing, validity windows, mandate objects and
// the Axis-A (legal representative / full-mandate) checks that gate mandates:*
// and wallet:* are the seams #27 fills; no functional role holds those here.

// Resource domains.
const (
	ResourceMembers        = "members"
	ResourceMandates       = "mandates"
	ResourceAttestations   = "attestations"
	ResourceQERDS          = "qerds"
	ResourceWallet         = "wallet"
	ResourceSettings       = "settings"
	ResourceRelyingParties = "relying_parties"
	ResourceAudit          = "audit"

	// Consent & approval layer (consent-approval-layer.md, #113). approvals is
	// the decision surface over the approval queue; policies is the authoring
	// surface for auto-approve/auto-decline rules. Neither is Axis-A-gated —
	// approving a disclosure or acceptance is a functional act inside the
	// administrative-mandate space, not a mandate grant.
	ResourceApprovals = "approvals"
	ResourcePolicies  = "policies"
)

// Actions. Read is shared across resources; the rest are namespaced by the
// resource in the "{resource}:{action}" permission key, so a name may recur
// (e.g. ActionRevoke on members, attestations and mandates).
const (
	ActionRead = "read"

	// members
	ActionInvite         = "invite"
	ActionChangeRole     = "change_role"
	ActionRevoke         = "revoke"
	ActionReviewIdentity = "review_identity"

	// mandates (Axis-A-gated; held by no functional role in v1)
	ActionGrant    = "grant"
	ActionDelegate = "delegate"

	// attestations
	ActionIssue           = "issue"
	ActionCancelOffer     = "cancel_offer"
	ActionManageTemplates = "manage_templates"
	ActionManageKeys      = "manage_keys"

	// qerds
	ActionProvisionAddress = "provision_address"
	ActionSend             = "send"

	// wallet (Axis-A-gated; held by no functional role in v1)
	ActionActivate = "activate"
	ActionRotate   = "rotate"

	// settings
	ActionManageTheming = "manage_theming"
	ActionManageIssuer  = "manage_issuer"
	ActionManageSMTP    = "manage_smtp"

	// relying parties (#27 follow-up; enumerated but granted to no role in v1)
	ActionAuthorise = "authorise"
	ActionManage    = "manage"

	// approvals (consent-approval-layer.md). ActionDecide approves/declines a
	// pending item; ActionDecideDual is the distinct second-approver capability
	// for a four-eyes item, deliberately separate from ActionDecide so a role can
	// originate an approval without being allowed to complete a dual-control one
	// alone. ActionRead sees the queue.
	ActionDecide     = "decide"
	ActionDecideDual = "decide_dual"

	// policies (consent-approval-layer.md). ActionAuthor creates/edits an
	// auto-approve/auto-decline rule — admin-only, because a policy pre-approves a
	// whole class of future actions. ActionRevoke (shared with members/attestations)
	// revokes a policy; ActionRead sees them.
	ActionAuthor = "author"
)

// Permission is a "{resource}:{action}" key. Use Perm to build one.
func Perm(resource, action string) string { return resource + ":" + action }

// rolePermissions is the compiled role -> permission set. It is the single
// source of truth; the table in rbac-model.md is illustrative. Two deliberate
// choices, both to preserve today's behaviour while adding the finer roles:
//
//   - member does NOT hold members:read. Reading the member directory is an
//     administrator/auditor capability in this product today (the member list
//     endpoints are admin-gated); a plain member reads only the org it belongs
//     to, via the ungated GET /orgs/{slug} route.
//   - no functional role holds any mandates:* or wallet:* permission. Those are
//     Axis-A-gated (legal representative / full-mandate holder) and enforced by
//     #27, never reachable through a role.
var rolePermissions = map[string]map[string]struct{}{
	RoleAdmin: permSet(
		Perm(ResourceMembers, ActionRead),
		Perm(ResourceMembers, ActionInvite),
		Perm(ResourceMembers, ActionChangeRole),
		Perm(ResourceMembers, ActionRevoke),
		Perm(ResourceMembers, ActionReviewIdentity),
		Perm(ResourceAttestations, ActionRead),
		Perm(ResourceAttestations, ActionIssue),
		Perm(ResourceAttestations, ActionCancelOffer),
		Perm(ResourceAttestations, ActionRevoke),
		Perm(ResourceAttestations, ActionManageTemplates),
		Perm(ResourceAttestations, ActionManageKeys),
		Perm(ResourceQERDS, ActionRead),
		Perm(ResourceQERDS, ActionProvisionAddress),
		Perm(ResourceQERDS, ActionSend),
		Perm(ResourceSettings, ActionRead),
		Perm(ResourceSettings, ActionManageTheming),
		Perm(ResourceSettings, ActionManageIssuer),
		Perm(ResourceSettings, ActionManageSMTP),
		Perm(ResourceAudit, ActionRead),
		// The full consent surface: decide any item (including as the second
		// four-eyes approver) and author/revoke policies.
		Perm(ResourceApprovals, ActionRead),
		Perm(ResourceApprovals, ActionDecide),
		Perm(ResourceApprovals, ActionDecideDual),
		Perm(ResourcePolicies, ActionRead),
		Perm(ResourcePolicies, ActionAuthor),
		Perm(ResourcePolicies, ActionRevoke),
	),
	RoleMember: permSet(
		Perm(ResourceAttestations, ActionRead),
		Perm(ResourceQERDS, ActionRead),
	),
	RoleAttestationIssuer: permSet(
		Perm(ResourceAttestations, ActionRead),
		Perm(ResourceAttestations, ActionIssue),
		Perm(ResourceAttestations, ActionCancelOffer),
		Perm(ResourceAttestations, ActionRevoke),
		// May decide items and read policies, but not author them or act as the
		// second four-eyes approver. Item-kind scoping (issuance only) rides #115's
		// resource-type scope, enforced org-wide in v1.
		Perm(ResourceApprovals, ActionRead),
		Perm(ResourceApprovals, ActionDecide),
		Perm(ResourcePolicies, ActionRead),
	),
	RoleQerdsOperator: permSet(
		Perm(ResourceQERDS, ActionRead),
		Perm(ResourceQERDS, ActionProvisionAddress),
		Perm(ResourceQERDS, ActionSend),
		Perm(ResourceApprovals, ActionRead),
		Perm(ResourceApprovals, ActionDecide),
		Perm(ResourcePolicies, ActionRead),
	),
	RoleAuditor: permSet(
		Perm(ResourceMembers, ActionRead),
		Perm(ResourceAttestations, ActionRead),
		Perm(ResourceQERDS, ActionRead),
		Perm(ResourceSettings, ActionRead),
		Perm(ResourceAudit, ActionRead),
		// Read-only over the consent surface too: sees the queue and the policies,
		// never a decider or author (mirrors its *:read-only rule).
		Perm(ResourceApprovals, ActionRead),
		Perm(ResourcePolicies, ActionRead),
	),
}

func permSet(perms ...string) map[string]struct{} {
	set := make(map[string]struct{}, len(perms))
	for _, p := range perms {
		set[p] = struct{}{}
	}
	return set
}

// HasPermission reports whether the role grants {resource}:{action}. An unknown
// role holds nothing.
func HasPermission(role, resource, action string) bool {
	perms, ok := rolePermissions[role]
	if !ok {
		return false
	}
	_, ok = perms[Perm(resource, action)]
	return ok
}

// assignableRoles are the functional roles an administrator may assign at invite
// time or via UpdateMembership. It deliberately excludes any Axis-A basis of
// authority (legal representative, full/administrative mandate): those are not
// role strings and are granted through #27's mandate lifecycle, not here.
var assignableRoles = map[string]struct{}{
	RoleAdmin:             {},
	RoleMember:            {},
	RoleAttestationIssuer: {},
	RoleQerdsOperator:     {},
	RoleAuditor:           {},
}

// IsAssignableRole reports whether role is one an administrator may assign.
func IsAssignableRole(role string) bool {
	_, ok := assignableRoles[role]
	return ok
}

// AssignableRolesList returns the assignable role names, for error messages and
// docs. Order is fixed so messages are stable.
func AssignableRolesList() []string {
	return []string{RoleAdmin, RoleMember, RoleAttestationIssuer, RoleQerdsOperator, RoleAuditor}
}

func assignableRolesText() string {
	return strings.Join(AssignableRolesList(), ", ")
}
