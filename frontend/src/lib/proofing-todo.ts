import type { ProofingRequest } from "../api/identity-proofing";
import { isProofingLive } from "./identity-proofing";

// How many rows a list shows before it leaves the rest to All people.
export const TODO_ROWS = 5;

// The statuses "Recently finished" lists. A withdrawn check is not news, and
// one in review is not finished.
const FINISHED_STATUSES = new Set(["approved", "rejected", "expired"]);

function sentAt(request: ProofingRequest): number {
  return Date.parse(request.createdAt);
}

// When a check ended. One that ran out has no result time, only when it was
// sent.
export function finishedOn(request: ProofingRequest): string {
  return request.completedAt ?? request.createdAt;
}

// The checks the person has not finished, the longest waiting first.
export function unfinishedChecks(
  requests: readonly ProofingRequest[],
): ProofingRequest[] {
  return requests
    .filter((r) => isProofingLive(r.status))
    .sort((a, b) => sentAt(a) - sentAt(b));
}

// The checks that ended in a result, the latest first.
export function finishedChecks(
  requests: readonly ProofingRequest[],
): ProofingRequest[] {
  return requests
    .filter((r) => FINISHED_STATUSES.has(r.status))
    .sort((a, b) => Date.parse(finishedOn(b)) - Date.parse(finishedOn(a)));
}

// Which proofing lists the dashboard reads. While a platform admin has paused
// the org's proofing, every org proofing route answers 403, so only what comes
// from elsewhere (the member insights) is left to show.
export function dashboardReads({
  paused,
  canReview,
}: {
  paused: boolean;
  canReview: boolean;
}): { reviews: boolean; checks: boolean } {
  return { reviews: canReview && !paused, checks: !paused };
}
