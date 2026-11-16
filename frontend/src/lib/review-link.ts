import type { ProofingRequest } from "../api/identity-proofing";
import type { MessageTarget } from "./hosted-completion";
import { decidedParts, waitingParts } from "./proofing-reviews";

// The message an embedding page receives once nothing of the session waits
// for its reviewer any more.
export interface ReviewCompletedMessage {
  type: "proofing.review_completed";
  session: string;
  status: string;
}

// Whether a review link's session is reviewed: something was decided and
// nothing waits.
export function reviewDone(request: ProofingRequest): boolean {
  return waitingParts(request).length === 0 && decidedParts(request).length > 0;
}

// Posts that the review is done to each of the org's origins, never "*", as a
// hosted page posts its completion.
export function postReviewCompleted(
  parent: MessageTarget,
  origins: readonly string[],
  session: string,
  status: string,
): void {
  const message: ReviewCompletedMessage = {
    type: "proofing.review_completed",
    session,
    status,
  };
  for (const origin of origins) {
    parent.postMessage(message, origin);
  }
}
