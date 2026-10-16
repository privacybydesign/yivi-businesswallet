import { describe, expect, it } from "vitest";
import { parseClaimList, statusTone, verdictTone } from "./verification";

describe("parseClaimList", () => {
  it("splits on commas and newlines and trims", () => {
    expect(parseClaimList(" vergunningnummer, markt\nstandplaats ")).toEqual([
      "vergunningnummer",
      "markt",
      "standplaats",
    ]);
  });

  it("drops empties and duplicates, keeping first-seen order", () => {
    expect(parseClaimList("a,,b, a ,\n,b")).toEqual(["a", "b"]);
  });

  it("returns nothing for blank input", () => {
    expect(parseClaimList("  \n , ")).toEqual([]);
  });
});

describe("statusTone", () => {
  it("is amber while waiting and neutral once expired", () => {
    expect(statusTone("pending", undefined)).toBe("amber");
    expect(statusTone("expired", undefined)).toBe("default");
  });

  it("takes the verdict's colour once answered", () => {
    expect(statusTone("completed", true)).toBe("green");
    expect(statusTone("completed", false)).toBe("red");
    // A completed session always carries a verdict; a missing one reads as failed.
    expect(statusTone("completed", undefined)).toBe("red");
  });
});

describe("verdictTone", () => {
  it("is green only for a completed, valid session", () => {
    expect(verdictTone({ status: "completed", valid: true })).toBe("green");
    expect(verdictTone({ status: "completed", valid: false })).toBe("red");
    expect(verdictTone({ status: "pending", valid: undefined })).toBe(
      "default",
    );
    expect(verdictTone({ status: "expired", valid: undefined })).toBe(
      "default",
    );
  });
});
