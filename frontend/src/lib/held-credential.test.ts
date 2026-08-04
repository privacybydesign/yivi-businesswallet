import { readFileSync } from "node:fs";
import { fileURLToPath } from "node:url";
import { describe, expect, it } from "vitest";
import type { HeldAttestation } from "../api/attestations";
import { HELD_SOURCES } from "../api/attestations";
import { en } from "../i18n/locales/en";
import {
  EXPIRING_SOON_DAYS,
  HELD_SOURCE_FILTERS,
  heldExpiryAt,
  heldExpiryIsPast,
  heldMatchesQuery,
  heldNeedsAttention,
  heldSections,
  heldStatus,
} from "./held-credential";

const NOW = new Date("2026-06-01T12:00:00Z");

function daysFromNow(days: number): string {
  return new Date(NOW.getTime() + days * 86_400_000).toISOString();
}

function held(overrides: Partial<HeldAttestation> = {}): HeldAttestation {
  return {
    id: overrides.id ?? "held-1",
    organizationId: "org-1",
    credentialRef: "ref-1",
    vct: "nl.kvk.registration",
    issuer: "https://issuer.test",
    source: "bootstrap",
    receivedAt: "2026-01-01T00:00:00Z",
    createdAt: "2026-01-01T00:00:00Z",
    displayName: "",
    logoUri: "",
    revoked: false,
    ...overrides,
  };
}

describe("heldStatus", () => {
  it("reads a credential with no expiry as valid", () => {
    expect(heldStatus({ revoked: false }, NOW)).toBe("valid");
  });

  it("reads an expiry beyond the window as valid", () => {
    expect(
      heldStatus(
        { expiresAt: daysFromNow(EXPIRING_SOON_DAYS + 1), revoked: false },
        NOW,
      ),
    ).toBe("valid");
  });

  it("reads an expiry inside the window as expiring soon", () => {
    expect(heldStatus({ expiresAt: daysFromNow(1), revoked: false }, NOW)).toBe(
      "expiringSoon",
    );
    // The boundary day itself still counts as expiring soon.
    expect(
      heldStatus(
        { expiresAt: daysFromNow(EXPIRING_SOON_DAYS), revoked: false },
        NOW,
      ),
    ).toBe("expiringSoon");
  });

  it("reads a past expiry as expired", () => {
    expect(
      heldStatus({ expiresAt: daysFromNow(-1), revoked: false }, NOW),
    ).toBe("expired");
    // Expiry exactly now has passed.
    expect(
      heldStatus({ expiresAt: NOW.toISOString(), revoked: false }, NOW),
    ).toBe("expired");
  });

  it("reports revoked over expiry, so an unusable credential is never shown as merely expiring", () => {
    expect(heldStatus({ expiresAt: daysFromNow(1), revoked: true }, NOW)).toBe(
      "revoked",
    );
    expect(heldStatus({ expiresAt: daysFromNow(-1), revoked: true }, NOW)).toBe(
      "revoked",
    );
  });

  it("falls back to valid for an unparseable expiry rather than badging it expired", () => {
    expect(heldStatus({ expiresAt: "not-a-date", revoked: false }, NOW)).toBe(
      "valid",
    );
  });
});

describe("heldExpiryAt", () => {
  it("reads a parseable expiry as a timestamp", () => {
    expect(
      heldExpiryAt({ expiresAt: "2026-01-15T00:00:00Z", revoked: false }),
    ).toBe(Date.parse("2026-01-15T00:00:00Z"));
  });

  it("has no timestamp for an absent or unparseable expiry", () => {
    expect(heldExpiryAt({ revoked: false })).toBeNull();
    expect(heldExpiryAt({ expiresAt: "", revoked: false })).toBeNull();
    expect(
      heldExpiryAt({ expiresAt: "not-a-date", revoked: false }),
    ).toBeNull();
  });
});

