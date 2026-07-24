import type { TFunction } from "i18next";
import type { AuditEvent } from "../api/organization";
import type { IconName } from "../ui";

export type AuditTone = "green" | "blue" | "red" | "amber" | "violet" | "slate";

export const AUDIT_TONE_CLASSES: Record<AuditTone, string> = {
  green: "bg-success-bg text-success",
  blue: "bg-highlight text-link",
  red: "bg-error-bg text-error",
  amber: "bg-warning-bg text-warning-fg",
  violet: "bg-[#ECE3F4] text-[#5B3B85]",
  slate: "bg-[#E4E2DF] text-ink",
};

const ACTION_VISUAL: Record<string, { icon: IconName; tone: AuditTone }> = {
  "organization.created": { icon: "add", tone: "green" },
  "organization.updated": { icon: "edit", tone: "blue" },
  "membership.invited": { icon: "email", tone: "amber" },
  "membership.invite_resent": { icon: "email", tone: "amber" },
  "membership.invite_revoked": { icon: "close", tone: "red" },
  "membership.accepted": { icon: "valid", tone: "green" },
  "membership.accept_rejected": { icon: "warning", tone: "amber" },
  "membership.declined": { icon: "close", tone: "slate" },
  "membership.revoked": { icon: "close", tone: "red" },
  "membership.role_changed": { icon: "settings", tone: "blue" },
  "membership.expired": { icon: "time", tone: "slate" },
  "department.created": { icon: "add", tone: "green" },
  "department.updated": { icon: "edit", tone: "blue" },
  "department.deleted": { icon: "delete", tone: "red" },
  "user.identity_changed": { icon: "personal", tone: "blue" },
  "user.identity_review_required": { icon: "warning", tone: "amber" },
  "user.identity_review_approved": { icon: "valid", tone: "green" },
  "user.identity_review_rejected": { icon: "close", tone: "red" },
  "user.purged": { icon: "delete", tone: "red" },
  "attestation.schema_created": { icon: "add", tone: "green" },
  "attestation.schema_updated": { icon: "edit", tone: "blue" },
  "attestation.schema_deleted": { icon: "delete", tone: "red" },
  "attestation.template_created": { icon: "add", tone: "green" },
  "attestation.template_updated": { icon: "edit", tone: "blue" },
  "attestation.template_deleted": { icon: "delete", tone: "red" },
  "attestation.issued": { icon: "valid", tone: "violet" },
  "attestation.claimed": { icon: "valid", tone: "green" },
  "attestation.revoked": { icon: "close", tone: "red" },
  "attestation.offer_cancelled": { icon: "close", tone: "amber" },
  "attestation.key_added": { icon: "add", tone: "green" },
  "attestation.key_suspended": { icon: "warning", tone: "amber" },
  "attestation.key_revoked": { icon: "close", tone: "red" },
  "email.settings_updated": { icon: "settings", tone: "blue" },
};

const DEFAULT_VISUAL: { icon: IconName; tone: AuditTone } = {
  icon: "info",
  tone: "slate",
};

export function auditVisual(action: string): {
  icon: IconName;
  tone: AuditTone;
} {
  return ACTION_VISUAL[action] ?? DEFAULT_VISUAL;
}

