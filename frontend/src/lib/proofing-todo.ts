import type { ListedStatus, ProofingRequest } from "../api/identity-proofing";
import { isProofingLive } from "./identity-proofing";
import { waitingOldestFirst } from "./proofing-reviews";

// How many rows a list shows before it leaves the rest to All people.
export const TODO_ROWS = 5;

// The statuses of a check the person has not finished.
export const UNFINISHED_STATUSES: readonly ListedStatus[] = [
  "pending",
  "in_progress",
];

// The status of a check held for a decision.
export const HELD_STATUSES: readonly ListedStatus[] = ["needs_review"];

// The statuses "Recently finished" lists. A withdrawn check is not news, and
// one in review is not finished.
const FINISHED_STATUSES: readonly ListedStatus[] = [
  "approved",
  "rejected",
  "expired",
];
const FINISHED = new Set<string>(FINISHED_STATUSES);

// The statuses the to-do lists read, each on its own so the backend's cap
// counts each apart: the checks the person has not finished; for a reader who
// cannot decide, their own checks held for an admin (a reviewer finds those
// under Waiting for your decision); and, with finished, the ones that ended.
export function todoStatuses({
  canReview,
  finished,
}: {
  canReview: boolean;
  finished: boolean;
}): ListedStatus[] {
  return [
    ...UNFINISHED_STATUSES,
    ...(canReview ? [] : HELD_STATUSES),
    ...(finished ? FINISHED_STATUSES : []),
  ];
}

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

// What Waiting for the person lists: for a reader who cannot decide, their own
// checks held for an admin first, since the dashboard sends them here to find
// those and a list shows only its first rows; then the checks the person has
// not finished.
export function waitingForOthers(
  requests: readonly ProofingRequest[],
  { canReview }: { canReview: boolean },
): ProofingRequest[] {
  return [
    ...(canReview ? [] : waitingOldestFirst(requests)),
    ...unfinishedChecks(requests),
  ];
}

// The checks that ended in a result, the latest first.
export function finishedChecks(
  requests: readonly ProofingRequest[],
): ProofingRequest[] {
  return requests
    .filter((r) => FINISHED.has(r.status))
    .sort((a, b) => Date.parse(finishedOn(b)) - Date.parse(finishedOn(a)));
}

// Whether one of statuses' reads hit the cap, so a count of rows read from them
// is only a lower bound (listedCount).
export function cappedAmong(
  capped: readonly ListedStatus[],
  statuses: readonly ListedStatus[],
): boolean {
  return statuses.some((status) => capped.includes(status));
}

// What stands in for the name of a person without one: their data was purged,
// or the check was sent without a name.
export function namelessKey(
  request: Pick<ProofingRequest, "purgedAt">,
):
  | "identityProofing.sessions.purgedSubject"
  | "identityProofing.requests.noName" {
  return request.purgedAt
    ? "identityProofing.sessions.purgedSubject"
    : "identityProofing.requests.noName";
}

// What the dashboard and the Overview show the reader. A platform pause stops
// only what starts proofing, so it hides Verify a person and leaves every list
// (their routes stay open, and running checks still settle). The line pointing
// to Settings › Organisation is an admin's: only an admin opens that page.
export function todoShows({
  isAdmin,
  canReview,
  paused,
}: {
  isAdmin: boolean;
  canReview: boolean;
  paused: boolean;
}): { verify: boolean; decisions: boolean; orgDetails: boolean } {
  return { verify: !paused, decisions: canReview, orgDetails: isAdmin };
}
