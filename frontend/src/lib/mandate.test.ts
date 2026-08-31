import { readFileSync } from "node:fs";
import { fileURLToPath } from "node:url";
import { describe, expect, it } from "vitest";
import type { Mandate, MandateAuthority } from "../api/mandates";
import { en } from "../i18n/locales/en";
import {
  MANDATE_SCOPES,
  MANDATE_STATUSES,
  MANDATE_TYPES,
  MAX_REVOCATION_REASON_LENGTH,
  isoFromLocalInput,
  mandateCascade,
  mandateGrantAvailability,
  mandateIsRevocable,
  mandateLineage,
  mandateScopeLabel,
  mandateStatusTone,
  mandateTypeHint,
  mandateWindowIsEmpty,
} from "./mandate";

function mandate(overrides: Partial<Mandate> & { id: string }): Mandate {
  return {
    organizationId: "org-1",
    type: "full",
    status: "active",
    grantorUserId: "user-boss",
    grantorName: "Bo Boss",
    granteeUserId: `user-${overrides.id}`,
    granteeName: overrides.id,
    scope: "organization",
    scopeDepartmentId: null,
    scopeDepartmentName: null,
    parentMandateId: null,
    validFrom: "2026-01-01T00:00:00Z",
    validUntil: null,
    revokedAt: null,
    revokedByUserId: null,
    revocationReason: null,
    createdAt: "2026-01-01T00:00:00Z",
    ...overrides,
  };
}

const ids = (rows: { mandate: Mandate }[]): string[] =>
  rows.map((row) => row.mandate.id);
const depths = (rows: { depth: number }[]): number[] =>
  rows.map((row) => row.depth);

describe("mandateLineage", () => {
  it("keeps the order the backend served for a register with no delegations", () => {
    const rows = mandateLineage([
      mandate({ id: "c" }),
      mandate({ id: "a" }),
      mandate({ id: "b" }),
    ]);
    expect(ids(rows)).toEqual(["c", "a", "b"]);
    expect(depths(rows)).toEqual([0, 0, 0]);
  });

  it("puts a delegation under the mandate it was cut from", () => {
    // The register is served most recent first, so the child arrives first.
    const rows = mandateLineage([
      mandate({ id: "child", parentMandateId: "root" }),
      mandate({ id: "root" }),
    ]);
    expect(ids(rows)).toEqual(["root", "child"]);
    expect(depths(rows)).toEqual([0, 1]);
  });

  it("nests a whole chain, depth-first", () => {
    const rows = mandateLineage([
      mandate({ id: "grandchild", parentMandateId: "child" }),
      mandate({ id: "child", parentMandateId: "root" }),
      mandate({ id: "sibling", parentMandateId: "root" }),
      mandate({ id: "root" }),
      mandate({ id: "other-root" }),
    ]);
    expect(ids(rows)).toEqual([
      "root",
      "child",
      "grandchild",
      "sibling",
      "other-root",
    ]);
    expect(depths(rows)).toEqual([0, 1, 2, 1, 0]);
  });

  it("shows a mandate whose parent is not in the register as a root", () => {
    // The register is the audit surface: a row that vanished from it would hide
    // a grant, so an unresolvable parent costs the indent, not the row.
    const rows = mandateLineage([
      mandate({ id: "orphan", parentMandateId: "gone" }),
    ]);
    expect(ids(rows)).toEqual(["orphan"]);
    expect(depths(rows)).toEqual([0]);
  });

  it("still lists every mandate when the parent links form a cycle", () => {
    // Nothing the backend serves can do this (a parent always predates its
    // child), but neither row may drop out of the register if it ever does.
    const rows = mandateLineage([
      mandate({ id: "a", parentMandateId: "b" }),
      mandate({ id: "b", parentMandateId: "a" }),
    ]);
    expect(ids(rows).toSorted()).toEqual(["a", "b"]);
  });
});

