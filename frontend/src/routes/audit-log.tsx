import { useParams } from "react-router";
import { useTranslation } from "react-i18next";
import type { AuditEvent } from "../api/organization";
import {
  useOrganizationAuditEventsQuery,
  useOrganizationQuery,
} from "../api/organization.queries";
import { accessMessage } from "../lib/access-message";
import {
  auditActionLabel,
  auditActorLabel,
  auditDetailFields,
  auditSubject,
  auditTargetLabel,
  auditVisual,
  AUDIT_TONE_CLASSES,
} from "../lib/audit-event";
import { fullName, personInitials } from "../lib/name";
import { useWhenFormatter } from "../lib/format-when";
import { Avatar, Button, Card, Icon, Table, TopBar } from "../ui";
import * as React from "react";

const COLUMN_COUNT = 6;
const OPEN_ICON_SIZE = 18;

export default function AuditLog(): React.JSX.Element {
  const { t, i18n } = useTranslation();
  const { orgSlug } = useParams();
  // Guaranteed by the ":orgSlug" route segment this component mounts under.
  const slug = orgSlug!;

  const org = useOrganizationQuery(slug);
  const isAdmin = org.data?.role === "admin";
  const audit = useOrganizationAuditEventsQuery(slug, org.isSuccess);
  const events = audit.data?.pages.flatMap((page) => page.events) ?? [];
  // The server decides: an admin whose mandate was withdrawn reads the log
  // as a member does.
  const detailHidden = !isAdmin || events.some((event) => event.detailHidden);

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
  const [open, setOpen] = React.useState<string | null>(null);

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

      <div className="p-4 sm:p-8">
        {org.isError ? (
          <Card className="p-6">
            <p className="text-error text-[14px]">
              {accessMessage(org.error, t)}
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
            {org.isSuccess && detailHidden && (
              <p className="text-ink-soft mb-4 text-[13px]">
                {t("auditLog.detailHidden")}
              </p>
            )}
            <Card className="overflow-hidden">
              <Table>
                <Table.Head>
                  <Table.HeaderCell>
                    {t("auditLog.columns.when")}
                  </Table.HeaderCell>
                  <Table.HeaderCell>
                    {t("auditLog.columns.actor")}
                  </Table.HeaderCell>
                  <Table.HeaderCell>
                    {t("auditLog.columns.action")}
                  </Table.HeaderCell>
                  <Table.HeaderCell>
                    {t("auditLog.columns.target")}
                  </Table.HeaderCell>
                  <Table.HeaderCell>
                    {t("auditLog.columns.subject")}
                  </Table.HeaderCell>
                  <Table.HeaderCell scope="col" srOnly>
                    {t("auditLog.columns.details")}
                  </Table.HeaderCell>
                </Table.Head>
                <Table.Body>
                  {org.isPending || audit.isPending ? (
                    <Table.State colSpan={COLUMN_COUNT}>
                      {t("common.loading")}
                    </Table.State>
                  ) : events.length === 0 ? (
                    <Table.State colSpan={COLUMN_COUNT}>
                      {t("auditLog.empty")}
                    </Table.State>
                  ) : (
                    events.map((event) => (
                      <AuditRow
                        key={event.id}
                        event={event}
                        showDetails={isAdmin && !event.detailHidden}
                        expanded={open === event.id}
                        onToggle={() =>
                          setOpen((current) =>
                            current === event.id ? null : event.id,
                          )
                        }
                        dateFormatter={dateFormatter}
                        formatWhen={formatWhen}
                      />
                    ))
                  )}
                </Table.Body>
              </Table>
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

function AuditRow({
  event,
  showDetails,
  expanded,
  onToggle,
  dateFormatter,
  formatWhen,
}: Readonly<{
  event: AuditEvent;
  showDetails: boolean;
  expanded: boolean;
  onToggle: () => void;
  dateFormatter: Intl.DateTimeFormat;
  formatWhen: (iso: string) => string;
}>): React.JSX.Element {
  const { t } = useTranslation();
  const visual = auditVisual(event.action);
  const action = auditActionLabel(event.action, t);
  const subject = auditSubject(event, dateFormatter, t);
  const detailsId = `audit-event-details-${event.id}`;
  const fields = showDetails ? auditDetailFields(event, dateFormatter, t) : [];

  return (
    <>
      <Table.Row
        className="hover:bg-surface-2 cursor-pointer transition-colors"
        onClick={onToggle}
      >
        <Table.Cell>
          <span className="text-ink-soft text-[12.5px]">
            {formatWhen(event.occurredAt)}
          </span>
        </Table.Cell>
        <Table.Cell>
          <div className="flex items-center gap-2.5">
            <span
              className={[
                "inline-flex h-7 w-7 shrink-0 items-center justify-center rounded-full",
                AUDIT_TONE_CLASSES[visual.tone],
              ].join(" ")}
            >
              <Icon name={visual.icon} size={14} />
            </span>
            {event.actor && (
              <Avatar
                initials={personInitials(event.actor)}
                src={event.actor.avatarUri}
                fit="cover"
              />
            )}
            <span className="text-ink truncate">
              {event.detailHidden
                ? t("auditLog.hiddenActor")
                : event.actor
                  ? fullName(event.actor)
                  : (auditActorLabel(event.actorLabel, t) ??
                    t("auditLog.system"))}
            </span>
          </div>
        </Table.Cell>
        <Table.Cell>
          <span className="font-semibold">{action}</span>
        </Table.Cell>
        <Table.Cell className="text-ink-soft">
          {auditTargetLabel(event.targetType, t)}
        </Table.Cell>
        <Table.Cell>
          <span
            className="text-ink-soft block max-w-[24rem] truncate text-[12.5px]"
            title={subject ?? undefined}
          >
            {subject ?? t("auditLog.noSubject")}
          </span>
        </Table.Cell>
        <Table.Cell className="w-10 text-right">
          <button
            type="button"
            aria-expanded={expanded}
            aria-controls={detailsId}
            aria-label={t("auditLog.detailsToggle", {
              action,
              when: dateFormatter.format(new Date(event.occurredAt)),
            })}
            onClick={(clickEvent) => {
              clickEvent.stopPropagation();
              onToggle();
            }}
            className="text-muted hover:text-ink"
          >
            <Icon
              name={expanded ? "chevron_down" : "chevron_right"}
              size={OPEN_ICON_SIZE}
            />
          </button>
        </Table.Cell>
      </Table.Row>
      {expanded && (
        <tr id={detailsId}>
          <td
            colSpan={COLUMN_COUNT}
            className="border-line bg-surface-2 border-b px-5 py-4"
          >
            {showDetails ? (
              <dl className="grid content-start gap-x-8 gap-y-2 text-[13px] sm:grid-cols-[auto_1fr]">
                <dt className="text-muted">{t("auditLog.columns.when")}</dt>
                <dd>{dateFormatter.format(new Date(event.occurredAt))}</dd>
                <dt className="text-muted">{t("auditLog.columns.subject")}</dt>
                <dd className="break-words">
                  {subject ?? t("auditLog.noSubject")}
                </dd>
                {fields.map((field) => (
                  <React.Fragment key={field.key}>
                    <dt className="text-muted">{field.label}</dt>
                    <dd className="break-words">
                      {field.before === undefined
                        ? field.value
                        : `${field.before} → ${field.value}`}
                    </dd>
                  </React.Fragment>
                ))}
                {fields.length === 0 && (
                  <dd className="text-muted sm:col-span-2">
                    {t("auditLog.noDetails")}
                  </dd>
                )}
              </dl>
            ) : (
              <p className="text-ink-soft text-[13px]">
                {t("auditLog.detailHidden")}
              </p>
            )}
          </td>
        </tr>
      )}
    </>
  );
}
