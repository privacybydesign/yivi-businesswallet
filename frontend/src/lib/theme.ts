import type { OrgTheme } from "../api/theme";

// Runtime theming. The app's colours resolve through the --yb-* custom
// properties on :root (see index.css); overriding those re-colours every
// Tailwind utility that maps to them. Clearing an override falls back to the
// :root default, so an unset field keeps the default look.
//
// Two mechanisms, chosen by whether a token is mode-safe:
//   * Brand fills (primary, accent) are self-contained coloured fills with their
//     own readable foreground, so a single value is correct in both light and
//     dark mode. They are applied INLINE on the documentElement (resolveThemeTokens
//     → applyOrgTheme), where they win over both the light and dark :root defaults.
//   * Neutral/semantic roles (surface, border, text, link, status) are
//     mode-specific — a tenant's light surface must not be forced into dark mode —
//     so each derives a light value AND a dark value, shipped as a single
//     <style id="ybw-org-theme"> block whose dark half lives under a
//     `prefers-color-scheme: dark` media query (an inline custom property can't
//     carry a media query). The block uses a doubled `:root:root` selector so it
//     wins over index.css by SPECIFICITY, not source order — which lets the inline
//     pre-paint script (index.html) inject it before Vite's CSS without a
//     source-order race, so a full reload never flashes the default palette (the
//     FOUC this all guards against).

// --- Mode-safe brand fills (applied inline on the documentElement) ---
const PRIMARY = "--yb-primary";
const PRIMARY_HOVER = "--yb-primary-hover";
const PRIMARY_FG = "--yb-primary-fg";
const ACCENT = "--yb-brand";
const ACCENT_600 = "--yb-brand-600";

const THEMED_PROPERTIES = [
  PRIMARY,
  PRIMARY_HOVER,
  PRIMARY_FG,
  ACCENT,
  ACCENT_600,
] as const;

// --- Mode-aware roles (shipped via the <style> block) ---
const LINK = "--yb-link";
const INK = "--yb-ink";
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

// The <style> element carrying the mode-aware overrides.
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

// The default token values (from index.css :root), shown as the placeholder /
// picker fallback when a field is unset. Keyed by the theme colour field so the
// settings UI can look each up.
export const DEFAULT_PRIMARY = "#484747";
export const DEFAULT_ACCENT = "#ba3354";
export const COLOR_FIELD_DEFAULTS = {
  primaryColor: DEFAULT_PRIMARY,
  accentColor: DEFAULT_ACCENT,
  textColor: "#484747",
  surfaceColor: "#faf8f6",
  borderColor: "#eae5e2",
  linkColor: "#1d4e89",
  successColor: "#00973a",
  warningColor: "#eba73b",
  errorColor: "#bd1919",
} as const;

// --- Default neutral anchors, mirrored from index.css ---
// The mode-aware derivations gate contrast against, and tint from, these
// baseline values so the derived palette stays coherent with the design system.
const LIGHT = {
  surface: "#ffffff",
  surface2: "#faf8f6",
  surface3: "#f5f2ef",
  line: "#eae5e2",
  lineStrong: "#d7d2cd",
  highlight: "#eaf3f9",
} as const;
const DARK = {
  surface: "#1d1b1a",
  surface2: "#141312",
  surface3: "#252322",
  line: "#2b2928",
  lineStrong: "#3a3736",
  highlight: "#1c3648",
} as const;

// How strongly a seed tints each neutral. Surfaces are backgrounds behind body
// text, so their tint is kept subtle to preserve the text contrast the default
// neutrals were designed for; borders are decorative (no text), so they take a
// stronger tint. Status backgrounds are pale/dark chips.
const SURFACE_TINT = 0.08;
const BORDER_TINT = 0.28;
const STATUS_BG_TINT_LIGHT = 0.14;
const STATUS_BG_TINT_DARK = 0.16;

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
// overrides (the mode-safe brand fills) to apply on the documentElement: the
// primary/accent seeds plus the hover/shade and readable-foreground variants
// derived from them. A missing or malformed seed is simply left out (so it falls
// back to the :root default), so the returned map only ever holds valid
// overrides. This is the single source of truth for both the runtime apply and
// the pre-paint cache (see index.html), so the cached palette and the applied
// palette can never drift apart. The mode-aware roles are handled separately by
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

// The light and dark link colours derived from a theme's link/accent seed.
export interface LinkTheme {
  light: string;
  dark: string;
}