describe("mandateCascade", () => {
  const register = [
    mandate({ id: "root" }),
    mandate({ id: "child", parentMandateId: "root" }),
    mandate({
      id: "pending-child",
      parentMandateId: "root",
      status: "pending",
    }),
    mandate({ id: "grandchild", parentMandateId: "child" }),
    mandate({ id: "elsewhere" }),
  ];

  it("reaches every descendant of the mandate being revoked", () => {
    expect(mandateCascade(register, "root").map((m) => m.id)).toEqual([
      "child",
      "pending-child",
      "grandchild",
    ]);
  });

  it("does not count the mandate itself, nor an unrelated one", () => {
    const reached = mandateCascade(register, "root").map((m) => m.id);
    expect(reached).not.toContain("root");
    expect(reached).not.toContain("elsewhere");
  });

  it("is empty for a mandate nothing was delegated from", () => {
    expect(mandateCascade(register, "grandchild")).toEqual([]);
  });

  it("leaves out a descendant that already ended, matching the backend", () => {
    // The backend walks the whole subtree but only touches rows that are neither
    // revoked nor expired, so counting an ended one would overstate the warning.
    const ended = [
      mandate({ id: "root" }),
      mandate({ id: "revoked", parentMandateId: "root", status: "revoked" }),
      mandate({ id: "expired", parentMandateId: "root", status: "expired" }),
    ];
    expect(mandateCascade(ended, "root")).toEqual([]);
  });

  it("keeps descending past an ended descendant to the live ones under it", () => {
    // The recursive CTE walks by parent id alone, so a live grandchild under a
    // revoked child is still revoked with it.
    const register = [
      mandate({ id: "root" }),
      mandate({ id: "revoked", parentMandateId: "root", status: "revoked" }),
      mandate({ id: "under-it", parentMandateId: "revoked" }),
    ];
    expect(mandateCascade(register, "root").map((m) => m.id)).toEqual([
      "under-it",
    ]);
  });
});

describe("mandateGrantAvailability", () => {
  const authority = (
    overrides: Partial<MandateAuthority> = {},
  ): MandateAuthority => ({
    mayGrant: false,
    legalRepresentative: false,
    fullMandate: false,
    jointAuthority: false,
    ...overrides,
  });

  it("offers nothing to a caller the backend would refuse", () => {
    expect(mandateGrantAvailability(authority())).toBe("noAuthority");
  });

  it("offers the flows to a legal representative and to a full-mandate holder", () => {
    expect(
      mandateGrantAvailability(
        authority({ mayGrant: true, legalRepresentative: true }),
      ),
    ).toBe("available");
    expect(
      mandateGrantAvailability(
        authority({ mayGrant: true, fullMandate: true }),
      ),
    ).toBe("available");
  });

  it("withholds them from a jointly registered director", () => {
    // The backend would accept the grant, because it does not read the authority
    // column yet; offering the flow would record a grant one director cannot make.
    expect(
      mandateGrantAvailability(
        authority({
          mayGrant: true,
          legalRepresentative: true,
          jointAuthority: true,
        }),
      ),
    ).toBe("jointAuthority");
  });

  it("lifts the hold when the joint director also holds a full mandate", () => {
    // That mandate is a basis of its own, granted through the register rather
    // than read off it, so acting on it is not acting alone as a director.
    expect(
      mandateGrantAvailability(
        authority({
          mayGrant: true,
          legalRepresentative: true,
          jointAuthority: true,
          fullMandate: true,
        }),
      ),
    ).toBe("available");
  });
});

describe("mandateIsRevocable", () => {
  it("covers the statuses that still have a life ahead of them", () => {
    expect(mandateIsRevocable(mandate({ id: "a", status: "active" }))).toBe(
      true,
    );
    expect(mandateIsRevocable(mandate({ id: "a", status: "pending" }))).toBe(
      true,
    );
  });

  it("is false for a mandate that ended, which the backend answers 409 for", () => {
    expect(mandateIsRevocable(mandate({ id: "a", status: "revoked" }))).toBe(
      false,
    );
    expect(mandateIsRevocable(mandate({ id: "a", status: "expired" }))).toBe(
      false,
    );
  });
});

