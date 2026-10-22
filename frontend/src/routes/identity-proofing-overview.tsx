import { Link, useParams } from "react-router";
import { useTranslation } from "react-i18next";
import * as React from "react";
import type { TFunction } from "i18next";
import { useOrganizationQuery } from "../api/organization.queries";
import {
  useExternalRequestsInQuery,
  useProofingPauseQuery,
  useProofingStatsQuery,
  useProofingWebhookQuery,
  useWaitingReviewsQuery,
} from "../api/identity-proofing.queries";
import type { ProofingRequest } from "../api/identity-proofing";
import { isDataRequest } from "../api/identity-proofing";
import { accessMessage } from "../lib/access-message";
import { useWhenFormatter } from "../lib/format-when";
import {
  listedCount,
  proofingErrorMessage,
  proofingMethodLabel,
  proofingStatusLabel,
  requestListCapped,
  requestSubject,
  sumProofingStats,
} from "../lib/identity-proofing";
import {
  personName,
  reviewPath,
  waitingOldestFirst,
  waitingSince,
} from "../lib/proofing-reviews";
import {
  TODO_ROWS,
  cappedAmong,
  finishedChecks,
  finishedOn,
  namelessKey,
  todoShows,
  todoStatuses,
  waitingForOthers,
} from "../lib/proofing-todo";
import { Card, Icon, TaskEmpty, TaskRow, TaskSection, TopBar } from "../ui";
import { ResultTag } from "./proofing-ui";
import { VerifyPerson } from "./proofing-send";
import { ProofingPausedNotice } from "./proofing-pause";
import { PRIMARY_LINK } from "./proofing-review-parts";

const ERROR = "text-error px-5 pt-3.5 pb-[18px] text-[12.5px]";
const WHEN = "text-ink-soft text-[13px] whitespace-nowrap";
const SECTION_LINK = "text-ink text-[13.5px] font-semibold hover:underline";
const ALERT_ICON_SIZE = 14;

// What a section holds below its title: why there are no rows yet, that there
// are none, or the rows.
function Rows({
  query,
  empty,
  rows,
}: {
  query: { isError: boolean; isPending: boolean; error: Error | null };
  empty: string;
  rows: React.ReactElement[];
}): React.JSX.Element {
  const { t } = useTranslation();
  if (query.isError) {
    return (
      <p role="alert" className={ERROR}>
        {proofingErrorMessage(query.error, t)}
      </p>
    );
  }
  if (query.isPending) return <TaskEmpty>{t("common.loading")}</TaskEmpty>;
  if (rows.length === 0) return <TaskEmpty>{empty}</TaskEmpty>;
  return <ul>{rows}</ul>;
}

// Identity proofing as a to-do list: what waits for the reader's decision, who
// the org waits for, and what finished lately. An admin sees the whole org's,
// a member the checks they sent. While a platform admin has paused the org's
// proofing, why, above the same lists, and no Verify a person.
export default function IdentityProofingOverview(): React.JSX.Element {
  const { t } = useTranslation();
  const { orgSlug } = useParams();
  // Guaranteed by the ":orgSlug" route segment this component mounts under.
  const slug = orgSlug!;
  const org = useOrganizationQuery(slug);
  const isAdmin = org.data?.role === "admin";
  const canReview = org.data?.canReview === true;
  const pause = useProofingPauseQuery(slug);
  const shows = todoShows({
    isAdmin,
    canReview,
    paused: pause.data?.paused === true,
  });

  return (
    <>
      <TopBar
        title={t("identityProofing.overview.title")}
        subtitle={t("identityProofing.overview.subtitle")}
        actions={
          org.data && shows.verify ? (
            <VerifyPerson slug={slug} isAdmin={isAdmin} />
          ) : undefined
        }
      />
      <div className="flex max-w-240 flex-col gap-6 p-4 sm:p-8">
        {pause.data?.paused && <ProofingPausedNotice pause={pause.data} />}
        {org.isError ? (
          <Card className="p-6">
            <p role="alert" className="text-error text-[14px]">
              {accessMessage(org.error, t)}
            </p>
          </Card>
        ) : org.data ? (
          // Which lists there are hangs on the reader's right to decide, so
          // they wait for it.
          <OverviewLists
            slug={slug}
            isAdmin={isAdmin}
            canReview={shows.decisions}
          />
        ) : (
          <Card>
            <TaskEmpty>{t("common.loading")}</TaskEmpty>
          </Card>
        )}
      </div>
    </>
  );
}

