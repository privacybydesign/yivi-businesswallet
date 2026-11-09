import { useParams } from "react-router";
import { useTranslation } from "react-i18next";
import * as React from "react";
import { useOrganizationQuery } from "../api/organization.queries";
import { useProofingRequestQuery } from "../api/identity-proofing.queries";
import { proofingErrorMessage } from "../lib/identity-proofing";
import { TopBar } from "../ui";
import { DataRequestReview } from "./data-request-review";
import { IdentityCheckReview } from "./identity-check-review";
import { AddressReview } from "./address-review";
import { EmploymentReview } from "./employment-review";
import { DiplomasReview } from "./diplomas-review";
import type { ProofingRequest, StepReviewKind } from "../api/identity-proofing";
import { stepReviewOf, waitingParts } from "../lib/proofing-reviews";

// The page of each step a reviewer decides.
const STEP_REVIEW_PAGES = {
  employment: EmploymentReview,
  diplomas: DiplomasReview,
  address: AddressReview,
} as const satisfies Record<
  StepReviewKind,
  (props: { slug: string; request: ProofingRequest }) => React.JSX.Element
>;

// A request's review page: the step that waits, else its identity check or
// data request. A closed session opens as its identity check, with every
// decision of its review listed together.
function RequestReview({
  slug,
  request,
}: Readonly<{ slug: string; request: ProofingRequest }>): React.JSX.Element {
  const step =
    waitingParts(request).length === 0 ? undefined : stepReviewOf(request);
  if (step !== undefined) {
    const Page = STEP_REVIEW_PAGES[step];
    return <Page slug={slug} request={request} />;
  }
  return request.flowKind === "identity" ? (
    <IdentityCheckReview slug={slug} request={request} />
  ) : (
    <DataRequestReview slug={slug} kind={request.flowKind} request={request} />
  );
}

// One thing that waits for a decision, or was decided: the page of an identity
// check, of one of its steps (a self-declared address, a Verzekeringsbericht,
// diplomas), or of a data request. For an admin and a member with the reviewer right.
export default function ProofingReview(): React.JSX.Element {
  const { t } = useTranslation();
  const { orgSlug, requestId } = useParams();
  // Guaranteed by the ":orgSlug/.../:requestId" route this component mounts under.
  const slug = orgSlug!;
  const id = requestId!;
  const org = useOrganizationQuery(slug);
  const canReview = org.data?.canReview === true;
  const request = useProofingRequestQuery(slug, id, { enabled: canReview });

  if (request.data) {
    return <RequestReview slug={slug} request={request.data} />;
  }
  let message: string;
  if (org.isPending || (canReview && request.isPending)) {
    message = t("common.loading");
  } else if (canReview) {
    message = proofingErrorMessage(request.error, t);
  } else {
    message = t("identityProofing.reviews.notAllowed");
  }
  return (
    <>
      <TopBar title={t("identityProofing.reviews.title")} />
      <p className="text-ink-soft p-4 text-[14px] sm:p-8">{message}</p>
    </>
  );
}
