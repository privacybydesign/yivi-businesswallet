import { useNavigate, useParams } from "react-router";
import { useTranslation } from "react-i18next";
import type { TFunction } from "i18next";
import { useMeQuery } from "../api/auth.queries";
import {
  useMemberAuditEventsQuery,
  useOrganizationMemberQuery,
  useOrganizationQuery,
  useRemoveMemberMutation,
  useRequestIdentificationMutation,
  useRequestVogMutation,
  useScreeningHistoryQuery,
  useUploadVogMutation,
} from "../api/organization.queries";
import type { AuditEvent } from "../api/organization";
import { ApiError } from "../api/http";
import { accessMessage } from "../lib/access-message";
import {
  identityStatusLabel,
  identityStatusTone,
  memberTypeLabel,
  requestableIdentity,
} from "../lib/identity-status";
import {
  requestableVog,
  screeningResultLabel,
  screeningResultTone,
  screeningStatusLabel,
  screeningStatusTone,
} from "../lib/screening-status";
import {
  auditActionLabel,
  auditSubject,
  auditVisual,
  AUDIT_TONE_CLASSES,
} from "../lib/audit-event";
import { fullName, personInitials } from "../lib/name";
import { useWhenFormatter } from "../lib/format-when";
import { Avatar, Button, Card, Icon, Modal, Tag, TopBar } from "../ui";
import * as React from "react";

const EYEBROW =
  "text-muted font-mono text-[11px] font-medium tracking-[0.06em] uppercase";

const CONFLICT_STATUS = 409;

function DetailRow({
  label,
  value,
  capitalize,
}: {
  label: string;
  value: string;
  capitalize?: boolean;
}): React.JSX.Element {
  return (
    <div className="flex items-center justify-between gap-3">
      <span className={EYEBROW}>{label}</span>
      <span
        className={[
          "text-ink text-[13px] font-medium",
          capitalize ? "capitalize" : "",
        ].join(" ")}
      >
        {value}
      </span>
    </div>
  );
}

function TimelineItem({
  event,
  dateFormatter,
  formatWhen,
  t,
  isLast,
}: {
  event: AuditEvent;
  dateFormatter: Intl.DateTimeFormat;
  formatWhen: (iso: string) => string;
  t: TFunction;
  isLast: boolean;
}): React.JSX.Element {
  const visual = auditVisual(event.action);
  const subject = auditSubject(event, dateFormatter);
  return (
    <li className="flex gap-3">
      <div className="flex flex-col items-center">
        <span
          className={[
            "inline-flex h-8 w-8 shrink-0 items-center justify-center rounded-full",
            AUDIT_TONE_CLASSES[visual.tone],
          ].join(" ")}
        >
          <Icon name={visual.icon} size={15} />
        </span>
        {!isLast && (
          <span className="bg-line mt-1 w-px flex-1" aria-hidden="true" />
        )}
      </div>
      <div className="flex-1 pb-5">
        <div className="text-ink text-[13.5px] font-semibold">
          {auditActionLabel(event.action, t)}
        </div>
        {subject && (
          <div className="text-ink-soft mt-0.5 text-[12.5px]">{subject}</div>
        )}
        <div className="text-muted mt-1 text-[11.5px]">
          {formatWhen(event.occurredAt)}
          {event.actor
            ? ` · ${t("memberDetail.timeline.by", { actor: fullName(event.actor) })}`
            : ""}
        </div>
      </div>
    </li>
  );
}