// The surface tiers a `text-link` element renders on, per mode: the seed-tinted
// surfaces plus the fixed highlight/info chip. The derived link must clear AA
// against every one, so we gate on the worst case (the darkest light bg / the
// lightest dark bg — clearing AA there clears it on all the others in that mode).
function linkBackgrounds(theme: OrgTheme | null | undefined): {
  light: string[];
  dark: string[];
} {
  const surfaces = surfaceTiers(theme);
  return {
    light: [...Object.values(surfaces.light), LIGHT.highlight],
    dark: [...Object.values(surfaces.dark), DARK.highlight],
  };
}

// resolveLinkTheme derives the mode-aware link colour from the link seed
// (falling back to the accent seed, so a brand accent still tints links even
// when no explicit link colour is set): darkened until it clears AA on the
// darkest light-mode background, and lightened until it clears AA on the
// lightest dark-mode background. Returns null when there is no valid seed (the
// default Yivi link then stands).
export function resolveLinkTheme(
  theme: OrgTheme | null | undefined,
): LinkTheme | null {
  const seed =
    theme?.linkColor && parseHex(theme.linkColor)
      ? theme.linkColor
      : (theme?.accentColor ?? "");
  if (!parseHex(seed)) {
    return null;
  }
  const backgrounds = linkBackgrounds(theme);
  return {
    light: adjustToContrast(
      seed,
      extremeBackground(backgrounds.light, "darkest"),
      "darken",
    ),
    dark: adjustToContrast(
      seed,
      extremeBackground(backgrounds.dark, "lightest"),
      "lighten",
    ),
  };
}

// surfaceTiers derives the light and dark surface tiers from the surface seed
// (each default neutral tinted subtly toward it), or the default neutrals when
// no seed is set. The subtle tint keeps the surfaces close to the AA-verified
// defaults so body text stays readable on them.
function surfaceTiers(theme: OrgTheme | null | undefined): {
  light: { surface: string; surface2: string; surface3: string };
  dark: { surface: string; surface2: string; surface3: string };
} {
  const seed = theme?.surfaceColor ?? "";
  if (!parseHex(seed)) {
    return {
      light: {
        surface: LIGHT.surface,
        surface2: LIGHT.surface2,
        surface3: LIGHT.surface3,
      },
      dark: {
        surface: DARK.surface,
        surface2: DARK.surface2,
        surface3: DARK.surface3,
      },
    };
  }
  return {
    light: {
      surface: mix(LIGHT.surface, seed, SURFACE_TINT),
      surface2: mix(LIGHT.surface2, seed, SURFACE_TINT),
      surface3: mix(LIGHT.surface3, seed, SURFACE_TINT),
    },
    dark: {
      surface: mix(DARK.surface, seed, SURFACE_TINT),
      surface2: mix(DARK.surface2, seed, SURFACE_TINT),
      surface3: mix(DARK.surface3, seed, SURFACE_TINT),
    },
  };
}

// A single mode-aware token: its value in light mode and in dark mode.
interface ModeValue {
  light: string;
  dark: string;
}

// statusPair derives a status role from its seed: a chip background (the seed
// mixed toward the mode's base, pale in light / dark in dark mode) and a solid
// foreground colour nudged to clear AA against that chip. Both modes are
// AA-safe by construction (the solid is forced past the 4.5:1 floor).
function statusPair(seed: string): { light: ModeValue; dark: ModeValue } {
  const lightBg = mix(LIGHT.surface, seed, STATUS_BG_TINT_LIGHT);
  const darkBg = mix(DARK.surface2, seed, STATUS_BG_TINT_DARK);
  return {
    light: {
      light: adjustToContrast(seed, lightBg, "darken"),
      dark: lightBg,
    },
    dark: {
      light: adjustToContrast(seed, darkBg, "lighten"),
      dark: darkBg,
    },
  };
}

