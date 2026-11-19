import i18n, { SUPPORTED_LANGUAGES } from "./index";
import { en } from "./locales/en";
import { nl } from "./locales/nl";

// Tests read strings synchronously, in either language, so they get both up
// front instead of the app's one-chunk-per-language loading.
const LOCALES = { en, nl };
for (const lng of SUPPORTED_LANGUAGES) {
  i18n.addResourceBundle(lng, "translation", LOCALES[lng]);
}
