import { useSyncExternalStore } from "react";

// Light or dark mode for the whole app. The user picks Auto (the OS
// appearance), Light or Dark with the switch in the top bar; the choice is kept
// in this browser. A white-labelled org (and a hosted customer page) is always
// light: its theme is the brand's own colours, picked for light pages, so the
// switch is hidden there. The resolved scheme is set as data-color-scheme on the
// documentElement, which index.css keys on. The inline pre-paint script in
// index.html resolves it the same way before first paint — keep the two in
// step.

const SYSTEM_PREFERENCE = "system";

export const COLOR_SCHEME_PREFERENCES = [
  SYSTEM_PREFERENCE,
  "light",
  "dark",
] as const;

export type ColorSchemePreference = (typeof COLOR_SCHEME_PREFERENCES)[number];

export type ColorScheme = "light" | "dark";

// The OS appearance, which Auto follows.
export type SystemAppearance = "os-dark" | "os-light";

// The attribute that switches the app to the white-label design (see the
// [data-design="white-label"] blocks in index.css): set while an org with a
// theme of its own, or a hosted customer page, is shown.
export const DESIGN_ATTRIBUTE = "data-design";
export const DEFAULT_DESIGN = "default";
export const WHITE_LABEL_DESIGN = "white-label";

export type Design = typeof DEFAULT_DESIGN | typeof WHITE_LABEL_DESIGN;

export const COLOR_SCHEME_STORAGE_KEY = "bw.colorScheme";

const COLOR_SCHEME_ATTRIBUTE = "data-color-scheme";
const SYSTEM_DARK_QUERY = "(prefers-color-scheme: dark)";

function isColorSchemePreference(
  value: string | null,
): value is ColorSchemePreference {
  return (
    value !== null &&
    (COLOR_SCHEME_PREFERENCES as readonly string[]).includes(value)
  );
}

// resolveColorScheme picks the scheme to show: the white-label design is always
// light; otherwise an explicit choice wins and Auto follows the OS.
export function resolveColorScheme(
  preference: ColorSchemePreference,
  appearance: SystemAppearance,
  design: Design,
): ColorScheme {
  if (design === WHITE_LABEL_DESIGN) {
    return "light";
  }
  if (preference !== SYSTEM_PREFERENCE) {
    return preference;
  }
  return appearance === "os-dark" ? "dark" : "light";
}

function readStoredPreference(): ColorSchemePreference {
  try {
    const stored = window.localStorage.getItem(COLOR_SCHEME_STORAGE_KEY);
    return isColorSchemePreference(stored) ? stored : SYSTEM_PREFERENCE;
  } catch {
    // Storage can be unavailable (private mode); Auto is the default anyway.
    return SYSTEM_PREFERENCE;
  }
}

// readInitialDesign starts from the design the pre-paint script in index.html
// already set, so the first apply does not undo its light scheme.
function readInitialDesign(): Design {
  return typeof document !== "undefined" &&
    document.documentElement.getAttribute(DESIGN_ATTRIBUTE) ===
      WHITE_LABEL_DESIGN
    ? WHITE_LABEL_DESIGN
    : DEFAULT_DESIGN;
}

let preference: ColorSchemePreference = readStoredPreference();
let design: Design = readInitialDesign();
const listeners = new Set<() => void>();

function systemAppearance(): SystemAppearance {
  return window.matchMedia(SYSTEM_DARK_QUERY).matches ? "os-dark" : "os-light";
}

function applyColorScheme(): void {
  document.documentElement.setAttribute(
    COLOR_SCHEME_ATTRIBUTE,
    resolveColorScheme(preference, systemAppearance(), design),
  );
}

function notify(): void {
  for (const listener of listeners) {
    listener();
  }
}

export function setColorSchemePreference(next: ColorSchemePreference): void {
  preference = next;
  try {
    window.localStorage.setItem(COLOR_SCHEME_STORAGE_KEY, next);
  } catch {
    // Not kept across reloads then; the switch still applies for this visit.
  }
  applyColorScheme();
  notify();
}

// setWhiteLabelScheme keeps the scheme light while the white-label design is
// shown (lib/theme.ts switches it), and hands it back to the user's choice when
// it is not. The stored choice itself is kept.
export function setWhiteLabelScheme(next: Design): void {
  if (design === next) {
    return;
  }
  design = next;
  applyColorScheme();
  notify();
}

// watchSystemColorScheme applies the scheme now and again whenever the OS
// appearance changes, so Auto follows it live.
export function watchSystemColorScheme(): void {
  applyColorScheme();
  window.matchMedia(SYSTEM_DARK_QUERY).addEventListener("change", () => {
    applyColorScheme();
  });
}

function subscribe(listener: () => void): () => void {
  listeners.add(listener);
  return () => listeners.delete(listener);
}

export function useColorSchemePreference(): ColorSchemePreference {
  return useSyncExternalStore(subscribe, () => preference);
}

// useColorSchemeLocked reports whether the white-label design holds the scheme
// at light, so the switch has nothing to offer.
export function useColorSchemeLocked(): boolean {
  return useSyncExternalStore(subscribe, () => design === WHITE_LABEL_DESIGN);
}
