import { describe, expect, it } from "vitest";
import i18n from "../i18n";
import type { ProofingRequest } from "../api/identity-proofing";
import { MAX_LISTED_REQUESTS } from "../api/identity-proofing";
import { listedCount } from "./identity-proofing";
import {
  TODO_ROWS,
  cappedAmong,
  finishedChecks,
  namelessKey,
  todoShows,
  todoStatuses,
  unfinishedChecks,
  waitingForOthers,
} from "./proofing-todo";

function request(over: Partial<ProofingRequest> = {}): ProofingRequest {
  return {
    id: "r1",
    requestedByName: "Sam",
    external: true,
    subjectName: "Noor",
    subjectEmail: "noor@example.org",
    flowId: "f1",
    flowName: "Ticket",
    status: "pending",
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

describe("unfinishedChecks", () => {
  it("keeps the checks the person can still finish, longest waiting first", () => {
    const newer = request({ id: "newer", createdAt: "2026-10-16T15:00:00Z" });
    const older = request({
      id: "older",
      status: "in_progress",
      createdAt: "2026-10-16T13:00:00Z",
    });
    const done = request({ id: "done", status: "approved" });
    const held = request({ id: "held", status: "needs_review" });

    expect(
      unfinishedChecks([newer, done, held, older]).map((r) => r.id),
    ).toEqual(["older", "newer"]);
  });

  it("leaves the list it is given as it was", () => {
    const list = [
      request({ id: "b", createdAt: "2026-10-16T15:00:00Z" }),
      request({ id: "a", createdAt: "2026-10-16T13:00:00Z" }),
    ];
    unfinishedChecks(list);
    expect(list.map((r) => r.id)).toEqual(["b", "a"]);
  });
});

describe("finishedChecks", () => {
  it("lists verified, failed and expired checks, the latest finish first", () => {
    const early = request({
      id: "early",
      status: "approved",
      completedAt: "2026-10-16T14:05:00Z",
    });
    const late = request({
      id: "late",
      status: "rejected",
      completedAt: "2026-10-16T16:00:00Z",
    });
    const expired = request({
      id: "expired",
      status: "expired",
      createdAt: "2026-10-16T15:00:00Z",
    });

    expect(finishedChecks([early, expired, late]).map((r) => r.id)).toEqual([
      "late",
      "expired",
      "early",
    ]);
  });

  it("leaves out what is not finished or is not news", () => {
    const rest = ["pending", "in_progress", "needs_review", "cancelled"].map(
      (status) => request({ id: status, status }),
    );
    expect(finishedChecks(rest)).toEqual([]);
  });
});

describe("todoStatuses", () => {
  it("reads a member's own checks held for an admin, not a reviewer's", () => {
    expect(todoStatuses({ canReview: false, finished: false })).toEqual([
      "pending",
      "in_progress",
      "needs_review",
    ]);
    expect(todoStatuses({ canReview: true, finished: false })).toEqual([
      "pending",
      "in_progress",
    ]);
  });

  it("reads each status a finished check ends in, expired included", () => {
    expect(todoStatuses({ canReview: true, finished: true })).toEqual([
      "pending",
      "in_progress",
      "approved",
      "rejected",
      "expired",
    ]);
  });
});

describe("waitingForOthers", () => {
  // A member's check held for an admin among more open links than a list
  // shows: the dashboard's "waiting for an admin" sends them here to find it.
  const open = Array.from({ length: TODO_ROWS + 2 }, (_, i) =>
    request({ id: `open${i}`, createdAt: `2026-10-16T1${i}:00:00Z` }),
  );
  const held = request({
    id: "held",
    status: "needs_review",
    createdAt: "2026-10-17T09:00:00Z",
  });

  it("puts a member's own held checks before the open links", () => {
    const rows = waitingForOthers([...open, held], { canReview: false });
    expect(rows.slice(0, TODO_ROWS).map((r) => r.id)).toContain("held");
    expect(rows[0].id).toBe("held");
    expect(rows).toHaveLength(open.length + 1);
  });

  it("leaves a reviewer's held checks to Waiting for your decision", () => {
    const rows = waitingForOthers([...open, held], { canReview: true });
    expect(rows.map((r) => r.id)).not.toContain("held");
  });
  it("leaves out an approved check whose address waits for a reviewer", () => {
    const addressPending = request({
      id: "address",
      status: "approved",
      completedAt: "2026-10-17T10:00:00Z",
      addressReview: "pending",
    });
    const rows = waitingForOthers([...open, addressPending], {
      canReview: false,
    });
    expect(rows.map((r) => r.id)).not.toContain("address");
  });
});

describe("counts from capped reads", () => {
  const en = i18n.getFixedT("en");

  it("say at least the rows once a read hit the cap", () => {
    const rows = MAX_LISTED_REQUESTS + 4;
    expect(
      en("dashboard.waiting.unfinished", {
        count: rows,
        number: listedCount(rows, true, en),
      }),
    ).toBe(`${rows}+ identity checks not finished`);
    expect(
      en("dashboard.waiting.unfinished", {
        count: 3,
        number: listedCount(3, false, en),
      }),
    ).toBe("3 identity checks not finished");
  });

  it("knows which statuses' reads hit the cap", () => {
    expect(cappedAmong(["expired"], ["pending", "in_progress"])).toBe(false);
    expect(cappedAmong(["in_progress"], ["pending", "in_progress"])).toBe(true);
  });
});

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