describe("mandateStatusTone", () => {
  it("badges each status", () => {
    expect(mandateStatusTone("active")).toBe("green");
    expect(mandateStatusTone("pending")).toBe("amber");
    expect(mandateStatusTone("revoked")).toBe("red");
    expect(mandateStatusTone("expired")).toBe("default");
  });

  it("falls back to the neutral tone for a status it does not know", () => {
    expect(mandateStatusTone("something-new")).toBe("default");
  });
});

describe("mandateScopeLabel", () => {
  const t = ((key: string) => key) as unknown as Parameters<
    typeof mandateScopeLabel
  >[1];

  it("names the department a department-scoped mandate is confined to", () => {
    expect(
      mandateScopeLabel(
        mandate({
          id: "a",
          scope: "department",
          scopeDepartmentId: "dept-1",
          scopeDepartmentName: "Finance",
        }),
        t,
      ),
    ).toBe("Finance");
  });

  it("falls back to the generic label when the department has no name", () => {
    expect(
      mandateScopeLabel(mandate({ id: "a", scope: "department" }), t),
    ).toBe("mandates.scopes.department");
  });

  it("labels an org-wide mandate", () => {
    expect(mandateScopeLabel(mandate({ id: "a" }), t)).toBe(
      "mandates.scopes.organization",
    );
  });
});

describe("mandateWindowIsEmpty", () => {
  const now = "2026-06-01T12:00:00.000Z";

  it("accepts an open-ended window", () => {
    expect(mandateWindowIsEmpty(undefined, undefined, now)).toBe(false);
    expect(
      mandateWindowIsEmpty("2026-07-01T00:00:00.000Z", undefined, now),
    ).toBe(false);
  });

  it("rejects an end at or before the start named", () => {
    expect(
      mandateWindowIsEmpty(
        "2026-07-01T00:00:00.000Z",
        "2026-06-15T00:00:00.000Z",
        now,
      ),
    ).toBe(true);
    expect(
      mandateWindowIsEmpty(
        "2026-07-01T00:00:00.000Z",
        "2026-07-01T00:00:00.000Z",
        now,
      ),
    ).toBe(true);
  });

  it("rejects an end in the past when no start is named", () => {
    // validateGrant fills valid_from with now, so this is the empty window the
    // backend answers 400 for; leaving it to the round trip shows its
    // untranslated prose.
    expect(
      mandateWindowIsEmpty(undefined, "2026-05-01T00:00:00.000Z", now),
    ).toBe(true);
    expect(mandateWindowIsEmpty(undefined, now, now)).toBe(true);
  });

  it("accepts an end in the future when no start is named", () => {
    expect(
      mandateWindowIsEmpty(undefined, "2026-07-01T00:00:00.000Z", now),
    ).toBe(false);
  });
});

describe("mandateTypeHint", () => {
  const t = ((key: string) => key) as unknown as Parameters<
    typeof mandateTypeHint
  >[1];

  it("explains each tier it knows", () => {
    expect(mandateTypeHint("full", t)).toBe("mandates.typeHints.full");
    expect(mandateTypeHint("administrative", t)).toBe(
      "mandates.typeHints.administrative",
    );
  });

  it("has no hint for a tier it does not know", () => {
    expect(mandateTypeHint("something-new", t)).toBeUndefined();
  });
});

describe("isoFromLocalInput", () => {
  it("has no value for an empty field, so the backend default applies", () => {
    expect(isoFromLocalInput("")).toBeUndefined();
    expect(isoFromLocalInput("   ")).toBeUndefined();
  });

  it("turns a datetime-local value into an instant", () => {
    // The field carries no zone, so it is read as local time; comparing against
    // Date keeps the test independent of the runner's timezone.
    expect(isoFromLocalInput("2026-06-01T12:30")).toBe(
      new Date("2026-06-01T12:30").toISOString(),
    );
  });

  it("has no value for something unparseable", () => {
    expect(isoFromLocalInput("not-a-date")).toBeUndefined();
  });
});

