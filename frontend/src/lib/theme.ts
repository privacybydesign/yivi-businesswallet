import type { OrgTheme } from "../api/theme";

// Runtime theming. The app's colours resolve through the --yb-* custom
// properties on :root (see index.css); overriding those on the documentElement
// re-colours every Tailwind utility that maps to them. Clearing an override
// falls back to the :root default, so an unset field keeps the default look.

// The design tokens an org theme re-colours. Primary drives buttons and the
// active-nav accent; accent (brand) drives avatars and small accents.
const PRIMARY = "--yb-primary";
const PRIMARY_HOVER = "--yb-primary-hover";
const PRIMARY_FG = "--yb-primary-fg";
const ACCENT = "--yb-brand";
const ACCENT_600 = "--yb-brand-600";
const LINK = "--yb-link";

const THEMED_PROPERTIES = [
  PRIMARY,
  PRIMARY_HOVER,
  PRIMARY_FG,
  ACCENT,
  ACCENT_600,
] as const;

// Foreground candidates for text sitting on a themed colour: near-white or the
// app's warm-dark ink. applyTheme picks whichever reads better.
const LIGHT_FG = "#ffffff";
const DARK_FG = "#211f1f";

// How far to darken a colour for its hover / -600 shade.
const HOVER_DARKEN = 0.12;
const ACCENT_DARKEN = 0.16;

// Link is a themeable role too: when a tenant sets a brand accent, the app's
// links pick it up (instead of staying the default Yivi blue) so the brand
// carries across the whole UI, not just buttons and avatars. A link must stay
// legible on every surface it sits on, and those surfaces differ between light
// and dark mode, so no single value clears WCAG-AA in both — the accent seed is
// darkened for light mode and lightened for dark mode until each clears the AA
// floor against the worst-case background it lands on. applyOrgLinkTheme ships
// the pair as a mode-aware <style> (an inline custom property can't carry a
// media query).

// The backgrounds a `text-link` element renders on, per mode: the surface tiers
// plus the highlight/info chip. Values mirror the light and dark
// --yb-surface*/--yb-highlight tokens in index.css. The derived link must clear
// AA against every one, so we gate on the worst case (the darkest light bg / the
// lightest dark bg — clearing AA there clears it on all the others in that mode).
const LINK_BACKGROUNDS_LIGHT = [
  "#ffffff",
  "#faf8f6",
  "#f5f2ef",
  "#eaf3f9",
] as const;
const LINK_BACKGROUNDS_DARK = [
  "#1d1b1a",
  "#141312",
  "#252322",
  "#1c3648",
] as const;

// Step size and iteration cap when nudging the accent toward the contrast floor.
const LINK_ADJUST_STEP = 0.06;
const LINK_ADJUST_MAX_STEPS = 24;

// The <style> element carrying the mode-aware link override.
const ORG_LINK_STYLE_ID = "ybw-org-link-theme";

// WCAG 2.2 AA needs 4.5:1 for normal-size text; buttons use ~13.5px semibold,
// which is "normal" text, so this is the bar a primary colour must clear.
export const AA_CONTRAST = 4.5;

// The default token values (from index.css :root), shown as the placeholder /
// picker fallback when a field is unset.
export const DEFAULT_PRIMARY = "#484747";
export const DEFAULT_ACCENT = "#ba3354";

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

// resolveThemeTokens turns a saved theme into the map of design-token overrides
// to apply on the documentElement: the brand seed colours plus the hover/shade
// and readable-foreground variants derived from them. A missing or malformed
// seed is simply left out (so it falls back to the :root default), so the
// returned map only ever holds valid overrides. This is the single source of
// truth for both the runtime apply and the pre-paint cache (see index.html),
// so the cached palette and the applied palette can never drift apart.
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

  return tokens;
}

// extremeBackground returns the background from the list with the highest or
// lowest relative luminance — the worst case to gate a link's contrast on (see
// resolveLinkTheme).
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
  for (let i = 0; i < LINK_ADJUST_MAX_STEPS; i++) {
    if ((contrastRatio(current, background) ?? 0) >= AA_CONTRAST) {
      break;
    }
    current =
      direction === "darken"
        ? darken(current, LINK_ADJUST_STEP)
        : lighten(current, LINK_ADJUST_STEP);
  }
  return current;
}

// The light and dark link colours derived from a theme's accent seed.
export interface LinkTheme {
  light: string;
  dark: string;
}

// resolveLinkTheme derives the mode-aware link colour from the accent seed:
// darkened until it clears AA on the darkest light-mode background, and
// lightened until it clears AA on the lightest dark-mode background. Returns
// null when there is no valid accent (the default Yivi link then stands).
export function resolveLinkTheme(
  theme: OrgTheme | null | undefined,
): LinkTheme | null {
  const accent = theme?.accentColor ?? "";
  if (!parseHex(accent)) {
    return null;
  }
  return {
    light: adjustToContrast(
      accent,
      extremeBackground(LINK_BACKGROUNDS_LIGHT, "darkest"),
      "darken",
    ),
    dark: adjustToContrast(
      accent,
      extremeBackground(LINK_BACKGROUNDS_DARK, "lightest"),
      "lighten",
    ),
  };
}

// applyOrgLinkTheme installs (or removes) the mode-aware link override as a
// <style> element. A media query can't live in an inline style property, so the
// light value goes on :root and the dark value under prefers-color-scheme;
// appended after index.css it wins by source order in both modes.
function applyOrgLinkTheme(theme: OrgTheme | null | undefined): void {
  const link = resolveLinkTheme(theme);
  const existing = document.getElementById(ORG_LINK_STYLE_ID);
  if (!link) {
    existing?.remove();
    return;
  }
  const css =
    `:root{${LINK}:${link.light}}` +
    `@media (prefers-color-scheme: dark){:root{${LINK}:${link.dark}}}`;
  const style =
    existing instanceof HTMLStyleElement
      ? existing
      : document.createElement("style");
  style.id = ORG_LINK_STYLE_ID;
  style.textContent = css;
  if (!style.isConnected) {
    document.head.appendChild(style);
  }
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

// applyOrgTheme maps a saved theme onto the design tokens on the documentElement.
// Missing/invalid fields are cleared so they fall back to the default look.
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
  applyOrgLinkTheme(theme);
}

// clearOrgTheme restores every token to its default (used when leaving an org).
export function clearOrgTheme(): void {
  const root = document.documentElement.style;
  for (const property of THEMED_PROPERTIES) {
    root.removeProperty(property);
  }
  document.getElementById(ORG_LINK_STYLE_ID)?.remove();
}

// Cached theme, keyed by org slug, so a full page reload can paint the tenant's
// colours before React hydrates (see the inline script in index.html). Without
// it the default palette paints first and then applyOrgTheme swaps it in — a
// visible flash of the wrong branding (FOUC).
export const THEME_CACHE_PREFIX = "ybw.theme.";

function themeCacheKey(slug: string): string {
  return THEME_CACHE_PREFIX + slug;
}

// cacheOrgTheme stores the resolved token overrides for an org so the next full
// load can apply them before first paint. An empty map (the org uses the
// default look) is written too, so clearing a theme also clears the stale cache.
export function cacheOrgTheme(
  slug: string,
  theme: OrgTheme | null | undefined,
): void {
  if (!slug) {
    return;
  }
  try {
    const tokens = resolveThemeTokens(theme);
    window.localStorage.setItem(themeCacheKey(slug), JSON.stringify(tokens));
  } catch {
    // Storage can be unavailable (private mode, quota). The runtime apply still
    // themes the app; only the pre-paint optimisation is lost, so this is
    // non-fatal.
  }
}