describe("heldExpiryIsPast", () => {
  // The tense of the expiry copy is picked from this rather than from the badge.
  // A revoked credential badges "revoked" whatever its exp claim says, so reading
  // the tense off the badge printed a past date as "Expires 15 Jan 2026".
  it("is true for a revoked credential whose expiry has already passed", () => {
    expect(
      heldExpiryIsPast({ expiresAt: daysFromNow(-200), revoked: true }, NOW),
    ).toBe(true);
  });

  it("is false for a revoked credential that has not expired yet", () => {
    expect(
      heldExpiryIsPast({ expiresAt: daysFromNow(200), revoked: true }, NOW),
    ).toBe(false);
  });

  it("treats an expiry exactly now as past, matching heldStatus", () => {
    expect(
      heldExpiryIsPast({ expiresAt: NOW.toISOString(), revoked: false }, NOW),
    ).toBe(true);
  });

  it("is false when there is no date to phrase", () => {
    expect(heldExpiryIsPast({ revoked: false }, NOW)).toBe(false);
    expect(
      heldExpiryIsPast({ expiresAt: "not-a-date", revoked: true }, NOW),
    ).toBe(false);
  });
});

describe("heldNeedsAttention", () => {
  it("covers every status but valid", () => {
    expect(heldNeedsAttention("revoked")).toBe(true);
    expect(heldNeedsAttention("expired")).toBe(true);
    expect(heldNeedsAttention("expiringSoon")).toBe(true);
    expect(heldNeedsAttention("valid")).toBe(false);
  });
});

describe("heldMatchesQuery", () => {
  const supplier = held({
    displayName: "Approved supplier",
    vct: "https://issuer.test/vct/nl-yivi-supplier",
    issuer: "https://kvk.example",
  });

  it("matches an empty query", () => {
    expect(heldMatchesQuery(supplier, "")).toBe(true);
    expect(heldMatchesQuery(supplier, "   ")).toBe(true);
  });

  it("matches on the display name, case-insensitively", () => {
    expect(heldMatchesQuery(supplier, "approved")).toBe(true);
    expect(heldMatchesQuery(supplier, "SUPPLIER")).toBe(true);
  });

  it("matches on the credential type and the issuer", () => {
    expect(heldMatchesQuery(supplier, "nl-yivi")).toBe(true);
    expect(heldMatchesQuery(supplier, "kvk.example")).toBe(true);
  });

  it("falls back to the VCT-derived name when the credential carries none", () => {
    // credentialDisplayName maps this VCT to "KVK registration", which is the name
    // the card shows — so it has to be searchable.
    expect(heldMatchesQuery(held(), "registration")).toBe(true);
  });

  it("requires every term, in any order", () => {
    expect(heldMatchesQuery(supplier, "supplier kvk")).toBe(true);
    expect(heldMatchesQuery(supplier, "kvk supplier")).toBe(true);
    expect(heldMatchesQuery(supplier, "supplier absent")).toBe(false);
  });
});

