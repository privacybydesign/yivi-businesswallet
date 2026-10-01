import { describe, expect, it } from "vitest";
import type { CredentialType } from "../api/credential-requests";
import type { CredentialRow } from "./credential-request";
import {
  buildSendPayload,
  catalogKey,
  claimValueText,
  EMPTY_ROW,
  hasErrors,
  MAX_CLAIMS,
  OTHER_TYPE,
  parseClaimList,
  rowCredential,
  validateCredentialRequest,
} from "./credential-request";

const membership: CredentialType = {
  vct: "nl.caesar.membership",
  name: "Membership",
  issuer: "Caesar",
  attributes: [
    { key: "legalName", label: "Legal name" },
    { key: "memberSince", label: "Member since" },
  ],
};
const catalog = [membership];

function picked(keys: string[]): CredentialRow {
  return { ...EMPTY_ROW, typeKey: catalogKey(membership), picked: keys };
}

function other(vct: string, claims: string): CredentialRow {
  return { ...EMPTY_ROW, typeKey: OTHER_TYPE, vct, claims };
}

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

describe("rowCredential", () => {
  it("asks for the catalogue type's vct and the ticked attributes, in catalogue order", () => {
    expect(
      rowCredential(picked(["memberSince", "legalName"]), catalog),
    ).toEqual({
      vct: "nl.caesar.membership",
      claims: ["legalName", "memberSince"],
    });
  });

  it("reads a typed row as typed", () => {
    expect(rowCredential(other(" nl.kvk ", "a, b"), catalog)).toEqual({
      vct: "nl.kvk",
      claims: ["a", "b"],
    });
  });

  it("has nothing for an unchosen row or a type that left the catalogue", () => {
    expect(rowCredential(EMPTY_ROW, catalog)).toBeUndefined();
    expect(rowCredential(picked([]), [])).toBeUndefined();
  });
});

describe("validateCredentialRequest", () => {
  it("accepts a catalogue row, even with no attribute ticked", () => {
    expect(
      hasErrors(
        validateCredentialRequest(
          "globex@qerds.localhost",
          [picked([])],
          catalog,
        ),
      ),
    ).toBe(false);
  });

  it("requires a plausible recipient address", () => {
    expect(validateCredentialRequest("", [picked([])], catalog).recipient).toBe(
      "recipientRequired",
    );
    expect(
      validateCredentialRequest("globex", [picked([])], catalog).recipient,
    ).toBe("recipientInvalid");
  });

  it("flags each bad row by index", () => {
    const errors = validateCredentialRequest(
      "globex@qerds.localhost",
      [
        picked(["legalName"]),
        EMPTY_ROW,
        other(" ", ""),
        other("nl kvk", ""),
        other("nl.kvk", "legal name"),
        other(
          "nl.kvk",
          Array.from({ length: MAX_CLAIMS + 1 }, (_, i) => `c${i}`).join(","),
        ),
      ],
      catalog,
    );
    expect(errors.rows).toEqual([
      undefined,
      "vctRequired",
      "vctRequired",
      "vctInvalid",
      "claimInvalid",
      "tooManyClaims",
    ]);
  });
});

describe("buildSendPayload", () => {
  it("resolves each row and omits an empty sender", () => {
    expect(
      buildSendPayload(
        "",
        " globex@qerds.localhost ",
        [picked(["legalName"]), other("nl.kvk.registration", "kvkNumber")],
        catalog,
      ),
    ).toEqual({
      recipient: "globex@qerds.localhost",
      credentials: [
        { vct: "nl.caesar.membership", claims: ["legalName"] },
        { vct: "nl.kvk.registration", claims: ["kvkNumber"] },
      ],
    });
  });

  it("keeps a chosen sender", () => {
    expect(
      buildSendPayload("acme@qerds.localhost", "g@q.l", [picked([])], catalog)
        .from,
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
