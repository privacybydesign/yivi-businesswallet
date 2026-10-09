import type { TFunction } from "i18next";
import type { AuditEvent } from "../api/organization";
import type { IconName } from "../ui";
import {
  proofingMethodLabel,
  proofingRejectionReason,
} from "./identity-proofing";

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
  "membership.invite_updated": { icon: "edit", tone: "blue" },
  "membership.accepted": { icon: "valid", tone: "green" },
  "membership.accept_rejected": { icon: "warning", tone: "amber" },
  "membership.declined": { icon: "close", tone: "slate" },
  "membership.revoked": { icon: "close", tone: "red" },
  "membership.role_changed": { icon: "settings", tone: "blue" },
  "membership.expired": { icon: "time", tone: "slate" },
  "membership.type_changed": { icon: "edit", tone: "blue" },
  "membership.identity_requested": { icon: "personal", tone: "amber" },
  "membership.identity_reverified": { icon: "valid", tone: "green" },
  "membership.identity_reverify_rejected": { icon: "warning", tone: "amber" },
  "membership.identity_reminder_sent": { icon: "email", tone: "amber" },
  "membership.identity_overdue": { icon: "warning", tone: "red" },
  "identity.settings_updated": { icon: "settings", tone: "blue" },
  "membership.vog_requested": { icon: "personal", tone: "amber" },
  "membership.vog_checked": { icon: "valid", tone: "green" },
  "membership.vog_rejected": { icon: "warning", tone: "amber" },
  "membership.vog_mismatch": { icon: "warning", tone: "amber" },
  "membership.vog_insufficient_scope": { icon: "warning", tone: "amber" },
  "membership.vog_reminder_sent": { icon: "email", tone: "amber" },
  "membership.vog_expired": { icon: "warning", tone: "red" },
  "screening.settings_updated": { icon: "settings", tone: "blue" },
  "mandate.granted": { icon: "valid", tone: "violet" },
  "mandate.revoked": { icon: "close", tone: "red" },
  "department.created": { icon: "add", tone: "green" },
  "department.updated": { icon: "edit", tone: "blue" },
  "department.deleted": { icon: "delete", tone: "red" },
  "user.identity_changed": { icon: "personal", tone: "blue" },
  "user.identity_review_required": { icon: "warning", tone: "amber" },
  "user.identity_review_approved": { icon: "valid", tone: "green" },
  "user.identity_review_rejected": { icon: "close", tone: "red" },
  "user.purged": { icon: "delete", tone: "red" },
  "attestation.schema_created": { icon: "add", tone: "green" },
  "attestation.held_received": { icon: "add", tone: "green" },
  "attestation.held_status_changed": { icon: "warning", tone: "amber" },
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
  "attestation.offer_accepted": { icon: "valid", tone: "green" },
  "attestation.offer_declined": { icon: "close", tone: "slate" },
  "email.settings_updated": { icon: "settings", tone: "blue" },
  "email.template_updated": { icon: "email", tone: "blue" },
  "email.template_reset": { icon: "email", tone: "amber" },
  "notification.settings_updated": { icon: "settings", tone: "blue" },
  "slack.settings_updated": { icon: "settings", tone: "blue" },
  "msteams.settings_updated": { icon: "settings", tone: "blue" },
  "provisioning.settings_updated": { icon: "settings", tone: "blue" },
  "provisioning.run_completed": { icon: "valid", tone: "green" },
  "provisioning.run_failed": { icon: "warning", tone: "red" },
  "csc.settings_updated": { icon: "settings", tone: "blue" },
  "signing.credential_linked": { icon: "settings", tone: "blue" },
  "signing.requested": { icon: "settings", tone: "blue" },
  "signing.signed": { icon: "edit", tone: "blue" },
  "signing.completed": { icon: "valid", tone: "green" },
  "signing.delivered": { icon: "email", tone: "green" },
  "signing.failed": { icon: "warning", tone: "red" },
  "signing.declined": { icon: "close", tone: "slate" },
  "presentation.requested": { icon: "scan_qrcode", tone: "amber" },
  "presentation.org_selected": { icon: "personal", tone: "blue" },
  "presentation.completed": { icon: "valid", tone: "green" },
  "presentation.denied": { icon: "close", tone: "red" },
  "presentation.expired": { icon: "time", tone: "slate" },
  "presentation.request_received": { icon: "scan_qrcode", tone: "amber" },
  "presentation.request_sent": { icon: "email", tone: "blue" },
  "presentation.response_received": { icon: "valid", tone: "green" },
  "presentation.request_failed": { icon: "warning", tone: "red" },
  "identity_proofing.provisioned": { icon: "settings", tone: "blue" },
  "identity_proofing.flow_created": { icon: "add", tone: "green" },
  "identity_proofing.flows_configured": { icon: "settings", tone: "blue" },
  "identity_proofing.flow_version_created": { icon: "edit", tone: "blue" },
  "identity_proofing.flow_version_activated": { icon: "valid", tone: "blue" },
  "identity_proofing.requested": { icon: "email", tone: "amber" },
  "identity_proofing.session_created": { icon: "time", tone: "blue" },
  "identity_proofing.session_started": { icon: "scan_qrcode", tone: "blue" },
  "identity_proofing.session_handover": { icon: "scan_qrcode", tone: "slate" },
  "identity_proofing.session_ended": { icon: "time", tone: "amber" },
  "identity_proofing.session_cancelled": { icon: "close", tone: "slate" },
  "identity_proofing.session_purged": { icon: "delete", tone: "red" },
  "identity_proofing.result_read": { icon: "view", tone: "slate" },
  "identity_proofing.device_claimed": { icon: "scan_qrcode", tone: "blue" },
  "identity_proofing.device_handed_over": {
    icon: "scan_qrcode",
    tone: "slate",
  },
  "identity_proofing.handover_issued": { icon: "scan_qrcode", tone: "slate" },
  "identity_proofing.handover_claim_failed": { icon: "warning", tone: "amber" },
  "identity_proofing.access_denied": { icon: "warning", tone: "red" },
  "identity_proofing.approved": { icon: "valid", tone: "green" },
  "identity_proofing.rejected": { icon: "close", tone: "red" },
  "identity_proofing.needs_review": { icon: "warning", tone: "amber" },
  "identity_proofing.review_decided": { icon: "valid", tone: "blue" },
  "identity_proofing.data_exported": { icon: "view", tone: "slate" },
  "identity_proofing.flow_kind_configured": { icon: "edit", tone: "blue" },
  "identity_proofing.customer_created": { icon: "add", tone: "green" },
  "identity_proofing.customer_updated": { icon: "edit", tone: "blue" },
  "identity_proofing.customer_flows_configured": {
    icon: "settings",
    tone: "blue",
  },
  "identity_proofing.customer_removed": { icon: "delete", tone: "red" },
  "identity_proofing.api_key_created": { icon: "add", tone: "green" },
  "identity_proofing.api_key_revoked": { icon: "close", tone: "red" },
  "identity_proofing.webhook_configured": { icon: "settings", tone: "blue" },
  "identity_proofing.webhook_secret_rotated": { icon: "lock", tone: "amber" },
  "identity_proofing.webhook_removed": { icon: "delete", tone: "red" },
  "identity_proofing.paused": { icon: "warning", tone: "amber" },
  "identity_proofing.flow_hosted_configured": {
    icon: "settings",
    tone: "blue",
  },
  "identity_proofing.flow_diplomas_configured": {
    icon: "settings",
    tone: "blue",
  },
  "identity_proofing.diploma_added": { icon: "add", tone: "green" },
  "identity_proofing.diploma_rejected": { icon: "close", tone: "red" },
  "identity_proofing.resumed": { icon: "valid", tone: "green" },
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

