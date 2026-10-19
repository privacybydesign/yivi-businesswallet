import type { OrgTheme } from "../api/theme";
import {
  DEFAULT_DESIGN,
  DESIGN_ATTRIBUTE,
  WHITE_LABEL_DESIGN,
  setWhiteLabelScheme,
  type Design,
} from "./color-scheme";

// Runtime theming. The app's colours resolve through the --yb-* custom
// properties on :root (see index.css); overriding those re-colours every
// Tailwind utility that maps to them. Clearing an override falls back to the
// :root default, so an unset field keeps the default look.
//
// A themed org is always shown light: its colours are the brand's own, picked
// for light pages, so the white-label design keeps the light scheme (see
// setWhiteLabelScheme) and every token has one value. Two mechanisms:
//   * Brand fills (primary, accent), the navigation chrome and the font are
//     applied INLINE on the documentElement (resolveThemeTokens → applyOrgTheme).
//   * Neutral/semantic roles (surface, border, text, link, status) are derived
//     to WCAG AA and shipped as one <style id="ybw-org-theme"> block. Its doubled
//     `:root:root` selector wins over index.css by SPECIFICITY, not source order
//     — which lets the inline pre-paint script (index.html) inject it before
//     Vite's CSS without a source-order race, so a full reload never flashes the
//     default palette (the FOUC this all guards against).

// --- Brand fills (applied inline on the documentElement) ---
const PRIMARY = "--yb-primary";
const PRIMARY_HOVER = "--yb-primary-hover";
const PRIMARY_FG = "--yb-primary-fg";
const ACCENT = "--yb-brand";
const ACCENT_600 = "--yb-brand-600";

// Navigation chrome (sidebar + top bar) and the body font: a chrome colour is a
// self-contained fill with its own readable foreground, so it is applied inline
// alongside the brand fills, as is the font.
const SIDEBAR = "--yb-sidebar";
const SIDEBAR_FG = "--yb-sidebar-fg";
const SIDEBAR_FG_SOFT = "--yb-sidebar-fg-soft";
const SIDEBAR_ACTIVE = "--yb-sidebar-active";
const SIDEBAR_LINE = "--yb-sidebar-line";
const TOPBAR = "--yb-topbar";
const TOPBAR_FG = "--yb-topbar-fg";
const TOPBAR_FG_SOFT = "--yb-topbar-fg-soft";
const TOPBAR_LINE = "--yb-topbar-line";
const FONT_SANS = "--yb-font-sans";
const FONT_DISPLAY = "--yb-font-display";

const THEMED_PROPERTIES = [
  PRIMARY,
  PRIMARY_HOVER,
  PRIMARY_FG,
  ACCENT,
  ACCENT_600,
  SIDEBAR,
  SIDEBAR_FG,
  SIDEBAR_FG_SOFT,
  SIDEBAR_ACTIVE,
  SIDEBAR_LINE,
  TOPBAR,
  TOPBAR_FG,
  TOPBAR_FG_SOFT,
  TOPBAR_LINE,
  FONT_SANS,
  FONT_DISPLAY,
] as const;

// How a chrome seed derives its companions: the soft foreground fades the
// readable foreground toward the background; the active/hover fill and the
// border nudge the background toward the foreground so they stay visible on the
// chrome.
const CHROME_FG_SOFT_MIX = 0.32;
const CHROME_ACTIVE_MIX = 0.12;
const CHROME_LINE_MIX = 0.18;

