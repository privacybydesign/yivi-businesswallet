import { describe, expect, it } from "vitest";
import {
  IDENTITY_STATUS_ORDER,
  notIdentifiedCount,
  screeningApplies,
  screeningAttentionCount,
  statusSegments,
} from "./member-insights";

describe("notIdentifiedCount", () => {
  it("counts never, requested and overdue but not due_soon", () => {
    expect(
      notIdentifiedCount({
        never: 3,
        requested: 1,
        overdue: 2,
        due_soon: 4,
        verified: 10,
      }),
    ).toBe(6);
  });

  it("treats a missing status as zero", () => {
    expect(notIdentifiedCount({ verified: 2 })).toBe(0);
  });
});

describe("screeningAttentionCount", () => {
  it("counts everything that needs the member to act, not expiring or not_required", () => {
    expect(
      screeningAttentionCount({
        none: 1,
        requested: 1,
        rejected: 1,
        expired: 1,
        recheck_required: 1,
        expiring: 5,
        valid: 5,
        not_required: 5,
      }),
    ).toBe(5);
  });
});

describe("screeningApplies", () => {
  it("is false when every member is not_required", () => {
    expect(screeningApplies({ not_required: 4, valid: 0, none: 0 })).toBe(
      false,
    );
  });

  it("is true as soon as one member has a screening status", () => {
    expect(screeningApplies({ not_required: 4, none: 1 })).toBe(true);
  });
});

describe("statusSegments", () => {
  it("orders known statuses as given, drops empty ones and computes shares", () => {
    const segments = statusSegments(
      { never: 1, verified: 3, overdue: 0 },
      IDENTITY_STATUS_ORDER,
    );
    expect(segments.map((s) => s.status)).toEqual(["verified", "never"]);
    expect(segments[0].share).toBe(75);
    expect(segments[1].count).toBe(1);
  });

  it("appends a status the frontend does not know instead of dropping it", () => {
    const segments = statusSegments(
      { verified: 1, brand_new: 1 },
      IDENTITY_STATUS_ORDER,
    );
    expect(segments.map((s) => s.status)).toEqual(["verified", "brand_new"]);
  });

  it("is empty for an org with no members", () => {
    expect(statusSegments({}, IDENTITY_STATUS_ORDER)).toEqual([]);
  });
});
