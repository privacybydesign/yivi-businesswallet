import { describe, expect, it } from "vitest";
import type { HeldAttestation } from "../api/attestations";
import {
  EXPIRING_SOON_DAYS,
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
