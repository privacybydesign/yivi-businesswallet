import { useTranslation } from "react-i18next";
import { changeLanguage, SUPPORTED_LANGUAGES } from "../i18n";
import type { Language } from "../i18n";
import gbFlag from "../assets/flags/gb.svg";
import nlFlag from "../assets/flags/nl.svg";
import * as React from "react";

// Each language shows its country flag instead of an "EN"/"NL" text code. Flags
// are shipped as SVG images rather than regional-indicator emoji: Windows (and
// some other platforms) render those emoji as the plain "GB"/"NL" letter pair,
// defeating the point. A flag image is not an accessible name, so every option
// carries an aria-label with the language's own name and keeps the aria-pressed
// state of the original segmented toggle.
const LANGUAGE_FLAGS: Record<Language, string> = {
  en: gbFlag,
  nl: nlFlag,
};

// A compact segmented toggle matching the members-list filter control, so it is
// indistinguishable from the existing house style. The selected language keeps
// the raised surface pill; the flag can't change colour, so the unselected one
// is dimmed to reinforce the state conveyed by aria-pressed.
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
          aria-label={t(`common.languageName.${lng}`)}
          className={[
            "flex h-[26px] w-[34px] cursor-pointer items-center justify-center rounded-md transition-opacity",
            active === lng
              ? "bg-surface shadow-sm"
              : "opacity-55 hover:opacity-100",
          ].join(" ")}
        >
          <img
            src={LANGUAGE_FLAGS[lng]}
            alt=""
            aria-hidden="true"
            className="border-line h-[15px] w-[21px] rounded-[2px] border object-cover"
          />
        </button>
      ))}
    </div>
  );
}
