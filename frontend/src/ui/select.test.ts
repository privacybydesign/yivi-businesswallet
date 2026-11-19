import { readdirSync, readFileSync } from "node:fs";
import { fileURLToPath } from "node:url";
import { describe, expect, it } from "vitest";

// Every select gets its chevron inset and its truncation from ui/select.tsx. A
// bare <select> brings back the per-file copy of the style, and with it the
// chevron against the right edge.

const SRC = fileURLToPath(new URL("..", import.meta.url));
const SHARED = "ui/select.tsx";
const BARE_SELECT = /<select[\s>]/;

describe("select", () => {
  it("is only written out in the shared component", () => {
    const bare = readdirSync(SRC, { recursive: true, encoding: "utf8" })
      .map((path) => path.replaceAll("\\", "/"))
      .filter((path) => path.endsWith(".tsx") && path !== SHARED)
      .filter((path) => BARE_SELECT.test(readFileSync(SRC + path, "utf8")));

    expect(bare).toEqual([]);
  });
});