function OverviewLists({
  slug,
  isAdmin,
  canReview,
}: {
  slug: string;
  isAdmin: boolean;
  canReview: boolean;
}): React.JSX.Element {
  const requests = useExternalRequestsInQuery(
    slug,
    todoStatuses({ canReview, finished: true }),
  );

  return (
    <>
      {isAdmin && <WebhookAlert slug={slug} />}
      {canReview && <DecisionSection slug={slug} />}
      <WaitingSection slug={slug} requests={requests} canReview={canReview} />
      <FinishedSection slug={slug} requests={requests} />
      <LastMonth slug={slug} />
    </>
  );
}

type StatusReads = ReturnType<typeof useExternalRequestsInQuery>;

// What the reader decides, the longest waiting first: an admin's and a
// reviewer's, who see what waits in the whole org.
function DecisionSection({ slug }: { slug: string }): React.JSX.Element {
  const { t } = useTranslation();
  const formatWhen = useWhenFormatter();
  const waiting = useWaitingReviewsQuery(slug, { enabled: true });
  const held = waitingOldestFirst(waiting.data ?? []);
  const count = listedCount(
    held.length,
    requestListCapped(waiting.data ?? []),
    t,
  );

  return (
    <TaskSection
      title={t("identityProofing.overview.decision.title")}
      count={held.length > 0 ? count : undefined}
      action={
        held.length > 0 ? (
          <Link to={reviewPath(slug)} className={SECTION_LINK}>
            {t("identityProofing.overview.decision.open")}
          </Link>
        ) : undefined
      }
    >
      <Rows
        query={waiting}
        empty={t("identityProofing.overview.decision.empty")}
        rows={held.slice(0, TODO_ROWS).map((request) => (
          <TaskRow
            key={request.id}
            title={
              <Link
                to={reviewPath(slug, request.id)}
                className="hover:underline"
              >
                {personName(request, t)}
              </Link>
            }
            detail={
              isDataRequest(request.flowKind)
                ? t(`identityProofingFlows.kinds.${request.flowKind}.title`)
                : request.flowName
            }
            aside={
              <span className="flex items-center gap-3">
                <span className={WHEN}>
                  {formatWhen(waitingSince(request))}
                </span>
                <Link
                  to={reviewPath(slug, request.id)}
                  aria-label={t("identityProofing.reviews.reviewOf", {
                    name: personName(request, t),
                  })}
                  className={PRIMARY_LINK}
                >
                  {t("identityProofing.reviews.review")}
                </Link>
              </span>
            }
          />
        ))}
      />
    </TaskSection>
  );
}

// Where a session sits in the list of people: its row on All people.
function personPath(slug: string, request: ProofingRequest): string {
  return `/${slug}/identity-proofing/people?session=${request.id}`;
}

// The person's name, or a word for why there is none.
function PersonName({
  slug,
  request,
}: {
  slug: string;
  request: ProofingRequest;
}): React.JSX.Element {
  const { t } = useTranslation();
  const name = requestSubject(request).name;
  return (
    <Link to={personPath(slug, request)} className="hover:underline">
      {name === "" ? (
        <span className="text-ink-soft font-normal italic">
          {t(namelessKey(request))}
        </span>
      ) : (
        name
      )}
    </Link>
  );
}

