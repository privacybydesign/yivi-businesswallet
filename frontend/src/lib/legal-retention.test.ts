import { readFileSync } from "node:fs";
import { describe, expect, it } from "vitest";
import type { ProofingLegalRetention } from "../api/identity-proofing";
import {
  MAX_LEGAL_REASON_CHARS,
  MAX_LEGAL_RETENTION_MONTHS,
  legalDraftDirty,
  legalDraftFrom,
  legalDraftInvalid,
  legalDraftMonths,
  legalDraftUpdate,
  legalPeriod,
  legalReasonIn,
  legalRetentionLoss,
  erasureLeft,
} from "./legal-retention";
import type { LegalRetentionDraft } from "./legal-retention";

const FIVE_YEARS_MONTHS = 60;

const saved: ProofingLegalRetention = {
  months: FIVE_YEARS_MONTHS,
  reasonEn: "The Wwft requires five years.",
  reasonNl: "De Wwft vraagt vijf jaar.",
};

function draft(patch: Partial<LegalRetentionDraft> = {}): LegalRetentionDraft {
  return { ...legalDraftFrom(saved), ...patch };
}

describe("legalPeriod", () => {
  it("reads whole years as years", () => {
    expect(legalPeriod(60)).toEqual({ count: 5, unit: "years" });
    expect(legalPeriod(12)).toEqual({ count: 1, unit: "years" });
  });

  it("reads anything else as months", () => {
    expect(legalPeriod(18)).toEqual({ count: 18, unit: "months" });
    expect(legalPeriod(1)).toEqual({ count: 1, unit: "months" });
  });
});

describe("legalDraftFrom", () => {
  it("starts switched off without a legal retention", () => {
    expect(legalDraftFrom(null)).toMatchObject({
      enabled: false,
      reasonEn: "",
      reasonNl: "",
    });
  });

  it("restores the saved period and reasons, switched on", () => {
    expect(legalDraftFrom(saved)).toEqual({
      enabled: true,
      amount: "5",
      unit: "years",
      reasonEn: saved.reasonEn,
      reasonNl: saved.reasonNl,
    });
  });
});

describe("legalDraftMonths", () => {
  it("counts a year as twelve months", () => {
    expect(legalDraftMonths(draft({ amount: "5", unit: "years" }))).toBe(60);
    expect(legalDraftMonths(draft({ amount: "18", unit: "months" }))).toBe(18);
  });

  it("accepts 1 month and the longest period", () => {
    expect(legalDraftMonths(draft({ amount: "1", unit: "months" }))).toBe(1);
    expect(
      legalDraftMonths(
        draft({
          amount: String(MAX_LEGAL_RETENTION_MONTHS / 12),
          unit: "years",
        }),
      ),
    ).toBe(MAX_LEGAL_RETENTION_MONTHS);
  });

  it("refuses what is not a whole number of months in range", () => {
    for (const amount of ["", "0", "-1", "1.5", "five", "1e2", " "]) {
      expect(legalDraftMonths(draft({ amount })), amount).toBeNull();
    }
    expect(legalDraftMonths(draft({ amount: "51", unit: "years" }))).toBeNull();
    expect(
      legalDraftMonths(draft({ amount: "601", unit: "months" })),
    ).toBeNull();
  });
});

describe("legalDraftInvalid", () => {
  it("accepts the saved draft", () => {
    expect(legalDraftInvalid(draft())).toEqual([]);
  });

  it("names every field that cannot be saved", () => {
    expect(
      legalDraftInvalid(draft({ amount: "0", reasonEn: " ", reasonNl: "" })),
    ).toEqual(["amount", "reasonEn", "reasonNl"]);
  });

  it("counts a reason in characters, as the backend does", () => {
    const longest = "é".repeat(MAX_LEGAL_REASON_CHARS);
    expect(
      legalDraftInvalid(draft({ reasonEn: longest, reasonNl: longest })),
    ).toEqual([]);
    expect(legalDraftInvalid(draft({ reasonEn: longest + "é" }))).toEqual([
      "reasonEn",
    ]);
    // An emoji is two UTF-16 units but one character.
    expect(
      legalDraftInvalid(
        draft({ reasonEn: "😀".repeat(MAX_LEGAL_REASON_CHARS) }),
      ),
    ).toEqual([]);
  });

  it("asks nothing of a switched-off draft", () => {
    expect(
      legalDraftInvalid(draft({ enabled: false, amount: "", reasonEn: "" })),
    ).toEqual([]);
  });
});

describe("legalDraftUpdate", () => {
  it("sends the months and the trimmed reasons", () => {
    expect(
      legalDraftUpdate(
        draft({ amount: "18", unit: "months", reasonEn: "  kept  " }),
      ),
    ).toEqual({
      months: 18,
      reasonEn: "kept",
      reasonNl: saved.reasonNl,
    });
  });

  it("sends null to remove it", () => {
    expect(legalDraftUpdate(draft({ enabled: false }))).toBeNull();
  });
});

