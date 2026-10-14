import { describe, expect, it } from "vitest";
import {
  AA_CONTRAST,
  buildThemeCss,
  contrastRatio,
  resolveLinkColor,
  resolveThemeCss,
  resolveThemeTokens,
  shouldApplyOrgTheme,
  usesWhiteLabelDesign,
} from "./theme";
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
    textColor: "",
    surfaceColor: "",
    borderColor: "",
    linkColor: "",
    successColor: "",
    warningColor: "",
    errorColor: "",
    sidebarColor: "",
    topbarColor: "",
    fontFamily: "",
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

// resolveLinkColor derives the link colour from the accent seed. The link must
// stay legible on every surface it renders on, so it must clear WCAG-AA against
// the worst-case background (and therefore against all of them). This list
// mirrors the --yb-surface*/--yb-highlight tokens in index.css.
const LINK_BACKGROUNDS = ["#ffffff", "#faf8f6", "#f5f2ef", "#eaf3f9"];

describe("resolveLinkColor", () => {
  it("returns null when the accent seed is missing or malformed", () => {
    expect(resolveLinkColor(theme({}))).toBeNull();
    expect(resolveLinkColor(theme({ accentColor: "not-a-hex" }))).toBeNull();
    expect(resolveLinkColor(theme({ accentColor: "#fff" }))).toBeNull();
    expect(resolveLinkColor(null)).toBeNull();
    expect(resolveLinkColor(undefined)).toBeNull();
  });

  // A few accents spanning the lightness range: a mid-tone brand, a very light
  // seed (must darken hard) and a very dark one.
  const ACCENTS = ["#ba3354", "#f2c94c", "#1d4e89", "#0a0a0a", "#f5f5f5"];

  it.each(ACCENTS)(
    "derives a link that clears AA on every surface (%s)",
    (accent) => {
      const link = resolveLinkColor(theme({ accentColor: accent }));
      expect(link).not.toBeNull();
      for (const bg of LINK_BACKGROUNDS) {
        expect(contrastRatio(link!, bg)!).toBeGreaterThanOrEqual(AA_CONTRAST);
      }
    },
  );

  it("leaves an accent that already clears the floor unchanged", () => {
    // #ba3354 clears AA on the darkest surface as-is, so the link is the seed
    // itself (no needless darkening).
    expect(resolveLinkColor(theme({ accentColor: "#ba3354" }))).toBe("#ba3354");
  });

  it("prefers an explicit link seed over the accent", () => {
    const fromAccent = resolveLinkColor(theme({ accentColor: "#1d4e89" }));
    const fromLink = resolveLinkColor(
      theme({ accentColor: "#1d4e89", linkColor: "#0a7d3a" }),
    );
    expect(fromLink).not.toEqual(fromAccent);
  });
});

