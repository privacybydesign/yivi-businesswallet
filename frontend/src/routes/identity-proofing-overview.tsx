import { Link, useNavigate, useParams } from "react-router";
import { useTranslation } from "react-i18next";
import * as React from "react";
import { useOrganizationQuery } from "../api/organization.queries";
import {
  useExternalRequestsQuery,
  useProofingFlowsQuery,
  useProofingPauseQuery,
  useProofingStatsQuery,
  useProofingWebhookQuery,
} from "../api/identity-proofing.queries";
import type { ProofingFlow, ProofingStats } from "../api/identity-proofing";
import { usePercentFormatter } from "../lib/format-percent";
import { useWhenFormatter } from "../lib/format-when";
import {
  noProofingSessions,
  proofingErrorMessage,
  proofingMethodLabel,
  proofingStatsBy,
  requestSubject,
  sumProofingStats,
  verifiedShare,
} from "../lib/identity-proofing";
import type { ProofingTotals } from "../lib/identity-proofing";
import { Card, Icon, Table, Tag, TopBar } from "../ui";
import { ResultTag } from "./proofing-ui";
import { VerifyPerson } from "./proofing-send";
import { absoluteApiUrl } from "../api/http";
import { ProofingPausedNotice } from "./proofing-pause";

const ERROR = "text-error text-[12.5px]";
// The recent sessions table shows the newest few; the rest are on the All
// people page.
const RECENT_SESSIONS = 6;
const RECENT_COLUMNS = 5;
const ALERT_ICON_SIZE = 14;
// The API reference the backend serves (internal/apidocs).
const API_DOCS_PATH = "/api/docs";

// Identity proofing at a glance: the last 30 days' sessions across every flow,
// the newest of them, and the flows themselves. An admin sees the whole org's,
// a member the sessions they sent. While a platform admin has paused the org's
// proofing, only why.
export default function IdentityProofingOverview(): React.JSX.Element {
  const { t } = useTranslation();
  const { orgSlug } = useParams();
  // Guaranteed by the ":orgSlug" route segment this component mounts under.
  const slug = orgSlug!;
  const org = useOrganizationQuery(slug);
  const isAdmin = org.data?.role === "admin";
  const pause = useProofingPauseQuery(slug);

  if (pause.data?.paused) {
    return (
      <>
        <TopBar
          title={t("identityProofing.overview.title")}
          subtitle={t("identityProofing.overview.subtitle")}
        />
        <div className="p-4 sm:p-8">
          <ProofingPausedNotice pause={pause.data} />
        </div>
      </>
    );
  }
  return <ActiveOverview slug={slug} isAdmin={isAdmin} />;
}

function ActiveOverview({
  slug,
  isAdmin,
}: {
  slug: string;
  isAdmin: boolean;
}): React.JSX.Element {
  const { t } = useTranslation();
  const stats = useProofingStatsQuery(slug);
  const flows = useProofingFlowsQuery(slug);

  return (
    <>
      <TopBar
        title={t("identityProofing.overview.title")}
        subtitle={t("identityProofing.overview.subtitle")}
        actions={
          <>
            <a
              href={absoluteApiUrl(API_DOCS_PATH)}
              target="_blank"
              rel="noreferrer"
              className="border-line-strong bg-surface text-ink hover:bg-surface-2 inline-flex h-9 items-center rounded-md border px-3.5 text-[13.5px] font-semibold transition-colors"
            >
              {t("identityProofing.overview.apiDocs")}
            </a>
            <VerifyPerson slug={slug} isAdmin={isAdmin} />
          </>
        }
      />
      <div className="flex flex-col gap-6 p-4 sm:p-8">
        {stats.isError ? (
          <p className={ERROR}>{proofingErrorMessage(stats.error, t)}</p>
        ) : (
          <StatsRow stats={stats.data} />
        )}
        <div className="grid grid-cols-1 items-start gap-6 xl:grid-cols-[minmax(0,3fr)_minmax(0,2fr)]">
          <RecentSessions slug={slug} isAdmin={isAdmin} />
          <FlowsCard
            slug={slug}
            isAdmin={isAdmin}
            flows={flows}
            stats={stats.data}
          />
        </div>
      </div>
    </>
  );
}

