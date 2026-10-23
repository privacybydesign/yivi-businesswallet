import { describe, expect, it } from "vitest";
import { notIdentifiedCount, screeningAttentionCount } from "./member-insights";

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