// resolveThemeCss derives the map of mode-aware token overrides (surface,
// border, text, link, status) from a saved theme, as { token: {light, dark} }.
// Only roles whose seed is set (or, for the link, whose seed or accent is set)
// appear. This is the single source of truth for both the runtime <style> block
// (applyOrgTheme) and the pre-paint cache (index.html), so they cannot drift.
export function resolveThemeCss(
  theme: OrgTheme | null | undefined,
): Record<string, ModeValue> {
  const out: Record<string, ModeValue> = {};

  // Surfaces (subtle brand tint of the neutral tiers).
  if (parseHex(theme?.surfaceColor ?? "")) {
    const s = surfaceTiers(theme);
    out[SURFACE] = { light: s.light.surface, dark: s.dark.surface };
    out[SURFACE_2] = { light: s.light.surface2, dark: s.dark.surface2 };
    out[SURFACE_3] = { light: s.light.surface3, dark: s.dark.surface3 };
  }

  // Borders (decorative — no text — so a stronger tint is fine).
  const border = theme?.borderColor ?? "";
  if (parseHex(border)) {
    out[LINE] = {
      light: mix(LIGHT.line, border, BORDER_TINT),
      dark: mix(DARK.line, border, BORDER_TINT),
    };
    out[LINE_STRONG] = {
      light: mix(LIGHT.lineStrong, border, BORDER_TINT),
      dark: mix(DARK.lineStrong, border, BORDER_TINT),
    };
  }

  // Body text/ink: gated to AA against the (possibly tinted) worst-case surface
  // in each mode — the darkest light surface for dark ink, the lightest dark
  // surface for light ink.
  const text = theme?.textColor ?? "";
  if (parseHex(text)) {
    const surfaces = surfaceTiers(theme);
    out[INK] = {
      light: adjustToContrast(
        text,
        extremeBackground(Object.values(surfaces.light), "darkest"),
        "darken",
      ),
      dark: adjustToContrast(
        text,
        extremeBackground(Object.values(surfaces.dark), "lightest"),
        "lighten",
      ),
    };
  }

  // Link (seed = link colour, else accent).
  const link = resolveLinkTheme(theme);
  if (link) {
    out[LINK] = { light: link.light, dark: link.dark };
  }

  // Semantic status roles.
  const success = theme?.successColor ?? "";
  if (parseHex(success)) {
    const p = statusPair(success);
    out[SUCCESS] = { light: p.light.light, dark: p.dark.light };
    out[SUCCESS_BG] = { light: p.light.dark, dark: p.dark.dark };
  }
  const warning = theme?.warningColor ?? "";
  if (parseHex(warning)) {
    const p = statusPair(warning);
    out[WARNING] = { light: p.light.light, dark: p.dark.light };
    out[WARNING_FG] = { light: p.light.light, dark: p.dark.light };
    out[WARNING_BG] = { light: p.light.dark, dark: p.dark.dark };
  }
  const error = theme?.errorColor ?? "";
  if (parseHex(error)) {
    const p = statusPair(error);
    out[ERROR] = { light: p.light.light, dark: p.dark.light };
    out[ERROR_BG] = { light: p.light.dark, dark: p.dark.dark };
  }

  return out;
}

// buildThemeCss serialises the mode-aware token map into a stylesheet. The
// doubled `:root:root` selector wins over index.css by specificity (not source
// order), so the pre-paint injection in index.html has no ordering race against
// Vite's CSS. Returns "" when there is nothing to override.
export function buildThemeCss(theme: OrgTheme | null | undefined): string {
  const tokens = resolveThemeCss(theme);
  const names = Object.keys(tokens);
  if (names.length === 0) {
    return "";
  }
  const light = names.map((n) => `${n}:${tokens[n].light}`).join(";");
  const dark = names.map((n) => `${n}:${tokens[n].dark}`).join(";");
  return (
    `:root:root{${light}}` +
    `@media (prefers-color-scheme: dark){:root:root{${dark}}}`
  );
}

// setThemeStyle installs (or clears) the mode-aware <style> block.
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

// applyOrgTheme maps a saved theme onto the design tokens: the mode-safe brand
// fills inline on the documentElement, the mode-aware roles as the <style>
// block. Missing/invalid fields are cleared so they fall back to the default
// look.
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
}

// clearOrgTheme restores every token to its default (used when leaving an org).
export function clearOrgTheme(): void {
  const root = document.documentElement.style;
  for (const property of THEMED_PROPERTIES) {
    root.removeProperty(property);
  }
  setThemeStyle("");
}

// Cached theme, keyed by org slug, so a full page reload can paint the tenant's
// colours before React hydrates (see the inline script in index.html). Without
// it the default palette paints first and then applyOrgTheme swaps it in — a
// visible flash of the wrong branding (FOUC).
export const THEME_CACHE_PREFIX = "ybw.theme.";

function themeCacheKey(slug: string): string {
  return THEME_CACHE_PREFIX + slug;
}

// The cached shape: the inline brand-fill overrides and the mode-aware CSS
// string. Kept in step with the reader in index.html.
interface CachedTheme {
  inline: Record<string, string>;
  css: string;
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
    return true;
  } catch {
    return false;
  }
}