// resolveThemeCss derives the palette (surface/border/text/link/status) from
// the extra seeds. Every derived text/status pair must clear WCAG-AA, since the
// whole point of deriving (rather than storing) these is to guarantee
// legibility on the surfaces they land on. If the derivation drifts below the
// floor, a tenant could ship an unreadable theme — so pin it here for a spread
// of seeds.
describe("resolveThemeCss", () => {
  const SEEDS = ["#ba3354", "#1d4e89", "#00973a", "#eba73b", "#0a0a0a"];

  it("returns nothing for an unset theme", () => {
    expect(resolveThemeCss(theme({}))).toEqual({});
    expect(buildThemeCss(theme({}))).toBe("");
    expect(buildThemeCss(null)).toBe("");
  });

  it("emits the three surface tiers when a surface seed is set", () => {
    const css = resolveThemeCss(theme({ surfaceColor: "#1d4e89" }));
    expect(Object.keys(css).sort()).toEqual([
      "--yb-surface",
      "--yb-surface-2",
      "--yb-surface-3",
    ]);
  });

  it("uses a light surface and a border seed as given", () => {
    const css = resolveThemeCss(
      theme({ surfaceColor: "#fbfbfc", borderColor: "#eaebee" }),
    );
    expect(css["--yb-surface-2"]).toBe("#fbfbfc");
    expect(css["--yb-line"]).toBe("#eaebee");
  });

  it("only tints the page with a surface seed text can't be read on", () => {
    const css = resolveThemeCss(theme({ surfaceColor: "#1d4e89" }));
    expect(css["--yb-surface-2"]).not.toBe("#1d4e89");
  });

  it.each(SEEDS)(
    "derives AA-legible secondary text from the text seed (%s)",
    (seed) => {
      const css = resolveThemeCss(
        theme({ textColor: seed, surfaceColor: "#fbfbfc" }),
      );
      expect(
        contrastRatio(css["--yb-ink-soft"], css["--yb-surface-3"])!,
      ).toBeGreaterThanOrEqual(AA_CONTRAST);
    },
  );

  it.each(SEEDS)("derives AA-legible ink (%s)", (seed) => {
    // Ink is gated against the worst-case surface tier; when no surface seed
    // is set that is the design default (darkest surface #f5f5f6).
    const css = resolveThemeCss(theme({ textColor: seed }));
    expect(contrastRatio(css["--yb-ink"], "#f5f5f6")!).toBeGreaterThanOrEqual(
      AA_CONTRAST,
    );
  });

  it.each(SEEDS)(
    "derives status colours that clear AA on their chip (%s)",
    (seed) => {
      const css = resolveThemeCss(theme({ successColor: seed }));
      expect(
        contrastRatio(css["--yb-success"], css["--yb-success-bg"])!,
      ).toBeGreaterThanOrEqual(AA_CONTRAST);
    },
  );

  // Regression: text-error/-success/-warning render the status SOLID bare on the
  // surface tiers (--yb-surface etc.), not only on its chip. A surface seed tints
  // those tiers, so a solid cleared only against its chip could fall below AA on
  // a darker surface. Pin the solid at AA against every tinted surface tier AND
  // its chip, with a surface seed set.
  const SURFACE_SEEDS = ["#ffffff", "#1d4e89", "#ba3354", "#0a0a0a"];
  const STATUS_ROLES = [
    ["--yb-success", "--yb-success-bg"],
    ["--yb-warning", "--yb-warning-bg"],
    ["--yb-error", "--yb-error-bg"],
  ] as const;

  it.each(SURFACE_SEEDS)(
    "keeps status solids AA on the tinted surfaces they sit on (surface %s)",
    (surfaceColor) => {
      const css = resolveThemeCss(
        theme({
          surfaceColor,
          successColor: "#1a7f37",
          warningColor: "#b06f00",
          errorColor: "#bd1919",
        }),
      );
      const surfaces = [
        css["--yb-surface"],
        css["--yb-surface-2"],
        css["--yb-surface-3"],
      ];
      for (const [solidKey, bgKey] of STATUS_ROLES) {
        for (const bg of [css[bgKey], ...surfaces]) {
          expect(contrastRatio(css[solidKey], bg)!).toBeGreaterThanOrEqual(
            AA_CONTRAST,
          );
        }
      }
    },
  );

  it("gives warning a foreground alongside its chip background", () => {
    const css = resolveThemeCss(theme({ warningColor: "#eba73b" }));
    expect(Object.keys(css).sort()).toEqual([
      "--yb-warning",
      "--yb-warning-bg",
      "--yb-warning-fg",
    ]);
    expect(
      contrastRatio(css["--yb-warning-fg"], css["--yb-warning-bg"])!,
    ).toBeGreaterThanOrEqual(AA_CONTRAST);
  });
});

