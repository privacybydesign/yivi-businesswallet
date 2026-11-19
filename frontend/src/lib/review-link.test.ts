import { describe, expect, it } from "vitest";
import type { ProofingRequest } from "../api/identity-proofing";
import { postReviewCompleted, reviewDone } from "./review-link";

function request(over: Partial<ProofingRequest> = {}): ProofingRequest {
  return {
    id: "r1",
    requestedByName: "Sam",
    external: true,
    subjectName: "Noor",
    subjectEmail: "noor@example.org",
    flowId: "f1",
    flowName: "Ticket",
    status: "needs_review",
    linkExpiresAt: "2026-10-30T00:00:00Z",
    createdAt: "2026-10-16T14:00:00Z",
    diplomaMode: "off",
    diplomas: [],
    addressMode: "off",
    employmentMode: "off",
    expectedSubject: false,
    flowKind: "identity",
    ...over,
  };
}

describe("reviewDone", () => {
  it("is done once a part was decided and nothing waits", () => {
    expect(reviewDone(request())).toBe(false);
    expect(
      reviewDone(
        request({
          status: "approved",
          identityReviewedAt: "2026-11-16T10:00:00Z",
          addressReview: "pending",
        }),
      ),
    ).toBe(false);
    expect(
      reviewDone(
        request({
          status: "approved",
          identityReviewedAt: "2026-11-16T10:00:00Z",
        }),
      ),
    ).toBe(true);
  });
});

describe("postReviewCompleted", () => {
  it("posts to each allowed origin, never to any", () => {
    const posted: [unknown, string][] = [];
    postReviewCompleted(
      { postMessage: (message, origin) => posted.push([message, origin]) },
      ["https://backoffice.example.org", "http://localhost:3000"],
      "ps_x",
      "approved",
    );
    expect(posted).toEqual([
      [
        {
          type: "proofing.review_completed",
          session: "ps_x",
          status: "approved",
        },
        "https://backoffice.example.org",
      ],
      [
        {
          type: "proofing.review_completed",
          session: "ps_x",
          status: "approved",
        },
        "http://localhost:3000",
      ],
    ]);
  });
});
