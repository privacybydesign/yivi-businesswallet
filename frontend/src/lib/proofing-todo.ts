import type { ProofingRequest } from "../api/identity-proofing";

// How many rows a list shows before it leaves the rest to All people.
export const TODO_ROWS = 5;

// When a check ended. One that ran out has no result time, only when it was
// sent.
export function finishedOn(request: ProofingRequest): string {
  return request.completedAt ?? request.createdAt;
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
