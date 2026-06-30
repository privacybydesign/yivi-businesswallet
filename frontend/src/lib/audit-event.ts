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
  "membership.declined": { icon: "close", tone: "slate" },
  "membership.revoked": { icon: "close", tone: "red" },
  "membership.role_changed": { icon: "settings", tone: "blue" },
  "membership.expired": { icon: "time", tone: "slate" },
  "department.created": { icon: "add", tone: "green" },
  "department.updated": { icon: "edit", tone: "blue" },
  "department.deleted": { icon: "delete", tone: "red" },
  "user.identity_changed": { icon: "personal", tone: "blue" },
  "user.identity_review_required": { icon: "warning", tone: "amber" },
  "user.identity_review_resolved": { icon: "valid", tone: "green" },
  "user.purged": { icon: "delete", tone: "red" },
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
    case "membership.invited":
      return t("auditLog.actions.memberInvited");
    case "membership.invite_resent":
      return t("auditLog.actions.inviteResent");
    case "membership.invite_revoked":
      return t("auditLog.actions.inviteRevoked");
    case "membership.accepted":
      return t("auditLog.actions.inviteAccepted");
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
    case "user.identity_review_resolved":
      return t("auditLog.actions.identityReviewResolved");
    case "user.purged":
      return t("auditLog.actions.userPurged");
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
  const id = snapshot.name ?? snapshot.email ?? snapshot.role;
  return typeof id === "string" ? id : null;
}
