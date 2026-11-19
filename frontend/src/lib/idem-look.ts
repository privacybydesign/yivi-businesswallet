import type { HostedTheme } from "../api/identity-proofing";
import { contrastRatio, lighten } from "./theme";

// The Idem app's look for a branded session, as AppBranding.fromSession and
// brandThemeFor derive it (vcmrtd idem lib/theme/app_theme.dart): a colour is
// kept only when it reads on white, else the app's own.
const WHITE = "#ffffff";
const INK = "#101e1e";
const IDEM_PRIMARY = "#ba3354";
const DEFAULT_BODY_TEXT = "#344055";
const DEFAULT_SLATE = "#212e39";
const DEFAULT_SUBTLE_BORDER = "#eaebee";
const TEXT_CONTRAST = 4.5;
const BODY_TEXT_LIGHTEN = 0.15;
const TILE_FILL_LIGHTEN = 0.9;
const TILE_BORDER_LIGHTEN = 0.8;
// The fonts the app bundles; a branded session without one takes the design
// pair, Montserrat for headings and Nunito for body text.
const BUNDLED_FONTS = new Set([
  "alexandria",
  "montserrat",
  "nunito",
  "open sans",
]);
const DESIGN_HEADING_FONT = '"Montserrat", system-ui, sans-serif';
const DESIGN_BODY_FONT = '"Nunito", system-ui, sans-serif';
const HEX = /^#[0-9a-fA-F]{6}$/;
// Idem's own look, for a session without branding (AppBranding.idem): Yivi's
// warm grey for text, its border, the platform font and 8px buttons.
const IDEM_WARM_GREY = "#484747";
const IDEM_BORDER = "#eae5e2";
const PLATFORM_FONT = "system-ui";
const PILL_RADIUS = "9999px";
const IDEM_BUTTON_RADIUS = "8px";

export interface IdemLook {
  primary: string;
  onPrimary: string;
  ink: string;
  bodyText: string;
  slate: string;
  subtleBorder: string;
  // The logo tile: the primary lightened, at half opacity, with a border.
  tileFill: string;
  tileBorder: string;
  headingFont: string;
  bodyFont: string;
  buttonRadius: string;
}

function readableOnWhite(color: string): string | undefined {
  if (!HEX.test(color)) {
    return undefined;
  }
  return (contrastRatio(color, WHITE) ?? 0) >= TEXT_CONTRAST
    ? color.toLowerCase()
    : undefined;
}

// White or ink, whichever reads better on the colour (_readableOn).
function readableOn(color: string): string {
  return (contrastRatio(WHITE, color) ?? 0) >= (contrastRatio(INK, color) ?? 0)
    ? WHITE
    : INK;
}

// The first family of a CSS list the app has (AppFont.fromCss), as CSS;
// undefined when it has none of them.
function idemFont(css: string): string | undefined {
  const known = css
    .split(",")
    .map((name) =>
      name
        .trim()
        .replace(/^["']|["']$/g, "")
        .toLowerCase(),
    )
    .find((name) => BUNDLED_FONTS.has(name));
  return known === undefined
    ? undefined
    : `"${known.replace(/\b\w/g, (c) => c.toUpperCase())}", system-ui, sans-serif`;
}

// Whether the app gets branding at all: the backend sends none without a
// colour, a font or a logo.
export function isIdemBranded(theme: HostedTheme, logoUri: string): boolean {
  return logoUri !== "" || Object.values(theme).some((value) => value !== "");
}

export function idemLook(theme: HostedTheme, branded = true): IdemLook {
  if (!branded) {
    return {
      ...derive({
        ...theme,
        primaryColor: IDEM_PRIMARY,
        textColor: IDEM_WARM_GREY,
        borderColor: IDEM_BORDER,
      }),
      headingFont: PLATFORM_FONT,
      bodyFont: PLATFORM_FONT,
      buttonRadius: IDEM_BUTTON_RADIUS,
    };
  }
  return derive(theme);
}

function derive(theme: HostedTheme): IdemLook {
  const primary = readableOnWhite(theme.primaryColor) ?? IDEM_PRIMARY;
  const text = readableOnWhite(theme.textColor);
  const lighter = text && readableOnWhite(lighten(text, BODY_TEXT_LIGHTEN));
  const font = theme.fontFamily ? idemFont(theme.fontFamily) : undefined;
  return {
    primary,
    onPrimary: readableOn(primary),
    ink: text ?? INK,
    bodyText: text ? (lighter ?? text) : DEFAULT_BODY_TEXT,
    slate: text ?? DEFAULT_SLATE,
    subtleBorder: HEX.test(theme.borderColor)
      ? theme.borderColor.toLowerCase()
      : DEFAULT_SUBTLE_BORDER,
    tileFill: lighten(primary, TILE_FILL_LIGHTEN),
    tileBorder: lighten(primary, TILE_BORDER_LIGHTEN),
    headingFont:
      font ?? (theme.fontFamily ? PLATFORM_FONT : DESIGN_HEADING_FONT),
    bodyFont: font ?? (theme.fontFamily ? PLATFORM_FONT : DESIGN_BODY_FONT),
    buttonRadius: PILL_RADIUS,
  };
}