export function auditActionLabel(action: string, t: TFunction): string {
  switch (action) {
    case "organization.created":
      return t("auditLog.actions.orgCreated");
    case "organization.updated":
      return t("auditLog.actions.orgUpdated");
    case "organization.deleted":
      return t("auditLog.actions.orgDeleted");
    case "membership.invited":
      return t("auditLog.actions.memberInvited");
    case "membership.invite_resent":
      return t("auditLog.actions.inviteResent");
    case "membership.invite_revoked":
      return t("auditLog.actions.inviteRevoked");
    case "membership.accepted":
      return t("auditLog.actions.inviteAccepted");
    case "membership.accept_rejected":
      return t("auditLog.actions.acceptRejected");
    case "membership.declined":
      return t("auditLog.actions.inviteDeclined");
    case "membership.revoked":
      return t("auditLog.actions.memberRevoked");
    case "membership.role_changed":
      return t("auditLog.actions.roleChanged");
    case "membership.expired":
      return t("auditLog.actions.inviteExpired");
    case "department.created":
      return t("auditLog.actions.deptCreated");
    case "department.updated":
      return t("auditLog.actions.deptUpdated");
    case "department.deleted":
      return t("auditLog.actions.deptDeleted");
    case "user.identity_changed":
      return t("auditLog.actions.identityChanged");
    case "user.identity_review_required":
      return t("auditLog.actions.identityReviewRequired");
    case "user.identity_review_approved":
      return t("auditLog.actions.identityReviewApproved");
    case "user.identity_review_rejected":
      return t("auditLog.actions.identityReviewRejected");
    case "user.purged":
      return t("auditLog.actions.userPurged");
    case "qerds.message_sent":
      return t("auditLog.actions.qerdsMessageSent");
    case "qerds.message_received":
      return t("auditLog.actions.qerdsMessageReceived");
    case "qerds.address_provisioned":
      return t("auditLog.actions.qerdsAddressProvisioned");
    case "qerds.address_default_changed":
      return t("auditLog.actions.qerdsAddressDefaultChanged");
    case "qerds.contact_added":
      return t("auditLog.actions.qerdsContactAdded");
    case "qerds.contact_deleted":
      return t("auditLog.actions.qerdsContactDeleted");
    case "postguard.key_set":
      return t("auditLog.actions.postguardKeySet");
    case "postguard.key_removed":
      return t("auditLog.actions.postguardKeyRemoved");
    case "postguard.encryption_key_set":
      return t("auditLog.actions.postguardEncryptionKeySet");
    case "postguard.encryption_key_removed":
      return t("auditLog.actions.postguardEncryptionKeyRemoved");
    case "postguard.file_sent":
      return t("auditLog.actions.postguardFileSent");
    case "postguard.notification_delivery_set":
      return t("auditLog.actions.postguardNotificationDeliverySet");
    case "wallet.opened":
      return t("auditLog.actions.walletOpened");
    case "wallet.bootstrapped":
      return t("auditLog.actions.walletBootstrapped");
    case "wallet.suspended":
      return t("auditLog.actions.walletSuspended");
    case "wallet.revoked":
      return t("auditLog.actions.walletRevoked");
    case "wallet.representation_claimed":
      return t("auditLog.actions.representationClaimed");
    case "wallet.representation_revoked":
      return t("auditLog.actions.representationRevoked");
    case "kvk.registration_validated":
      return t("auditLog.actions.kvkRegistrationValidated");
    case "kvk.registration_not_validated":
      return t("auditLog.actions.kvkRegistrationNotValidated");
    case "attestation.schema_created":
      return t("auditLog.actions.attestationSchemaCreated");
    case "attestation.schema_updated":
      return t("auditLog.actions.attestationSchemaUpdated");
    case "attestation.schema_deleted":
      return t("auditLog.actions.attestationSchemaDeleted");
    case "attestation.template_created":
      return t("auditLog.actions.attestationTemplateCreated");
    case "attestation.template_updated":
      return t("auditLog.actions.attestationTemplateUpdated");
    case "attestation.template_deleted":
      return t("auditLog.actions.attestationTemplateDeleted");
    case "attestation.issued":
      return t("auditLog.actions.attestationIssued");
    case "attestation.claimed":
      return t("auditLog.actions.attestationClaimed");
    case "attestation.revoked":
      return t("auditLog.actions.attestationRevoked");
    case "attestation.offer_cancelled":
      return t("auditLog.actions.attestationOfferCancelled");
    case "attestation.key_added":
      return t("auditLog.actions.attestationKeyAdded");
    case "attestation.key_suspended":
      return t("auditLog.actions.attestationKeySuspended");
    case "attestation.key_revoked":
      return t("auditLog.actions.attestationKeyRevoked");
    case "attestation.held_deleted":
      return t("auditLog.actions.attestationHeldDeleted");
    case "email.settings_updated":
      return t("auditLog.actions.emailSettingsUpdated");
    case "issuer.settings_updated":
      return t("auditLog.actions.issuerSettingsUpdated");
    case "theme.settings_updated":
      return t("auditLog.actions.themeSettingsUpdated");
    case "onboarding.settings_updated":
      return t("auditLog.actions.onboardingSettingsUpdated");
    default:
      return action;
  }
}

