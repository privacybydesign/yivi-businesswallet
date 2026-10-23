import type { TFunction } from "i18next";
import type { AuditEvent } from "../api/organization";
import type { en } from "../i18n/locales/en";
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

type AuditVisual = { icon: IconName; tone: AuditTone };

// An action's entry sits under its prefix, then its name:
// "identity_proofing.approved" is ACTION_VISUAL.identity_proofing.approved.
type ByAction<T> = Record<string, Record<string, T>>;

function lookupAction<T>(table: ByAction<T>, action: string): T | undefined {
  const dot = action.indexOf(".");
  const prefix = action.slice(0, dot);
  const name = action.slice(dot + 1);
  if (dot < 0 || !Object.hasOwn(table, prefix)) return undefined;
  const names = table[prefix];
  return Object.hasOwn(names, name) ? names[name] : undefined;
}

const ACTION_VISUAL: ByAction<AuditVisual> = {
  organization: {
    created: { icon: "add", tone: "green" },
    updated: { icon: "edit", tone: "blue" },
  },
  membership: {
    invited: { icon: "email", tone: "amber" },
    invite_resent: { icon: "email", tone: "amber" },
    invite_revoked: { icon: "close", tone: "red" },
    invite_updated: { icon: "edit", tone: "blue" },
    accepted: { icon: "valid", tone: "green" },
    accept_rejected: { icon: "warning", tone: "amber" },
    declined: { icon: "close", tone: "slate" },
    revoked: { icon: "close", tone: "red" },
    role_changed: { icon: "settings", tone: "blue" },
    expired: { icon: "time", tone: "slate" },
    type_changed: { icon: "edit", tone: "blue" },
    identity_requested: { icon: "personal", tone: "amber" },
    identity_reverified: { icon: "valid", tone: "green" },
    identity_reverify_rejected: { icon: "warning", tone: "amber" },
    identity_reminder_sent: { icon: "email", tone: "amber" },
    identity_overdue: { icon: "warning", tone: "red" },
    vog_requested: { icon: "personal", tone: "amber" },
    vog_checked: { icon: "valid", tone: "green" },
    vog_rejected: { icon: "warning", tone: "amber" },
    vog_mismatch: { icon: "warning", tone: "amber" },
    vog_insufficient_scope: { icon: "warning", tone: "amber" },
    vog_reminder_sent: { icon: "email", tone: "amber" },
    vog_expired: { icon: "warning", tone: "red" },
  },
  identity: {
    settings_updated: { icon: "settings", tone: "blue" },
  },
  screening: {
    settings_updated: { icon: "settings", tone: "blue" },
  },
  mandate: {
    granted: { icon: "valid", tone: "violet" },
    revoked: { icon: "close", tone: "red" },
  },
  department: {
    created: { icon: "add", tone: "green" },
    updated: { icon: "edit", tone: "blue" },
    deleted: { icon: "delete", tone: "red" },
  },
  user: {
    identity_changed: { icon: "personal", tone: "blue" },
    identity_review_required: { icon: "warning", tone: "amber" },
    identity_review_approved: { icon: "valid", tone: "green" },
    identity_review_rejected: { icon: "close", tone: "red" },
    purged: { icon: "delete", tone: "red" },
  },
  attestation: {
    schema_created: { icon: "add", tone: "green" },
    held_received: { icon: "add", tone: "green" },
    held_status_changed: { icon: "warning", tone: "amber" },
    schema_updated: { icon: "edit", tone: "blue" },
    schema_deleted: { icon: "delete", tone: "red" },
    template_created: { icon: "add", tone: "green" },
    template_updated: { icon: "edit", tone: "blue" },
    template_deleted: { icon: "delete", tone: "red" },
    issued: { icon: "valid", tone: "violet" },
    claimed: { icon: "valid", tone: "green" },
    revoked: { icon: "close", tone: "red" },
    offer_cancelled: { icon: "close", tone: "amber" },
    key_added: { icon: "add", tone: "green" },
    key_suspended: { icon: "warning", tone: "amber" },
    key_revoked: { icon: "close", tone: "red" },
    offer_accepted: { icon: "valid", tone: "green" },
    offer_declined: { icon: "close", tone: "slate" },
  },
  email: {
    settings_updated: { icon: "settings", tone: "blue" },
    template_updated: { icon: "email", tone: "blue" },
    template_reset: { icon: "email", tone: "amber" },
  },
  notification: {
    settings_updated: { icon: "settings", tone: "blue" },
  },
  slack: {
    settings_updated: { icon: "settings", tone: "blue" },
  },
  msteams: {
    settings_updated: { icon: "settings", tone: "blue" },
  },
  provisioning: {
    settings_updated: { icon: "settings", tone: "blue" },
    run_completed: { icon: "valid", tone: "green" },
    run_failed: { icon: "warning", tone: "red" },
  },
  csc: {
    settings_updated: { icon: "settings", tone: "blue" },
  },
  signing: {
    credential_linked: { icon: "settings", tone: "blue" },
    requested: { icon: "settings", tone: "blue" },
    signed: { icon: "edit", tone: "blue" },
    completed: { icon: "valid", tone: "green" },
    delivered: { icon: "email", tone: "green" },
    failed: { icon: "warning", tone: "red" },
    declined: { icon: "close", tone: "slate" },
  },
  presentation: {
    requested: { icon: "scan_qrcode", tone: "amber" },
    org_selected: { icon: "personal", tone: "blue" },
    completed: { icon: "valid", tone: "green" },
    denied: { icon: "close", tone: "red" },
    expired: { icon: "time", tone: "slate" },
    request_received: { icon: "scan_qrcode", tone: "amber" },
    request_sent: { icon: "email", tone: "blue" },
    response_received: { icon: "valid", tone: "green" },
    request_failed: { icon: "warning", tone: "red" },
  },
  identity_proofing: {
    flow_created: { icon: "add", tone: "green" },
    flows_configured: { icon: "settings", tone: "blue" },
    flow_version_created: { icon: "edit", tone: "blue" },
    flow_version_activated: { icon: "valid", tone: "blue" },
    requested: { icon: "email", tone: "amber" },
    session_created: { icon: "time", tone: "blue" },
    session_started: { icon: "scan_qrcode", tone: "blue" },
    session_handover: { icon: "scan_qrcode", tone: "slate" },
    session_ended: { icon: "time", tone: "amber" },
    session_cancelled: { icon: "close", tone: "slate" },
    session_purged: { icon: "delete", tone: "red" },
    result_read: { icon: "view", tone: "slate" },
    device_claimed: { icon: "scan_qrcode", tone: "blue" },
    device_handed_over: { icon: "scan_qrcode", tone: "slate" },
    handover_issued: { icon: "scan_qrcode", tone: "slate" },
    handover_claim_failed: { icon: "warning", tone: "amber" },
    access_denied: { icon: "warning", tone: "red" },
    approved: { icon: "valid", tone: "green" },
    rejected: { icon: "close", tone: "red" },
    needs_review: { icon: "warning", tone: "amber" },
    review_decided: { icon: "valid", tone: "blue" },
    data_exported: { icon: "view", tone: "slate" },
    flow_kind_configured: { icon: "edit", tone: "blue" },
    customer_created: { icon: "add", tone: "green" },
    customer_updated: { icon: "edit", tone: "blue" },
    customer_flows_configured: { icon: "settings", tone: "blue" },
    customer_removed: { icon: "delete", tone: "red" },
    api_key_created: { icon: "add", tone: "green" },
    api_key_revoked: { icon: "close", tone: "red" },
    webhook_configured: { icon: "settings", tone: "blue" },
    webhook_secret_rotated: { icon: "lock", tone: "amber" },
    webhook_removed: { icon: "delete", tone: "red" },
    paused: { icon: "warning", tone: "amber" },
    flow_hosted_configured: { icon: "settings", tone: "blue" },
    flow_diplomas_configured: { icon: "settings", tone: "blue" },
    diploma_added: { icon: "add", tone: "green" },
    diploma_rejected: { icon: "close", tone: "red" },
    flow_address_configured: { icon: "settings", tone: "blue" },
    address_added: { icon: "add", tone: "green" },
    flow_employment_configured: { icon: "settings", tone: "blue" },
    employment_added: { icon: "add", tone: "green" },
    employment_rejected: { icon: "close", tone: "red" },
    resumed: { icon: "valid", tone: "green" },
  },
};

