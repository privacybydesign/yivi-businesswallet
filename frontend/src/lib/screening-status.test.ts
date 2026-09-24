import { describe, expect, it } from "vitest";
import i18n from "../i18n";
import { screeningConfigured, vogResultMessage } from "./screening-status";

const t = i18n.getFixedT("en");

describe("vogResultMessage", () => {
  it("reports success for a valid result regardless of rejectionReason", () => {
    expect(vogResultMessage("valid", undefined, "Acme", t)).toBe(
      "Your VOG is valid.",
    );
  });

  it("distinguishes an identity mismatch from a too-old rejection", () => {
    const mismatch = vogResultMessage(
      "rejected",
      "identity_mismatch",
      "Acme",
      t,
    );
    const tooOld = vogResultMessage("rejected", "too_old", "Acme", t);
    expect(mismatch).not.toBe(tooOld);
    expect(mismatch).toContain("does not match");
    expect(tooOld).toContain("older than Acme accepts");
  });

  it("names the insufficient-scope reason with the org", () => {
    expect(vogResultMessage("rejected", "insufficient_scope", "Acme", t)).toBe(
      "This VOG does not cover everything Acme requires.",
    );
  });

  it("falls back to the generic rejection for an unrecognized or missing reason", () => {
    const generic =
      "This document could not be validated as a genuine, current VOG.";
    expect(vogResultMessage("rejected", "gaav_rejected", "Acme", t)).toBe(
      generic,
    );
    expect(vogResultMessage("rejected", undefined, "Acme", t)).toBe(generic);
  });
});

describe("screeningConfigured", () => {
  it("is off until the settings are known", () => {
    expect(screeningConfigured(undefined)).toBe(false);
  });

  it("is off for a policy that requires a VOG from nobody", () => {
    expect(screeningConfigured({ requiredFor: "nobody" })).toBe(false);
  });

  it("is on once the policy requires a VOG from anyone", () => {
    expect(screeningConfigured({ requiredFor: "employees" })).toBe(true);
    expect(screeningConfigured({ requiredFor: "externals" })).toBe(true);
    expect(screeningConfigured({ requiredFor: "both" })).toBe(true);
  });
});
