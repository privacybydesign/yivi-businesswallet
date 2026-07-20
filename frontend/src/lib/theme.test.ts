import { describe, expect, it } from "vitest";
import { resolveThemeTokens, shouldApplyOrgTheme } from "./theme";
import type { OrgTheme } from "../api/theme";

// resolveThemeTokens is the single source of truth for both the runtime apply
// (applyOrgTheme) and the pre-paint cache read by index.html. If its output
// drifts, a full reload would flash a palette that differs from the one React
// applies — the exact FOUC the cache exists to prevent — so pin the derivation.

function theme(overrides: Partial<OrgTheme>): OrgTheme {
  return {
    configured: true,
    primaryColor: "",
    accentColor: "",
    logoUri: "",
    ...overrides,
  };
}

describe("resolveThemeTokens", () => {
  it("returns no overrides for an unset theme", () => {
    expect(resolveThemeTokens(theme({}))).toEqual({});
    expect(resolveThemeTokens(null)).toEqual({});
    expect(resolveThemeTokens(undefined)).toEqual({});
  });

  it("derives the hover shade and readable foreground from the primary seed", () => {
    // #1d4e89 is dark, so white reads best on it; the hover shade is the seed
    // darkened by 12%.
    expect(resolveThemeTokens(theme({ primaryColor: "#1d4e89" }))).toEqual({
      "--yb-primary": "#1d4e89",
      "--yb-primary-hover": "#1a4579",
      "--yb-primary-fg": "#ffffff",
    });
  });

  it("picks a dark foreground for a light primary seed", () => {
    expect(
      resolveThemeTokens(theme({ primaryColor: "#f2c94c" }))["--yb-primary-fg"],
    ).toBe("#211f1f");
  });

  it("derives the -600 shade from the accent seed", () => {
    expect(resolveThemeTokens(theme({ accentColor: "#ba3354" }))).toEqual({
      "--yb-brand": "#ba3354",
      "--yb-brand-600": "#9c2b47",
    });
  });

  it("emits both seed groups together", () => {
    const tokens = resolveThemeTokens(
      theme({ primaryColor: "#1d4e89", accentColor: "#ba3354" }),
    );
    expect(Object.keys(tokens).sort()).toEqual([
      "--yb-brand",
      "--yb-brand-600",
      "--yb-primary",
      "--yb-primary-fg",
      "--yb-primary-hover",
    ]);
  });

  it("omits a malformed seed rather than emitting a broken token", () => {
    expect(resolveThemeTokens(theme({ primaryColor: "not-a-hex" }))).toEqual(
      {},
    );
    expect(
      resolveThemeTokens(theme({ primaryColor: "#fff" })), // 3-digit shorthand is not accepted
    ).toEqual({});
  });
});

// The runtime theme effect (routes/root.tsx) must skip applying while the theme
// query is still loading: its data is `undefined` in flight, and applying then
// would clear the tokens the inline pre-paint script (index.html) already set,
// flashing the default palette back in before the fetch resolves (the FOUC).
describe("shouldApplyOrgTheme", () => {
  it("does not apply while the theme query is loading", () => {
    expect(shouldApplyOrgTheme(undefined)).toBe(false);
  });

  it("applies once real theme data has arrived", () => {
    expect(shouldApplyOrgTheme(theme({ primaryColor: "#1d4e89" }))).toBe(true);
    // An org with no theme resolves to a value (not undefined) and legitimately
    // resets to the default look.
    expect(shouldApplyOrgTheme(theme({}))).toBe(true);
    expect(shouldApplyOrgTheme(null)).toBe(true);
  });
});
