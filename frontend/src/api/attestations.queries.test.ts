import { describe, expect, it } from "vitest";
import {
  attestationReads,
  attestationSchemasQueryKey,
  credentialOffersQueryKey,
  heldAttestationsQueryKey,
  issuedAttestationQueryKey,
  issuedAttestationsQueryKey,
} from "./attestations.queries";

describe("attestationReads", () => {
  const reads = attestationReads("acme");
  const covers = (queryKey: readonly unknown[]): boolean => reads({ queryKey });

  it("covers the ledger, the offers and the wallet", () => {
    expect(covers(issuedAttestationsQueryKey("acme"))).toBe(true);
    expect(covers(issuedAttestationQueryKey("acme", "id-1"))).toBe(true);
    expect(covers(credentialOffersQueryKey("acme"))).toBe(true);
    expect(covers(heldAttestationsQueryKey("acme", "nl"))).toBe(true);
  });

  it("leaves what only the reader changes, and other orgs", () => {
    expect(covers(attestationSchemasQueryKey("acme"))).toBe(false);
    expect(covers(issuedAttestationsQueryKey("other"))).toBe(false);
  });
});
