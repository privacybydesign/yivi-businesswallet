// Language constants and the pure resolver used to pick the initial language.
// Kept free of i18next and DOM access so the resolver can be unit-tested.

export const SUPPORTED_LANGUAGES = ["en", "nl"] as const;

export type Language = (typeof SUPPORTED_LANGUAGES)[number];

export const DEFAULT_LANGUAGE: Language = "en";

// localStorage key holding the user's explicit language choice. Read and
// written only by the resolver in index.ts (no i18next detector is wired), so
// the choice survives reloads.
export const LANGUAGE_STORAGE_KEY = "bw.language";

export function isSupportedLanguage(
  value: string | null | undefined,
): value is Language {
  return (
    value != null && (SUPPORTED_LANGUAGES as readonly string[]).includes(value)
  );
}

// An explicit stored choice wins; otherwise fall back to the first browser
// preference we support (matching on the base tag, so "nl-NL" counts as "nl");
// otherwise the default. `en` stays the fallback for any unmatched preference.
export function resolveInitialLanguage(
  stored: string | null,
  preferred: readonly string[],
): Language {
  if (isSupportedLanguage(stored)) {
    return stored;
  }
  for (const preference of preferred) {
    const base = preference.toLowerCase().split("-")[0];
    if (isSupportedLanguage(base)) {
      return base;
    }
  }
  return DEFAULT_LANGUAGE;
}