describe("heldSections", () => {
  const noFilters = { query: "", status: "", source: "" } as const;

  const valid = held({ id: "valid", expiresAt: daysFromNow(90) });
  const soon = held({
    id: "soon",
    expiresAt: daysFromNow(5),
    source: "qerds",
  });
  const expired = held({ id: "expired", expiresAt: daysFromNow(-5) });
  const revoked = held({ id: "revoked", revoked: true, source: "openid4vci" });
  const all = [valid, soon, expired, revoked];

  const ids = (rows: { credential: HeldAttestation }[]): string[] =>
    rows.map((row) => row.credential.id);

  it("splits the list into what needs attention and what is valid", () => {
    const sections = heldSections(all, noFilters, NOW);
    expect(ids(sections.attention)).toEqual(["soon", "expired", "revoked"]);
    expect(ids(sections.valid)).toEqual(["valid"]);
  });

  it("keeps the order the backend served (most recent first)", () => {
    const sections = heldSections([revoked, expired, soon], noFilters, NOW);
    expect(ids(sections.attention)).toEqual(["revoked", "expired", "soon"]);
  });

  it("pairs each credential with the status the card badges", () => {
    const sections = heldSections(all, noFilters, NOW);
    expect(sections.attention.map((row) => row.status)).toEqual([
      "expiringSoon",
      "expired",
      "revoked",
    ]);
    expect(sections.valid[0].status).toBe("valid");
  });

  it("filters by the grouped attention status", () => {
    const sections = heldSections(
      all,
      { ...noFilters, status: "attention" },
      NOW,
    );
    expect(ids(sections.attention)).toEqual(["soon", "expired", "revoked"]);
    expect(sections.valid).toEqual([]);
  });

  it("filters by a single status", () => {
    const sections = heldSections(
      all,
      { ...noFilters, status: "revoked" },
      NOW,
    );
    expect(ids(sections.attention)).toEqual(["revoked"]);
    expect(sections.valid).toEqual([]);
  });

  it("filters by source across both sections", () => {
    const sections = heldSections(all, { ...noFilters, source: "qerds" }, NOW);
    expect(ids(sections.attention)).toEqual(["soon"]);
    expect(sections.valid).toEqual([]);
  });

  it("combines search with the filters", () => {
    const sections = heldSections(
      all,
      { query: "registration", status: "attention", source: "" },
      NOW,
    );
    expect(ids(sections.attention)).toEqual(["soon", "expired", "revoked"]);

    const noMatch = heldSections(
      all,
      { query: "nothing-matches-this", status: "", source: "" },
      NOW,
    );
    expect(noMatch.attention).toEqual([]);
    expect(noMatch.valid).toEqual([]);
  });
});

describe("HELD_SOURCE_FILTERS", () => {
  it("offers every source the list responses can carry, plus no filter", () => {
    expect(HELD_SOURCE_FILTERS[0]).toBe("");
    expect([...HELD_SOURCE_FILTERS].slice(1)).toEqual([...HELD_SOURCES]);
  });
});

// The backend is the source of truth for held-credential sources
// (backend/internal/attestation/held_store.go). HELD_SOURCES is the zod enum every
// held-list response is parsed through, so a source the backend serves and the enum
// omits fails the whole list document and the Wallet tab stops loading — not just
// the row carrying it. This test parses the Go constants and asserts the two lists
// hold the same sources, and that each one is named in en.ts. nl.ts is typed
// against en.ts, so the typecheck already fails on a missing Dutch twin.

const heldStoreGoPath = fileURLToPath(
  new URL(
    "../../../backend/internal/attestation/held_store.go",
    import.meta.url,
  ),
);
const heldStoreSource = readFileSync(heldStoreGoPath, "utf8");

// The type is optional in the pattern on purpose. Inside a `const` block a later
// source may be written `HeldSourceFoo = "foo"` without repeating it — still an
// untyped string constant, so it compiles and can be served. Requiring the type
// here would skip it, and a length-only assertion would pass too, both lists
// being short by the same one. Hence membership asserted in both directions.
const backendSources = [
  ...heldStoreSource.matchAll(/^\s*HeldSource\w+(?:\s+\w+)?\s*=\s*"([^"]+)"/gm),
].map((m) => m[1]);

const sourceLabels: Record<string, string> = en.attestations.held.sources;

describe("held sources backend/frontend parity", () => {
  it("extracts the sources from held_store.go", () => {
    expect(backendSources).toContain("qerds");
    expect(backendSources).toHaveLength(HELD_SOURCES.length);
  });

  it.each(backendSources)("accepts the source %s", (source) => {
    expect(HELD_SOURCES).toContain(source);
  });

  it.each([...HELD_SOURCES])("is served the source %s", (source) => {
    expect(backendSources).toContain(source);
  });

  it.each(backendSources)("names the source %s", (source) => {
    expect(sourceLabels[source]).toBeTruthy();
  });
});
