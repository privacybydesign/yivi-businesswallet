import i18n from "i18next";
import { initReactI18next } from "react-i18next";
import { en } from "./locales/en";

export const DEFAULT_LANGUAGE = "en";

// To add a language: create `locales/<lng>.ts` mirroring `en`, register it in
// `resources` below, and add a language switcher (no detector is wired yet —
// the app ships single-language).
void i18n.use(initReactI18next).init({
  resources: {
    [DEFAULT_LANGUAGE]: { translation: en },
  },
  lng: DEFAULT_LANGUAGE,
  fallbackLng: DEFAULT_LANGUAGE,
  defaultNS: "translation",
  interpolation: {
    escapeValue: false, // React escapes interpolated values against XSS already
  },
  returnNull: false,
});

export default i18n;
