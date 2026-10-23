import { readdirSync, readFileSync } from "node:fs";
import { fileURLToPath } from "node:url";
import { describe, expect, it } from "vitest";

// The identity proofing screens, the flow editor and its tabs, the hosted
// page, Settings › Data and privacy and Organisation, and the dashboard show
// versions, statuses and counts as plain text (ui/status-text.tsx). A Tag chip
// there brings back the pill that wraps in a table cell. Pages outside the
// redesign keep theirs.

const ROUTES = fileURLToPath(new URL("../routes/", import.meta.url));
const PLAIN_TEXT_SCREENS =
  /^(proofing-|identity-proofing|identity-check-review|data-request-review|developers|dashboard|flow-|settings-privacy\.tsx$|org-profile-settings\.tsx$|proof\.tsx$)/;
const TAG_IMPORT = /import\s*\{[^}]*\bTag\b[^}]*\}\s*from\s*"\.\.\/ui(\/tag)?"/;

describe("plain text screens", () => {
  const screens = readdirSync(ROUTES).filter(
    (file) => file.endsWith(".tsx") && PLAIN_TEXT_SCREENS.test(file),
  );

  it("finds the screens it guards", () => {
    for (const screen of [
      "proofing-people.tsx",
      "dashboard.tsx",
      "flow-editor.tsx",
      "flow-editor-advanced.tsx",
      "flow-branding.tsx",
      "flow-hosted-settings.tsx",
      "flow-tabs.tsx",
      "settings-privacy.tsx",
      "org-profile-settings.tsx",
      "proof.tsx",
    ]) {
      expect(screens).toContain(screen);
    }
  });

  it("render no Tag chip", () => {
    const chipped = screens.filter((file) =>
      TAG_IMPORT.test(readFileSync(ROUTES + file, "utf8")),
    );

    expect(chipped).toEqual([]);
  });
});
