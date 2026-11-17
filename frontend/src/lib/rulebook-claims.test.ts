import { describe, expect, it } from "vitest";
import { claimSummary, formatClaims, parseClaims } from "./rulebook-claims";

describe("parseClaims", () => {
  it("returns the object for valid JSON", () => {
    const text = formatClaims({ lei: "724500VKKSH9QOLTFR81", nested: { a: 1 } });
    expect(parseClaims(text)).toEqual({
      ok: true,
      claims: { lei: "724500VKKSH9QOLTFR81", nested: { a: 1 } },
    });
  });

  it("rejects text that is not JSON", () => {
    expect(parseClaims("{lei: 1")).toEqual({
      ok: false,
      reason: "invalidJson",
    });
  });

  it("rejects JSON that is not an object", () => {
    for (const text of ["[]", "null", '"x"', "3"]) {
      expect(parseClaims(text)).toEqual({ ok: false, reason: "notAnObject" });
    }
  });
});

describe("claimSummary", () => {
  it("shows strings as they are and other values as JSON", () => {
    expect(claimSummary("Ada")).toBe("Ada");
    expect(claimSummary({ a: [1, 2] })).toBe('{"a":[1,2]}');
    expect(claimSummary(true)).toBe("true");
  });

  it("shows a dash for an empty value", () => {
    expect(claimSummary("")).toBe("—");
    expect(claimSummary(undefined)).toBe("—");
    expect(claimSummary(null)).toBe("—");
  });
});