function StatsRow({
  stats,
}: {
  stats: ProofingStats | undefined;
}): React.JSX.Element {
  const { t } = useTranslation();
  const formatPercent = usePercentFormatter();
  const totals = sumProofingStats(stats?.rows ?? []);
  const share = verifiedShare(totals);
  const flowCount = new Set(stats?.rows.map((r) => r.flowId)).size;
  // While the counts load, the tiles hold their place without a number.
  const value = (n: number): string => (stats ? String(n) : "—");

  return (
    <Card className="divide-line grid grid-cols-2 lg:grid-cols-6 lg:divide-x">
      <StatCell
        label={t("identityProofing.overview.stats.sessions")}
        value={value(totals.sessions)}
        hint={t("identityProofing.overview.stats.sessionsHint", {
          count: flowCount,
        })}
      />
      <StatCell
        label={t("identityProofing.overview.stats.verified")}
        value={value(totals.approved)}
        hint={
          share === undefined
            ? t("identityProofing.overview.stats.noSessions")
            : t("identityProofing.overview.stats.verifiedHint", {
                share: formatPercent(share),
              })
        }
      />
      <StatCell
        label={t("identityProofing.overview.stats.failed")}
        value={value(totals.rejected)}
        hint={t("identityProofing.overview.stats.failedHint")}
      />
      <StatCell
        label={t("identityProofing.overview.stats.needsReview")}
        value={value(totals.needsReview)}
        hint={t("identityProofing.overview.stats.needsReviewHint")}
      />
      <StatCell
        label={t("identityProofing.overview.stats.expired")}
        value={value(totals.expired)}
        hint={t("identityProofing.overview.stats.expiredHint")}
      />
      <StatCell
        label={t("identityProofing.overview.stats.cancelled")}
        value={value(totals.cancelled)}
        hint={t("identityProofing.overview.stats.cancelledHint")}
      />
    </Card>
  );
}

// One figure of the stats row: the row is one card, split in cells.
function StatCell({
  label,
  value,
  hint,
}: {
  label: string;
  value: string;
  hint: string;
}): React.JSX.Element {
  return (
    <div className="px-5 py-4">
      <div className="text-muted font-label text-[11px] font-medium tracking-[0.08em] uppercase">
        {label}
      </div>
      <div className="font-display text-ink mt-1.5 text-[26px] font-bold tracking-[-0.01em]">
        {value}
      </div>
      <div className="text-muted mt-0.5 text-[12px]">{hint}</div>
    </div>
  );
}

// The newest sessions sent to external people, across every flow.
function RecentSessions({
  slug,
  isAdmin,
}: {
  slug: string;
  isAdmin: boolean;
}): React.JSX.Element {
  const { t } = useTranslation();
  const requests = useExternalRequestsQuery(slug);
  const formatWhen = useWhenFormatter();
  const navigate = useNavigate();
  const recent = (requests.data ?? []).slice(0, RECENT_SESSIONS);

  return (
    <Card>
      <div className="flex flex-wrap items-center justify-between gap-x-4 gap-y-2 px-5 pt-4 pb-3">
        <div className="flex items-baseline gap-2">
          <h2 className="font-display text-[16px] font-bold">
            {t("identityProofing.overview.recent.title")}
          </h2>
          <span className="text-muted text-[12px]">
            {isAdmin
              ? t("identityProofing.overview.recent.scope")
              : t("identityProofing.overview.recent.scopeOwn")}
          </span>
        </div>
        <Link
          to={`/${slug}/identity-proofing/people`}
          className="text-ink text-[12.5px] font-semibold hover:underline"
        >
          {t("identityProofing.overview.recent.viewAll")}
        </Link>
      </div>
      <Table>
        <Table.Head>
          <Table.HeaderCell>
            {t("identityProofing.overview.recent.person")}
          </Table.HeaderCell>
          <Table.HeaderCell>
            {t("identityProofing.overview.recent.flow")}
          </Table.HeaderCell>
          <Table.HeaderCell>
            {t("identityProofing.sessions.method")}
          </Table.HeaderCell>
          <Table.HeaderCell>
            {t("identityProofing.overview.recent.result")}
          </Table.HeaderCell>
          <Table.HeaderCell className="text-right">
            {t("identityProofing.overview.recent.started")}
          </Table.HeaderCell>
        </Table.Head>
        <Table.Body>
          {requests.isPending ? (
            <Table.State colSpan={RECENT_COLUMNS}>
              {t("common.loading")}
            </Table.State>
          ) : requests.isError ? (
            <Table.State colSpan={RECENT_COLUMNS}>
              {proofingErrorMessage(requests.error, t)}
            </Table.State>
          ) : recent.length === 0 ? (
            <Table.State colSpan={RECENT_COLUMNS}>
              {t("identityProofing.overview.recent.empty")}
            </Table.State>
          ) : (
            recent.map((request) => {
              const sessionPath = `/${slug}/identity-proofing/people?session=${request.id}`;
              return (
                <Table.Row
                  key={request.id}
                  onClick={() => void navigate(sessionPath)}
                  className="hover:bg-surface-3 cursor-pointer transition-colors"
                >
                  <Table.Cell>
                    <Link
                      to={sessionPath}
                      // The row navigates too; one history entry per click.
                      onClick={(event) => event.stopPropagation()}
                      className="text-ink font-semibold hover:underline"
                    >
                      {requestSubject(request).name ||
                        t("identityProofing.requests.noName")}
                    </Link>
                  </Table.Cell>
                  <Table.Cell className="text-ink-soft min-w-40">
                    {request.flowName}
                  </Table.Cell>
                  <Table.Cell className="text-ink-soft whitespace-nowrap">
                    {proofingMethodLabel(request.method, t)}
                  </Table.Cell>
                  <Table.Cell>
                    <ResultTag request={request} compact />
                  </Table.Cell>
                  <Table.Cell className="text-ink-soft text-right whitespace-nowrap">
                    {formatWhen(request.createdAt)}
                  </Table.Cell>
                </Table.Row>
              );
            })
          )}
        </Table.Body>
      </Table>
    </Card>
  );
}

