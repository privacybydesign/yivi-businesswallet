import { useState } from "react";
import { useTranslation } from "react-i18next";
import * as React from "react";
import { useMeQuery } from "../api/auth.queries";
import type {
  AddressReviewInput,
  ProofingRequest,
  StepReviewKind,
} from "../api/identity-proofing";
import { useDecideStepReviewMutation } from "../api/identity-proofing.queries";
import { useWhenFormatter } from "../lib/format-when";
import {
  addressVerificationNote,
  proofingErrorMessage,
  proofingMethodLabel,
  requestSubject,
  REVIEW_REASON_MAX_CHARS,
} from "../lib/identity-proofing";
import { toast } from "../lib/toast";
import {
  nextPart,
  ownReview,
  reasonProblem,
  reviewPath,
  STEP_DECIDED_KEYS,
} from "../lib/proofing-reviews";
import { Button, TopBar } from "../ui";
import {
  BackToReviews,
  DecisionReasons,
  REVIEW_ERROR,
  REVIEW_FIELD_LABEL,
  REVIEW_HINT,
  REVIEW_TEXTAREA,
  ReviewSection,
  SessionDecisions,
} from "./proofing-review-parts";
import { useAfterDecision } from "../lib/use-after-decision";
import { PostalAddress } from "./proofing-ui";

// An address that waits for a person, or was decided: the address (and the
// registered one it differs from), the identity check it came with (decided
// already, and never changed by this), and the decision.
export function AddressReview({
  slug,
  request,
}: Readonly<{
  slug: string;
  request: ProofingRequest;
}>): React.JSX.Element {
  const { t } = useTranslation();
  const formatWhen = useWhenFormatter();
  const me = useMeQuery();
  // An address of a check the reader sent or that is about them is another
  // reviewer's.
  const own = ownReview(request, me.data);
  const address = request.address;
  const review = request.addressReview;
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
        title={t("identityProofing.reviews.address.title")}
        subtitle={subtitle}
      />
      <div className="flex max-w-3xl flex-col gap-6 p-4 sm:p-8">
        <ReviewSection
          id="address-review-why"
          title={t("identityProofing.reviews.address.whyTitle")}
          hint={t(
            address?.verification === "registered_mismatch"
              ? "identityProofing.reviews.address.whyMismatch"
              : "identityProofing.reviews.address.why",
          )}
        >
          {address === undefined ? (
            <p className={REVIEW_HINT}>
              {t("identityProofing.sessions.address.none")}
            </p>
          ) : (
            <PostalAddress address={address} />
          )}
          <p className={REVIEW_HINT}>
            {addressVerificationNote(address?.verification, t)}
          </p>
          {address?.registered !== undefined && (
            <>
              <h3 className={REVIEW_FIELD_LABEL}>
                {t("identityProofing.sessions.address.registeredTitle")}
              </h3>
              <PostalAddress address={address.registered} />
            </>
          )}
        </ReviewSection>
        <SessionDecisions request={request} except="address" />
        <ReviewSection
          id="address-review-decision"
          title={t("identityProofing.reviews.address.decisionTitle")}
        >
          {review === "pending" &&
            (own ? (
              <output className="block text-[13.5px]">
                {t(`identityProofing.reviews.check.own.${own}`)}
              </output>
            ) : (
              <StepDecision slug={slug} request={request} kind="address" />
            ))}
          {review !== undefined && review !== "pending" && (
            <output className="border-line bg-surface-2 text-ink rounded-yivi block border px-4 py-3 text-[13.5px]">
              {t(
                `identityProofing.reviews.address.${STEP_DECIDED_KEYS[review]}`,
              )}
              <DecisionReasons request={request} kind="address" />
            </output>
          )}
        </ReviewSection>
        <BackToReviews to={reviewPath(slug)} />
      </div>
    </>
  );
}

// Confirm or reject a step (the address, the Verzekeringsbericht), with a
// reason the audit log keeps. The review then goes on to the session's next
// part waiting, else back to the Reviews page.
export function StepDecision({
  slug,
  request,
  kind,
}: Readonly<{
  slug: string;
  request: ProofingRequest;
  kind: StepReviewKind;
}>): React.JSX.Element {
  const { t } = useTranslation();
  const afterDecision = useAfterDecision(slug);
  const requestId = request.id;
  const decide = useDecideStepReviewMutation(slug, requestId, kind);
  const [reason, setReason] = useState("");
  const [touched, setTouched] = useState(false);
  const problem = reasonProblem(reason);
  const idPrefix = `${kind}-review-${requestId}`;

  function submit(decision: AddressReviewInput["decision"]): void {
    setTouched(true);
    if (problem !== undefined) return;
    decide.mutate(
      { decision, reason: reason.trim() },
      {
        onSuccess: () => {
          toast.success(
            t(
              decision === "confirm"
                ? `identityProofing.reviews.${kind}.confirmed`
                : `identityProofing.reviews.${kind}.rejected`,
            ),
          );
          afterDecision(nextPart(request, kind));
        },
      },
    );
  }

  return (
    <div className="flex flex-col gap-4">
      <div className="flex flex-col gap-1.5">
        <label htmlFor={`${idPrefix}-reason`} className={REVIEW_FIELD_LABEL}>
          {t("identityProofing.reviews.check.decision.reason")}
        </label>
        <textarea
          id={`${idPrefix}-reason`}
          value={reason}
          aria-invalid={touched && problem !== undefined}
          aria-describedby={`${idPrefix}-reason-hint`}
          onChange={(event) => setReason(event.target.value)}
          className={REVIEW_TEXTAREA}
        />
        <p id={`${idPrefix}-reason-hint`} className={REVIEW_HINT}>
          {t("identityProofing.reviews.check.decision.reasonHint")}
        </p>
        {touched && problem === "reason" && (
          <p className={REVIEW_ERROR}>
            {t("identityProofing.sessions.review.reasonRequired")}
          </p>
        )}
        {problem === "reasonTooLong" && (
          <p className={REVIEW_ERROR}>
            {t("identityProofing.sessions.review.reasonTooLong", {
              max: REVIEW_REASON_MAX_CHARS,
            })}
          </p>
        )}
      </div>
      <div className="flex flex-wrap gap-2">
        <Button
          loading={decide.isPending && decide.variables?.decision === "confirm"}
          disabled={decide.isPending}
          onClick={() => submit("confirm")}
        >
          {t(`identityProofing.reviews.${kind}.confirm`)}
        </Button>
        <Button
          variant="secondary"
          loading={decide.isPending && decide.variables?.decision === "reject"}
          disabled={decide.isPending}
          onClick={() => submit("reject")}
        >
          {t(`identityProofing.reviews.${kind}.reject`)}
        </Button>
      </div>
      {decide.isError && (
        <p role="alert" className={REVIEW_ERROR}>
          {proofingErrorMessage(decide.error, t)}
        </p>
      )}
    </div>
  );
}
