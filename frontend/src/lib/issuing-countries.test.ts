import { readFileSync } from "node:fs";
import { fileURLToPath } from "node:url";
import { describe, expect, it } from "vitest";
import i18n from "../i18n";
import { issuingCountryChoices, issuingCountryName } from "./issuing-countries";

const en = i18n.getFixedT("en");
const nl = i18n.getFixedT("nl");

// A name that fell back to a code: the ICAO one, or the 2-letter region.
const BARE_CODE = /^[A-Z]{2,3}$/;

describe("issuingCountryChoices", () => {
  const choices = issuingCountryChoices("en", en);
  const codes = choices.map((choice) => choice.code);

  it("lists every state once, by a 3-letter code", () => {
    expect(new Set(codes).size).toBe(codes.length);
    expect(codes.every((code) => /^[A-Z]{3}$/.test(code))).toBe(true);
    expect(codes).toContain("NLD");
    expect(codes).toContain("DEU");
  });

  it("names each state in the language of the page", () => {
    const name = (code: string, list = choices): string | undefined =>
      list.find((choice) => choice.code === code)?.name;
    expect(name("NLD")).toBe("Netherlands");
    expect(name("NLD", issuingCountryChoices("nl", nl))).toBe("Nederland");
    expect(name("RKS")).toBe("Kosovo");
  });

  it("sorts by name in the language, and names no state by a code", () => {
    const names = choices.map((choice) => choice.name);
    const collator = new Intl.Collator("en");
    expect(names).toEqual([...names].sort((a, b) => collator.compare(a, b)));
    expect(names.filter((name) => BARE_CODE.test(name))).toEqual([]);
    expect(
      issuingCountryChoices("nl", nl)
        .map((choice) => choice.name)
        .filter((name) => BARE_CODE.test(name)),
    ).toEqual([]);
  });

  it("names the codes only ICAO has from the editor's copy", () => {
    expect(issuingCountryName("GBD", "en", en)).toBe(
      "British overseas territories citizen",
    );
    expect(issuingCountryName("UNO", "nl", nl)).toBe("Verenigde Naties");
  });

  it("shows a code the list does not hold as it is", () => {
    expect(issuingCountryName("ANT", "en", en)).toBe("ANT");
  });
});

// The codes ICAO adds to the ISO countries are the engine's own list: the
// editor names each of them, and the Go test beside the engine's list holds
// every code the editor offers to what the engine accepts.
describe("the codes only ICAO has mirror backend/internal/proofingengine/flow", () => {
  const source = readFileSync(
    fileURLToPath(
      new URL(
        "../../../backend/internal/proofingengine/flow/flow.go",
        import.meta.url,
      ),
    ),
    "utf8",
  );

  it("offers every one of them", () => {
    const list = /var icaoIssuingCodes = \[\]string\{([^}]*)\}/.exec(source);
    expect(list).not.toBeNull();
    const backend = [...list![1].matchAll(/"([A-Z]{3})"/g)].map((m) => m[1]);
    expect(backend.length).toBeGreaterThan(0);
    const offered = issuingCountryChoices("en", en).map((c) => c.code);
    for (const code of backend) {
      expect(offered).toContain(code);
    }
  });
});