// isSafeFontFamily gates a stored font-family string before it is set as a CSS
// custom property (defense-in-depth alongside the backend's own validation):
// only letters, digits, spaces, commas, quotes and hyphens, and a sane length,
// so no value can carry CSS-injection punctuation (;{}()/*).
const FONT_FAMILY_PATTERN = /^[A-Za-z0-9 ,'"-]{1,120}$/;
function isSafeFontFamily(value: string): boolean {
  return FONT_FAMILY_PATTERN.test(value);
}

// --- Derived roles (shipped via the <style> block) ---
const LINK = "--yb-link";
const INK = "--yb-ink";
const INK_SOFT = "--yb-ink-soft";
const MUTED = "--yb-muted";
const SURFACE = "--yb-surface";
const SURFACE_2 = "--yb-surface-2";
const SURFACE_3 = "--yb-surface-3";
const LINE = "--yb-line";
const LINE_STRONG = "--yb-line-strong";
const SUCCESS = "--yb-success";
const SUCCESS_BG = "--yb-success-bg";
const WARNING = "--yb-warning";
const WARNING_FG = "--yb-warning-fg";
const WARNING_BG = "--yb-warning-bg";
const ERROR = "--yb-error";
const ERROR_BG = "--yb-error-bg";

// The <style> element carrying the derived overrides.
const ORG_THEME_STYLE_ID = "ybw-org-theme";

// Foreground candidates for text sitting on a themed colour: near-white or the
// app's warm-dark ink. readableForeground picks whichever reads better.
const LIGHT_FG = "#ffffff";
const DARK_FG = "#211f1f";

// How far to darken a colour for its hover / -600 shade.
const HOVER_DARKEN = 0.12;
const ACCENT_DARKEN = 0.16;

// WCAG 2.2 AA needs 4.5:1 for normal-size text; buttons use ~13.5px semibold,
// which is "normal" text, so this is the bar a primary colour must clear.
export const AA_CONTRAST = 4.5;

// Step size and iteration cap when nudging a colour toward the contrast floor.
const ADJUST_STEP = 0.06;
const ADJUST_MAX_STEPS = 24;

// What each colour is when the theme leaves it unset: the white-label design's
// own values (from index.css), as a themed org is shown in that design. Shown
// as the placeholder / picker fallback in the settings UI, keyed by the theme
// colour field so it can look each up.
export const DEFAULT_PRIMARY = "#101e1e";
export const DEFAULT_ACCENT = "#ba3354";
export const COLOR_FIELD_DEFAULTS = {
  primaryColor: DEFAULT_PRIMARY,
  accentColor: DEFAULT_ACCENT,
  textColor: "#101e1e",
  surfaceColor: "#fbfbfc",
  borderColor: "#eaebee",
  linkColor: "#1d4e89",
  successColor: "#1f6b4c",
  warningColor: "#eba73b",
  errorColor: "#b42335",
  sidebarColor: "#ffffff",
  topbarColor: "#ffffff",
} as const;

// Curated font options a theme may pick (org and customer alike), for headings
// and body alike. Values are full
// font-family stacks; each is either a bundled font (Montserrat, Nunito, Open
// Sans, Alexandria) shipped with its open licence, or a system/web-safe stack
// rendered from the client's own fonts — so nothing is distributed by us and
// there is no commercial-licence exposure. The default stores "" so the theme
// keeps the white-label design's own pair (Montserrat headings, Nunito body).
// Names are proper nouns and stay untranslated.
export const FONT_OPTIONS = [
  { name: "Montserrat / Nunito", value: "" },
  { name: "Montserrat", value: '"Montserrat", system-ui, sans-serif' },
  { name: "Nunito", value: '"Nunito", system-ui, sans-serif' },
  { name: "Open Sans", value: '"Open Sans", system-ui, sans-serif' },
  { name: "Alexandria", value: '"Alexandria", system-ui, sans-serif' },
  {
    name: "System UI",
    value: 'system-ui, -apple-system, "Segoe UI", Roboto, sans-serif',
  },
  { name: "Helvetica / Arial", value: '"Helvetica Neue", Arial, sans-serif' },
  { name: "Georgia", value: 'Georgia, "Times New Roman", serif' },
  {
    name: "Monospace",
    value: "ui-monospace, SFMono-Regular, Menlo, Consolas, monospace",
  },
] as const;

// --- Neutral anchors: the white-label design's, mirrored from index.css ---
// The derivations gate contrast against, and tint from, these baseline values
// so the derived palette stays coherent with the design. A theme is always
// shown in the white-label design (usesWhiteLabelDesign), so its anchors are
// that design's neutrals, not Yivi's.
const LIGHT = {
  ink: "#101e1e",
  surface: "#ffffff",
  surface2: "#fbfbfc",
  surface3: "#f5f5f6",
  line: "#eaebee",
  lineStrong: "#d6d9dd",
  highlight: "#eaf3f9",
} as const;
// How strongly a surface seed tints the cards, which are backgrounds behind
// body text, so the tint is kept subtle.
const SURFACE_TINT = 0.08;

// A surface seed is the page background and a border seed the line colour as
// given; the hover row and the strong line are those darkened,
// as Yivi's defaults are (#faf8f6 → #f5f2ef, #eae5e2 → #d7d2cd).
const SURFACE_3_DARKEN = 0.02;
const LINE_STRONG_DARKEN = 0.08;

// How far the secondary and muted text fade from the text seed towards white,
// as Yivi's defaults do from its ink (#484747 → #757375, #9f9a9a).
const INK_SOFT_FADE = 0.25;
const MUTED_FADE = 0.45;
// How strongly a status seed tints its pale chip background.
const STATUS_BG_TINT = 0.14;

interface Rgb {
  r: number;
  g: number;
  b: number;
}

// parseHex accepts a 6-digit "#rrggbb" string (the only format the backend
// stores) and returns null for anything else.
function parseHex(hex: string): Rgb | null {
  if (!/^#[0-9a-fA-F]{6}$/.test(hex)) {
    return null;
  }
  return {
    r: parseInt(hex.slice(1, 3), 16),
    g: parseInt(hex.slice(3, 5), 16),
    b: parseInt(hex.slice(5, 7), 16),
  };
}

function toHex({ r, g, b }: Rgb): string {
  const channel = (c: number): string =>
    Math.max(0, Math.min(255, Math.round(c)))
      .toString(16)
      .padStart(2, "0");
  return `#${channel(r)}${channel(g)}${channel(b)}`;
}

// relativeLuminance follows the WCAG definition (sRGB → linear, weighted sum).
function relativeLuminance({ r, g, b }: Rgb): number {
  const linear = (c: number): number => {
    const s = c / 255;
    return s <= 0.03928 ? s / 12.92 : Math.pow((s + 0.055) / 1.055, 2.4);
  };
  return 0.2126 * linear(r) + 0.7152 * linear(g) + 0.0722 * linear(b);
}

// contrastRatio returns the WCAG contrast ratio (1–21) between two hex colours,
// or null if either is malformed.
export function contrastRatio(a: string, b: string): number | null {
  const rgbA = parseHex(a);
  const rgbB = parseHex(b);
  if (!rgbA || !rgbB) {
    return null;
  }
  const la = relativeLuminance(rgbA);
  const lb = relativeLuminance(rgbB);
  const lighter = Math.max(la, lb);
  const darker = Math.min(la, lb);
  return (lighter + 0.05) / (darker + 0.05);
}

// readableForeground picks the foreground (light or dark) that reads best on the
// given background.
export function readableForeground(background: string): string {
  const light = contrastRatio(background, LIGHT_FG) ?? 0;
  const dark = contrastRatio(background, DARK_FG) ?? 0;
  return dark > light ? DARK_FG : LIGHT_FG;
}

// readableForegroundAA is like readableForeground but GUARANTEES the WCAG-AA
// floor: for a mid-tone background neither near-white nor the warm-dark ink may
// reach 4.5:1, so the dark candidate is pushed toward pure black until it clears
// AA, then whichever candidate has the most contrast wins. Since pure black or
// pure white clears ~4.5:1 against any background, the result is always AA — used
// for the navigation chrome, whose backgrounds aren't gated at save time (unlike
// the primary button, which is).
export function readableForegroundAA(background: string): string {
  const dark = adjustToContrast(DARK_FG, background, "darken");
  const lightRatio = contrastRatio(LIGHT_FG, background) ?? 0;
  const darkRatio = contrastRatio(dark, background) ?? 0;
  return darkRatio >= lightRatio ? dark : LIGHT_FG;
}

function darken(hex: string, amount: number): string {
  const rgb = parseHex(hex);
  if (!rgb) {
    return hex;
  }
  const scale = 1 - amount;
  return toHex({ r: rgb.r * scale, g: rgb.g * scale, b: rgb.b * scale });
}

// lighten mixes a colour towards white by the given fraction (0–1).
function lighten(hex: string, amount: number): string {
  const rgb = parseHex(hex);
  if (!rgb) {
    return hex;
  }
  return toHex({
    r: rgb.r + (255 - rgb.r) * amount,
    g: rgb.g + (255 - rgb.g) * amount,
    b: rgb.b + (255 - rgb.b) * amount,
  });
}

// mix blends `base` toward `tint` by fraction t (0 = base, 1 = tint). Both must
// be valid hex; a malformed tint leaves the base unchanged.
function mix(base: string, tint: string, t: number): string {
  const a = parseHex(base);
  const b = parseHex(tint);
  if (!a || !b) {
    return base;
  }
  return toHex({
    r: a.r + (b.r - a.r) * t,
    g: a.g + (b.g - a.g) * t,
    b: a.b + (b.b - a.b) * t,
  });
}

// primaryContrastFloor is the lowest contrast the applied foreground reaches
// across the button's resting and hover backgrounds — the number to check a
// primary colour against the AA bar. The hover shade is a darkened primary (see
// applyOrgTheme) that keeps the same --yb-primary-fg, so a light primary which
// only just clears 4.5:1 at rest can dip below it on hover; gating on the floor
// keeps every interactive state at AA, not just the resting one.
export function primaryContrastFloor(background: string): number | null {
  const foreground = readableForeground(background);
  const resting = contrastRatio(background, foreground);
  const hover = contrastRatio(darken(background, HOVER_DARKEN), foreground);
  if (resting === null || hover === null) {
    return null;
  }
  return Math.min(resting, hover);
}

// resolveThemeTokens turns a saved theme into the map of INLINE design-token
// overrides (the brand fills) to apply on the documentElement: the
// primary/accent seeds plus the hover/shade and readable-foreground variants
// derived from them. A missing or malformed seed is simply left out (so it falls
// back to the :root default), so the returned map only ever holds valid
// overrides. This is the single source of truth for both the runtime apply and
// the pre-paint cache (see index.html), so the cached palette and the applied
// palette can never drift apart. The derived roles are handled separately by
// resolveThemeCss.
export function resolveThemeTokens(
  theme: OrgTheme | null | undefined,
): Record<string, string> {
  const tokens: Record<string, string> = {};

  const primary = theme?.primaryColor ?? "";
  if (parseHex(primary)) {
    tokens[PRIMARY] = primary;
    tokens[PRIMARY_HOVER] = darken(primary, HOVER_DARKEN);
    tokens[PRIMARY_FG] = readableForeground(primary);
  }

  const accent = theme?.accentColor ?? "";
  if (parseHex(accent)) {
    tokens[ACCENT] = accent;
    tokens[ACCENT_600] = darken(accent, ACCENT_DARKEN);
  }

  // Navigation chrome: each seed is a brand fill with a readable foreground, a
  // faded soft foreground, and a nudged active/border shade so nav elements
  // stay legible on the coloured chrome.
  const sidebar = theme?.sidebarColor ?? "";
  if (parseHex(sidebar)) {
    const fg = readableForegroundAA(sidebar);
    tokens[SIDEBAR] = sidebar;
    tokens[SIDEBAR_FG] = fg;
    tokens[SIDEBAR_FG_SOFT] = mix(fg, sidebar, CHROME_FG_SOFT_MIX);
    tokens[SIDEBAR_ACTIVE] = mix(sidebar, fg, CHROME_ACTIVE_MIX);
    tokens[SIDEBAR_LINE] = mix(sidebar, fg, CHROME_LINE_MIX);
  }

  const topbar = theme?.topbarColor ?? "";
  if (parseHex(topbar)) {
    const fg = readableForegroundAA(topbar);
    tokens[TOPBAR] = topbar;
    tokens[TOPBAR_FG] = fg;
    tokens[TOPBAR_FG_SOFT] = mix(fg, topbar, CHROME_FG_SOFT_MIX);
    tokens[TOPBAR_LINE] = mix(topbar, fg, CHROME_LINE_MIX);
  }

  // An org's font is its headings' as well as its body text's.
  const font = theme?.fontFamily ?? "";
  if (isSafeFontFamily(font)) {
    tokens[FONT_SANS] = font;
    tokens[FONT_DISPLAY] = font;
  }

  return tokens;
}

// extremeBackground returns the background from the list with the highest or
// lowest relative luminance — the worst case to gate a colour's contrast on.
function extremeBackground(
  backgrounds: readonly string[],
  pick: "lightest" | "darkest",
): string {
  let chosen = backgrounds[0];
  let chosenLuminance = pick === "lightest" ? -1 : 2;
  for (const bg of backgrounds) {
    const rgb = parseHex(bg);
    if (!rgb) {
      continue;
    }
    const luminance = relativeLuminance(rgb);
    if (
      (pick === "lightest" && luminance > chosenLuminance) ||
      (pick === "darkest" && luminance < chosenLuminance)
    ) {
      chosen = bg;
      chosenLuminance = luminance;
    }
  }
  return chosen;
}

// adjustToContrast nudges a colour towards black (darken) or white (lighten) in
// small steps until it clears the AA floor against the given background, or the
// step cap is hit (by which point it is near-black / near-white and clears AA
// against any surface tier).
function adjustToContrast(
  color: string,
  background: string,
  direction: "darken" | "lighten",
): string {
  let current = color;
  for (let i = 0; i < ADJUST_MAX_STEPS; i++) {
    if ((contrastRatio(current, background) ?? 0) >= AA_CONTRAST) {
      break;
    }
    current =
      direction === "darken"
        ? darken(current, ADJUST_STEP)
        : lighten(current, ADJUST_STEP);
  }
  return current;
}

// The backgrounds a `text-link` element renders on: the seed-tinted surfaces
// plus the fixed highlight/info chip. The derived link must clear AA against
// every one, so we gate on the darkest (clearing AA there clears it on all the
// others).
function linkBackgrounds(theme: OrgTheme | null | undefined): string[] {
  return [...Object.values(surfaceTiers(theme)), LIGHT.highlight];
}

// resolveLinkColor derives the link colour from the link seed (falling back to
// the accent seed, so a brand accent still tints links even when no explicit
// link colour is set), darkened until it clears AA on the darkest background.
// Returns null when there is no valid seed (the default link then stands).
export function resolveLinkColor(
  theme: OrgTheme | null | undefined,
): string | null {
  const seed =
    theme?.linkColor && parseHex(theme.linkColor)
      ? theme.linkColor
      : (theme?.accentColor ?? "");
  if (!parseHex(seed)) {
    return null;
  }
  return adjustToContrast(
    seed,
    extremeBackground(linkBackgrounds(theme), "darkest"),
    "darken",
  );
}

// usableAsSurface reports whether a seed is light enough to be the page
// background as given: the default ink must read on it.
function usableAsSurface(seed: string): boolean {
  return (contrastRatio(seed, LIGHT.ink) ?? 0) >= AA_CONTRAST;
}

// surfaceTiers derives the surface tiers from the surface seed, or the default
// neutrals when no seed is set. A seed light enough for text is the page
// background itself, with the cards subtly tinted towards it; a darker one
// only tints the default neutrals, which keeps the surfaces close to the
// AA-verified defaults.
function surfaceTiers(theme: OrgTheme | null | undefined): {
  surface: string;
  surface2: string;
  surface3: string;
} {
  const seed = theme?.surfaceColor ?? "";
  if (!parseHex(seed)) {
    return {
      surface: LIGHT.surface,
      surface2: LIGHT.surface2,
      surface3: LIGHT.surface3,
    };
  }
  return usableAsSurface(seed)
    ? {
        surface: mix(LIGHT.surface, seed, SURFACE_TINT),
        surface2: seed,
        surface3: darken(seed, SURFACE_3_DARKEN),
      }
    : {
        surface: mix(LIGHT.surface, seed, SURFACE_TINT),
        surface2: mix(LIGHT.surface2, seed, SURFACE_TINT),
        surface3: mix(LIGHT.surface3, seed, SURFACE_TINT),
      };
}

// statusPair derives a status role from its seed: a pale chip background (the
// seed mixed toward the surface) and a solid foreground/icon colour. The solid
// is nudged past the WCAG-AA floor against the WORST-CASE background it
// actually lands on — not just its own chip. `text-error` / `text-success` /
// `text-warning` render the solid bare on the surface tiers (--yb-surface etc.)
// all over the app, and a surface seed tints those tiers, so the candidates are
// the chip PLUS every (possibly tinted) surface tier, and we gate on the
// darkest of them.
function statusPair(
  seed: string,
  surfaces: ReturnType<typeof surfaceTiers>,
): { solid: string; bg: string } {
  const bg = mix(LIGHT.surface, seed, STATUS_BG_TINT);
  const worst = extremeBackground([...Object.values(surfaces), bg], "darkest");
  return { solid: adjustToContrast(seed, worst, "darken"), bg };
}

// resolveThemeCss derives the map of token overrides (surface, border, text,
// link, status) from a saved theme. Only roles whose seed is set (or, for the
// link, whose seed or accent is set) appear. This is the single source of
// truth for both the runtime <style> block (applyOrgTheme) and the pre-paint
// cache (index.html), so they cannot drift.
export function resolveThemeCss(
  theme: OrgTheme | null | undefined,
): Record<string, string> {
  const out: Record<string, string> = {};
  const surfaces = surfaceTiers(theme);

  // Surfaces (subtle brand tint of the neutral tiers).
  if (parseHex(theme?.surfaceColor ?? "")) {
    out[SURFACE] = surfaces.surface;
    out[SURFACE_2] = surfaces.surface2;
    out[SURFACE_3] = surfaces.surface3;
  }

  // Borders (decorative, no text): the seed itself, and darkened for the
  // strong line.
  const border = theme?.borderColor ?? "";
  if (parseHex(border)) {
    out[LINE] = border;
    out[LINE_STRONG] = darken(border, LINE_STRONG_DARKEN);
  }

  // Body text/ink: gated to AA against the (possibly tinted) darkest surface.
  // The secondary text fades from the ink and is gated the same way; the muted
  // text (placeholders, disabled) is not gated, as its default is not.
  const text = theme?.textColor ?? "";
  if (parseHex(text)) {
    const darkest = extremeBackground(Object.values(surfaces), "darkest");
    const ink = adjustToContrast(text, darkest, "darken");
    out[INK] = ink;
    out[INK_SOFT] = adjustToContrast(
      lighten(ink, INK_SOFT_FADE),
      darkest,
      "darken",
    );
    out[MUTED] = lighten(ink, MUTED_FADE);
  }

  // Link (seed = link colour, else accent).
  const link = resolveLinkColor(theme);
  if (link) {
    out[LINK] = link;
  }

  // Semantic status roles. The solid is gated against the surface tiers it
  // renders on (see statusPair), so it needs the theme's (possibly tinted)
  // surfaces.
  const success = theme?.successColor ?? "";
  if (parseHex(success)) {
    const p = statusPair(success, surfaces);
    out[SUCCESS] = p.solid;
    out[SUCCESS_BG] = p.bg;
  }
  const warning = theme?.warningColor ?? "";
  if (parseHex(warning)) {
    const p = statusPair(warning, surfaces);
    out[WARNING] = p.solid;
    out[WARNING_FG] = p.solid;
    out[WARNING_BG] = p.bg;
  }
  const error = theme?.errorColor ?? "";
  if (parseHex(error)) {
    const p = statusPair(error, surfaces);
    out[ERROR] = p.solid;
    out[ERROR_BG] = p.bg;
  }

  return out;
}

// buildThemeCss serialises the token map into a stylesheet. The doubled
// `:root:root` selector wins over index.css by specificity (not source order),
// so the pre-paint injection in index.html has no ordering race against Vite's
// CSS. Returns "" when there is nothing to override.
export function buildThemeCss(theme: OrgTheme | null | undefined): string {
  const tokens = resolveThemeCss(theme);
  const names = Object.keys(tokens);
  if (names.length === 0) {
    return "";
  }
  return `:root:root{${names.map((n) => `${n}:${tokens[n]}`).join(";")}}`;
}

// setThemeStyle installs (or clears) the derived <style> block.
function setThemeStyle(css: string): void {
  const existing = document.getElementById(ORG_THEME_STYLE_ID);
  if (!css) {
    existing?.remove();
    return;
  }
  const style =
    existing instanceof HTMLStyleElement
      ? existing
      : document.createElement("style");
  style.id = ORG_THEME_STYLE_ID;
  style.textContent = css;
  if (!style.isConnected) {
    document.head.appendChild(style);
  }
}

// usesWhiteLabelDesign reports whether a theme is shown in the white-label
// design: any theme the org has set up. An org without one keeps Yivi's.
export function usesWhiteLabelDesign(
  theme: OrgTheme | null | undefined,
): boolean {
  return theme?.configured === true;
}

// setWhiteLabelDesign switches the white-label design on or off, and with it
// the light scheme it is always shown in.
function setWhiteLabelDesign(design: Design): void {
  if (design === WHITE_LABEL_DESIGN) {
    document.documentElement.setAttribute(DESIGN_ATTRIBUTE, WHITE_LABEL_DESIGN);
  } else {
    document.documentElement.removeAttribute(DESIGN_ATTRIBUTE);
  }
  setWhiteLabelScheme(design);
}

// themeDesign is the design a theme is shown in.
function themeDesign(theme: OrgTheme | null | undefined): Design {
  return usesWhiteLabelDesign(theme) ? WHITE_LABEL_DESIGN : DEFAULT_DESIGN;
}

// shouldApplyOrgTheme reports whether the runtime theme effect should push
// tokens onto the document for the current query state. While the theme query is
// in flight its data is `undefined`; applying then runs applyOrgTheme(undefined),
// which clears every token — stripping the palette the inline pre-paint script
// (index.html) already set and flashing the default look back in (the FOUC).
// Only apply once real data has arrived (a theme object, or null for an org that
// has no theme, which legitimately resets to the default).
export function shouldApplyOrgTheme(
  theme: OrgTheme | null | undefined,
): boolean {
  return theme !== undefined;
}

// applyOrgTheme maps a saved theme onto the design tokens: the white-label
// design for a configured theme, the brand fills inline on the
// documentElement, the derived roles as the <style> block. Missing/invalid
// fields are cleared so they fall back to the design's own.
export function applyOrgTheme(theme: OrgTheme | null | undefined): void {
  const root = document.documentElement.style;
  const tokens = resolveThemeTokens(theme);
  for (const property of THEMED_PROPERTIES) {
    const value = tokens[property];
    if (value === undefined) {
      root.removeProperty(property);
    } else {
      root.setProperty(property, value);
    }
  }
  setThemeStyle(buildThemeCss(theme));
  setWhiteLabelDesign(themeDesign(theme));
}

// clearOrgTheme restores every token to its default (used when leaving an org).
export function clearOrgTheme(): void {
  const root = document.documentElement.style;
  for (const property of THEMED_PROPERTIES) {
    root.removeProperty(property);
  }
  setThemeStyle("");
  setWhiteLabelDesign(DEFAULT_DESIGN);
}

// Cached theme, keyed by org slug, so a full page reload can paint the tenant's
// colours before React hydrates (see the inline script in index.html). Without
// it the default palette paints first and then applyOrgTheme swaps it in — a
// visible flash of the wrong branding (FOUC).
export const THEME_CACHE_PREFIX = "ybw.theme.";

function themeCacheKey(slug: string): string {
  return THEME_CACHE_PREFIX + slug;
}

// The cached shape: the inline brand-fill overrides, the derived CSS string
// and whether it uses the white-label design. Kept
// in step with the reader in index.html.
interface CachedTheme {
  inline: Record<string, string>;
  css: string;
  whiteLabel: boolean;
}

// cacheOrgTheme stores the resolved overrides for an org so the next full load
// can apply them before first paint. An empty payload (the org uses the default
// look) is written too, so clearing a theme also clears the stale cache.
export function cacheOrgTheme(
  slug: string,
  theme: OrgTheme | null | undefined,
): void {
  if (!slug) {
    return;
  }
  try {
    const payload: CachedTheme = {
      inline: resolveThemeTokens(theme),
      css: buildThemeCss(theme),
      whiteLabel: usesWhiteLabelDesign(theme),
    };
    window.localStorage.setItem(themeCacheKey(slug), JSON.stringify(payload));
  } catch {
    // Storage can be unavailable (private mode, quota). The runtime apply still
    // themes the app; only the pre-paint optimisation is lost, so this is
    // non-fatal.
  }
}

// applyCachedOrgTheme applies an org's cached palette without a network fetch,
// for the pre-auth screens (login/register) where a `?org=<slug>` hints the
// tenant but the theme endpoint is member-gated. It re-uses the cache written on
// a prior authenticated visit; a first-time visitor simply keeps the default
// Yivi look (graceful fallback). Returns whether a cached theme was applied.
export function applyCachedOrgTheme(slug: string): boolean {
  if (!slug) {
    return false;
  }
  try {
    const raw = window.localStorage.getItem(themeCacheKey(slug));
    if (!raw) {
      return false;
    }
    const payload = JSON.parse(raw) as Partial<CachedTheme>;
    const root = document.documentElement.style;
    for (const [property, value] of Object.entries(payload.inline ?? {})) {
      root.setProperty(property, value);
    }
    setThemeStyle(payload.css ?? "");
    setWhiteLabelDesign(
      payload.whiteLabel === true ? WHITE_LABEL_DESIGN : DEFAULT_DESIGN,
    );
    return true;
  } catch {
    return false;
  }
}
