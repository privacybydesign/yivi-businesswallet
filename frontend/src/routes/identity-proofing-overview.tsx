import { useState } from "react";
import { Link, useNavigate, useParams } from "react-router";
import { useTranslation } from "react-i18next";
import * as React from "react";
import { useOrganizationQuery } from "../api/organization.queries";
import {
  useProofingCustomersQuery,
  useProofingPauseQuery,
  useProofingRequestsQuery,
  useProofingStatsQuery,
} from "../api/identity-proofing.queries";
import type { ProofingCustomer, ProofingStats } from "../api/identity-proofing";
import { usePercentFormatter } from "../lib/format-percent";
import { useWhenFormatter } from "../lib/format-when";
import {
  noProofingSessions,
  proofingErrorMessage,
  proofingMethodLabel,
  proofingStatsBy,
  sumProofingStats,
  verifiedShare,
} from "../lib/identity-proofing";
import type { ProofingTotals } from "../lib/identity-proofing";
import { Button, Card, Icon, Table, TopBar } from "../ui";
import {
  CustomerMark,
  CustomerStatusTag,
  NewCustomerModal,
  ResultTag,
} from "./proofing-customer-ui";
import { absoluteApiUrl } from "../api/http";
import { ProofingPauseCard, ProofingPausedNotice } from "./proofing-pause";

const ERROR = "text-error text-[12.5px]";
// The recent sessions table shows the newest few; the rest are on each
// customer's Sessions tab.
const RECENT_SESSIONS = 6;
const RECENT_COLUMNS = 5;
const ALERT_ICON_SIZE = 14;
// The recent sessions' customer filter: a compact version of the form select.
const FILTER_SELECT_CLASS =
  "rounded-yivi border-line-strong bg-surface text-ink h-8 max-w-56 border px-2.5 text-[12.5px] transition-colors outline-none focus:border-ink focus:ring-ink/10 focus:ring-3";
// The API reference the backend serves (internal/apidocs).
const API_DOCS_PATH = "/api/docs";

