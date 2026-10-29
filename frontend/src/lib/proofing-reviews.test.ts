import { readFileSync } from "node:fs";
import { fileURLToPath } from "node:url";
import { describe, expect, it } from "vitest";
import i18n from "../i18n";
import type { ProofingRequest } from "../api/identity-proofing";
import {
  REVIEW_REASON_MAX_CHARS,
  proofingRejectionReason,
} from "./identity-proofing";
import {
  REJECT_REASONS,
  answerBy,
  askedAt,
  askedLine,
  automaticChecks,
  checkDecisionInput,
  checkDecisionProblems,
  closedAt,
  closedRequests,
  inReviewOldestFirst,
  isAddressReview,
  ownReview,
  personName,
  provenWithParts,
  reviewCounts,
  reviewTabFrom,
  waitingOfTab,
  reasonProblem,
  waitingOldestFirst,
  WALLET_REVIEW_REASONS,
  walletReviewReason,
} from "./proofing-reviews";

const t = i18n.getFixedT("en");

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
    completedAt: "2026-10-16T14:40:00Z",
    diplomaMode: "off",
    diplomas: [],
    addressMode: "off",
    employmentMode: "off",
    expectedSubject: false,
    flowKind: "identity",
    ...over,
  };
}

describe("reviewTabFrom", () => {
  it("reads a known tab and falls back to all", () => {
    expect(reviewTabFrom("closed")).toBe("closed");
    expect(reviewTabFrom("data")).toBe("data");
    expect(reviewTabFrom("nope")).toBe("all");
    expect(reviewTabFrom(null)).toBe("all");
  });
});

describe("waitingOldestFirst", () => {
  it("orders by when each went to review, longest first", () => {
    const newer = request({ id: "newer", completedAt: "2026-10-19T10:05:00Z" });
    const older = request({ id: "older", completedAt: "2026-10-16T14:40:00Z" });
    expect(waitingOldestFirst([newer, older]).map((r) => r.id)).toEqual([
      "older",
      "newer",
    ]);
  });

  it("counts from the request when it has no end yet", () => {
    const noEnd = request({
      id: "noend",
      completedAt: undefined,
      createdAt: "2026-10-01T00:00:00Z",
    });
    const dated = request({ id: "dated", completedAt: "2026-10-16T14:40:00Z" });
    expect(waitingOldestFirst([dated, noEnd]).map((r) => r.id)).toEqual([
      "noend",
      "dated",
    ]);
  });

  it("drops what no longer waits: expired, decided or purged", () => {
    const kept = request({ id: "kept" });
    const others = [
      request({ id: "expired", status: "expired" }),
      request({ id: "approved", status: "approved" }),
      request({ id: "purged", purgedAt: "2026-10-18T00:00:00Z" }),
    ];
    expect(waitingOldestFirst([...others, kept]).map((r) => r.id)).toEqual([
      "kept",
    ]);
  });
});

describe("review tabs", () => {
  const check = request({ id: "check" });
  const access = request({ id: "access", flowKind: "data_access" });
  const erasure = request({ id: "erasure", flowKind: "data_erasure" });
  const waiting = [check, access, erasure];

  it("counts checks and data requests apart", () => {
    expect(reviewCounts(waiting)).toEqual({ all: 3, checks: 1, data: 2 });
    expect(reviewCounts([])).toEqual({ all: 0, checks: 0, data: 0 });
  });

  it("lists each kind under its own tab, and none under closed", () => {
    expect(waitingOfTab("all", waiting)).toHaveLength(3);
    expect(waitingOfTab("checks", waiting).map((r) => r.id)).toEqual(["check"]);
    expect(waitingOfTab("data", waiting).map((r) => r.id)).toEqual([
      "access",
      "erasure",
    ]);
    expect(waitingOfTab("closed", waiting)).toEqual([]);
  });
});

describe("closedRequests", () => {
  it("keeps data requests that ended, the latest first", () => {
    const early = request({
      id: "early",
      flowKind: "data_erasure",
      status: "approved",
      completedAt: "2026-10-17T09:00:00Z",
    });
    const late = request({
      id: "late",
      flowKind: "data_access",
      status: "rejected",
      completedAt: "2026-10-18T09:00:00Z",
    });
    const waiting = request({ id: "waiting", flowKind: "data_access" });
    const automatic = request({ id: "auto", status: "approved" });
    expect(
      closedRequests([early, waiting, automatic, late]).map((r) => r.id),
    ).toEqual(["late", "early"]);
  });

  it("lists one that ended without a reviewer, with its error code", () => {
    const lapsed = request({
      id: "lapsed",
      flowKind: "data_erasure",
      status: "rejected",
      errorCode: "REVIEW_LAPSED",
    });
    const faceMismatch = request({
      id: "face",
      flowKind: "data_access",
      status: "rejected",
      errorCode: "FACE_NO_MATCH",
    });
    const closed = closedRequests([lapsed, faceMismatch]);
    expect(closed.map((r) => r.errorCode).sort()).toEqual([
      "FACE_NO_MATCH",
      "REVIEW_LAPSED",
    ]);
  });

  it("lists a decided address by when it was decided, not a waiting one", () => {
    const data = request({
      id: "data",
      flowKind: "data_access",
      status: "approved",
      completedAt: "2026-10-18T09:00:00Z",
    });
    const decided = request({
      id: "decided",
      status: "approved",
      addressReview: "rejected",
      completedAt: "2026-10-16T09:00:00Z",
      addressReviewedAt: "2026-10-19T09:00:00Z",
    });
    const waiting = request({
      id: "waiting",
      status: "approved",
      addressReview: "pending",
    });
    const closed = closedRequests([data, waiting, decided]);
    expect(closed.map((r) => r.id)).toEqual(["decided", "data"]);
    expect(closedAt(decided)).toBe("2026-10-19T09:00:00Z");
  });
});

