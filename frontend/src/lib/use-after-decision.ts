import { useNavigate } from "react-router";
import { useTranslation } from "react-i18next";
import type { ReviewTarget } from "../api/identity-proofing";
import { REVIEW_PART_KEYS, reviewPath } from "./proofing-reviews";
import type { ReviewPart } from "./proofing-reviews";
import { toast } from "./toast";

// Where a review goes once a part is decided: on to the session's next part
// waiting, which the page then shows, else back to the Reviews page. A review
// link's page has no Reviews page: it shows the session decided.
export function useAfterDecision(
  target: ReviewTarget,
): (next?: ReviewPart) => void {
  const { t } = useTranslation();
  const navigate = useNavigate();
  return (next) => {
    if (next === undefined) {
      if (target.kind === "org") void navigate(reviewPath(target.slug));
      else window.scrollTo({ top: 0 });
      return;
    }
    toast.info(
      t("identityProofing.reviews.nextPart", {
        part: t(REVIEW_PART_KEYS[next]),
      }),
    );
    window.scrollTo({ top: 0 });
  };
}
