import { readFileSync } from "node:fs";
import { resolve } from "node:path";
import { describe, expect, it } from "vitest";
import { ORG_EVENT_KINDS } from "../api/org-events";

// The org's stream names its events after the backend's kinds; a kind missing
// here is an event no page listens for, so its reads would never refresh.
describe("org event kinds", () => {
  const source = readFileSync(
    resolve(__dirname, "../../../backend/internal/events/events.go"),
    "utf8",
  );
  const constants = new Map(
    [...source.matchAll(/^\t(\w+)\s+Kind = "([\w-]+)"$/gm)].map((m) => [
      m[1],
      m[2],
    ]),
  );
  const orgKinds = (/OrgKinds = \[\]Kind\{([^}]*)\}/.exec(source)?.[1] ?? "")
    .split(",")
    .map((name) => constants.get(name.trim()))
    .filter((kind) => kind !== undefined);

  it("match the backend's OrgKinds in both directions", () => {
    expect(orgKinds.length).toBeGreaterThan(0);
    expect([...ORG_EVENT_KINDS].sort()).toEqual([...orgKinds].sort());
  });
});
