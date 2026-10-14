import { useTranslation } from "react-i18next";
import {
  COLOR_SCHEME_PREFERENCES,
  setColorSchemePreference,
  useColorSchemeLocked,
  useColorSchemePreference,
} from "../lib/color-scheme";
import * as React from "react";

// Auto / Light / Dark, as the same segmented toggle as the LanguageSwitcher
// beside it. The bundled icon set has no sun or moon, so the options are
// labelled. Hidden while a white-labelled org is shown, which is always light.
export function ColorSchemeSwitcher(): React.JSX.Element | null {
  const { t } = useTranslation();
  const active = useColorSchemePreference();
  const locked = useColorSchemeLocked();

  if (locked) {
    return null;
  }

  return (
    <div
      role="group"
      aria-label={t("common.colorScheme")}
      className="bg-surface-3 rounded-yivi inline-flex gap-1 p-[3px]"
    >
      {COLOR_SCHEME_PREFERENCES.map((preference) => (
        <button
          key={preference}
          type="button"
          onClick={() => setColorSchemePreference(preference)}
          aria-pressed={active === preference}
          className={[
            "text-ink flex h-[26px] cursor-pointer items-center justify-center rounded-md px-2 text-[12px] font-semibold transition-opacity",
            active === preference
              ? "bg-surface shadow-sm"
              : "opacity-55 hover:opacity-100",
          ].join(" ")}
        >
          {t(`common.colorSchemeName.${preference}`)}
        </button>
      ))}
    </div>
  );
}
