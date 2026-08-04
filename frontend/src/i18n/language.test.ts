import { describe, expect, it } from "vitest";
import { DEFAULT_LANGUAGE, resolveInitialLanguage } from "./language";

describe("resolveInitialLanguage", () => {
  it("prefers a valid stored choice over browser preferences", () => {
    expect(resolveInitialLanguage("nl", ["en-US", "en"])).toBe("nl");
    expect(resolveInitialLanguage("en", ["nl-NL"])).toBe("en");
  });

  it("ignores an unsupported or empty stored choice", () => {
    expect(resolveInitialLanguage("fr", ["nl"])).toBe("nl");
    expect(resolveInitialLanguage("", ["nl"])).toBe("nl");
    expect(resolveInitialLanguage(null, ["nl"])).toBe("nl");
  });

  it("falls back to the first supported browser preference", () => {
    expect(resolveInitialLanguage(null, ["fr-FR", "nl-NL", "en"])).toBe("nl");
    expect(resolveInitialLanguage(null, ["nl"])).toBe("nl");
  });

  it("matches on the base tag, so a region variant still counts", () => {
    expect(resolveInitialLanguage(null, ["NL-nl"])).toBe("nl");
    expect(resolveInitialLanguage(null, ["en-GB"])).toBe("en");
  });

  it("defaults to English when nothing matches", () => {
    expect(resolveInitialLanguage(null, ["fr", "de"])).toBe(DEFAULT_LANGUAGE);
    expect(resolveInitialLanguage(null, [])).toBe(DEFAULT_LANGUAGE);
    expect(DEFAULT_LANGUAGE).toBe("en");
  });
});