// buildThemeCss serialises the map into a stylesheet the pre-paint script
// (index.html) and the runtime apply both inject. The doubled `:root:root`
// selector must win over index.css by specificity; a theme is always shown
// light, so there is no dark half.
describe("buildThemeCss", () => {
  it("derives a light brand's page and text from its seeds", () => {
    const cm = theme({
      surfaceColor: "#fbfbfc",
      textColor: "#101e1e",
      borderColor: "#eaebee",
    });
    const css = buildThemeCss(cm);
    expect(css).toContain("--yb-surface-2:#fbfbfc");
  });

  it("emits one :root:root block and no dark values", () => {
    const css = buildThemeCss(theme({ successColor: "#00973a" }));
    expect(css.startsWith(":root:root{")).toBe(true);
    expect(css).toContain("--yb-success:");
    expect(css).not.toContain("data-color-scheme");
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

// Navigation chrome (sidebar / top bar) is a brand fill applied inline
// via resolveThemeTokens. Its foreground must clear WCAG-AA on the chrome
// background for EVERY seed — including mid-tones where a plain light/dark pick
// would dip below the floor — since nav labels are normal-size text and the
// chrome background isn't gated at save time. Also pins the derived companions.
describe("resolveThemeTokens navigation chrome", () => {
  // A spread including mid-tones (#808080, #6b7f9e) that are the hard cases for
  // AA, plus a light and a dark seed.
  const CHROME_SEEDS = ["#1a2b4c", "#808080", "#6b7f9e", "#f2f2f2", "#0a0a0a"];

  it("emits no chrome tokens when the seeds are unset", () => {
    const tokens = resolveThemeTokens(theme({}));
    for (const name of [
      "--yb-sidebar",
      "--yb-sidebar-fg",
      "--yb-topbar",
      "--yb-topbar-fg",
    ]) {
      expect(tokens[name]).toBeUndefined();
    }
  });

  it.each(CHROME_SEEDS)(
    "derives an AA-legible sidebar foreground (%s)",
    (seed) => {
      const tokens = resolveThemeTokens(theme({ sidebarColor: seed }));
      expect(tokens["--yb-sidebar"]).toBe(seed);
      expect(Object.keys(tokens).sort()).toEqual([
        "--yb-sidebar",
        "--yb-sidebar-active",
        "--yb-sidebar-fg",
        "--yb-sidebar-fg-soft",
        "--yb-sidebar-line",
      ]);
      expect(
        contrastRatio(tokens["--yb-sidebar-fg"], seed)!,
      ).toBeGreaterThanOrEqual(AA_CONTRAST);
    },
  );

  it.each(CHROME_SEEDS)(
    "derives an AA-legible top-bar foreground (%s)",
    (seed) => {
      const tokens = resolveThemeTokens(theme({ topbarColor: seed }));
      expect(
        contrastRatio(tokens["--yb-topbar-fg"], seed)!,
      ).toBeGreaterThanOrEqual(AA_CONTRAST);
    },
  );
});

// The org's font is a mode-agnostic override applied inline, but the stored value
// is injected as a CSS custom property, so resolveThemeTokens must only emit a
// sanitised font-family string (defense-in-depth alongside backend validation).
describe("resolveThemeTokens font family", () => {
  it("emits a curated font-family value for body and headings", () => {
    const tokens = resolveThemeTokens(
      theme({ fontFamily: '"Alexandria", system-ui, sans-serif' }),
    );
    expect(tokens["--yb-font-sans"]).toBe(
      '"Alexandria", system-ui, sans-serif',
    );
    expect(tokens["--yb-font-display"]).toBe(
      '"Alexandria", system-ui, sans-serif',
    );
  });

  it("drops a value carrying CSS-injection punctuation", () => {
    expect(
      resolveThemeTokens(theme({ fontFamily: "Arial; } body { color: red }" }))[
        "--yb-font-sans"
      ],
    ).toBeUndefined();
  });

  it("drops an over-long value", () => {
    expect(
      resolveThemeTokens(theme({ fontFamily: "a".repeat(200) }))[
        "--yb-font-sans"
      ],
    ).toBeUndefined();
  });
});

// A themed org, and so a hosted customer page, is shown in the white-label
// design; an org that never set a theme keeps Yivi's.
describe("usesWhiteLabelDesign", () => {
  it("is on for a configured theme, even one leaving every colour unset", () => {
    expect(usesWhiteLabelDesign(theme({}))).toBe(true);
  });

  it("is off for an org without a theme", () => {
    expect(usesWhiteLabelDesign(theme({ configured: false }))).toBe(false);
    expect(usesWhiteLabelDesign(null)).toBe(false);
    expect(usesWhiteLabelDesign(undefined)).toBe(false);
  });
});
