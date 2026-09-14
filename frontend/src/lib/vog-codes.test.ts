import { readFileSync } from "node:fs";
import { fileURLToPath } from "node:url";
import { describe, expect, it } from "vitest";
import { VOG_FUNCTION_ASPECTS } from "./vog-codes";

// The backend is the source of truth for the 19 function-aspect codes
// (backend/internal/vog/vog.go, FunctionAspects). This parses the Go literal
// and asserts both lists agree, membership in both directions - a length
// check alone would pass on a divergence where both lists are short by the
// same one entry.
const vogGoPath = fileURLToPath(
  new URL("../../../backend/internal/vog/vog.go", import.meta.url),
);
const source = readFileSync(vogGoPath, "utf8");

const match = /var FunctionAspects = \[\]string\{([^}]+)\}/.exec(source);
if (!match) {
  throw new Error("could not find FunctionAspects in vog.go");
}
const backendCodes = [...match[1].matchAll(/"(\d{2})"/g)].map((m) => m[1]);

describe("VOG function-aspect codes backend/frontend parity", () => {
  it("extracts the codes from vog.go", () => {
    expect(backendCodes.length).toBeGreaterThan(0);
    expect(backendCodes).toHaveLength(VOG_FUNCTION_ASPECTS.length);
  });

  it.each(backendCodes)("frontend lists backend code %s", (code) => {
    expect(VOG_FUNCTION_ASPECTS as readonly string[]).toContain(code);
  });

  it.each(VOG_FUNCTION_ASPECTS)("backend lists frontend code %s", (code) => {
    expect(backendCodes).toContain(code);
  });
});