const API_KEY_ACTOR_PREFIX = "api_key:";
// hostedSubjectActor in backend/internal/proofing/hosted.go: the subject of a
// hosted link, who has no account.
const HOSTED_LINK_ACTOR = "hosted_link";
// SubjectAppActorPrefix in backend/internal/proofing/service.go: the app a
// subject proofed with, as the actor of what it caused.
const SUBJECT_APP_ACTOR_PREFIX = "app:";

// A non-user actor in words: a customer API key by its prefix, a hosted link's
// subject, the app a subject proofed with, or the label as it is; null when
// the event has none.
export function auditActorLabel(
  label: string | null | undefined,
  t: TFunction,
): string | null {
  if (!label) return null;
  if (label.startsWith(API_KEY_ACTOR_PREFIX)) {
    return t("auditLog.apiKeyActor", {
      prefix: label.slice(API_KEY_ACTOR_PREFIX.length),
    });
  }
  if (label === HOSTED_LINK_ACTOR) {
    return t("auditLog.hostedLinkActor");
  }
  if (label.startsWith(SUBJECT_APP_ACTOR_PREFIX)) {
    return proofingMethodLabel(label.slice(SUBJECT_APP_ACTOR_PREFIX.length), t);
  }
  return label;
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
    case "membership.invite_updated":
      return t("auditLog.actions.inviteUpdated");
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
    case "membership.type_changed":
      return t("auditLog.actions.memberTypeChanged");
    case "membership.identity_requested":
      return t("auditLog.actions.identityRequested");
    case "membership.identity_reverified":
      return t("auditLog.actions.identityReverified");
    case "membership.identity_reverify_rejected":
      return t("auditLog.actions.identityReverifyRejected");
    case "membership.identity_reminder_sent":
      return t("auditLog.actions.identityReminderSent");
    case "membership.identity_overdue":
      return t("auditLog.actions.identityOverdue");
    case "identity.settings_updated":
      return t("auditLog.actions.identitySettingsUpdated");
    case "membership.vog_requested":
      return t("auditLog.actions.vogRequested");
    case "membership.vog_checked":
      return t("auditLog.actions.vogChecked");
    case "membership.vog_rejected":
      return t("auditLog.actions.vogRejected");
    case "membership.vog_mismatch":
      return t("auditLog.actions.vogMismatch");
    case "membership.vog_insufficient_scope":
      return t("auditLog.actions.vogInsufficientScope");
    case "membership.vog_reminder_sent":
      return t("auditLog.actions.vogReminderSent");
    case "membership.vog_expired":
      return t("auditLog.actions.vogExpired");
    case "screening.settings_updated":
      return t("auditLog.actions.screeningSettingsUpdated");
    case "mandate.granted":
      return t("auditLog.actions.mandateGranted");
    case "mandate.revoked":
      return t("auditLog.actions.mandateRevoked");
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
    case "qerds.address_deleted":
      return t("auditLog.actions.qerdsAddressDeleted");
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
    case "attestation.held_received":
      return t("auditLog.actions.attestationHeldReceived");
    case "attestation.held_status_changed":
      return t("auditLog.actions.attestationHeldStatusChanged");
    case "attestation.held_deleted":
      return t("auditLog.actions.attestationHeldDeleted");
    case "attestation.offer_accepted":
      return t("auditLog.actions.attestationOfferAccepted");
    case "attestation.offer_declined":
      return t("auditLog.actions.attestationOfferDeclined");
    case "email.settings_updated":
      return t("auditLog.actions.emailSettingsUpdated");
    case "email.template_updated":
      return t("auditLog.actions.emailTemplateUpdated");
    case "email.template_reset":
      return t("auditLog.actions.emailTemplateReset");
    case "issuer.settings_updated":
      return t("auditLog.actions.issuerSettingsUpdated");
    case "theme.settings_updated":
      return t("auditLog.actions.themeSettingsUpdated");
    case "onboarding.settings_updated":
      return t("auditLog.actions.onboardingSettingsUpdated");
    case "notification.settings_updated":
      return t("auditLog.actions.notificationSettingsUpdated");
    case "slack.settings_updated":
      return t("auditLog.actions.slackSettingsUpdated");
    case "msteams.settings_updated":
      return t("auditLog.actions.teamsSettingsUpdated");
    case "provisioning.settings_updated":
      return t("auditLog.actions.provisioningSettingsUpdated");
    case "provisioning.run_completed":
      return t("auditLog.actions.provisioningRunCompleted");
    case "provisioning.run_failed":
      return t("auditLog.actions.provisioningRunFailed");
    case "csc.settings_updated":
      return t("auditLog.actions.cscSettingsUpdated");
    case "signing.credential_linked":
      return t("auditLog.actions.signingCredentialLinked");
    case "signing.requested":
      return t("auditLog.actions.signingRequested");
    case "signing.signed":
      return t("auditLog.actions.signingSigned");
    case "signing.completed":
      return t("auditLog.actions.signingCompleted");
    case "signing.delivered":
      return t("auditLog.actions.signingDelivered");
    case "signing.failed":
      return t("auditLog.actions.signingFailed");
    case "signing.declined":
      return t("auditLog.actions.signingDeclined");
    case "presentation.requested":
      return t("auditLog.actions.presentationRequested");
    case "presentation.org_selected":
      return t("auditLog.actions.presentationOrgSelected");
    case "presentation.completed":
      return t("auditLog.actions.presentationCompleted");
    case "presentation.denied":
      return t("auditLog.actions.presentationDenied");
    case "presentation.expired":
      return t("auditLog.actions.presentationExpired");
    case "presentation.request_received":
      return t("auditLog.actions.presentationRequestReceived");
    case "presentation.request_sent":
      return t("auditLog.actions.presentationRequestSent");
    case "presentation.response_received":
      return t("auditLog.actions.presentationResponseReceived");
    case "presentation.request_failed":
      return t("auditLog.actions.presentationRequestFailed");
    case "identity_proofing.provisioned":
      return t("auditLog.actions.identityProofingProvisioned");
    case "identity_proofing.flow_created":
      return t("auditLog.actions.identityProofingFlowCreated");
    case "identity_proofing.flows_configured":
      return t("auditLog.actions.identityProofingFlowsConfigured");
    case "identity_proofing.flow_version_created":
      return t("auditLog.actions.identityProofingFlowVersionCreated");
    case "identity_proofing.flow_version_activated":
      return t("auditLog.actions.identityProofingFlowVersionActivated");
    case "identity_proofing.requested":
      return t("auditLog.actions.identityProofingRequested");
    case "identity_proofing.session_created":
      return t("auditLog.actions.identityProofingSessionCreated");
    case "identity_proofing.session_started":
      return t("auditLog.actions.identityProofingSessionStarted");
    case "identity_proofing.session_handover":
      return t("auditLog.actions.identityProofingSessionHandover");
    case "identity_proofing.session_ended":
      return t("auditLog.actions.identityProofingSessionEnded");
    case "identity_proofing.session_cancelled":
      return t("auditLog.actions.identityProofingSessionCancelled");
    case "identity_proofing.session_purged":
      return t("auditLog.actions.identityProofingSessionPurged");
    case "identity_proofing.result_read":
      return t("auditLog.actions.identityProofingResultRead");
    case "identity_proofing.device_claimed":
      return t("auditLog.actions.identityProofingDeviceClaimed");
    case "identity_proofing.device_handed_over":
      return t("auditLog.actions.identityProofingDeviceHandedOver");
    case "identity_proofing.handover_issued":
      return t("auditLog.actions.identityProofingHandoverIssued");
    case "identity_proofing.handover_claim_failed":
      return t("auditLog.actions.identityProofingHandoverClaimFailed");
    case "identity_proofing.access_denied":
      return t("auditLog.actions.identityProofingAccessDenied");
    case "identity_proofing.approved":
      return t("auditLog.actions.identityProofingApproved");
    case "identity_proofing.rejected":
      return t("auditLog.actions.identityProofingRejected");
    case "identity_proofing.needs_review":
      return t("auditLog.actions.identityProofingNeedsReview");
    case "identity_proofing.review_decided":
      return t("auditLog.actions.identityProofingReviewDecided");
    case "identity_proofing.data_exported":
      return t("auditLog.actions.identityProofingDataExported");
    case "identity_proofing.flow_kind_configured":
      return t("auditLog.actions.identityProofingFlowKindConfigured");
    case "identity_proofing.customer_created":
      return t("auditLog.actions.identityProofingCustomerCreated");
    case "identity_proofing.customer_updated":
      return t("auditLog.actions.identityProofingCustomerUpdated");
    case "identity_proofing.customer_flows_configured":
      return t("auditLog.actions.identityProofingCustomerFlowsConfigured");
    case "identity_proofing.customer_removed":
      return t("auditLog.actions.identityProofingCustomerRemoved");
    case "identity_proofing.api_key_created":
      return t("auditLog.actions.identityProofingApiKeyCreated");
    case "identity_proofing.api_key_revoked":
      return t("auditLog.actions.identityProofingApiKeyRevoked");
    case "identity_proofing.webhook_configured":
      return t("auditLog.actions.identityProofingWebhookConfigured");
    case "identity_proofing.webhook_secret_rotated":
      return t("auditLog.actions.identityProofingWebhookSecretRotated");
    case "identity_proofing.webhook_removed":
      return t("auditLog.actions.identityProofingWebhookRemoved");
    case "identity_proofing.flow_hosted_configured":
      return t("auditLog.actions.identityProofingFlowHostedConfigured");
    case "identity_proofing.flow_diplomas_configured":
      return t("auditLog.actions.identityProofingFlowDiplomasConfigured");
    case "identity_proofing.diploma_added":
      return t("auditLog.actions.identityProofingDiplomaAdded");
    case "identity_proofing.diploma_rejected":
      return t("auditLog.actions.identityProofingDiplomaRejected");
    case "identity_proofing.paused":
      return t("auditLog.actions.identityProofingPaused");
    case "identity_proofing.resumed":
      return t("auditLog.actions.identityProofingResumed");
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
    case "mandate":
      return t("auditLog.targets.mandate");
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
    case "credential_offer":
      return t("auditLog.targets.credentialOffer");
    case "org_email_settings":
      return t("auditLog.targets.orgEmailSettings");
    case "org_email_template":
      return t("auditLog.targets.orgEmailTemplate");
    case "org_issuer_settings":
      return t("auditLog.targets.orgIssuerSettings");
    case "org_theme_settings":
      return t("auditLog.targets.orgThemeSettings");
    case "org_onboarding_attestations":
      return t("auditLog.targets.orgOnboardingAttestations");
    case "org_notification_settings":
      return t("auditLog.targets.orgNotificationSettings");
    case "org_identity_settings":
      return t("auditLog.targets.orgIdentitySettings");
    case "org_screening_settings":
      return t("auditLog.targets.orgScreeningSettings");
    case "org_slack_settings":
      return t("auditLog.targets.orgSlackSettings");
    case "org_teams_settings":
      return t("auditLog.targets.orgTeamsSettings");
    case "org_provisioning_settings":
      return t("auditLog.targets.orgProvisioningSettings");
    case "org_csc_settings":
      return t("auditLog.targets.orgCscSettings");
    case "signing_credentials":
      return t("auditLog.targets.signingCredentials");
    case "signing_requests":
      return t("auditLog.targets.signingRequests");
    case "presentation_transaction":
      return t("auditLog.targets.presentationTransaction");
    case "outbound_presentation_request":
      return t("auditLog.targets.outboundPresentationRequest");
    case "org_identity_proofing_settings":
      return t("auditLog.targets.orgIdentityProofingSettings");
    case "identity_proofing_flow":
      return t("auditLog.targets.identityProofingFlow");
    case "identity_proofing_request":
      return t("auditLog.targets.identityProofingRequest");
    case "identity_proofing_customer":
      return t("auditLog.targets.identityProofingCustomer");
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

// Fields that identify whom an event is about, most specific first. On an
// update they ride along unchanged on both sides and lead the detail.
const IDENTITY_KEYS = ["subjectName", "subjectEmail"] as const;

function isEmpty(value: unknown): boolean {
  return value === null || value === undefined || value === "";
}

// A field an update adds (absent before) reads as "label: value" rather than
// "— → value" when it has a label.
function addedFieldLabel(key: string, t: TFunction): string | null {
  switch (key) {
    case "assuranceLevel":
      return t("auditLog.fields.assuranceLevel");
    case "eidasLevel":
      return t("auditLog.fields.eidasLevel");
    case "errorCode":
      return t("auditLog.fields.errorCode");
    case "ipsStatus":
      return t("auditLog.fields.ipsStatus");
    case "method":
      return t("auditLog.fields.method");
    default:
      return null;
  }
}

// The human-readable detail for an event, derived from the uniform
// {before, after} metadata: an update diffs changed fields ("old → new"),
// led by whom it is about; a create/delete shows the snapshot's identifying
// field.
export function auditSubject(
  event: AuditEvent,
  dateFormatter: Intl.DateTimeFormat,
  t: TFunction,
): string | null {
  const { before, after } = event.metadata as {
    before?: Record<string, unknown> | null;
    after?: Record<string, unknown> | null;
  };

  if (before && after) {
    const keys = [...new Set([...Object.keys(before), ...Object.keys(after)])];
    const changes = keys
      .filter(
        (key) =>
          before[key] !== after[key] &&
          !(isEmpty(before[key]) && isEmpty(after[key])),
      )
      .map((key) => {
        const label = isEmpty(before[key]) ? addedFieldLabel(key, t) : null;
        const value =
          key === "errorCode" && typeof after[key] === "string"
            ? proofingRejectionReason(after[key], t)
            : key === "method" && typeof after[key] === "string"
              ? proofingMethodLabel(after[key], t)
              : fieldValue(after[key], dateFormatter);
        return label
          ? `${label}: ${value}`
          : `${fieldValue(before[key], dateFormatter)} → ${value}`;
      });
    if (changes.length === 0) return null;
    const who = IDENTITY_KEYS.filter((key) => before[key] === after[key])
      .map((key) => after[key])
      .find(
        (value): value is string => typeof value === "string" && value !== "",
      );
    const detail = changes.join(", ");
    return who ? `${who}: ${detail}` : detail;
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
    snapshot.name ??
    snapshot.email ??
    IDENTITY_KEYS.map((key) => snapshot[key]).find((v) => !isEmpty(v)) ??
    snapshot.recipient ??
    snapshot.role;
  return typeof id === "string" ? id : null;
}
