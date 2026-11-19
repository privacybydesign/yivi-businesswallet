import { lstatSync, readFileSync, readlinkSync } from "node:fs";
import { fileURLToPath } from "node:url";
import { describe, expect, it } from "vitest";

// The repo-root agent instructions are orientation, not a corpus: what this repo
// is, which sibling repos a change here touches, and where the real operational
// knowledge lives. They were 49,910 bytes before the cut, and a file that size is
// one nothing reads. The bound is also a gate on the agent side — above 4,000
// bytes a container working this repo no longer gets the file auto-loaded, so
// letting it refill costs exactly the readers it was written for.
const MAX_BYTES = 4000;

const repoRoot = new URL("../../../", import.meta.url);
const agentsPath = fileURLToPath(new URL("AGENTS.md", repoRoot));
const claudePath = fileURLToPath(new URL("CLAUDE.md", repoRoot));

describe("root agent instructions stay orientation", () => {
  it(`keeps AGENTS.md under ${MAX_BYTES} bytes`, () => {
    const bytes = readFileSync(agentsPath).byteLength;

    expect(
      bytes,
      `AGENTS.md is ${bytes} bytes. Durable knowledge belongs in .ai/conventions/ or .ai/features/ (see .ai/plans/README.md), not here.`,
    ).toBeLessThan(MAX_BYTES);
  });

  // One file serves both agent conventions, so the size bound only covers
  // CLAUDE.md for as long as the link does. A tool that writes through it
  // silently unshares the two.
  it("keeps CLAUDE.md a symlink to AGENTS.md", () => {
    expect(lstatSync(claudePath).isSymbolicLink()).toBe(true);
    expect(readlinkSync(claudePath)).toBe("AGENTS.md");
  });
});
