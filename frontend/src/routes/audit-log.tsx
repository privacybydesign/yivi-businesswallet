import { useParams } from "react-router";
import { useTranslation } from "react-i18next";
import {
  useOrganizationAuditEventsQuery,
  useOrganizationQuery,
} from "../api/organization.queries";
import type { AuditEvent } from "../api/organization";
import { accessMessage } from "../lib/access-message";
import { fullName } from "../lib/name";
import { Button, Card, Icon, TopBar } from "../ui";
import type { IconName } from "../ui";
import * as React from "react";

type Tone = "green" | "blue" | "red" | "amber" | "violet" | "slate";

const TONE_CLASSES: Record<Tone, string> = {
  green: "bg-success-bg text-success",
  blue: "bg-highlight text-link",
  red: "bg-error-bg text-error",
  amber: "bg-warning-bg text-warning-fg",
  violet: "bg-[#ECE3F4] text-[#5B3B85]",
  slate: "bg-[#E4E2DF] text-ink",
};

const ACTION_VISUAL: Record<string, { icon: IconName; tone: Tone }> = {
  "organization.created": { icon: "add", tone: "green" },
  "organization.updated": { icon: "edit", tone: "blue" },
  "membership.invited": { icon: "email", tone: "amber" },
  "membership.invite_resent": { icon: "email", tone: "amber" },
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

const DEFAULT_VISUAL: { icon: IconName; tone: Tone } = {
  icon: "info",
  tone: "slate",
};

function fieldValue(value: unknown): string {
  if (value === null || value === undefined) return "—";
  return typeof value === "string" ? value : JSON.stringify(value);
}

// The human-readable detail for a row, derived from the uniform {before, after}
// metadata: an update diffs changed fields ("old → new"); a create/delete shows
// the snapshot's identifying field.
function subjectOf(event: AuditEvent): string | null {
  const { before, after } = event.metadata as {
    before?: Record<string, unknown> | null;
    after?: Record<string, unknown> | null;
  };

  if (before && after) {
    const keys = [...new Set([...Object.keys(before), ...Object.keys(after)])];
    const changes = keys
      .filter((key) => before[key] !== after[key])
      .map((key) => `${fieldValue(before[key])} → ${fieldValue(after[key])}`);
    return changes.length > 0 ? changes.join(", ") : null;
  }

  const snapshot = after ?? before;
  if (!snapshot) return null;
  const id = snapshot.name ?? snapshot.email ?? snapshot.role;
  return typeof id === "string" ? id : null;
}

const TH_CLASS =
  "border-line bg-surface-2 text-muted border-b px-3.5 py-2.5 text-left font-mono text-[11px] font-medium tracking-[0.06em] uppercase";
const TD_CLASS = "border-line border-b px-3.5 py-3";
const COLUMN_COUNT = 5;

export default function AuditLog(): React.JSX.Element {
  const { t, i18n } = useTranslation();
  const { orgSlug } = useParams();
  // Guaranteed by the ":orgSlug" route segment this component mounts under.
  const slug = orgSlug!;

  const org = useOrganizationQuery(slug);
  const isAdmin = org.data?.role === "admin";
  const audit = useOrganizationAuditEventsQuery(slug, isAdmin);
  const events = audit.data?.pages.flatMap((page) => page.events) ?? [];

  const dateFormatter = React.useMemo(
    () =>
      new Intl.DateTimeFormat(i18n.language, {
        dateStyle: "medium",
        timeStyle: "short",
      }),
    [i18n.language],
  );

  const actionLabel = (action: string): string => {
    switch (action) {
      case "organization.created":
        return t("auditLog.actions.orgCreated");
      case "organization.updated":
        return t("auditLog.actions.orgUpdated");
      case "membership.invited":
        return t("auditLog.actions.memberInvited");
      case "membership.invite_resent":
        return t("auditLog.actions.inviteResent");
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
  };

  const targetLabel = (targetType: string): string => {
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
  };

  return (
    <>
      <TopBar
        title={t("auditLog.title")}
        subtitle={t("auditLog.subtitle")}
        actions={
          <>
            <Button
              variant="secondary"
              icon="filter"
              disabled
              title={t("common.comingSoon")}
            >
              {t("auditLog.filter")}
            </Button>
            <Button
              variant="secondary"
              icon="arrow_front"
              disabled
              title={t("common.comingSoon")}
            >
              {t("auditLog.export")}
            </Button>
          </>
        }
      />

      <div className="p-8">
        {org.isError ? (
          <Card className="p-6">
            <p className="text-error text-[14px]">
              {accessMessage(org.error, t)}
            </p>
          </Card>
        ) : !org.isPending && !isAdmin ? (
          <Card className="p-6">
            <p className="text-ink-soft text-[14px]">
              {t("auditLog.adminOnly")}
            </p>
          </Card>
        ) : audit.isError ? (
          <Card className="p-6">
            <p className="text-error text-[14px]">
              {t("auditLog.loadError", { message: audit.error.message })}
            </p>
          </Card>
        ) : (
          <>
            <Card className="overflow-hidden">
              <table className="w-full border-collapse text-[13.5px]">
                <thead>
                  <tr>
                    <th className={TH_CLASS}>{t("auditLog.columnWhen")}</th>
                    <th className={TH_CLASS}>{t("auditLog.columnActor")}</th>
                    <th className={TH_CLASS}>{t("auditLog.columnAction")}</th>
                    <th className={TH_CLASS}>{t("auditLog.columnTarget")}</th>
                    <th className={TH_CLASS}>{t("auditLog.columnSubject")}</th>
                  </tr>
                </thead>
                <tbody>
                  {org.isPending || audit.isPending ? (
                    <tr>
                      <td
                        className="text-ink-soft px-3.5 py-3"
                        colSpan={COLUMN_COUNT}
                      >
                        {t("common.loading")}
                      </td>
                    </tr>
                  ) : events.length === 0 ? (
                    <tr>
                      <td
                        className="text-ink-soft px-3.5 py-3"
                        colSpan={COLUMN_COUNT}
                      >
                        {t("auditLog.empty")}
                      </td>
                    </tr>
                  ) : (
                    events.map((event) => {
                      const visual =
                        ACTION_VISUAL[event.action] ?? DEFAULT_VISUAL;
                      const subject = subjectOf(event);
                      return (
                        <tr key={event.id}>
                          <td className={TD_CLASS}>
                            <span className="text-ink-soft text-[12.5px]">
                              {dateFormatter.format(new Date(event.occurredAt))}
                            </span>
                          </td>
                          <td className={TD_CLASS}>
                            <div className="flex items-center gap-2.5">
                              <span
                                className={[
                                  "inline-flex h-7 w-7 shrink-0 items-center justify-center rounded-full",
                                  TONE_CLASSES[visual.tone],
                                ].join(" ")}
                              >
                                <Icon name={visual.icon} size={14} />
                              </span>
                              <span className="text-ink truncate">
                                {event.actor
                                  ? fullName(event.actor)
                                  : t("auditLog.system")}
                              </span>
                            </div>
                          </td>
                          <td className={TD_CLASS}>
                            <span className="font-semibold">
                              {actionLabel(event.action)}
                            </span>
                          </td>
                          <td className={`${TD_CLASS} text-ink-soft`}>
                            {targetLabel(event.targetType)}
                          </td>
                          <td className={TD_CLASS}>
                            <span className="text-ink-soft text-[12.5px]">
                              {subject ?? t("auditLog.noSubject")}
                            </span>
                          </td>
                        </tr>
                      );
                    })
                  )}
                </tbody>
              </table>
            </Card>

            {audit.hasNextPage && (
              <div className="mt-4 flex justify-center">
                <Button
                  variant="secondary"
                  onClick={() => void audit.fetchNextPage()}
                  disabled={audit.isFetchingNextPage}
                >
                  {audit.isFetchingNextPage
                    ? t("common.loading")
                    : t("auditLog.loadMore")}
                </Button>
              </div>
            )}
          </>
        )}
      </div>
    </>
  );
}
