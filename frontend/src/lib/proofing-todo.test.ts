import { describe, expect, it } from "vitest";
import type { ProofingRequest } from "../api/identity-proofing";
import {
  dashboardReads,
  finishedChecks,
  unfinishedChecks,
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

describe("dashboardReads", () => {
  it("reads the decisions for a reviewer, and the checks for everyone", () => {
    expect(dashboardReads({ paused: false, canReview: true })).toEqual({
      reviews: true,
      checks: true,
    });
    expect(dashboardReads({ paused: false, canReview: false })).toEqual({
      reviews: false,
      checks: true,
    });
  });

  it("reads no proofing list while the org is paused", () => {
    expect(dashboardReads({ paused: true, canReview: true })).toEqual({
      reviews: false,
      checks: false,
    });
    expect(dashboardReads({ paused: true, canReview: false })).toEqual({
      reviews: false,
      checks: false,
    });
  });
});