describe("closed copy", () => {
  const KEYS = [
    "identityProofing.reviews.notWaiting.approved",
    "identityProofing.reviews.notWaiting.rejected",
    "identityProofing.reviews.dataRequest.found.closed",
  ] as const;

  it.each(["en", "nl"])("names no reviewer in %s", (language) => {
    const translate = i18n.getFixedT(language);
    for (const key of KEYS) {
      expect(translate(key)).not.toMatch(/review|beoordel/i);
    }
  });
});

describe("personName", () => {
  it("is the person's name, else a word for a purged one", () => {
    expect(personName(request(), t)).toBe("Noor");
    expect(personName(request({ subjectName: "", subjectEmail: "" }), t)).toBe(
      "Purged",
    );
  });
});

describe("askedLine", () => {
  const when = "16 Oct 2026";

  it("says who asked and when", () => {
    expect(askedLine(request(), when, t)).toBe("Asked by Noor on 16 Oct 2026.");
  });

  it("leaves out the name of a purged request", () => {
    const purged = request({ subjectName: "", subjectEmail: "" });
    expect(askedLine(purged, when, t)).toBe("Asked on 16 Oct 2026.");
  });
});

describe("askedAt", () => {
  it("is when a waiting request reached review, its month's start", () => {
    expect(askedAt(request({ flowKind: "data_erasure" }))).toBe(
      "2026-10-16T14:40:00Z",
    );
  });

  it("is when a closed one was made: the decision overwrote the other", () => {
    expect(
      askedAt(request({ flowKind: "data_erasure", status: "approved" })),
    ).toBe("2026-10-16T14:00:00Z");
  });
});

describe("ownReview", () => {
  const check = request({ requestedById: "sam", subjectUserId: "alex" });

  const reader = (id: string, reviewsOwn = false) => ({ id, reviewsOwn });

  it("is a check the reader sent or that is about them", () => {
    expect(ownReview(check, reader("sam"))).toBe("sent");
    expect(ownReview(check, reader("alex"))).toBe("subject");
    expect(ownReview(check, reader("anna"))).toBeUndefined();
    expect(ownReview(check, undefined)).toBeUndefined();
  });

  it("is never a data request, which its link's sender may decide", () => {
    expect(
      ownReview({ ...check, flowKind: "data_erasure" }, reader("sam")),
    ).toBeUndefined();
  });

  it("is none for a reader the backend lets decide their own", () => {
    expect(ownReview(check, reader("sam", true))).toBeUndefined();
  });
});

describe("answerBy", () => {
  const purgeAt = "2026-11-15T14:40:00Z";

  it("is when a data request in review lapses", () => {
    expect(answerBy(request({ flowKind: "data_erasure", purgeAt }))).toBe(
      purgeAt,
    );
  });

  it("is absent for a check, and for a request already decided", () => {
    expect(answerBy(request({ purgeAt }))).toBeUndefined();
    expect(
      answerBy(
        request({ flowKind: "data_access", status: "approved", purgeAt }),
      ),
    ).toBeUndefined();
  });
});

