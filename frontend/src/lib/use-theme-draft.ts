import { useEffect, useState } from "react";
import { useTranslation } from "react-i18next";
import { THEME_COLOR_FIELDS } from "../api/theme";
import type {
  LogoChange,
  OrgTheme,
  OrgThemeInput,
  ThemeColorField,
} from "../api/theme";
import { AA_CONTRAST, primaryContrastFloor } from "./theme";

export const HEX_PATTERN = /^#[0-9a-fA-F]{6}$/;

// Keep the accepted types and size cap in step with the backend detectLogoType
// allowlist and MaxLogoBytes.
export const ACCEPTED_LOGO_TYPES = [
  "image/png",
  "image/jpeg",
  "image/gif",
  "image/webp",
  "image/svg+xml",
];
const MAX_LOGO_BYTES = 512 * 1024;

type Colors = Record<ThemeColorField, string>;

function colorsFromSettings(settings: OrgTheme): Colors {
  return THEME_COLOR_FIELDS.reduce((acc, field) => {
    acc[field] = settings[field];
    return acc;
  }, {} as Colors);
}

// The wallet theme form's unsaved state: its colours, font and logo, and
// whether they can be saved.
export interface ThemeDraft {
  colors: Colors;
  setColor: (field: ThemeColorField, next: string) => void;
  fontFamily: string;
  setFontFamily: (next: string) => void;
  logoFile: File | null;
  // The logo the draft shows: a picked file's, else the saved one unless
  // removed; null for none.
  logoPreviewSrc: string | null;
  removeLogo: boolean;
  logoError: string | null;
  selectLogo: (file: File | null) => void;
  clearLogo: () => void;
  anyColorInvalid: boolean;
  contrastFails: boolean;
  primaryContrast: number | null;
  invalid: boolean;
  dirty: boolean;
  // What a save sends.
  input: () => OrgThemeInput;
  reset: () => void;
}

export function useThemeDraft(settings: OrgTheme): ThemeDraft {
  const { t } = useTranslation();
  const [colors, setColors] = useState<Colors>(() =>
    colorsFromSettings(settings),
  );
  const [fontFamily, setFontFamily] = useState(settings.fontFamily);
  const [logoFile, setLogoFile] = useState<File | null>(null);
  const [newLogoUrl, setNewLogoUrl] = useState<string | null>(null);
  const [removeLogo, setRemoveLogo] = useState(false);
  const [logoError, setLogoError] = useState<string | null>(null);

  // The object URL for a freshly picked file is created in selectLogo (not
  // render), so this effect only revokes the previous one when it changes or
  // the form unmounts.
  useEffect(() => {
    if (!newLogoUrl) {
      return;
    }
    return () => URL.revokeObjectURL(newLogoUrl);
  }, [newLogoUrl]);

  const primary = colors.primaryColor;
  const primaryContrast = HEX_PATTERN.test(primary)
    ? (primaryContrastFloor(primary) ?? 0)
    : null;
  const contrastFails =
    primaryContrast !== null && primaryContrast < AA_CONTRAST;
  // A field is invalid only when it is non-empty and malformed (empty clears it
  // back to the default look). The extra palette roles are auto-corrected to AA
  // by the theme engine, so only the as-applied primary is gated on contrast.
  const anyColorInvalid = THEME_COLOR_FIELDS.some(
    (field) => colors[field] !== "" && !HEX_PATTERN.test(colors[field]),
  );
  const storedLogo =
    settings.logoUri !== "" && !removeLogo ? settings.logoUri : null;
  const dirty =
    logoFile !== null ||
    (removeLogo && settings.logoUri !== "") ||
    fontFamily !== settings.fontFamily ||
    THEME_COLOR_FIELDS.some((field) => colors[field] !== settings[field]);

  return {
    colors,
    setColor: (field, next) =>
      setColors((prev) => ({ ...prev, [field]: next })),
    fontFamily,
    setFontFamily,
    logoFile,
    logoPreviewSrc: logoFile ? newLogoUrl : storedLogo,
    removeLogo,
    logoError,
    selectLogo: (file) => {
      if (!file) {
        return;
      }
      if (!ACCEPTED_LOGO_TYPES.includes(file.type)) {
        setLogoError(t("themeSettings.logoTypeInvalid"));
        return;
      }
      if (file.size > MAX_LOGO_BYTES) {
        setLogoError(t("themeSettings.logoTooLarge"));
        return;
      }
      setLogoError(null);
      setRemoveLogo(false);
      setLogoFile(file);
      setNewLogoUrl(URL.createObjectURL(file));
    },
    clearLogo: () => {
      setLogoError(null);
      setLogoFile(null);
      setNewLogoUrl(null);
      setRemoveLogo(true);
    },
    anyColorInvalid,
    contrastFails,
    primaryContrast,
    invalid: anyColorInvalid || contrastFails,
    dirty,
    input: () => {
      const trimmed = THEME_COLOR_FIELDS.reduce((acc, field) => {
        acc[field] = colors[field].trim();
        return acc;
      }, {} as Colors);
      const logo: LogoChange = logoFile ?? (removeLogo ? "remove" : "keep");
      return { ...trimmed, fontFamily, logo };
    },
    reset: () => {
      setColors(colorsFromSettings(settings));
      setFontFamily(settings.fontFamily);
      setLogoFile(null);
      setNewLogoUrl(null);
      setRemoveLogo(false);
      setLogoError(null);
    },
  };
}