describe("legalDraftDirty", () => {
  it("is clean for the saved legal retention", () => {
    expect(legalDraftDirty(draft(), saved)).toBe(false);
    expect(legalDraftDirty(legalDraftFrom(null), null)).toBe(false);
  });

  it("is dirty once the period or a reason changes", () => {
    expect(legalDraftDirty(draft({ amount: "6" }), saved)).toBe(true);
    expect(legalDraftDirty(draft({ unit: "months" }), saved)).toBe(true);
    expect(legalDraftDirty(draft({ reasonNl: "Anders" }), saved)).toBe(true);
  });

  it("ignores padding around a reason", () => {
    expect(
      legalDraftDirty(draft({ reasonEn: `  ${saved.reasonEn} ` }), saved),
    ).toBe(false);
  });

  it("is dirty when switched on or off", () => {
    expect(
      legalDraftDirty(
        draft({ amount: "1", reasonEn: "a", reasonNl: "b" }),
        null,
      ),
    ).toBe(true);
    expect(legalDraftDirty(draft({ enabled: false }), saved)).toBe(true);
  });
});

// Removing the legal retention or shortening it purges the checks past what is
// left within the hour, so it asks first; nothing else does.
describe("legalRetentionLoss", () => {
  it("asks before the legal retention is switched off", () => {
    expect(legalRetentionLoss(draft({ enabled: false }), saved)).toEqual({
      kind: "removed",
    });
  });

  it("asks before a shorter period, with the period it becomes", () => {
    expect(
      legalRetentionLoss(draft({ amount: "18", unit: "months" }), saved),
    ).toEqual({ kind: "shortened", months: 18 });
    expect(legalRetentionLoss(draft({ amount: "4" }), saved)).toEqual({
      kind: "shortened",
      months: 48,
    });
  });

  it("does not ask for a longer or the same period, or other reasons", () => {
    expect(legalRetentionLoss(draft({ amount: "6" }), saved)).toBeNull();
    expect(legalRetentionLoss(draft(), saved)).toBeNull();
    expect(
      legalRetentionLoss(draft({ amount: "60", unit: "months" }), saved),
    ).toBeNull();
    expect(legalRetentionLoss(draft({ reasonEn: "Other." }), saved)).toBeNull();
  });

  it("does not ask when there was none to lose", () => {
    expect(legalRetentionLoss(draft({ amount: "1" }), null)).toBeNull();
    expect(legalRetentionLoss(draft({ enabled: false }), null)).toBeNull();
  });
});

// An approved erasure counts the sessions the law holds among those it kept:
// the person reads those apart from the ones it could not link to them.
describe("erasureLeft", () => {
  const held = {
    sessions: 2,
    until: "2031-10-19T12:00:00Z",
    reason: { en: "Kept by law.", nl: "Bewaard volgens de wet." },
  };

  it("takes the held sessions out of the kept ones", () => {
    expect(erasureLeft(3, held)).toEqual({ unsure: 1, held });
    expect(erasureLeft(2, held)).toEqual({ unsure: 0, held });
  });

  it("keeps every kept session unsure without a hold", () => {
    expect(erasureLeft(3, undefined)).toEqual({ unsure: 3, held: undefined });
    expect(erasureLeft(undefined, undefined)).toEqual({
      unsure: 0,
      held: undefined,
    });
  });
});

describe("legalReasonIn", () => {
  const reason = { en: "Kept by law.", nl: "Bewaard volgens de wet." };

  it("reads Dutch on a Dutch page, English on any other", () => {
    expect(legalReasonIn(reason, "nl")).toBe(reason.nl);
    expect(legalReasonIn(reason, "nl-NL")).toBe(reason.nl);
    expect(legalReasonIn(reason, "en")).toBe(reason.en);
    expect(legalReasonIn(reason, "de")).toBe(reason.en);
  });
});

// The bounds the form checks are the ones the backend enforces.
describe("legal retention bounds mirror backend/internal/proofing", () => {
  const source = readFileSync(
    new URL(
      "../../../backend/internal/proofing/legal_retention.go",
      import.meta.url,
    ),
    "utf8",
  );

  function backendConstant(name: string): number {
    const match = new RegExp(`${name}\\s*=\\s*(\\d+)`).exec(source);
    expect(match, `${name} is declared in legal_retention.go`).not.toBeNull();
    return Number(match![1]);
  }

  it("allows as many months as the backend does", () => {
    expect(MAX_LEGAL_RETENTION_MONTHS).toBe(
      backendConstant("maxLegalRetentionMonths"),
    );
  });

  it("allows reasons as long as the backend does", () => {
    expect(MAX_LEGAL_REASON_CHARS).toBe(
      backendConstant("maxLegalReasonLength"),
    );
  });
});
