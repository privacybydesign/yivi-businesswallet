import { useNavigate } from "react-router";
import { useTranslation } from "react-i18next";
import { REVIEW_PART_KEYS, reviewPath } from "./proofing-reviews";
import type { ReviewPart } from "./proofing-reviews";
import { toast } from "./toast";

// Where a review goes once a part is decided: on to the session's next part
// waiting, which the page then shows, else back to the Reviews page.
export function useAfterDecision(slug: string): (next?: ReviewPart) => void {
  const { t } = useTranslation();
  const navigate = useNavigate();
  return (next) => {
    if (next === undefined) {
      void navigate(reviewPath(slug));
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