// The flow, and the app the person opened it in once they did.
function flowLine(request: ProofingRequest, t: TFunction): string {
  return request.method
    ? `${request.flowName} · ${proofingMethodLabel(request.method, t)}`
    : request.flowName;
}

// The checks the org waits on others for: for a member who cannot decide,
// their own checks held for an admin, and the people who have not finished.
function WaitingSection({
  slug,
  requests,
  canReview,
}: {
  slug: string;
  requests: StatusReads;
  canReview: boolean;
}): React.JSX.Element {
  const { t } = useTranslation();
  const formatWhen = useWhenFormatter();
  const waiting = waitingForOthers(requests.data ?? [], { canReview });
  const count = listedCount(
    waiting.length,
    cappedAmong(requests.capped, todoStatuses({ canReview, finished: false })),
    t,
  );

  return (
    <TaskSection
      title={t("identityProofing.overview.waiting.title")}
      count={requests.data ? count : undefined}
      hint={t("identityProofing.overview.waiting.hint")}
      action={
        waiting.length > TODO_ROWS ? (
          <Link
            to={`/${slug}/identity-proofing/people`}
            className={SECTION_LINK}
          >
            {t("identityProofing.overview.allPeople")}
          </Link>
        ) : undefined
      }
    >
      <Rows
        query={requests}
        empty={t("identityProofing.overview.waiting.empty")}
        rows={waiting.slice(0, TODO_ROWS).map((request) => (
          <TaskRow
            key={request.id}
            title={<PersonName slug={slug} request={request} />}
            detail={flowLine(request, t)}
            aside={
              <span className={WHEN}>
                {request.status === "needs_review"
                  ? t("identityProofing.overview.waiting.forAdmin")
                  : `${proofingStatusLabel(request.status, t)} · ${formatWhen(request.createdAt)}`}
              </span>
            }
          />
        ))}
      />
    </TaskSection>
  );
}

// The newest checks that ended in a result; the rest are on All people.
function FinishedSection({
  slug,
  requests,
}: {
  slug: string;
  requests: StatusReads;
}): React.JSX.Element {
  const { t } = useTranslation();
  const formatWhen = useWhenFormatter();
  const finished = finishedChecks(requests.data ?? []);

  return (
    <TaskSection
      title={t("identityProofing.overview.finished.title")}
      action={
        <Link to={`/${slug}/identity-proofing/people`} className={SECTION_LINK}>
          {t("identityProofing.overview.allPeople")}
        </Link>
      }
    >
      <Rows
        query={requests}
        empty={t("identityProofing.overview.finished.empty")}
        rows={finished.slice(0, TODO_ROWS).map((request) => (
          <TaskRow
            key={request.id}
            title={<PersonName slug={slug} request={request} />}
            detail={flowLine(request, t)}
            aside={
              <span className="flex items-center gap-3">
                <ResultTag request={request} compact />
                <span className={WHEN}>{formatWhen(finishedOn(request))}</span>
              </span>
            }
          />
        ))}
      />
    </TaskSection>
  );
}

// The counts of the last 30 days, in one line.
function LastMonth({ slug }: { slug: string }): React.JSX.Element | null {
  const { t } = useTranslation();
  const stats = useProofingStatsQuery(slug);
  if (!stats.data) {
    return stats.isError ? (
      <p role="alert" className="text-error text-[12.5px]">
        {proofingErrorMessage(stats.error, t)}
      </p>
    ) : null;
  }

  const totals = sumProofingStats(stats.data.rows);
  return (
    <p className="text-ink-soft text-[13px]">
      {t("identityProofing.overview.summary", {
        count: totals.sessions,
        verified: totals.approved,
        failed: totals.rejected,
      })}
    </p>
  );
}

// The org's webhook endpoint, while it is failing, with what waits for it: the
// one thing on this page an admin has to act on besides the lists.
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
    <div className="bg-warning-bg text-warning-fg rounded-yivi flex items-start gap-2 px-5 py-3 text-[12.5px]">
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
