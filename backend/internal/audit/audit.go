package audit

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/google/uuid"

	"github.com/privacybydesign/yivi-businesswallet/backend/internal/database"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/logging"
)

const (
	OrganizationCreated = "organization.created"
	OrganizationUpdated = "organization.updated"
	OrganizationDeleted = "organization.deleted"

	MembershipInvited        = "membership.invited"
	MembershipInviteResent   = "membership.invite_resent"
	MembershipInviteRevoked  = "membership.invite_revoked"
	MembershipAccepted       = "membership.accepted"
	MembershipAcceptRejected = "membership.accept_rejected"
	MembershipDeclined       = "membership.declined"
	MembershipRevoked        = "membership.revoked"
	MembershipRoleChanged    = "membership.role_changed"
	MembershipExpired        = "membership.expired"

	DepartmentCreated = "department.created"
	DepartmentUpdated = "department.updated"
	DepartmentDeleted = "department.deleted"

	UserIdentityChanged        = "user.identity_changed"
	UserIdentityReviewRequired = "user.identity_review_required"
	UserIdentityReviewApproved = "user.identity_review_approved"
	UserIdentityReviewRejected = "user.identity_review_rejected"
	UserPurged                 = "user.purged"

	QerdsMessageSent           = "qerds.message_sent"
	QerdsMessageReceived       = "qerds.message_received"
	QerdsAddressProvisioned    = "qerds.address_provisioned"
	QerdsAddressDefaultChanged = "qerds.address_default_changed"
	QerdsContactAdded          = "qerds.contact_added"
	QerdsContactDeleted        = "qerds.contact_deleted"

	PostGuardKeySet               = "postguard.key_set"
	PostGuardKeyRemoved           = "postguard.key_removed"
	PostGuardEncryptionKeySet     = "postguard.encryption_key_set"
	PostGuardEncryptionKeyRemoved = "postguard.encryption_key_removed"
	PostGuardFileSent             = "postguard.file_sent"

	WalletOpened          = "wallet.opened"
	WalletBootstrapped    = "wallet.bootstrapped"
	WalletSuspended       = "wallet.suspended"
	WalletRevoked         = "wallet.revoked"
	RepresentationClaimed = "wallet.representation_claimed"
	RepresentationRevoked = "wallet.representation_revoked"

	AttestationSchemaCreated   = "attestation.schema_created"
	AttestationSchemaUpdated   = "attestation.schema_updated"
	AttestationSchemaDeleted   = "attestation.schema_deleted"
	AttestationTemplateCreated = "attestation.template_created"
	AttestationTemplateUpdated = "attestation.template_updated"
	AttestationTemplateDeleted = "attestation.template_deleted"
	AttestationIssued          = "attestation.issued"
	AttestationClaimed         = "attestation.claimed"
	AttestationRevoked         = "attestation.revoked"
	AttestationKeyAdded        = "attestation.key_added"
	AttestationKeySuspended    = "attestation.key_suspended"
	AttestationKeyRevoked      = "attestation.key_revoked"
	AttestationHeldDeleted     = "attestation.held_deleted"

	EmailSettingsUpdated = "email.settings_updated"
)

const (
	TargetOrganization = "organization"
	TargetMembership   = "membership"
	TargetDepartment   = "department"
	TargetUser         = "user"
	TargetQerdsMessage = "qerds_message"
	TargetQerdsAddress = "qerds_address"
	TargetQerdsContact = "qerds_contact"

	TargetWalletInstance = "wallet_instance"
	TargetRepresentation = "wallet_representation"

	TargetPostGuardKey           = "postguard_key"
	TargetPostGuardEncryptionKey = "postguard_encryption_key"
	TargetPostGuardFile          = "postguard_file"

	TargetAttestationSchema   = "attestation_schema"
	TargetAttestationTemplate = "attestation_template"
	TargetIssuedAttestation   = "issued_attestation"
	TargetAttestationKey      = "attestation_key"
	TargetHeldAttestation     = "held_attestation"

	TargetEmailSettings = "org_email_settings"
)

type Actor struct {
	UserID uuid.UUID
}

type ctxKey struct{}

func ContextWithActor(ctx context.Context, a Actor) context.Context {
	return context.WithValue(ctx, ctxKey{}, a)
}

func actorFromContext(ctx context.Context) (Actor, bool) {
	a, ok := ctx.Value(ctxKey{}).(Actor)
	return a, ok
}

type Target struct {
	Type  string
	ID    string
	OrgID *uuid.UUID
}

// Created, Updated and Deleted build the uniform {before, after} metadata
// envelope: a create has no before, a delete has no after, an update has both.
func Created(after map[string]any) map[string]any {
	return map[string]any{"after": after}
}

func Updated(before, after map[string]any) map[string]any {
	return map[string]any{"before": before, "after": after}
}

func Deleted(before map[string]any) map[string]any {
	return map[string]any{"before": before}
}

type Recorder interface {
	Record(ctx context.Context, q database.Querier, action string, target Target, metadata map[string]any) error
}

type DBRecorder struct{}

func NewDBRecorder() DBRecorder { return DBRecorder{} }

func (DBRecorder) Record(ctx context.Context, q database.Querier, action string, target Target, metadata map[string]any) error {
	meta := []byte("{}")
	if metadata != nil {
		m, err := json.Marshal(metadata)
		if err != nil {
			return fmt.Errorf("audit: marshal metadata for %s: %w", action, err)
		}
		meta = m
	}

	var actorID *uuid.UUID
	if a, ok := actorFromContext(ctx); ok {
		actorID = &a.UserID
	}

	var requestID *string
	if id := logging.RequestIDFromContext(ctx); id != "" {
		requestID = &id
	}

	const insert = `INSERT INTO audit_events
		(actor_user_id, organization_id, action, target_type, target_id, metadata, request_id)
		VALUES ($1, $2, $3, $4, $5, $6, $7)`
	if _, err := q.Exec(ctx, insert, actorID, target.OrgID, action, target.Type, target.ID, meta, requestID); err != nil {
		return fmt.Errorf("audit: record %s: %w", action, err)
	}
	return nil
}

type NopRecorder struct{}

func (NopRecorder) Record(context.Context, database.Querier, string, Target, map[string]any) error {
	return nil
}
