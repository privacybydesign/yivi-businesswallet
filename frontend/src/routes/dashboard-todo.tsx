import { Link } from "react-router";
import { useTranslation } from "react-i18next";
import * as React from "react";
import { useMemberInsightsQuery } from "../api/organization.queries";
import {
  useExternalRequestsInQuery,
  useWaitingReviewsQuery,
} from "../api/identity-proofing.queries";
import { accessMessage } from "../lib/access-message";
import {
  listedCount,
  proofingErrorMessage,
  requestListCapped,
} from "../lib/identity-proofing";
import {
  notIdentifiedCount,
  screeningAttentionCount,
} from "../lib/member-insights";
import { reviewPath, waitingOldestFirst } from "../lib/proofing-reviews";
import {
  HELD_STATUSES,
  UNFINISHED_STATUSES,
  cappedAmong,
  todoStatuses,
  unfinishedChecks,
} from "../lib/proofing-todo";
import { TaskEmpty, TaskRow, TaskSection } from "../ui";
import { PRIMARY_LINK, SECONDARY_LINK } from "./proofing-review-parts";

const ERROR = "text-error px-5 pt-3.5 pb-[18px] text-[12.5px]";

// One thing that needs the reader, and where they act on it.
interface Item {
  key: string;
  title: string;
  detail: string;
  to: string;
  action: string;
}

function ItemRows({
  items,
  className,
}: {
  items: Item[];
  className: string;
}): React.JSX.Element {
  return (
    <ul>
      {items.map((item) => (
        <TaskRow
          key={item.key}
          title={item.title}
          detail={item.detail}
          aside={
            <Link to={item.to} className={className}>
              {item.action}
            </Link>
          }
        />
      ))}
    </ul>
  );
}

// What needs the reader across the org: what waits for their decision, and,
// for an admin, the members who have not identified or whose VOG needs
// attention.
export function DashboardTodo({
  slug,
  orgName,
  isAdmin,
  canReview,
}: {
  slug: string;
  orgName: string;
  isAdmin: boolean;
  canReview: boolean;
}): React.JSX.Element {
  const { t } = useTranslation();
  const insights = useMemberInsightsQuery(slug, isAdmin);
  const waiting = useWaitingReviewsQuery(slug, { enabled: canReview });
  const items: Item[] = [];

  // A disabled query keeps its last result, from before the right was
  // withdrawn too.
  const held = canReview ? (waiting.data ?? []) : [];
  const reviews = waitingOldestFirst(held).length;
  if (reviews > 0) {
    items.push({
      key: "reviews",
      title: t("dashboard.todo.reviews", {
        count: reviews,
        number: listedCount(reviews, requestListCapped(held), t),
      }),
      detail: t("dashboard.todo.reviewsDetail"),
      to: reviewPath(slug),
      action: t("dashboard.todo.openReviews"),
    });
  }

  const notIdentified = insights.data
    ? notIdentifiedCount(insights.data.identity)
    : 0;
  if (notIdentified > 0) {
    items.push({
      key: "notIdentified",
      title: t("dashboard.todo.notIdentified", { count: notIdentified }),
      detail: t("dashboard.todo.notIdentifiedDetail", { org: orgName }),
      to: `/${slug}/members`,
      action: t("dashboard.viewMembers"),
    });
  }

  const vog = insights.data
    ? screeningAttentionCount(insights.data.screening)
    : 0;
  if (vog > 0) {
    items.push({
      key: "vog",
      title: t("dashboard.todo.vogAttention", { count: vog }),
      detail: t("dashboard.todo.vogAttentionDetail"),
      to: `/${slug}/members`,
      action: t("dashboard.viewMembers"),
    });
  }

  const loading =
    (isAdmin && insights.isPending) || (canReview && waiting.isPending);
  let failure: string | undefined;
  if (insights.isError) {
    failure = t("dashboard.todo.error", {
      message: accessMessage(insights.error, t),
    });
  } else if (canReview && waiting.isError) {
    failure = proofingErrorMessage(waiting.error, t);
  }

  return (
    <TaskSection
      title={t("dashboard.todo.title")}
      count={loading ? undefined : items.length}
      hint={t("dashboard.todo.hint")}
    >
      {items.length > 0 && <ItemRows items={items} className={PRIMARY_LINK} />}
      {failure && (
        <p role="alert" className={ERROR}>
          {failure}
        </p>
      )}
      {items.length === 0 && !failure && (
        <TaskEmpty>
          {loading ? t("common.loading") : t("dashboard.todo.empty")}
        </TaskEmpty>
      )}
    </TaskSection>
  );
}

// Who the org waits for: people who have not finished a check, and for a
// member who cannot decide, their own checks held for an admin.
export function DashboardWaiting({
  slug,
  canReview,
}: {
  slug: string;
  canReview: boolean;
}): React.JSX.Element {
  const { t } = useTranslation();
  const requests = useExternalRequestsInQuery(
    slug,
    todoStatuses({ canReview, finished: false }),
  );
  const all = requests.data ?? [];
  const items: Item[] = [];
  const overview = `/${slug}/identity-proofing`;

  const unfinished = unfinishedChecks(all).length;
  if (unfinished > 0) {
    items.push({
      key: "unfinished",
      title: t("dashboard.waiting.unfinished", {
        count: unfinished,
        number: listedCount(
          unfinished,
          cappedAmong(requests.capped, UNFINISHED_STATUSES),
          t,
        ),
      }),
      detail: t("dashboard.waiting.unfinishedDetail"),
      to: overview,
      action: t("dashboard.waiting.openOverview"),
    });
  }

  const forAdmin = canReview ? 0 : waitingOldestFirst(all).length;
  if (forAdmin > 0) {
    items.push({
      key: "forAdmin",
      title: t("dashboard.waiting.forAdmin", {
        count: forAdmin,
        number: listedCount(
          forAdmin,
          cappedAmong(requests.capped, HELD_STATUSES),
          t,
        ),
      }),
      detail: t("dashboard.waiting.forAdminDetail"),
      to: overview,
      action: t("dashboard.waiting.openOverview"),
    });
  }

  let body: React.JSX.Element;
  if (requests.isError) {
    body = (
      <p role="alert" className={ERROR}>
        {proofingErrorMessage(requests.error, t)}
      </p>
    );
  } else if (items.length > 0) {
    body = <ItemRows items={items} className={SECONDARY_LINK} />;
  } else {
    body = (
      <TaskEmpty>
        {requests.isPending
          ? t("common.loading")
          : t("dashboard.waiting.empty")}
      </TaskEmpty>
    );
  }
  return (
    <TaskSection
      title={t("dashboard.waiting.title")}
      hint={t("dashboard.waiting.hint")}
    >
      {body}
    </TaskSection>
  );
}
