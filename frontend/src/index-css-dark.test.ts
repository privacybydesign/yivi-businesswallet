import { readFileSync } from "node:fs";
import { fileURLToPath } from "node:url";
import { describe, expect, it } from "vitest";
import { AA_CONTRAST, contrastRatio } from "./lib/theme";

// The dark palette lives as a `@media (prefers-color-scheme: dark)` override of
// the --yb-* tokens in index.css (values from the Yivi Business design system).
// Every utility resolves through those tokens, so a foreground token that does
// not clear WCAG 2.2 AA on the background it lands on ships an unreadable dark
// mode. This test parses the dark block and asserts each fg/bg pairing the
// shell actually renders (see ui/tag.tsx, ui/outcome.tsx, ui/avatar.tsx) stays
// ≥4.5:1, so a future edit to a dark value can't silently regress contrast.

const cssPath = fileURLToPath(new URL("./index.css", import.meta.url));
const css = readFileSync(cssPath, "utf8");

const darkBlock = css.match(
  /@media \(prefers-color-scheme: dark\)\s*\{\s*:root\s*\{([^}]*)\}/,
);

const tokens: Record<string, string> = {};
for (const [, name, value] of (darkBlock?.[1] ?? "").matchAll(
  /(--yb-[\w-]+):\s*([^;]+);/g,
)) {
  tokens[name] = value.trim();
}

// The fg → bg pairings the components render (tone classes pair these tokens).
const CONTRAST_PAIRS: ReadonlyArray<[fg: string, bg: string, label: string]> = [
  ["--yb-ink", "--yb-surface", "body text on cards"],
  ["--yb-ink", "--yb-surface-2", "body text on the page"],
  ["--yb-ink", "--yb-surface-3", "body text on row hover"],
  ["--yb-ink-soft", "--yb-surface-2", "secondary text on the page"],
  ["--yb-success", "--yb-success-bg", "success tag"],
  ["--yb-warning-fg", "--yb-warning-bg", "warning tag"],
  ["--yb-error", "--yb-error-bg", "error tag"],
  ["--yb-link", "--yb-highlight", "link/info tag"],
  ["--yb-primary-fg", "--yb-primary", "default primary button"],
];

describe("dark palette (index.css)", () => {
  it("defines a prefers-color-scheme: dark override", () => {
    expect(darkBlock).not.toBeNull();
    expect(Object.keys(tokens).length).toBeGreaterThan(0);
  });

  it.each(CONTRAST_PAIRS)(
    "keeps %s on %s at WCAG AA (%s)",
    (fgToken, bgToken) => {
      const fg = tokens[fgToken];
      const bg = tokens[bgToken];
      expect(fg, `${fgToken} is defined in the dark block`).toBeDefined();
      expect(bg, `${bgToken} is defined in the dark block`).toBeDefined();
      const ratio = contrastRatio(fg, bg);
      expect(ratio).not.toBeNull();
      expect(ratio!).toBeGreaterThanOrEqual(AA_CONTRAST);
    },
  );
});
