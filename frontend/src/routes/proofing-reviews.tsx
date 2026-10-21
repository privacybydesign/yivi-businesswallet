import { Link, useParams, useSearchParams } from "react-router";
import { Trans, useTranslation } from "react-i18next";
import * as React from "react";
import type { TFunction } from "i18next";
import { useOrganizationQuery } from "../api/organization.queries";
import {
  useClosedDataRequestsQuery,
  useWaitingReviewsQuery,
} from "../api/identity-proofing.queries";
import type { ProofingRequest } from "../api/identity-proofing";
import { isDataRequest } from "../api/identity-proofing";
import { useDayMonthFormatter, useWhenFormatter } from "../lib/format-when";
import {
  assuranceLevelLabel,
  listedCount,
  proofingErrorMessage,
  proofingRejectionReason,
  requestListCapped,
  requestSubject,
  requestedBy,
} from "../lib/identity-proofing";
import {
  DEFAULT_REVIEW_TAB,
  REVIEW_TABS,
  answerBy,
  closedRequests,
  personName,
  reviewCounts,
  reviewPath,
  reviewTabFrom,
  waitingOfTab,
  waitingOldestFirst,
  waitingSince,
} from "../lib/proofing-reviews";
import type { ReviewTab } from "../lib/proofing-reviews";
import { Card, Table, Tag, TopBar } from "../ui";
import { PRIMARY_LINK, SECONDARY_LINK } from "./proofing-review-parts";

const REVIEW_COLUMNS = 5;
const SHOW_PARAM = "show";
const SUB_LINE = "text-ink-soft text-[12.5px]";

// Everything that waits for a person's decision, the longest first: identity
// checks and data requests. For an admin and a member with the reviewer right.
export default function ProofingReviews(): React.JSX.Element {
  const { t } = useTranslation();
  const { orgSlug } = useParams();
  // Guaranteed by the ":orgSlug" route segment this component mounts under.
  const slug = orgSlug!;
  const org = useOrganizationQuery(slug);
  const isAdmin = org.data?.role === "admin";
  const canReview = org.data?.canReview === true;
  const waiting = useWaitingReviewsQuery(slug, { enabled: canReview });
  const [searchParams] = useSearchParams();
  const tab = reviewTabFrom(searchParams.get(SHOW_PARAM));
  const waitingNow = waitingOldestFirst(waiting.data ?? []);
  const counts = reviewCounts(waitingNow);
  // The backend lists at most the 200 longest in review: past that the counts
  // are lower bounds.
  const capped = requestListCapped(waiting.data ?? []);
  // What shows instead of the reviews to anyone without the right, and until
  // the org is known.
  const notice = org.isPending
    ? t("common.loading")
    : t("identityProofing.reviews.notAllowed");

  return (
    <>
      <TopBar
        title={t("identityProofing.reviews.title")}
        subtitle={t("identityProofing.reviews.subtitle")}
      />
      {canReview ? (
        <>
          <ReviewTabs active={tab} counts={counts} capped={capped} />
          <div className="flex flex-col gap-4 p-4 sm:p-8">
            <Card className="overflow-hidden">
              {tab === "closed" ? (
                <ClosedTable slug={slug} />
              ) : (
                <WaitingTable
                  slug={slug}
                  requests={waitingOfTab(tab, waitingNow)}
                  pending={waiting.isPending}
                  error={waiting.error}
                />
              )}
            </Card>
            <p className="text-ink-soft text-[13px]">
              {t("identityProofing.reviews.footer")}
              {isAdmin && (
                <>
                  {" "}
                  <Trans
                    i18nKey="identityProofing.reviews.footerWho"
                    components={{
                      members: (
                        <Link
                          to={`/${slug}/members`}
                          className="text-link underline"
                        />
                      ),
                    }}
                  />
                </>
              )}
            </p>
          </div>
        </>
      ) : (
        <p className="text-ink-soft p-4 text-[14px] sm:p-8">{notice}</p>
      )}
    </>
  );
}