export function auditTargetLabel(targetType: string, t: TFunction): string {
  switch (targetType) {
    case "organization":
      return t("auditLog.targets.organization");
    case "membership":
      return t("auditLog.targets.member");
    case "department":
      return t("auditLog.targets.department");
    case "user":
      return t("auditLog.targets.user");
    case "qerds_message":
      return t("auditLog.targets.qerdsMessage");
    case "qerds_address":
      return t("auditLog.targets.qerdsAddress");
    case "qerds_contact":
      return t("auditLog.targets.qerdsContact");
    case "wallet_instance":
      return t("auditLog.targets.walletInstance");
    case "wallet_representation":
      return t("auditLog.targets.walletRepresentation");
    case "kvk_registration":
      return t("auditLog.targets.kvkRegistration");
    case "postguard_key":
      return t("auditLog.targets.postguardKey");
    case "postguard_encryption_key":
      return t("auditLog.targets.postguardEncryptionKey");
    case "postguard_file":
      return t("auditLog.targets.postguardFile");
    case "postguard_settings":
      return t("auditLog.targets.postguardSettings");
    case "attestation_schema":
      return t("auditLog.targets.attestationSchema");
    case "attestation_template":
      return t("auditLog.targets.attestationTemplate");
    case "issued_attestation":
      return t("auditLog.targets.issuedAttestation");
    case "attestation_key":
      return t("auditLog.targets.attestationKey");
    case "held_attestation":
      return t("auditLog.targets.heldAttestation");
    case "org_email_settings":
      return t("auditLog.targets.orgEmailSettings");
    case "org_issuer_settings":
      return t("auditLog.targets.orgIssuerSettings");
    case "org_theme_settings":
      return t("auditLog.targets.orgThemeSettings");
    case "org_onboarding_attestations":
      return t("auditLog.targets.orgOnboardingAttestations");
    default:
      return targetType;
  }
}

function fieldValue(
  value: unknown,
  dateFormatter: Intl.DateTimeFormat,
): string {
  if (value === null || value === undefined) return "—";
  if (typeof value === "string") {
    if (/^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}/.test(value)) {
      const date = new Date(value);
      if (!Number.isNaN(date.getTime())) return dateFormatter.format(date);
    }
    return value;
  }
  return JSON.stringify(value);
}

// The human-readable detail for an event, derived from the uniform
// {before, after} metadata: an update diffs changed fields ("old → new"); a
// create/delete shows the snapshot's identifying field.
export function auditSubject(
  event: AuditEvent,
  dateFormatter: Intl.DateTimeFormat,
): string | null {
  const { before, after } = event.metadata as {
    before?: Record<string, unknown> | null;
    after?: Record<string, unknown> | null;
  };

  if (before && after) {
    const keys = [...new Set([...Object.keys(before), ...Object.keys(after)])];
    const changes = keys
      .filter((key) => before[key] !== after[key])
      .map(
        (key) =>
          `${fieldValue(before[key], dateFormatter)} → ${fieldValue(after[key], dateFormatter)}`,
      );
    return changes.length > 0 ? changes.join(", ") : null;
  }

  const snapshot = after ?? before;
  if (!snapshot) return null;
  // `recipients` identifies a sent encrypted file (who it was sent to): the
  // send handler rejects an empty list, so it is always a non-empty array.
  if (Array.isArray(snapshot.recipients) && snapshot.recipients.length > 0) {
    return snapshot.recipients.filter((r) => typeof r === "string").join(", ");
  }
  // `recipient` identifies an issued attestation (who it was issued to); the
  // issue handler rejects an empty ref, so it is always present on that event.
  const id =
    snapshot.name ?? snapshot.email ?? snapshot.recipient ?? snapshot.role;
  return typeof id === "string" ? id : null;
}
