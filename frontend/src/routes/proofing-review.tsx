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
import { isAddressReview } from "../lib/proofing-reviews";

// One thing that waits for a decision, or was decided: the page of an identity
// check, of a self-declared address, or of a data request. For an admin and a member with the reviewer right.
export default function ProofingReview(): React.JSX.Element {
  const { t } = useTranslation();
  const { orgSlug, requestId } = useParams();
  // Guaranteed by the ":orgSlug/.../:requestId" route this component mounts under.
  const slug = orgSlug!;
  const id = requestId!;
  const org = useOrganizationQuery(slug);
  const canReview = org.data?.canReview === true;
  const request = useProofingRequestQuery(slug, id, { enabled: canReview });

  const { data } = request;
  if (data && isAddressReview(data)) {
    return <AddressReview slug={slug} request={data} />;
  }
  if (data) {
    return data.flowKind === "identity" ? (
      <IdentityCheckReview slug={slug} request={data} />
    ) : (
      <DataRequestReview slug={slug} kind={data.flowKind} request={data} />
    );
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
