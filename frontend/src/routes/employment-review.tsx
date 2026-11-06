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
  walletReviewReason,
} from "../lib/proofing-reviews";
import { TopBar } from "../ui";
import { StepDecision } from "./address-review";
import {
  BackToReviews,
  DecisionReasons,
  ReviewSection,
  SessionDecisions,
} from "./proofing-review-parts";
import { SessionEmployment } from "./proofing-people";

// A Verzekeringsbericht that waits for a person, or was decided: why, the
// statement, the identity check it came with (decided already, and never
// changed by this), and the decision.
export function EmploymentReview({
  slug,
  request,
}: Readonly<{
  slug: string;
  request: ProofingRequest;
}>): React.JSX.Element {
  const { t } = useTranslation();
  const formatWhen = useWhenFormatter();
  const me = useMeQuery();
  // A statement of a check the reader sent or that is about them is another
  // reviewer's.
  const own = ownReview(request, me.data);
  const review = request.employmentReview;
  const reason = walletReviewReason(request);
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
        title={t("identityProofing.reviews.employment.title")}
        subtitle={subtitle}
      />
      <div className="flex max-w-3xl flex-col gap-6 p-4 sm:p-8">
        <ReviewSection
          id="employment-review-why"
          title={t("identityProofing.reviews.employment.whyTitle")}
          hint={t(
            `identityProofing.reviews.check.why.${reason ?? "no_current_employment"}`,
          )}
        >
          <SessionEmployment request={request} />
        </ReviewSection>
        <SessionDecisions request={request} except="employment" />
        <ReviewSection
          id="employment-review-decision"
          title={t("identityProofing.reviews.employment.decisionTitle")}
        >
          {review === "pending" &&
            (own ? (
              <output className="block text-[13.5px]">
                {t(`identityProofing.reviews.check.own.${own}`)}
              </output>
            ) : (
              <StepDecision slug={slug} request={request} kind="employment" />
            ))}
          {review !== undefined && review !== "pending" && (
            <output className="border-line bg-surface-2 text-ink rounded-yivi block border px-4 py-3 text-[13.5px]">
              {t(
                `identityProofing.reviews.employment.${STEP_DECIDED_KEYS[review]}`,
              )}
              <DecisionReasons request={request} kind="employment" />
            </output>
          )}
        </ReviewSection>
        <BackToReviews to={reviewPath(slug)} />
      </div>
    </>
  );
}