// The views of the page, as links: each is a page of its own in the address.
function ReviewTabs({
  active,
  counts,
  capped,
}: {
  active: ReviewTab;
  counts: ReturnType<typeof reviewCounts>;
  capped: boolean;
}): React.JSX.Element {
  const { t } = useTranslation();
  const countOf: Record<ReviewTab, number | undefined> = {
    all: counts.all,
    checks: counts.checks,
    data: counts.data,
    closed: undefined,
  };
  return (
    <nav
      aria-label={t("identityProofing.reviews.tabsLabel")}
      className="border-line bg-surface flex gap-1 overflow-x-auto border-b px-4 sm:px-8"
    >
      {REVIEW_TABS.map((tab) => {
        const selected = tab === active;
        return (
          <Link
            key={tab}
            to={{
              search: tab === DEFAULT_REVIEW_TAB ? "" : `?${SHOW_PARAM}=${tab}`,
            }}
            aria-current={selected ? "page" : undefined}
            className={[
              "flex h-11 items-center gap-2 border-b-2 px-3.5 text-[13.5px] whitespace-nowrap transition-colors",
              selected
                ? "border-primary text-ink font-semibold"
                : "text-ink-soft hover:text-ink border-transparent font-medium",
            ].join(" ")}
          >
            {t(`identityProofing.reviews.tabs.${tab}`)}
            {countOf[tab] !== undefined && (
              <span className="text-ink-soft font-normal">
                {listedCount(countOf[tab], capped, t)}
              </span>
            )}
          </Link>
        );
      })}
    </nav>
  );
}

function WaitingTable({
  slug,
  requests,
  pending,
  error,
}: {
  slug: string;
  requests: ProofingRequest[];
  pending: boolean;
  error: Error | null;
}): React.JSX.Element {
  const { t } = useTranslation();
  // The one line the table shows instead of rows, if any.
  let state: string | undefined;
  if (pending) {
    state = t("common.loading");
  } else if (error) {
    state = proofingErrorMessage(error, t);
  } else if (requests.length === 0) {
    state = t("identityProofing.reviews.empty");
  }
  return (
    <Table>
      <Table.Head>
        <Table.HeaderCell>
          {t("identityProofing.reviews.columns.person")}
        </Table.HeaderCell>
        <Table.HeaderCell>
          {t("identityProofing.reviews.columns.what")}
        </Table.HeaderCell>
        <Table.HeaderCell>
          {t("identityProofing.reviews.columns.why")}
        </Table.HeaderCell>
        <Table.HeaderCell>
          {t("identityProofing.reviews.columns.since")}
        </Table.HeaderCell>
        <Table.HeaderCell srOnly>
          {t("identityProofing.reviews.columns.action")}
        </Table.HeaderCell>
      </Table.Head>
      <Table.Body>
        {state === undefined ? (
          requests.map((request) => (
            <WaitingRow key={request.id} slug={slug} request={request} />
          ))
        ) : (
          <Table.State colSpan={REVIEW_COLUMNS}>{state}</Table.State>
        )}
      </Table.Body>
    </Table>
  );
}

// How a data request's person was proven: the app, and the level reached.
function provenLine(request: ProofingRequest, t: TFunction): string {
  const how =
    request.method === "idem_app" ||
    request.method === "yivi_app" ||
    request.method === "browser"
      ? t(`identityProofing.reviews.provenWith.${request.method}`)
      : t("identityProofing.reviews.provenWith.unknown");
  return request.eidasLevel
    ? `${how} · ${assuranceLevelLabel(request.eidasLevel, t)}`
    : how;
}

function PersonCell({
  request,
  children,
}: {
  request: ProofingRequest;
  children: React.ReactNode;
}): React.JSX.Element {
  const { t } = useTranslation();
  const name = personName(request, t);
  return (
    <Table.Cell>
      <div className="flex flex-col gap-0.5">
        <span className="font-semibold">{name}</span>
        <span className={SUB_LINE}>{children}</span>
      </div>
    </Table.Cell>
  );
}

function WhatCell({
  request,
}: {
  request: ProofingRequest;
}): React.JSX.Element {
  const { t } = useTranslation();
  if (isDataRequest(request.flowKind)) {
    return (
      <Table.Cell>
        {t(`identityProofingFlows.kinds.${request.flowKind}.title`)}
      </Table.Cell>
    );
  }
  return (
    <Table.Cell>
      {t("identityProofing.reviews.identityCheck")}
      <div className={SUB_LINE}>{request.flowName}</div>
    </Table.Cell>
  );
}

