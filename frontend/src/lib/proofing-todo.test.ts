import { describe, expect, it } from "vitest";
import { namelessKey, todoShows } from "./proofing-todo";

describe("namelessKey", () => {
  it("says a purged request was purged, not that it had no name", () => {
    expect(namelessKey({ purgedAt: "2026-10-20T00:00:00Z" })).toBe(
      "identityProofing.sessions.purgedSubject",
    );
    expect(namelessKey({})).toBe("identityProofing.requests.noName");
  });
});

describe("todoShows", () => {
  it("hides only Verify a person while the org is paused", () => {
    expect(todoShows({ isAdmin: true, canReview: true, paused: true })).toEqual(
      { verify: false, decisions: true, orgDetails: true },
    );
    expect(
      todoShows({ isAdmin: true, canReview: true, paused: false }),
    ).toEqual({ verify: true, decisions: true, orgDetails: true });
  });

  it("shows decisions to a reviewer and the org details line to an admin", () => {
    expect(
      todoShows({ isAdmin: false, canReview: true, paused: false }),
    ).toEqual({ verify: true, decisions: true, orgDetails: false });
    expect(
      todoShows({ isAdmin: false, canReview: false, paused: false }),
    ).toEqual({ verify: true, decisions: false, orgDetails: false });
  });
});
