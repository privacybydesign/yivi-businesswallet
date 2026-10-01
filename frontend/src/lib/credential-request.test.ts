import { describe, expect, it } from "vitest";
import {
  buildSendPayload,
  claimValueText,
  hasErrors,
  MAX_CLAIMS,
  parseClaimList,
  validateCredentialRequest,
} from "./credential-request";

describe("parseClaimList", () => {
  it("splits on commas and newlines, trims, and drops empties and duplicates", () => {
    expect(parseClaimList(" legalName, kvkNumber\n\nlegalName ,")).toEqual([
      "legalName",
      "kvkNumber",
    ]);
  });

  it("yields no claims for an empty field", () => {
    expect(parseClaimList("  ")).toEqual([]);
  });
});

describe("validateCredentialRequest", () => {
  const row = { vct: "nl.kvk.registration", claims: "legalName" };

  it("accepts a well-formed request", () => {
    expect(
      hasErrors(validateCredentialRequest("globex@qerds.localhost", [row])),
    ).toBe(false);
  });

  it("requires a plausible recipient address", () => {
    expect(validateCredentialRequest("", [row]).recipient).toBe(
      "recipientRequired",
    );
    expect(validateCredentialRequest("globex", [row]).recipient).toBe(
      "recipientInvalid",
    );
  });

  it("flags each bad row by index", () => {
    const errors = validateCredentialRequest("globex@qerds.localhost", [
      row,
      { vct: " ", claims: "" },
      { vct: "nl kvk", claims: "" },
      { vct: "nl.kvk", claims: "legal name" },
      {
        vct: "nl.kvk",
        claims: Array.from({ length: MAX_CLAIMS + 1 }, (_, i) => `c${i}`).join(
          ",",
        ),
      },
    ]);
    expect(errors.rows).toEqual([
      undefined,
      "vctRequired",
      "vctInvalid",
      "claimInvalid",
      "tooManyClaims",
    ]);
  });
});

describe("buildSendPayload", () => {
  it("trims, parses claims and omits an empty sender", () => {
    expect(
      buildSendPayload("", " globex@qerds.localhost ", [
        { vct: " nl.kvk.registration ", claims: "legalName, kvkNumber" },
      ]),
    ).toEqual({
      recipient: "globex@qerds.localhost",
      credentials: [
        { vct: "nl.kvk.registration", claims: ["legalName", "kvkNumber"] },
      ],
    });
  });

  it("keeps a chosen sender", () => {
    expect(
      buildSendPayload("acme@qerds.localhost", "g@q.l", [
        { vct: "t", claims: "" },
      ]).from,
    ).toBe("acme@qerds.localhost");
  });
});

describe("claimValueText", () => {
  it("renders strings verbatim and structures as JSON", () => {
    expect(claimValueText("Globex")).toBe("Globex");
    expect(claimValueText(42)).toBe("42");
    expect(claimValueText({ city: "Nijmegen" })).toBe('{"city":"Nijmegen"}');
    expect(claimValueText(null)).toBe("");
  });
});
