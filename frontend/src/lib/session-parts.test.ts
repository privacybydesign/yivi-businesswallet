import { describe, expect, it } from "vitest";
import { partState, sessionSummary, summaryKinds } from "./session-parts";

describe("partState", () => {
  it("reads every step in the same words", () => {
    expect(partState({ kind: "address", state: "in_review" })).toBe(
      "to_review",
    );
    expect(partState({ kind: "address", state: "done" })).toBe("done");
    expect(
      partState({ kind: "employment", state: "done", reviewed: true }),
    ).toBe("accepted");
    expect(
      partState({ kind: "employment", state: "rejected", code: "X" }),
    ).toBe("rejected");
    expect(
      partState({
        kind: "employment",
        state: "rejected",
        code: "REVIEW_LAPSED",
      }),
    ).toBe("lapsed");
    expect(partState({ kind: "diplomas", state: "not_provided" })).toBe(
      "not_provided",
    );
  });
});

describe("sessionSummary", () => {
  const done = { kind: "address", state: "done" };
  it("needs review while the identity or any step waits", () => {
    expect(
      sessionSummary({
        status: "approved",
        steps: [done, { kind: "employment", state: "in_review" }],
      }),
    ).toBe("needs_review");
    expect(sessionSummary({ status: "needs_review", steps: [done] })).toBe(
      "needs_review",
    );
  });

  it("is complete, under way or incomplete for an approved identity", () => {
    expect(sessionSummary({ status: "approved", steps: [done] })).toBe(
      "complete",
    );
    expect(
      sessionSummary({
        status: "approved",
        steps: [done, { kind: "diplomas", state: "open" }],
      }),
    ).toBe("in_progress");
    expect(
      sessionSummary({
        status: "approved",
        steps: [done, { kind: "diplomas", state: "not_provided" }],
      }),
    ).toBe("incomplete");
  });

  it("is nothing without steps, or for an identity that did not pass", () => {
    expect(sessionSummary({ status: "approved", steps: [] })).toBeUndefined();
    expect(
      sessionSummary({ status: "rejected", steps: [done] }),
    ).toBeUndefined();
  });
});

describe("summaryKinds", () => {
  it("names only the steps the summary is about", () => {
    const steps = [
      { kind: "address", state: "done" },
      { kind: "diplomas", state: "not_provided" },
      { kind: "employment", state: "in_review" },
    ];
    expect(summaryKinds({ steps }, "needs_review")).toEqual(["employment"]);
    expect(summaryKinds({ steps }, "incomplete")).toEqual(["diplomas"]);
    expect(summaryKinds({ steps }, "complete")).toEqual([]);
  });
});
