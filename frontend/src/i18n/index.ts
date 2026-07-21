import i18n from "i18next";
import { initReactI18next } from "react-i18next";
import { en } from "./locales/en";
import { nl } from "./locales/nl";
import {
  DEFAULT_LANGUAGE,
  LANGUAGE_STORAGE_KEY,
  SUPPORTED_LANGUAGES,
  resolveInitialLanguage,
  type Language,
} from "./language";

export { DEFAULT_LANGUAGE, SUPPORTED_LANGUAGES } from "./language";
export type { Language } from "./language";

// To add a language: create `locales/<lng>.ts` (typed against `en`, so the build
// fails if key sets diverge), register it in `resources` below, and add the tag
// to `SUPPORTED_LANGUAGES` in `language.ts`.

function readStoredLanguage(): string | null {
  try {
    return localStorage.getItem(LANGUAGE_STORAGE_KEY);
  } catch {
    // localStorage can throw when disabled (private mode, blocked cookies).
    return null;
  }
}

const browserPreferences =
  typeof navigator !== "undefined"
    ? (navigator.languages ?? [navigator.language])
    : [];

const initialLanguage = resolveInitialLanguage(
  readStoredLanguage(),
  browserPreferences,
);

void i18n.use(initReactI18next).init({
  resources: {
    en: { translation: en },
    nl: { translation: nl },
  },
  lng: initialLanguage,
  fallbackLng: DEFAULT_LANGUAGE,
  supportedLngs: SUPPORTED_LANGUAGES,
  defaultNS: "translation",
  interpolation: {
    escapeValue: false, // React escapes interpolated values against XSS already
  },
  returnNull: false,
});

// Keep the document language in sync so assistive tech announces content in the
// right language (WCAG 2.2 SC 3.1.1 / 3.1.2).
function applyDocumentLanguage(lng: string): void {
  if (typeof document !== "undefined") {
    document.documentElement.lang = lng;
  }
}
applyDocumentLanguage(i18n.language);
i18n.on("languageChanged", applyDocumentLanguage);

// Switch language and persist the explicit choice so it survives reloads.
export function changeLanguage(lng: Language): void {
  try {
    localStorage.setItem(LANGUAGE_STORAGE_KEY, lng);
  } catch {
    // Persistence is best-effort; the in-memory switch still applies.
  }
  void i18n.changeLanguage(lng);
}

export default i18n;