// Identity proofing at a glance: the last 30 days' sessions across every
// customer, the newest of them, and the customers themselves. An admin sees the
// whole org's, a member the sessions they sent. While the org's proofing is
// paused, only why.
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
          <ProofingPausedNotice
            slug={slug}
            pause={pause.data}
            isAdmin={isAdmin}
          />
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
  const customers = useProofingCustomersQuery(slug);
  const [adding, setAdding] = useState(false);

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
            {isAdmin && (
              <Button icon="add" onClick={() => setAdding(true)}>
                {t("customers.add")}
              </Button>
            )}
          </>
        }
      />
      {adding && (
        <NewCustomerModal slug={slug} onClose={() => setAdding(false)} />
      )}
      <div className="flex flex-col gap-6 p-4 sm:p-8">
        {stats.isError ? (
          <p className={ERROR}>{proofingErrorMessage(stats.error, t)}</p>
        ) : (
          <StatsRow stats={stats.data} />
        )}
        <div className="grid grid-cols-1 items-start gap-6 xl:grid-cols-[minmax(0,3fr)_minmax(0,2fr)]">
          <RecentSessions
            slug={slug}
            isAdmin={isAdmin}
            customers={customers.data ?? []}
          />
          <CustomersCard slug={slug} customers={customers} stats={stats.data} />
        </div>
        {isAdmin && <ProofingPauseCard slug={slug} />}
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
  const customerCount = new Set(stats?.rows.map((r) => r.customerId)).size;
  // While the counts load, the tiles hold their place without a number.
  const value = (n: number): string => (stats ? String(n) : "—");

  return (
    <Card className="divide-line grid grid-cols-2 lg:grid-cols-6 lg:divide-x">
      <StatCell
        label={t("identityProofing.overview.stats.sessions")}
        value={value(totals.sessions)}
        hint={t("identityProofing.overview.stats.sessionsHint", {
          count: customerCount,
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
      <div className="text-muted font-mono text-[11px] font-medium tracking-[0.08em] uppercase">
        {label}
      </div>
      <div className="font-display text-ink mt-1.5 text-[26px] font-bold tracking-[-0.01em]">
        {value}
      </div>
      <div className="text-muted mt-0.5 text-[12px]">{hint}</div>
    </div>
  );
}

// The newest sessions across every customer, or of the one customer picked:
// that customer's own list, so its newest show even when other customers'
// fill the org's.
function RecentSessions({
  slug,
  isAdmin,
  customers,
}: {
  slug: string;
  isAdmin: boolean;
  customers: ProofingCustomer[];
}): React.JSX.Element {
  const { t } = useTranslation();
  // "" is every customer.
  const [customerId, setCustomerId] = useState("");
  const requests = useProofingRequestsQuery(slug, customerId || undefined);
  const formatWhen = useWhenFormatter();
  const navigate = useNavigate();
  // Requests arrive newest first; a member's own proofing is not a customer's.
  const recent = (requests.data ?? [])
    .filter((r) => r.customerId !== undefined)
    .slice(0, RECENT_SESSIONS);

  return (
    <Card>
      <div className="flex flex-wrap items-center justify-between gap-x-4 gap-y-2 px-5 pt-4 pb-3">
        <div className="flex items-baseline gap-2">
          <h2 className="font-display text-[16px] font-bold">
            {t("identityProofing.overview.recent.title")}
          </h2>
          {customerId === "" && (
            <span className="text-muted text-[12px]">
              {isAdmin
                ? t("identityProofing.overview.recent.scope")
                : t("identityProofing.overview.recent.scopeOwn")}
            </span>
          )}
        </div>
        {customers.length > 0 && (
          <select
            aria-label={t("identityProofing.overview.recent.filterLabel")}
            className={FILTER_SELECT_CLASS}
            value={customerId}
            onChange={(event) => setCustomerId(event.target.value)}
          >
            <option value="">
              {t("identityProofing.overview.recent.allCustomers")}
            </option>
            {customers.map((customer) => (
              <option key={customer.id} value={customer.id}>
                {customer.name}
              </option>
            ))}
          </select>
        )}
      </div>
      <Table>
        <Table.Head>
          <Table.HeaderCell>
            {t("identityProofing.overview.recent.customer")}
          </Table.HeaderCell>
          <Table.HeaderCell>
            {t("identityProofing.overview.recent.flow")}
          </Table.HeaderCell>
          <Table.HeaderCell>{t("customers.sessions.method")}</Table.HeaderCell>
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
              {customerId === ""
                ? t("identityProofing.overview.recent.empty")
                : t("identityProofing.overview.recent.emptyCustomer")}
            </Table.State>
          ) : (
            recent.map((request) => {
              const customerPath = `/${slug}/identity-proofing/customers/${request.customerId}`;
              return (
                <Table.Row
                  key={request.id}
                  onClick={() => void navigate(customerPath)}
                  className="hover:bg-surface-3 cursor-pointer transition-colors"
                >
                  <Table.Cell>
                    <Link
                      to={customerPath}
                      // The row navigates too; one history entry per click.
                      onClick={(event) => event.stopPropagation()}
                      className="text-ink font-semibold hover:underline"
                    >
                      {request.customerName}
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

function CustomersCard({
  slug,
  customers,
  stats,
}: {
  slug: string;
  customers: ReturnType<typeof useProofingCustomersQuery>;
  stats: ProofingStats | undefined;
}): React.JSX.Element {
  const { t } = useTranslation();
  const perCustomer = proofingStatsBy(stats?.rows ?? [], (r) => r.customerId);

  return (
    <Card>
      <div className="border-line flex items-center justify-between border-b px-5 pt-4 pb-3">
        <h2 className="font-display text-[16px] font-bold">
          {t("identityProofing.overview.customers.title")}
        </h2>
        <Link
          to={`/${slug}/identity-proofing/customers`}
          className="text-ink text-[12.5px] font-semibold hover:underline"
        >
          {t("identityProofing.overview.customers.viewAll")}
        </Link>
      </div>
      {customers.isPending ? (
        <p className="text-ink-soft px-5 py-4 text-[13px]">
          {t("common.loading")}
        </p>
      ) : customers.isError ? (
        <p className={`${ERROR} px-5 py-4`}>
          {proofingErrorMessage(customers.error, t)}
        </p>
      ) : customers.data.length === 0 ? (
        <p className="text-ink-soft px-5 py-4 text-[13px]">
          {t("identityProofing.overview.customers.empty")}
        </p>
      ) : (
        <>
          <ul className="divide-line divide-y">
            {customers.data.map((customer) => (
              <CustomerRow
                key={customer.id}
                slug={slug}
                customer={customer}
                totals={perCustomer.get(customer.id) ?? noProofingSessions()}
              />
            ))}
          </ul>
          <WebhookAlerts customers={customers.data} />
        </>
      )}
    </Card>
  );
}

// Every customer whose webhook endpoint is failing, with what waits for it: the
// one thing on this page an admin has to act on.
function WebhookAlerts({
  customers,
}: {
  customers: ProofingCustomer[];
}): React.JSX.Element | null {
  const { t } = useTranslation();
  const formatWhen = useWhenFormatter();
  const failing = customers.filter((c) => c.webhook.state === "failing");
  const sinceOf = (c: ProofingCustomer): string =>
    c.webhook.failingSince ? formatWhen(c.webhook.failingSince) : "—";
  if (failing.length === 0) {
    return null;
  }
  return (
    <div className="bg-warning-bg text-warning-fg flex flex-col gap-1.5 rounded-b-[inherit] px-5 py-3 text-[12.5px]">
      {failing.map((c) => (
        <p key={c.id} className="flex items-start gap-2">
          <Icon
            name="warning"
            size={ALERT_ICON_SIZE}
            className="mt-0.5 shrink-0"
          />
          <span>
            {c.webhook.lastStatusCode === undefined
              ? t("identityProofing.overview.customers.webhookAlertNoAnswer", {
                  name: c.name,
                  since: sinceOf(c),
                  count: c.webhook.pendingRetries,
                })
              : t("identityProofing.overview.customers.webhookAlert", {
                  name: c.name,
                  code: c.webhook.lastStatusCode,
                  since: sinceOf(c),
                  count: c.webhook.pendingRetries,
                })}
          </span>
        </p>
      ))}
    </div>
  );
}

function CustomerRow({
  slug,
  customer,
  totals,
}: {
  slug: string;
  customer: ProofingCustomer;
  totals: ProofingTotals;
}): React.JSX.Element {
  const { t } = useTranslation();
  const formatPercent = usePercentFormatter();
  const share = verifiedShare(totals);

  return (
    <li>
      <Link
        to={`/${slug}/identity-proofing/customers/${customer.id}`}
        className="hover:bg-surface-2 flex items-center gap-3 px-5 py-3 transition-colors"
      >
        <CustomerMark customer={customer} />
        <div className="min-w-0 flex-1">
          <div className="text-ink truncate text-[13.5px] font-semibold">
            {customer.name}
          </div>
          <div className="text-muted text-[12px]">
            {share === undefined
              ? t("identityProofing.overview.customers.noSessions")
              : t("identityProofing.overview.customers.summary", {
                  count: totals.sessions,
                  share: formatPercent(share),
                })}
          </div>
        </div>
        <CustomerStatusTag customer={customer} />
      </Link>
    </li>
  );
}
