import { afterEach, describe, expect, it, vi } from "vitest";
import {
  COLOR_SCHEME_PREFERENCES,
  DEFAULT_DESIGN,
  DESIGN_ATTRIBUTE,
  WHITE_LABEL_DESIGN,
  resolveColorScheme,
  type Design,
  type SystemAppearance,
} from "./color-scheme";

const COLOR_SCHEME_ATTRIBUTE = "data-color-scheme";

describe("resolveColorScheme", () => {
  it.each([
    ["light", "os-dark", "light"],
    ["dark", "os-light", "dark"],
    ["system", "os-dark", "dark"],
    ["system", "os-light", "light"],
  ] as const)("resolves %s (%s) to %s", (preference, appearance, expected) => {
    expect(resolveColorScheme(preference, appearance, DEFAULT_DESIGN)).toBe(
      expected,
    );
  });

  // A white-labelled org is shown in its brand's light colours, whatever the
  // switch or the OS says.
  it.each(COLOR_SCHEME_PREFERENCES)(
    "keeps the white-label design light (%s)",
    (preference) => {
      expect(
        resolveColorScheme(preference, "os-dark", WHITE_LABEL_DESIGN),
      ).toBe("light");
    },
  );
});

// The module reads the design the pre-paint script in index.html set when it
// loads, so stub that document and re-import the module per case.
async function loadAfterPrePaint(design: Design, appearance: SystemAppearance) {
  const attributes = new Map<string, string>();
  if (design === WHITE_LABEL_DESIGN) {
    attributes.set(DESIGN_ATTRIBUTE, WHITE_LABEL_DESIGN);
  }

  vi.resetModules();
  vi.stubGlobal("document", {
    documentElement: {
      getAttribute: (name: string) => attributes.get(name) ?? null,
      setAttribute: (name: string, value: string) => {
        attributes.set(name, value);
      },
    },
  });
  vi.stubGlobal("window", {
    localStorage: { getItem: () => null },
    matchMedia: () => ({
      matches: appearance === "os-dark",
      addEventListener: () => {},
    }),
  });

  const loaded = await import("./color-scheme");
  return { ...loaded, attributes };
}

describe("first apply after the pre-paint script", () => {
  afterEach(() => {
    vi.unstubAllGlobals();
    vi.resetModules();
  });

  it("keeps a white-label page light on a dark OS", async () => {
    const { watchSystemColorScheme, attributes } = await loadAfterPrePaint(
      WHITE_LABEL_DESIGN,
      "os-dark",
    );
    watchSystemColorScheme();
    expect(attributes.get(COLOR_SCHEME_ATTRIBUTE)).toBe("light");
  });

  it("follows a dark OS on the default design", async () => {
    const { watchSystemColorScheme, attributes } = await loadAfterPrePaint(
      DEFAULT_DESIGN,
      "os-dark",
    );
    watchSystemColorScheme();
    expect(attributes.get(COLOR_SCHEME_ATTRIBUTE)).toBe("dark");
  });
});
