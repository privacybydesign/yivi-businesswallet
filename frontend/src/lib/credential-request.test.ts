import { readFileSync } from "node:fs";
import { describe, expect, it } from "vitest";
import type { CredentialType } from "../api/credential-requests";
import type { CredentialRow } from "./credential-request";
import {
  buildSendPayload,
  buildTemplatePayload,
  catalogKey,
  claimValueText,
  EMPTY_ROW,
  hasErrors,
  MAX_CLAIMS,
  MAX_TEMPLATE_NAME,
  OTHER_TYPE,
  parseClaimList,
  rowCredential,
  rowsFromTemplate,
  validateCredentialRequest,
  validateTemplateName,
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

describe("request templates", () => {
  const template = {
    id: "t1",
    name: "Beschikking beschermingsbewind",
    credentials: [
      {
        vct: "nl.rechtspraak.beschermingsbewind",
        claims: ["zaaknummer", "omvang"],
      },
    ],
    createdAt: "2026-10-28T12:00:00Z",
  };

  const designed: CredentialType = {
    vct: "nl.rechtspraak.beschermingsbewind",
    name: "Beschikking beschermingsbewind",
    issuer: "Rechtspraak",
    attributes: [
      { key: "omvang", label: "Omvang" },
      { key: "zaaknummer", label: "Zaaknummer" },
    ],
  };

  it("fills a catalogue row when the catalogue offers the type and every claim", () => {
    expect(rowsFromTemplate(template, [designed])).toEqual([
      {
        ...EMPTY_ROW,
        typeKey: catalogKey(designed),
        picked: ["zaaknummer", "omvang"],
      },
    ]);
  });

  it("keeps a type outside the catalogue as typed", () => {
    expect(rowsFromTemplate(template, [])).toEqual([
      other("nl.rechtspraak.beschermingsbewind", "zaaknummer, omvang"),
    ]);
  });

  it("keeps a credential typed when the catalogue lacks one of its claims", () => {
    const narrowed = { ...designed, attributes: [designed.attributes[0]] };
    expect(rowsFromTemplate(template, [narrowed])).toEqual([
      other("nl.rechtspraak.beschermingsbewind", "zaaknummer, omvang"),
    ]);
  });

  it("saves the rows as a send would carry them", () => {
    expect(
      buildTemplatePayload(
        "  Beschikking  ",
        rowsFromTemplate(template, [designed]),
        [designed],
      ),
    ).toEqual({
      name: "Beschikking",
      credentials: [
        {
          vct: "nl.rechtspraak.beschermingsbewind",
          claims: ["omvang", "zaaknummer"],
        },
      ],
    });
  });

  it("needs a name of at most the backend's length", () => {
    expect(validateTemplateName(" ")).toBe("templateNameRequired");
    expect(validateTemplateName("x".repeat(MAX_TEMPLATE_NAME + 1))).toBe(
      "templateNameTooLong",
    );
    expect(validateTemplateName("Beschikking")).toBeUndefined();
  });

  it("mirrors openid4vprequester.maxTemplateName", () => {
    const source = readFileSync(
      new URL(
        "../../../backend/internal/openid4vprequester/template.go",
        import.meta.url,
      ),
      "utf8",
    );
    const match = /maxTemplateName = (\d+)/.exec(source);
    expect(Number(match?.[1])).toBe(MAX_TEMPLATE_NAME);
  });
});