// The flows with their sessions of the last 30 days; an admin's links on to
// the Flows page. A member sees the flows members may use.
function FlowsCard({
  slug,
  isAdmin,
  flows,
  stats,
}: {
  slug: string;
  isAdmin: boolean;
  flows: ReturnType<typeof useProofingFlowsQuery>;
  stats: ProofingStats | undefined;
}): React.JSX.Element {
  const { t } = useTranslation();
  const perFlow = proofingStatsBy(stats?.rows ?? [], (r) => r.flowId);

  return (
    <Card>
      <div className="border-line flex items-center justify-between border-b px-5 pt-4 pb-3">
        <h2 className="font-display text-[16px] font-bold">
          {t("identityProofing.overview.flows.title")}
        </h2>
        {isAdmin && (
          <Link
            to={`/${slug}/identity-proofing/flows`}
            className="text-ink text-[12.5px] font-semibold hover:underline"
          >
            {t("identityProofing.overview.flows.viewAll")}
          </Link>
        )}
      </div>
      {flows.isPending && (
        <p className="text-ink-soft px-5 py-4 text-[13px]">
          {t("common.loading")}
        </p>
      )}
      {flows.isError && (
        <p className={`${ERROR} px-5 py-4`}>
          {proofingErrorMessage(flows.error, t)}
        </p>
      )}
      {flows.isSuccess && flows.data.length === 0 && (
        <p className="text-ink-soft px-5 py-4 text-[13px]">
          {t("identityProofing.overview.flows.empty")}
        </p>
      )}
      {flows.isSuccess && flows.data.length > 0 && (
        <ul className="divide-line divide-y">
          {flows.data.map((flow) => (
            <FlowRow
              key={flow.id}
              flow={flow}
              totals={perFlow.get(flow.id) ?? noProofingSessions()}
            />
          ))}
        </ul>
      )}
      {isAdmin && <WebhookAlert slug={slug} />}
    </Card>
  );
}

function FlowRow({
  flow,
  totals,
}: {
  flow: ProofingFlow;
  totals: ProofingTotals;
}): React.JSX.Element {
  const { t } = useTranslation();
  const formatPercent = usePercentFormatter();
  const share = verifiedShare(totals);

  return (
    <li className="flex items-center gap-3 px-5 py-3">
      <div className="min-w-0 flex-1">
        <div className="text-ink truncate text-[13.5px] font-semibold">
          {flow.name}
        </div>
        <div className="text-muted text-[12px]">
          {share === undefined
            ? t("identityProofing.overview.flows.noSessions")
            : t("identityProofing.overview.flows.summary", {
                count: totals.sessions,
                share: formatPercent(share),
              })}
        </div>
      </div>
      {flow.default && (
        <Tag>{t("identityProofing.overview.flows.default")}</Tag>
      )}
    </li>
  );
}

// The org's webhook endpoint, while it is failing, with what waits for it: the
// one thing on this page an admin has to act on.
function WebhookAlert({ slug }: { slug: string }): React.JSX.Element | null {
  const { t } = useTranslation();
  const formatWhen = useWhenFormatter();
  const webhook = useProofingWebhookQuery(slug);
  const health = webhook.data?.health;
  if (health?.state !== "failing") {
    return null;
  }
  const since = health.failingSince ? formatWhen(health.failingSince) : "—";
  return (
    <div className="bg-warning-bg text-warning-fg flex items-start gap-2 rounded-b-[inherit] px-5 py-3 text-[12.5px]">
      <Icon name="warning" size={ALERT_ICON_SIZE} className="mt-0.5 shrink-0" />
      <span>
        {health.lastStatusCode === undefined
          ? t("identityProofing.overview.webhookAlertNoAnswer", {
              since,
              count: health.pendingRetries,
            })
          : t("identityProofing.overview.webhookAlert", {
              code: health.lastStatusCode,
              since,
              count: health.pendingRetries,
            })}{" "}
        <Link
          to={`/${slug}/identity-proofing/developers`}
          className="font-semibold underline"
        >
          {t("identityProofing.overview.webhookAlertLink")}
        </Link>
      </span>
    </div>
  );
}