export default function MemberDetail(): React.JSX.Element {
  const { t, i18n } = useTranslation();
  const navigate = useNavigate();
  const { orgSlug, userId } = useParams();
  // Both are guaranteed by the ":orgSlug/members/:userId" route.
  const slug = orgSlug!;
  const id = userId!;
  const me = useMeQuery();
  const isSelf = me.data?.id === id;
  const org = useOrganizationQuery(slug);
  const isAdmin = org.data?.role === "admin";
  const memberQuery = useOrganizationMemberQuery(slug, id, isAdmin);
  const member = memberQuery.data;
  const notFound =
    memberQuery.error instanceof ApiError && memberQuery.error.status === 404;

  const timeline = useMemberAuditEventsQuery(slug, id, isAdmin);
  const events = timeline.data?.pages.flatMap((page) => page.events) ?? [];
  const dateFormatter = React.useMemo(
    () =>
      new Intl.DateTimeFormat(i18n.language, {
        dateStyle: "medium",
        timeStyle: "short",
        hour12: false,
      }),
    [i18n.language],
  );
  const formatWhen = useWhenFormatter();
  const removeMember = useRemoveMemberMutation(slug);
  const requestIdentification = useRequestIdentificationMutation(slug);
  const requestVog = useRequestVogMutation(slug);
  const uploadVog = useUploadVogMutation(slug, id);
  const vogHistory = useScreeningHistoryQuery(slug, id, isAdmin);
  const vogFileInput = React.useRef<HTMLInputElement>(null);
  const [confirmingOffboard, setConfirmingOffboard] = React.useState(false);

  const shell = (body: React.ReactNode): React.JSX.Element => (
    <>
      <TopBar title={t("memberDetail.title")} />
      <div className="p-8">{body}</div>
    </>
  );
  const message = (text: string, isError = false): React.JSX.Element => (
    <Card className="p-6">
      <p className={`text-[14px] ${isError ? "text-error" : "text-ink-soft"}`}>
        {text}
      </p>
    </Card>
  );

  if (org.isError) {
    return shell(message(accessMessage(org.error, t), true));
  }
  if (org.isPending) {
    return shell(message(t("common.loading")));
  }
  if (!isAdmin) {
    return shell(message(t("members.adminOnly")));
  }
  if (notFound) {
    return shell(message(t("memberDetail.notFound")));
  }
  if (memberQuery.isError) {
    return shell(message(accessMessage(memberQuery.error, t), true));
  }
  if (memberQuery.isPending) {
    return shell(message(t("common.loading")));
  }
  if (!member) {
    return shell(message(t("memberDetail.notFound")));
  }

  const subtitleParts = [member.jobTitle, member.departmentName].filter(
    Boolean,
  );
  const subtitle =
    subtitleParts.length > 0 ? subtitleParts.join(" · ") : undefined;

  return (
    <>
      <TopBar
        title={fullName(member)}
        subtitle={subtitle}
        actions={
          <>
            <Button
              variant="secondary"
              onClick={() => void navigate(`/${slug}/members/${id}/edit`)}
            >
              {t("common.edit")}
            </Button>
            <Button icon="add">{t("memberDetail.issue")}</Button>
          </>
        }
      />

      <div className="grid grid-cols-1 gap-5 p-8 lg:grid-cols-[1fr_320px]">
        <div className="flex flex-col gap-4">
          <Card className="p-6">
            <h2 className="text-[16px] font-semibold">
              {t("memberDetail.attestations")}
            </h2>
            <p className="text-ink-soft mt-2 text-[14px]">
              {t("memberDetail.attestationsPlaceholder")}
            </p>
          </Card>
          <Card className="p-6">
            <h2 className="text-[16px] font-semibold">
              {t("memberDetail.timeline.title")}
            </h2>
            {timeline.isError ? (
              <p className="text-error mt-2 text-[14px]">
                {t("memberDetail.timeline.error", {
                  message: timeline.error.message,
                })}
              </p>
            ) : timeline.isPending ? (
              <p className="text-ink-soft mt-2 text-[14px]">
                {t("common.loading")}
              </p>
            ) : events.length === 0 ? (
              <p className="text-ink-soft mt-2 text-[14px]">
                {t("memberDetail.timeline.empty")}
              </p>
            ) : (
              <>
                <ul className="mt-5">
                  {events.map((event, index) => (
                    <TimelineItem
                      key={event.id}
                      event={event}
                      dateFormatter={dateFormatter}
                      formatWhen={formatWhen}
                      t={t}
                      isLast={index === events.length - 1}
                    />
                  ))}
                </ul>
                {timeline.hasNextPage && (
                  <div className="flex justify-center">
                    <Button
                      variant="secondary"
                      onClick={() => void timeline.fetchNextPage()}
                      disabled={timeline.isFetchingNextPage}
                    >
                      {timeline.isFetchingNextPage
                        ? t("common.loading")
                        : t("memberDetail.timeline.loadMore")}
                    </Button>
                  </div>
                )}
              </>
            )}
          </Card>
          {member.vogStatus !== "not_required" && (
            <Card className="p-6">
              <h2 className="text-[16px] font-semibold">
                {t("memberDetail.vogHistory.title")}
              </h2>
              {vogHistory.isError ? (
                <p className="text-error mt-2 text-[14px]">
                  {t("memberDetail.vogHistory.error", {
                    message: vogHistory.error.message,
                  })}
                </p>
              ) : vogHistory.isPending ? (
                <p className="text-ink-soft mt-2 text-[14px]">
                  {t("common.loading")}
                </p>
              ) : vogHistory.data.length === 0 ? (
                <p className="text-ink-soft mt-2 text-[14px]">
                  {t("memberDetail.vogHistory.empty")}
                </p>
              ) : (
                <table className="mt-4 w-full text-[13px]">
                  <thead>
                    <tr className="text-ink-soft text-left text-[11.5px]">
                      <th className="pb-2 font-medium">
                        {t("memberDetail.vogHistory.columns.checkedAt")}
                      </th>
                      <th className="pb-2 font-medium">
                        {t("memberDetail.vogHistory.columns.method")}
                      </th>
                      <th className="pb-2 font-medium">
                        {t("memberDetail.vogHistory.columns.result")}
                      </th>
                      <th className="pb-2 font-medium">
                        {t("memberDetail.vogHistory.columns.validUntil")}
                      </th>
                    </tr>
                  </thead>
                  <tbody>
                    {vogHistory.data.map((record) => (
                      <tr key={record.id} className="border-line border-t">
                        <td className="py-2">
                          {dateFormatter.format(new Date(record.checkedAt))}
                        </td>
                        <td className="py-2">
                          {record.method === "pdf"
                            ? t("memberDetail.vogHistory.methodPdf")
                            : t("memberDetail.vogHistory.methodCredential")}
                        </td>
                        <td className="py-2">
                          <Tag tone={screeningResultTone(record.result)}>
                            {screeningResultLabel(record.result, t)}
                          </Tag>
                        </td>
                        <td className="py-2">
                          {record.validUntil
                            ? dateFormatter.format(new Date(record.validUntil))
                            : "—"}
                        </td>
                      </tr>
                    ))}
                  </tbody>
                </table>
              )}
            </Card>
          )}
        </div>

        <Card className="h-fit p-0">
          <div className="border-line flex flex-col items-center gap-3 border-b p-6">
            <Avatar
              initials={personInitials(member)}
              src={member.avatarUri}
              size="lg"
              fit="cover"
            />
            <div className="text-center">
              <div className="font-display text-[18px] font-bold">
                {fullName(member)}
              </div>
              <div className="text-ink-soft text-[12.5px]">{member.email}</div>
            </div>
            <div className="flex flex-wrap items-center justify-center gap-1.5">
              <Tag tone="green" dot>
                {t("memberDetail.active")}
              </Tag>
              <Tag tone={identityStatusTone(member.identityStatus)}>
                {identityStatusLabel(member.identityStatus, t)}
              </Tag>
              {member.vogStatus !== "not_required" && (
                <Tag tone={screeningStatusTone(member.vogStatus)}>
                  {screeningStatusLabel(member.vogStatus, t)}
                </Tag>
              )}
            </div>
          </div>
          <div className="flex flex-col gap-2.5 p-5">
            <DetailRow
              label={t("common.role")}
              value={member.role}
              capitalize
            />
            <DetailRow
              label={t("common.jobTitle")}
              value={member.jobTitle ?? "—"}
            />
            <DetailRow
              label={t("common.department")}
              value={member.departmentName ?? "—"}
            />
            <DetailRow label={t("common.phone")} value={member.phone ?? "—"} />
            <DetailRow
              label={t("memberDetail.memberType")}
              value={memberTypeLabel(member.memberType, t)}
            />
            {member.memberType === "external" && (
              <DetailRow
                label={t("memberDetail.externalOrganisation")}
                value={member.externalOrganisation ?? "—"}
              />
            )}
            <DetailRow
              label={t("memberDetail.identifiedOn")}
              value={
                member.identityVerifiedAt
                  ? dateFormatter.format(new Date(member.identityVerifiedAt))
                  : t("memberDetail.never")
              }
            />
            {member.identityDueAt && (
              <DetailRow
                label={t("memberDetail.identityDueOn")}
                value={dateFormatter.format(new Date(member.identityDueAt))}
              />
            )}
            {member.vogStatus !== "not_required" && (
              <DetailRow
                label={t("memberDetail.vogValidUntil")}
                value={
                  member.vogValidUntil
                    ? dateFormatter.format(new Date(member.vogValidUntil))
                    : t("memberDetail.vogNone")
                }
              />
            )}
          </div>
          <div className="border-line flex flex-col gap-2 border-t p-4">
            {requestableIdentity({
              status: "active",
              identityStatus: member.identityStatus,
            }) ? (
              <>
                <Button
                  variant="secondary"
                  icon="personal"
                  className="w-full"
                  loading={requestIdentification.isPending}
                  onClick={() =>
                    requestIdentification.mutate({ userIds: [id] })
                  }
                >
                  {t("memberDetail.requestIdentification")}
                </Button>
                <p className="text-ink-soft text-[12px]">
                  {t("memberDetail.requestIdentificationHint")}
                </p>
              </>
            ) : (
              <p className="text-ink-soft text-[12px]">
                {t("memberDetail.identityRequested")}
              </p>
            )}
            {requestableVog({
              status: "active",
              vogStatus: member.vogStatus,
            }) ? (
              <>
                <Button
                  variant="secondary"
                  icon="valid"
                  className="w-full"
                  loading={requestVog.isPending}
                  onClick={() => requestVog.mutate({ userIds: [id] })}
                >
                  {t("memberDetail.requestVog")}
                </Button>
                <p className="text-ink-soft text-[12px]">
                  {t("memberDetail.requestVogHint")}
                </p>
              </>
            ) : (
              member.vogStatus !== "not_required" && (
                <p className="text-ink-soft text-[12px]">
                  {t("memberDetail.vogRequested")}
                </p>
              )
            )}
            <input
              ref={vogFileInput}
              type="file"
              accept="application/pdf"
              className="hidden"
              onChange={(event) => {
                const file = event.target.files?.[0];
                event.target.value = "";
                if (file) uploadVog.mutate(file);
              }}
            />
            <Button
              variant="secondary"
              icon="add"
              className="w-full"
              loading={uploadVog.isPending}
              onClick={() => vogFileInput.current?.click()}
            >
              {t("memberDetail.uploadVog")}
            </Button>
            <p className="text-ink-soft text-[12px]">
              {t("memberDetail.uploadVogHint")}
            </p>
            {uploadVog.data && (
              <p
                className={`text-[12px] ${uploadVog.data.result === "valid" ? "text-success" : "text-error"}`}
              >
                {screeningResultLabel(uploadVog.data.result, t)}
              </p>
            )}
            <Button
              variant="secondary"
              icon="email"
              className="w-full"
              onClick={() =>
                void navigate(
                  `/${slug}/qerds/compose?to=${encodeURIComponent(member.email)}`,
                )
              }
            >
              {t("memberDetail.sendMessage")}
            </Button>
            {!isSelf && (
              <Button
                variant="danger"
                icon="logout"
                className="w-full"
                onClick={() => setConfirmingOffboard(true)}
              >
                {t("memberDetail.offboard")}
              </Button>
            )}
          </div>
        </Card>
      </div>

      {confirmingOffboard && (
        <Modal
          title={t("memberDetail.offboardConfirm.title")}
          closeLabel={t("common.cancel")}
          onClose={() => setConfirmingOffboard(false)}
          footer={
            <>
              <Button
                variant="secondary"
                onClick={() => setConfirmingOffboard(false)}
              >
                {t("common.cancel")}
              </Button>
              <Button
                variant="danger"
                icon="logout"
                loading={removeMember.isPending}
                onClick={() =>
                  removeMember.mutate(
                    { userId: id },
                    { onSuccess: () => void navigate(`/${slug}/members`) },
                  )
                }
              >
                {t("memberDetail.offboardConfirm.confirm")}
              </Button>
            </>
          }
        >
          <p className="text-ink-soft text-[14px]">
            {t("memberDetail.offboardConfirm.body", { name: fullName(member) })}
          </p>
          {removeMember.isError && (
            <p
              role="alert"
              className="rounded-yivi bg-error-bg text-error mt-3 px-3 py-2 text-[13px]"
            >
              {removeMember.error instanceof ApiError &&
              removeMember.error.status === CONFLICT_STATUS
                ? t("memberDetail.offboardConfirm.lastAdmin")
                : t("memberDetail.offboardConfirm.error", {
                    message: removeMember.error.message,
                  })}
            </p>
          )}
        </Modal>
      )}
    </>
  );
}