describe("check decision", () => {
  const base = {
    choice: "approve" as const,
    rejectReason: "someoneElse" as const,
    reason: "Matches",
  };

  it("needs a choice and a reason", () => {
    expect(
      checkDecisionProblems({ ...base, choice: undefined, reason: "  " }),
    ).toEqual(["choice", "reason"]);
    expect(checkDecisionProblems(base)).toEqual([]);
  });

  it("refuses a reason longer than the service takes", () => {
    const reason = "x".repeat(REVIEW_REASON_MAX_CHARS + 1);
    expect(checkDecisionProblems({ ...base, reason })).toEqual([
      "reasonTooLong",
    ]);
    expect(
      checkDecisionProblems({
        ...base,
        reason: "x".repeat(REVIEW_REASON_MAX_CHARS),
      }),
    ).toEqual([]);
  });

  it("sends an approval without an error code", () => {
    expect(checkDecisionInput({ ...base, reason: " Looks right " })).toEqual({
      decision: "approve",
      reason: "Looks right",
    });
  });

  it("sends a rejection with the code of what is wrong", () => {
    expect(
      checkDecisionInput({
        ...base,
        choice: "reject",
        rejectReason: "altered",
      }),
    ).toEqual({
      decision: "reject",
      reason: "Matches",
      errorCode: "REVIEW_ALTERED",
    });
  });

  it("names what a reviewer found with the reviewer's own codes", () => {
    // A wallet code would mean something else: IDENTITY_MISMATCH hides the
    // session from a later data request, DOC_TAMPERED is the chip check's.
    const codes = REJECT_REASONS.map(({ errorCode }) => errorCode);
    expect(codes).toEqual([
      "REVIEW_NOT_HOLDER",
      "REVIEW_ALTERED",
      "EVIDENCE_NOT_AS_REQUESTED",
      undefined,
    ]);
  });

  it("sends no code for something else, so the service uses its own", () => {
    expect(
      checkDecisionInput({ ...base, choice: "reject", rejectReason: "other" }),
    ).toEqual({ decision: "reject", reason: "Matches" });
  });

  it("names every code the service accepts and the page can word", () => {
    for (const { errorCode } of REJECT_REASONS) {
      if (errorCode === undefined) continue;
      expect(errorCode).toMatch(/^[A-Z0-9_]{1,64}$/);
      // An unknown code is shown as is: a worded one differs from it.
      expect(proofingRejectionReason(errorCode, t)).not.toBe(errorCode);
    }
  });
});

describe("automaticChecks", () => {
  it("lists the checks the engine reported, in the page's order", () => {
    expect(
      automaticChecks({
        type: "document",
        passiveAuth: "valid",
        activeAuth: "not_performed",
        liveness: "passed",
        faceMatch: 0.87,
      }),
    ).toEqual([
      { key: "chipGenuine", outcome: "passed" },
      { key: "chipNotCopy", outcome: "notPerformed" },
      { key: "livePerson", outcome: "passed" },
      { key: "faceMatch", outcome: undefined, score: 0.87 },
    ]);
  });

  it("leaves out what the engine did not report or words unknown", () => {
    expect(automaticChecks({ type: "document", liveness: "mystery" })).toEqual(
      [],
    );
    expect(automaticChecks(undefined)).toEqual([]);
  });
});

describe("provenWithParts", () => {
  it("names the chip read and the face match when the evidence has them", () => {
    expect(
      provenWithParts({
        type: "document",
        passiveAuth: "valid",
        faceMatch: 0.9,
      }),
    ).toEqual(["chip", "faceMatch"]);
    expect(
      provenWithParts({ type: "reference_photo", faceMatch: 0.9 }),
    ).toEqual(["faceMatch"]);
    expect(provenWithParts(undefined)).toEqual([]);
  });
});

describe("address reviews", () => {
  const address = request({
    id: "address",
    status: "approved",
    addressMode: "required",
    addressReview: "pending",
    completedAt: "2026-10-17T09:00:00Z",
  });

  it("waits while the address is pending, beside the checks in review", () => {
    const decided = request({
      id: "decided",
      status: "approved",
      addressReview: "confirmed",
    });
    expect(
      waitingOldestFirst([address, decided, request()]).map((r) => r.id),
    ).toEqual(["r1", "address"]);
  });

  it("leaves the address out of what waits in review", () => {
    expect(inReviewOldestFirst([address, request()]).map((r) => r.id)).toEqual([
      "r1",
    ]);
  });

  it("asks for a reason of at most the bound", () => {
    expect(reasonProblem("  ")).toBe("reason");
    expect(reasonProblem("x".repeat(REVIEW_REASON_MAX_CHARS + 1))).toBe(
      "reasonTooLong",
    );
    expect(reasonProblem(" moved last month ")).toBeUndefined();
  });

  it("is an address review only once the check is decided", () => {
    expect(isAddressReview(address)).toBe(true);
    expect(isAddressReview(request())).toBe(false);
    expect(
      isAddressReview(request({ flowKind: "data_access", status: "approved" })),
    ).toBe(false);
  });
});

// The wallet's own review reasons are the ones backend/internal/proofing
// opens a review with, both ways.
describe("wallet review reasons mirror the backend", () => {
  const source = readFileSync(
    fileURLToPath(
      new URL(
        "../../../backend/internal/proofing/employment.go",
        import.meta.url,
      ),
    ),
    "utf8",
  );
  const backend = [...source.matchAll(/\bReview[A-Z]\w*\s*= "(\w+)"/g)].map(
    (m) => m[1],
  );

  it("has every backend reason and no other", () => {
    expect(backend.length).toBeGreaterThan(0);
    expect([...WALLET_REVIEW_REASONS].sort()).toEqual(backend.sort());
  });

  it("reads only a known reason", () => {
    expect(walletReviewReason({ reviewReason: "current_benefit" })).toBe(
      "current_benefit",
    );
    expect(walletReviewReason({ reviewReason: "other" })).toBeUndefined();
    expect(walletReviewReason({})).toBeUndefined();
  });
});
