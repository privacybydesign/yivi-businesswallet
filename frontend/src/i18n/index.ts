import i18n from "i18next";
import { initReactI18next } from "react-i18next";
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
// fails if key sets diverge), register it in `LOCALE_LOADERS` below, and add the tag
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

const NAMESPACE = "translation";

// Each language is its own chunk: a visitor downloads only the one they read.
const LOCALE_LOADERS: Record<Language, () => Promise<object>> = {
  en: () => import("./locales/en").then((m) => m.en),
  nl: () => import("./locales/nl").then((m) => m.nl),
};

void i18n.use(initReactI18next).init({
  resources: {},
  lng: initialLanguage,
  fallbackLng: DEFAULT_LANGUAGE,
  supportedLngs: SUPPORTED_LANGUAGES,
  defaultNS: NAMESPACE,
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

export async function loadLanguage(lng: Language): Promise<void> {
  if (!i18n.hasResourceBundle(lng, NAMESPACE)) {
    i18n.addResourceBundle(lng, NAMESPACE, await LOCALE_LOADERS[lng]());
  }
}

// The language asked for last, so a slow download cannot override a newer choice.
let wanted: Language = initialLanguage;

// Switch language without persisting it (a page that follows its own setting).
export async function showLanguage(lng: Language): Promise<void> {
  wanted = lng;
  await loadLanguage(lng);
  // A later call may have asked for another language while this one loaded.
  if (wanted === lng) {
    await i18n.changeLanguage(lng);
  }
}

// Resolves once the initial language's strings are in; render after it. It
// goes through changeLanguage because i18next only sets resolvedLanguage when
// the strings are there at the time of the switch.
export const i18nReady: Promise<void> = showLanguage(initialLanguage);

// Switch language and persist the explicit choice so it survives reloads.
export function changeLanguage(lng: Language): void {
  try {
    localStorage.setItem(LANGUAGE_STORAGE_KEY, lng);
  } catch {
    // Persistence is best-effort; the in-memory switch still applies.
  }
  showLanguage(lng).catch(reportLanguageFailure);
}

// A language whose strings cannot be fetched leaves the page as it was.
export function reportLanguageFailure(error: unknown): void {
  console.error("loading language failed", error);
}

export default i18n;
