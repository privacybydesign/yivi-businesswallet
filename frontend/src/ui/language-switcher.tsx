import { useTranslation } from "react-i18next";
import { changeLanguage, SUPPORTED_LANGUAGES } from "../i18n";
import type { Language } from "../i18n";
import * as React from "react";

// Language tags render as their own uppercase code (a "NL"/"EN" toggle), which
// reads the same in any language, so no per-language label lookup is needed.
const LANGUAGE_LABELS: Record<Language, string> = {
  en: "EN",
  nl: "NL",
};

// A compact segmented toggle matching the members-list filter control, so it is
// indistinguishable from the existing house style.
export function LanguageSwitcher(): React.JSX.Element {
  const { t, i18n } = useTranslation();
  const active = (i18n.resolvedLanguage ?? i18n.language) as Language;

  return (
    <div
      role="group"
      aria-label={t("common.language")}
      className="bg-surface-3 rounded-yivi inline-flex gap-1 p-[3px]"
    >
      {SUPPORTED_LANGUAGES.map((lng) => (
        <button
          key={lng}
          type="button"
          onClick={() => changeLanguage(lng)}
          aria-pressed={active === lng}
          className={[
            "h-[26px] cursor-pointer rounded-md px-2.5 text-[12.5px] font-semibold transition-colors",
            active === lng
              ? "bg-surface text-ink shadow-sm"
              : "text-ink-soft hover:text-ink",
          ].join(" ")}
        >
          {LANGUAGE_LABELS[lng]}
        </button>
      ))}
    </div>
  );
}
