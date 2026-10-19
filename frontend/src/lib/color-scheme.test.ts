import { describe, expect, it } from "vitest";
import { COLOR_SCHEME_PREFERENCES, resolveColorScheme } from "./color-scheme";

describe("resolveColorScheme", () => {
  it.each([
    ["light", true, "light"],
    ["dark", false, "dark"],
    ["system", true, "dark"],
    ["system", false, "light"],
  ] as const)(
    "resolves %s (OS dark %s) to %s",
    (preference, systemDark, expected) => {
      expect(resolveColorScheme(preference, systemDark, false)).toBe(expected);
    },
  );

  // A white-labelled org is shown in its brand's light colours, whatever the
  // switch or the OS says.
  it.each(COLOR_SCHEME_PREFERENCES)(
    "keeps the white-label design light (%s)",
    (preference) => {
      expect(resolveColorScheme(preference, true, true)).toBe("light");
    },
  );
});
