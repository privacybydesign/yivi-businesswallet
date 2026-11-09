import { useTranslation } from "react-i18next";
import * as React from "react";
import { useMeQuery } from "../api/auth.queries";
import type { ProofingRequest } from "../api/identity-proofing";
import { useWhenFormatter } from "../lib/format-when";
import { proofingMethodLabel, requestSubject } from "../lib/identity-proofing";
import {
  ownReview,
  reviewPath,
  STEP_DECIDED_KEYS,
} from "../lib/proofing-reviews";
import { TopBar } from "../ui";
import { StepDecision } from "./address-review";
import {
  BackToReviews,
  DecisionReasons,
  ReviewProgress,
  ReviewSection,
  SessionDecisions,
} from "./proofing-review-parts";
import { SessionDiplomas } from "./proofing-people";

// The kept diploma extracts of a flow that sends them to review: why, the
// extracts, the session's other decisions, and the decision.
export function DiplomasReview({
  slug,
  request,
}: Readonly<{
  slug: string;
  request: ProofingRequest;
}>): React.JSX.Element {
  const { t } = useTranslation();
  const formatWhen = useWhenFormatter();
  const me = useMeQuery();
  const own = ownReview(request, me.data);
  const review = request.diplomasReview;
  const subtitle = [
    requestSubject(request).name,
    request.flowName,
    proofingMethodLabel(request.method, t),
    formatWhen(request.createdAt),
  ]
    .filter((part) => part !== "" && part !== "—")
    .join(" · ");

  return (
    <>
      <TopBar
        title={t("identityProofing.reviews.diplomas.title")}
        subtitle={subtitle}
      />
      <div className="flex max-w-3xl flex-col gap-6 p-4 sm:p-8">
        <ReviewProgress request={request} />
        <ReviewSection
          id="diplomas-review-why"
          title={t("identityProofing.reviews.diplomas.whyTitle")}
          hint={t("identityProofing.reviews.diplomas.whyAlways")}
        >
          <SessionDiplomas request={request} />
        </ReviewSection>
        <SessionDecisions request={request} except="diplomas" />
        <ReviewSection
          id="diplomas-review-decision"
          title={t("identityProofing.reviews.diplomas.decisionTitle")}
        >
          {review === "pending" &&
            (own ? (
              <output className="block text-[13.5px]">
                {t(`identityProofing.reviews.check.own.${own}`)}
              </output>
            ) : (
              <StepDecision slug={slug} request={request} kind="diplomas" />
            ))}
          {review !== undefined && review !== "pending" && (
            <output className="border-line bg-surface-2 text-ink rounded-yivi block border px-4 py-3 text-[13.5px]">
              {t(
                `identityProofing.reviews.diplomas.${STEP_DECIDED_KEYS[review]}`,
              )}
              <DecisionReasons request={request} kind="diplomas" />
            </output>
          )}
        </ReviewSection>
        <BackToReviews to={reviewPath(slug)} />
      </div>
    </>
  );
}
