import { describe, expect, it } from "vitest";
import { en } from "./locales/en";
import { nl } from "./locales/nl";
import { SUPPORTED_LANGUAGES } from "./language";

// The language switcher renders a flag per language and relies on
// `common.languageName.<lng>` for each option's accessible name (a flag glyph
// alone is not an accessible label). A supported language without a name would
// leave a flag button announcing only its raw i18n key, so pin the coverage.
describe("common.languageName", () => {
  it.each(SUPPORTED_LANGUAGES)(
    "has a non-empty accessible name for %s in both locales",
    (lng) => {
      expect(en.common.languageName[lng]).toBeTruthy();
      expect(nl.common.languageName[lng]).toBeTruthy();
    },
  );
});