// The backend is the source of truth for the mandate tiers, scopes and statuses
// (backend/internal/organization/mandate.go). Every one of them reaches the
// register screen as a label, and the grant form offers the tiers from
// MANDATE_TYPES, so a tier the backend gains and this file omits would be offered
// to nobody and would render as a bare "full" in the table. This test parses the
// Go constants and asserts membership in both directions, plus that each value is
// named in en.ts. nl.ts is typed against en.ts, so the typecheck already fails on
// a missing Dutch twin.

const mandateGoPath = fileURLToPath(
  new URL("../../../backend/internal/organization/mandate.go", import.meta.url),
);
const mandateSource = readFileSync(mandateGoPath, "utf8");

const handlerGoPath = fileURLToPath(
  new URL(
    "../../../backend/internal/organization/mandates.go",
    import.meta.url,
  ),
);
const handlerSource = readFileSync(handlerGoPath, "utf8");

// The type is optional in each pattern on purpose: inside a `const` block a later
// member may be written without repeating it and is still an untyped string
// constant the API can serve.
function goStringConstants(prefix: string): string[] {
  const pattern = new RegExp(
    `^\\s*${prefix}\\w+(?:\\s+\\w+)?\\s*=\\s*"([^"]+)"`,
    "gm",
  );
  return [...mandateSource.matchAll(pattern)].map((match) => match[1]);
}

const backendStatuses = goStringConstants("MandateStatus");
const backendScopes = goStringConstants("MandateScope");
const backendTypes = goStringConstants("Mandate(?!Scope|Status)");

describe("mandate constants backend/frontend parity", () => {
  it("extracts the constants from mandate.go", () => {
    expect(backendTypes).toContain("full");
    expect(backendScopes).toContain("organization");
    expect(backendStatuses).toContain("active");
  });

  it.each(backendTypes)("accepts and names the tier %s", (type) => {
    expect(MANDATE_TYPES).toContain(type);
    expect(
      en.mandates.types[type as keyof typeof en.mandates.types],
    ).toBeTruthy();
    expect(
      en.mandates.typeHints[type as keyof typeof en.mandates.typeHints],
    ).toBeTruthy();
    // Naming the hint in en.ts is not enough: the grant form reads it through
    // TYPE_HINT_KEYS, so a tier missing from that map would go unexplained.
    expect(mandateTypeHint(type, ((key: string) => key) as never)).toBe(
      `mandates.typeHints.${type}`,
    );
  });

  it.each([...MANDATE_TYPES])("is served the tier %s", (type) => {
    expect(backendTypes).toContain(type);
  });

  it.each(backendScopes)("accepts and names the scope %s", (scope) => {
    expect(MANDATE_SCOPES).toContain(scope);
    expect(
      en.mandates.scopes[scope as keyof typeof en.mandates.scopes],
    ).toBeTruthy();
  });

  it.each([...MANDATE_SCOPES])("is served the scope %s", (scope) => {
    expect(backendScopes).toContain(scope);
  });

  it.each(backendStatuses)("accepts and names the status %s", (status) => {
    expect(MANDATE_STATUSES).toContain(status);
    expect(
      en.mandates.statuses[status as keyof typeof en.mandates.statuses],
    ).toBeTruthy();
  });

  it.each([...MANDATE_STATUSES])("is served the status %s", (status) => {
    expect(backendStatuses).toContain(status);
  });

  it("bounds the revocation reason at the length the backend accepts", () => {
    const found = /maxRevocationReasonLength\s*=\s*(\d+)/.exec(
      handlerSource,
    )?.[1];
    expect(found).toBe(String(MAX_REVOCATION_REASON_LENGTH));
  });
});