const DEFAULT_VISUAL: AuditVisual = {
  icon: "info",
  tone: "slate",
};

export function auditVisual(action: string): AuditVisual {
  return lookupAction(ACTION_VISUAL, action) ?? DEFAULT_VISUAL;
}

const API_KEY_ACTOR_PREFIX = "api_key:";
// hostedSubjectActor in backend/internal/proofing/hosted.go: the subject of a
// hosted link, who has no account.
const HOSTED_LINK_ACTOR = "hosted_link";
// adminActorLabel in backend/internal/proofing/service.go: an admin who acted
// on a member's request, unnamed in the member's timeline.
const ADMIN_ACTOR = "admin";
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
  if (label === ADMIN_ACTOR) {
    return t("auditLog.adminActor");
  }
  if (label.startsWith(SUBJECT_APP_ACTOR_PREFIX)) {
    return proofingMethodLabel(label.slice(SUBJECT_APP_ACTOR_PREFIX.length), t);
  }
  return label;
}

type ActionLabelKey = keyof (typeof en)["auditLog"]["actions"];
type TargetLabelKey = keyof (typeof en)["auditLog"]["targets"];

const ACTION_LABEL: ByAction<ActionLabelKey> = {
  organization: {
    created: "orgCreated",
    updated: "orgUpdated",
    deleted: "orgDeleted",
  },
  membership: {
    invited: "memberInvited",
    invite_resent: "inviteResent",
    invite_revoked: "inviteRevoked",
    invite_updated: "inviteUpdated",
    accepted: "inviteAccepted",
    accept_rejected: "acceptRejected",
    declined: "inviteDeclined",
    revoked: "memberRevoked",
    role_changed: "roleChanged",
    expired: "inviteExpired",
    type_changed: "memberTypeChanged",
    identity_requested: "identityRequested",
    identity_reverified: "identityReverified",
    identity_reverify_rejected: "identityReverifyRejected",
    identity_reminder_sent: "identityReminderSent",
    identity_overdue: "identityOverdue",
    vog_requested: "vogRequested",
    vog_checked: "vogChecked",
    vog_rejected: "vogRejected",
    vog_mismatch: "vogMismatch",
    vog_insufficient_scope: "vogInsufficientScope",
    vog_reminder_sent: "vogReminderSent",
    vog_expired: "vogExpired",
  },
  identity: {
    settings_updated: "identitySettingsUpdated",
  },
  screening: {
    settings_updated: "screeningSettingsUpdated",
  },
  mandate: {
    granted: "mandateGranted",
    revoked: "mandateRevoked",
  },
  department: {
    created: "deptCreated",
    updated: "deptUpdated",
    deleted: "deptDeleted",
  },
  user: {
    identity_changed: "identityChanged",
    identity_review_required: "identityReviewRequired",
    identity_review_approved: "identityReviewApproved",
    identity_review_rejected: "identityReviewRejected",
    purged: "userPurged",
  },
  qerds: {
    message_sent: "qerdsMessageSent",
    message_received: "qerdsMessageReceived",
    address_provisioned: "qerdsAddressProvisioned",
    address_default_changed: "qerdsAddressDefaultChanged",
    address_deleted: "qerdsAddressDeleted",
    contact_added: "qerdsContactAdded",
    contact_deleted: "qerdsContactDeleted",
  },
  postguard: {
    key_set: "postguardKeySet",
    key_removed: "postguardKeyRemoved",
    encryption_key_set: "postguardEncryptionKeySet",
    encryption_key_removed: "postguardEncryptionKeyRemoved",
    file_sent: "postguardFileSent",
    notification_delivery_set: "postguardNotificationDeliverySet",
  },
  wallet: {
    opened: "walletOpened",
    bootstrapped: "walletBootstrapped",
    suspended: "walletSuspended",
    revoked: "walletRevoked",
    representation_claimed: "representationClaimed",
    representation_revoked: "representationRevoked",
  },
  kvk: {
    registration_validated: "kvkRegistrationValidated",
    registration_not_validated: "kvkRegistrationNotValidated",
  },
  attestation: {
    schema_created: "attestationSchemaCreated",
    schema_updated: "attestationSchemaUpdated",
    schema_deleted: "attestationSchemaDeleted",
    template_created: "attestationTemplateCreated",
    template_updated: "attestationTemplateUpdated",
    template_deleted: "attestationTemplateDeleted",
    issued: "attestationIssued",
    claimed: "attestationClaimed",
    revoked: "attestationRevoked",
    offer_cancelled: "attestationOfferCancelled",
    key_added: "attestationKeyAdded",
    key_suspended: "attestationKeySuspended",
    key_revoked: "attestationKeyRevoked",
    held_received: "attestationHeldReceived",
    held_status_changed: "attestationHeldStatusChanged",
    held_deleted: "attestationHeldDeleted",
    offer_accepted: "attestationOfferAccepted",
    offer_declined: "attestationOfferDeclined",
  },
  email: {
    settings_updated: "emailSettingsUpdated",
    template_updated: "emailTemplateUpdated",
    template_reset: "emailTemplateReset",
  },
  issuer: {
    settings_updated: "issuerSettingsUpdated",
  },
  theme: {
    settings_updated: "themeSettingsUpdated",
  },
  onboarding: {
    settings_updated: "onboardingSettingsUpdated",
  },
  notification: {
    settings_updated: "notificationSettingsUpdated",
  },
  slack: {
    settings_updated: "slackSettingsUpdated",
  },
  msteams: {
    settings_updated: "teamsSettingsUpdated",
  },
  provisioning: {
    settings_updated: "provisioningSettingsUpdated",
    run_completed: "provisioningRunCompleted",
    run_failed: "provisioningRunFailed",
  },
  csc: {
    settings_updated: "cscSettingsUpdated",
  },
  signing: {
    credential_linked: "signingCredentialLinked",
    requested: "signingRequested",
    signed: "signingSigned",
    completed: "signingCompleted",
    delivered: "signingDelivered",
    failed: "signingFailed",
    declined: "signingDeclined",
  },
  presentation: {
    requested: "presentationRequested",
    org_selected: "presentationOrgSelected",
    completed: "presentationCompleted",
    denied: "presentationDenied",
    expired: "presentationExpired",
    request_received: "presentationRequestReceived",
    request_sent: "presentationRequestSent",
    response_received: "presentationResponseReceived",
    request_failed: "presentationRequestFailed",
  },
  identity_proofing: {
    flow_created: "identityProofingFlowCreated",
    flows_configured: "identityProofingFlowsConfigured",
    flow_version_created: "identityProofingFlowVersionCreated",
    flow_version_activated: "identityProofingFlowVersionActivated",
    requested: "identityProofingRequested",
    session_created: "identityProofingSessionCreated",
    session_started: "identityProofingSessionStarted",
    session_handover: "identityProofingSessionHandover",
    session_ended: "identityProofingSessionEnded",
    session_cancelled: "identityProofingSessionCancelled",
    session_purged: "identityProofingSessionPurged",
    result_read: "identityProofingResultRead",
    device_claimed: "identityProofingDeviceClaimed",
    device_handed_over: "identityProofingDeviceHandedOver",
    handover_issued: "identityProofingHandoverIssued",
    handover_claim_failed: "identityProofingHandoverClaimFailed",
    access_denied: "identityProofingAccessDenied",
    approved: "identityProofingApproved",
    rejected: "identityProofingRejected",
    needs_review: "identityProofingNeedsReview",
    review_decided: "identityProofingReviewDecided",
    data_exported: "identityProofingDataExported",
    flow_kind_configured: "identityProofingFlowKindConfigured",
    customer_created: "identityProofingCustomerCreated",
    customer_updated: "identityProofingCustomerUpdated",
    customer_flows_configured: "identityProofingCustomerFlowsConfigured",
    customer_removed: "identityProofingCustomerRemoved",
    api_key_created: "identityProofingApiKeyCreated",
    api_key_revoked: "identityProofingApiKeyRevoked",
    webhook_configured: "identityProofingWebhookConfigured",
    webhook_secret_rotated: "identityProofingWebhookSecretRotated",
    webhook_removed: "identityProofingWebhookRemoved",
    flow_hosted_configured: "identityProofingFlowHostedConfigured",
    flow_diplomas_configured: "identityProofingFlowDiplomasConfigured",
    diploma_added: "identityProofingDiplomaAdded",
    diploma_rejected: "identityProofingDiplomaRejected",
    flow_address_configured: "identityProofingFlowAddressConfigured",
    address_added: "identityProofingAddressAdded",
    flow_employment_configured: "identityProofingFlowEmploymentConfigured",
    employment_added: "identityProofingEmploymentAdded",
    employment_rejected: "identityProofingEmploymentRejected",
    paused: "identityProofingPaused",
    resumed: "identityProofingResumed",
  },
};

