import { describe, expect, it } from "vitest";
import {
  COLOR_SCHEME_PREFERENCES,
  DEFAULT_DESIGN,
  WHITE_LABEL_DESIGN,
  resolveColorScheme,
} from "./color-scheme";

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
