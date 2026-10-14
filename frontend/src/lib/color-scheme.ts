import { useSyncExternalStore } from "react";

// Light or dark mode for the whole app. The user picks Auto (the OS
// appearance), Light or Dark with the switch in the top bar; the choice is kept
// in this browser. A white-labelled org (and a hosted customer page) is always
// light: its theme is the brand's own colours, picked for light pages, so the
// switch is hidden there. The resolved scheme is set as data-color-scheme on the
// documentElement, which index.css keys on. The inline pre-paint script in
// index.html resolves it the same way before first paint — keep the two in
// step.

export const COLOR_SCHEME_PREFERENCES = ["system", "light", "dark"] as const;

export type ColorSchemePreference = (typeof COLOR_SCHEME_PREFERENCES)[number];

export type ColorScheme = "light" | "dark";

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
  systemDark: boolean,
  whiteLabel: boolean,
): ColorScheme {
  if (whiteLabel) {
    return "light";
  }
  if (preference !== "system") {
    return preference;
  }
  return systemDark ? "dark" : "light";
}

function readStoredPreference(): ColorSchemePreference {
  try {
    const stored = window.localStorage.getItem(COLOR_SCHEME_STORAGE_KEY);
    return isColorSchemePreference(stored) ? stored : "system";
  } catch {
    // Storage can be unavailable (private mode); Auto is the default anyway.
    return "system";
  }
}

let preference: ColorSchemePreference = readStoredPreference();
let whiteLabel = false;
const listeners = new Set<() => void>();

function systemDark(): boolean {
  return window.matchMedia(SYSTEM_DARK_QUERY).matches;
}

function applyColorScheme(): void {
  document.documentElement.setAttribute(
    COLOR_SCHEME_ATTRIBUTE,
    resolveColorScheme(preference, systemDark(), whiteLabel),
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
export function setWhiteLabelScheme(on: boolean): void {
  if (whiteLabel === on) {
    return;
  }
  whiteLabel = on;
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
  return useSyncExternalStore(subscribe, () => whiteLabel);
}
