import { readFileSync } from "node:fs";
import { fileURLToPath } from "node:url";
import { describe, expect, it } from "vitest";
import { WALLET_UNIT_REVOKE_REASONS } from "../api/wallet-unit";
import { walletUnitState } from "./wallet-unit";

const unit = { id: "u1", createdAt: "2026-11-11T10:00:00Z" };

describe("walletUnitState", () => {
  it("is not issued before the first attestation", () => {
    expect(walletUnitState({ issued: false })).toBe("notIssued");
  });

  it("follows the latest unit's status", () => {
    expect(
      walletUnitState({
        issued: true,
        summary: { unit: { ...unit, status: "active" }, issuedCount: 2 },
      }),
    ).toBe("active");
    expect(
      walletUnitState({
        issued: true,
        summary: {
          unit: {
            ...unit,
            status: "revoked",
            revokedReason: "key_compromise",
            revokedAt: "2026-11-11T11:00:00Z",
          },
          issuedCount: 2,
        },
      }),
    ).toBe("revoked");
  });
});

describe("revoke reasons", () => {
  it("mirror the backend in both directions", () => {
    const source = readFileSync(
      fileURLToPath(
        new URL(
          "../../../backend/internal/walletunit/walletunit.go",
          import.meta.url,
        ),
      ),
      "utf8",
    );
    const backend = [...source.matchAll(/Reason\w+\s*=\s*"([a-z_]+)"/g)].map(
      (m) => m[1],
    );
    expect([...backend].sort()).toEqual([...WALLET_UNIT_REVOKE_REASONS].sort());
  });
});