function WaitingRow({
  slug,
  request,
}: {
  slug: string;
  request: ProofingRequest;
}): React.JSX.Element {
  const { t } = useTranslation();
  const formatWhen = useWhenFormatter();
  const formatDay = useDayMonthFormatter();
  const due = answerBy(request);
  const name = requestSubject(request).name;
  return (
    <Table.Row>
      <PersonCell request={request}>
        {isDataRequest(request.flowKind)
          ? provenLine(request, t)
          : t("identityProofing.reviews.requestedBy", {
              name: requestedBy(request, t),
            })}
      </PersonCell>
      <WhatCell request={request} />
      <Table.Cell>
        {t(`identityProofing.reviews.why.${request.flowKind}`)}
      </Table.Cell>
      <Table.Cell className="whitespace-nowrap">
        {formatWhen(waitingSince(request))}
        {due && (
          <div className={SUB_LINE}>
            {t("identityProofing.reviews.answerBy", { date: formatDay(due) })}
          </div>
        )}
      </Table.Cell>
      <Table.Cell className="text-right">
        <Link
          to={reviewPath(slug, request.id)}
          aria-label={t("identityProofing.reviews.reviewOf", { name })}
          className={PRIMARY_LINK}
        >
          {t("identityProofing.reviews.review")}
        </Link>
      </Table.Cell>
    </Table.Row>
  );
}

// The data requests that ended, read as such: no identity check takes their
// place under the list's cap.
function ClosedTable({ slug }: { slug: string }): React.JSX.Element {
  const { t } = useTranslation();
  const requests = useClosedDataRequestsQuery(slug, { enabled: true });
  const closed = closedRequests(requests.data ?? []);
  // The one line the table shows instead of rows, if any.
  let state: string | undefined;
  if (requests.isPending) {
    state = t("common.loading");
  } else if (requests.isError) {
    state = proofingErrorMessage(requests.error, t);
  } else if (closed.length === 0) {
    state = t("identityProofing.reviews.emptyClosed");
  }
  return (
    <Table>
      <Table.Head>
        <Table.HeaderCell>
          {t("identityProofing.reviews.columns.person")}
        </Table.HeaderCell>
        <Table.HeaderCell>
          {t("identityProofing.reviews.columns.what")}
        </Table.HeaderCell>
        <Table.HeaderCell>
          {t("identityProofing.reviews.columns.outcome")}
        </Table.HeaderCell>
        <Table.HeaderCell>
          {t("identityProofing.reviews.columns.closed")}
        </Table.HeaderCell>
        <Table.HeaderCell srOnly>
          {t("identityProofing.reviews.columns.action")}
        </Table.HeaderCell>
      </Table.Head>
      <Table.Body>
        {state === undefined ? (
          closed.map((request) => (
            <ClosedRow key={request.id} slug={slug} request={request} />
          ))
        ) : (
          <Table.State colSpan={REVIEW_COLUMNS}>{state}</Table.State>
        )}
      </Table.Body>
    </Table>
  );
}

function ClosedRow({
  slug,
  request,
}: {
  slug: string;
  request: ProofingRequest;
}): React.JSX.Element {
  const { t } = useTranslation();
  const formatWhen = useWhenFormatter();
  const approved = request.status === "approved";
  const name = personName(request, t);
  return (
    <Table.Row>
      <PersonCell request={request}>{provenLine(request, t)}</PersonCell>
      <WhatCell request={request} />
      <Table.Cell>
        <Tag tone={approved ? "green" : "red"}>
          {t(
            approved
              ? "identityProofing.reviews.outcome.approved"
              : "identityProofing.reviews.outcome.rejected",
          )}
        </Tag>
        {request.errorCode && (
          <div className={SUB_LINE}>
            {proofingRejectionReason(request.errorCode, t)}
          </div>
        )}
      </Table.Cell>
      <Table.Cell className="whitespace-nowrap">
        {formatWhen(request.completedAt ?? request.createdAt)}
      </Table.Cell>
      <Table.Cell className="text-right">
        <Link
          to={reviewPath(slug, request.id)}
          aria-label={t("identityProofing.reviews.viewOf", { name })}
          className={SECONDARY_LINK}
        >
          {t("identityProofing.reviews.view")}
        </Link>
      </Table.Cell>
    </Table.Row>
  );
}