const TARGET_LABEL: Record<string, TargetLabelKey> = {
  organization: "organization",
  membership: "member",
  department: "department",
  mandate: "mandate",
  user: "user",
  qerds_message: "qerdsMessage",
  qerds_address: "qerdsAddress",
  qerds_contact: "qerdsContact",
  wallet_instance: "walletInstance",
  wallet_representation: "walletRepresentation",
  kvk_registration: "kvkRegistration",
  postguard_key: "postguardKey",
  postguard_encryption_key: "postguardEncryptionKey",
  postguard_file: "postguardFile",
  postguard_settings: "postguardSettings",
  attestation_schema: "attestationSchema",
  attestation_template: "attestationTemplate",
  issued_attestation: "issuedAttestation",
  attestation_key: "attestationKey",
  held_attestation: "heldAttestation",
  credential_offer: "credentialOffer",
  org_email_settings: "orgEmailSettings",
  org_email_template: "orgEmailTemplate",
  org_issuer_settings: "orgIssuerSettings",
  org_theme_settings: "orgThemeSettings",
  org_onboarding_attestations: "orgOnboardingAttestations",
  org_notification_settings: "orgNotificationSettings",
  org_identity_settings: "orgIdentitySettings",
  org_screening_settings: "orgScreeningSettings",
  org_slack_settings: "orgSlackSettings",
  org_teams_settings: "orgTeamsSettings",
  org_provisioning_settings: "orgProvisioningSettings",
  org_csc_settings: "orgCscSettings",
  signing_credentials: "signingCredentials",
  signing_requests: "signingRequests",
  presentation_transaction: "presentationTransaction",
  outbound_presentation_request: "outboundPresentationRequest",
  org_identity_proofing_settings: "orgIdentityProofingSettings",
  identity_proofing_flow: "identityProofingFlow",
  identity_proofing_request: "identityProofingRequest",
  identity_proofing_customer: "identityProofingCustomer",
};

export function auditActionLabel(action: string, t: TFunction): string {
  const key = lookupAction(ACTION_LABEL, action);
  return key ? t(`auditLog.actions.${key}`) : action;
}

export function auditTargetLabel(targetType: string, t: TFunction): string {
  return Object.hasOwn(TARGET_LABEL, targetType)
    ? t(`auditLog.targets.${TARGET_LABEL[targetType]}`)
    : targetType;
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

function changedValue(
  key: string,
  value: unknown,
  dateFormatter: Intl.DateTimeFormat,
  t: TFunction,
): string {
  if (key === "errorCode" && typeof value === "string") {
    return proofingRejectionReason(value, t);
  }
  if (key === "method" && typeof value === "string") {
    return proofingMethodLabel(value, t);
  }
  return fieldValue(value, dateFormatter);
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
        const value = changedValue(key, after[key], dateFormatter, t);
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
