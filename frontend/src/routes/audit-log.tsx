import { useParams } from "react-router";
import { useTranslation } from "react-i18next";
import {
  useOrganizationAuditEventsQuery,
  useOrganizationQuery,
} from "../api/organization.queries";
import { accessMessage } from "../lib/access-message";
import {
  auditActionLabel,
  auditSubject,
  auditTargetLabel,
  auditVisual,
  AUDIT_TONE_CLASSES,
} from "../lib/audit-event";
import { fullName } from "../lib/name";
import { Button, Card, Icon, Table, TopBar } from "../ui";
import * as React from "react";

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
                    events.map((event) => {
                      const visual = auditVisual(event.action);
                      const subject = auditSubject(event, dateFormatter);
                      return (
                        <Table.Row key={event.id}>
                          <Table.Cell>
                            <span className="text-ink-soft text-[12.5px]">
                              {dateFormatter.format(new Date(event.occurredAt))}
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
                              <span className="text-ink truncate">
                                {event.actor
                                  ? fullName(event.actor)
                                  : t("auditLog.system")}
                              </span>
                            </div>
                          </Table.Cell>
                          <Table.Cell>
                            <span className="font-semibold">
                              {auditActionLabel(event.action, t)}
                            </span>
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
                        </Table.Row>
                      );
                    })
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
