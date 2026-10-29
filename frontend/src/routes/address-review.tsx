import { useState } from "react";
import { useNavigate } from "react-router";
import { useTranslation } from "react-i18next";
import * as React from "react";
import { useMeQuery } from "../api/auth.queries";
import type {
  AddressReviewInput,
  ProofingRequest,
} from "../api/identity-proofing";
import { useDecideAddressReviewMutation } from "../api/identity-proofing.queries";
import { useWhenFormatter } from "../lib/format-when";
import {
  addressVerificationNote,
  assuranceLevelLabel,
  proofingErrorMessage,
  proofingMethodLabel,
  requestSubject,
  REVIEW_REASON_MAX_CHARS,
} from "../lib/identity-proofing";
import { toast } from "../lib/toast";
import { ownReview, reasonProblem, reviewPath } from "../lib/proofing-reviews";
import { Button, TopBar } from "../ui";
import {
  BackToReviews,
  DecisionReasons,
  REVIEW_ERROR,
  REVIEW_FIELD_LABEL,
  REVIEW_HINT,
  REVIEW_TEXTAREA,
  ReviewFacts,
  ReviewSection,
} from "./proofing-review-parts";
import { PostalAddress } from "./proofing-ui";

const DECIDED_KEYS = {
  confirmed: "confirmed",
  rejected: "rejected",
} as const;

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
        <ReviewSection
          id="address-review-check"
          title={t("identityProofing.reviews.address.checkTitle")}
          hint={t("identityProofing.reviews.address.checkHint")}
        >
          <ReviewFacts
            rows={[
              {
                label: t("identityProofing.reviews.address.method"),
                value: proofingMethodLabel(request.method, t),
              },
              {
                label: t("identityProofing.reviews.address.level"),
                value: request.eidasLevel
                  ? assuranceLevelLabel(request.eidasLevel, t)
                  : "—",
              },
            ]}
          />
        </ReviewSection>
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
              <AddressDecision slug={slug} requestId={request.id} />
            ))}
          {review !== undefined && review !== "pending" && (
            <output className="border-line bg-surface-2 text-ink rounded-yivi block border px-4 py-3 text-[13.5px]">
              {t(`identityProofing.reviews.address.${DECIDED_KEYS[review]}`)}
              <DecisionReasons request={request} kind="address" />
            </output>
          )}
        </ReviewSection>
        <BackToReviews to={reviewPath(slug)} />
      </div>
    </>
  );
}

// Confirm or reject, with a reason the audit log keeps. A decided address
// leaves the reader's reach, so the page goes back to the Reviews page.
function AddressDecision({
  slug,
  requestId,
}: Readonly<{
  slug: string;
  requestId: string;
}>): React.JSX.Element {
  const { t } = useTranslation();
  const navigate = useNavigate();
  const decide = useDecideAddressReviewMutation(slug, requestId);
  const [reason, setReason] = useState("");
  const [touched, setTouched] = useState(false);
  const problem = reasonProblem(reason);
  const idPrefix = `address-review-${requestId}`;

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
                ? "identityProofing.reviews.address.confirmed"
                : "identityProofing.reviews.address.rejected",
            ),
          );
          void navigate(reviewPath(slug));
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
          {t("identityProofing.reviews.address.confirm")}
        </Button>
        <Button
          variant="secondary"
          loading={decide.isPending && decide.variables?.decision === "reject"}
          disabled={decide.isPending}
          onClick={() => submit("reject")}
        >
          {t("identityProofing.reviews.address.reject")}
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
