import { useEffect, useMemo, useRef } from "react";
import { useParams } from "react-router";
import { useTranslation } from "react-i18next";
import * as React from "react";
import { useReviewLinkQuery } from "../api/identity-proofing.queries";
import type { ReviewTarget } from "../api/identity-proofing";
import { ApiError } from "../api/http";
import { useWhenFormatter } from "../lib/format-when";
import { postReviewCompleted, reviewDone } from "../lib/review-link";
import { RequestReview } from "./proofing-review";

const NOT_FOUND_STATUS = 404;

// The hosted review page a review link opens, /r/:token: one session's review,
// as the wallet's Reviews page shows it, for the reviewer the org's software
// named. Framed on one of the org's allowed origins, it tells that page when
// nothing waits any more.
export default function ReviewLink(): React.JSX.Element {
  const { t } = useTranslation();
  const formatWhen = useWhenFormatter();
  // Guaranteed by the "/r/:token" route this component mounts under.
  const token = useParams().token!;
  const target = useMemo<ReviewTarget>(
    () => ({ kind: "link", token }),
    [token],
  );
  const page = useReviewLinkQuery(token);
  const posted = useRef(false);

  const data = page.data;
  const done = data !== undefined && reviewDone(data.request);
  useEffect(() => {
    if (!done || posted.current || window.parent === window) return;
    posted.current = true;
    postReviewCompleted(
      window.parent,
      data.embedOrigins,
      data.sessionId,
      data.request.status,
    );
  }, [done, data]);

  if (page.isPending) {
    return (
      <p className="text-ink-soft p-8 text-[14px]">{t("common.loading")}</p>
    );
  }
  if (data === undefined) {
    const gone =
      page.error instanceof ApiError && page.error.status === NOT_FOUND_STATUS;
    return (
      <p role="alert" className="text-ink p-8 text-[14px]">
        {t(gone ? "reviewLink.invalid" : "reviewLink.error")}
      </p>
    );
  }
  return (
    <div className="bg-surface-2 min-h-screen">
      <div className="border-line bg-surface flex flex-wrap items-center justify-between gap-2 border-b px-4 py-3 text-[13px] sm:px-8">
        <span className="font-semibold">{data.organization}</span>
        <span className="text-ink-soft">
          {t("reviewLink.reviewingAs", {
            name: data.reviewer,
            until: formatWhen(data.expiresAt),
          })}
        </span>
      </div>
      {done && (
        <output className="border-line bg-surface-2 text-ink rounded-yivi mx-4 mt-4 block border px-4 py-3 text-[13.5px] sm:mx-8">
          {t("reviewLink.done")}
        </output>
      )}
      <RequestReview target={target} request={data.request} />
    </div>
  );
}
