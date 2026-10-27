import { useTranslation } from "react-i18next";
import * as React from "react";
import type { ProofingRequest } from "../api/identity-proofing";
import { useDecideAddressReviewMutation } from "../api/identity-proofing.queries";
import { useWhenFormatter } from "../lib/format-when";
import {
  addressVerificationNote,
  assuranceLevelLabel,
  proofingErrorMessage,
  proofingMethodLabel,
  requestSubject,
} from "../lib/identity-proofing";
import { reviewPath } from "../lib/proofing-reviews";
import { Button, TopBar } from "../ui";
import {
  BackToReviews,
  REVIEW_ERROR,
  REVIEW_HINT,
  ReviewFacts,
  ReviewSection,
} from "./proofing-review-parts";
import { PostalAddress } from "./proofing-ui";

const DECIDED_KEYS = {
  confirmed: "confirmed",
  rejected: "rejected",
} as const;

// A self-declared address that waits for a person, or was decided: the
// address, the identity check it came with (decided already, and never
// changed by this), and the decision.
export function AddressReview({
  slug,
  request,
}: Readonly<{
  slug: string;
  request: ProofingRequest;
}>): React.JSX.Element {
  const { t } = useTranslation();
  const formatWhen = useWhenFormatter();
  const decide = useDecideAddressReviewMutation(slug, request.id);
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
          hint={t("identityProofing.reviews.address.why")}
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
          {review === "pending" ? (
            <div className="flex flex-wrap gap-2">
              <Button
                loading={decide.isPending && decide.variables === "confirm"}
                disabled={decide.isPending}
                onClick={() => decide.mutate("confirm")}
              >
                {t("identityProofing.sessions.address.confirm")}
              </Button>
              <Button
                variant="secondary"
                loading={decide.isPending && decide.variables === "reject"}
                disabled={decide.isPending}
                onClick={() => decide.mutate("reject")}
              >
                {t("identityProofing.sessions.address.reject")}
              </Button>
            </div>
          ) : (
            review !== undefined && (
              <output className="border-line bg-surface-2 text-ink rounded-yivi block border px-4 py-3 text-[13.5px]">
                {t(`identityProofing.reviews.address.${DECIDED_KEYS[review]}`)}
              </output>
            )
          )}
          {decide.isError && (
            <p role="alert" className={REVIEW_ERROR}>
              {proofingErrorMessage(decide.error, t)}
            </p>
          )}
        </ReviewSection>
        <BackToReviews to={reviewPath(slug)} />
      </div>
    </>
  );
}
