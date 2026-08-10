import { describe, expect, it } from "vitest";
import { SIGNER_KIND } from "../api/signing";
import type { SignerSelection } from "../api/signing";
import { alreadyChosen, isEmailish, signerKey } from "./signer-selection";

describe("isEmailish", () => {
  it("accepts an ordinary address", () => {
    expect(isEmailish("outsider@example.org")).toBe(true);
    expect(isEmailish("  sam.smith+sign@sub.example.co.uk  ")).toBe(true);
  });

  it("refuses what cannot be an address", () => {
    for (const value of [
      "",
      "   ",
      "no-at-sign",
      "@example.org",
      "two@at@example.org",
      "out@example",
      "out@.example.org",
      "out@example.",
      "out @example.org",
    ]) {
      expect(isEmailish(value), value).toBe(false);
    }
  });
});

describe("signerKey", () => {
  it("keys a member by user id and an external signee by address", () => {
    expect(signerKey({ kind: SIGNER_KIND.internal, userId: "u-1" })).toBe(
      "member:u-1",
    );
    expect(
      signerKey({
        kind: SIGNER_KIND.external,
        email: "Out@Example.ORG",
        name: "Out",
      }),
    ).toBe("external:out@example.org");
  });
});

describe("alreadyChosen", () => {
  const signers: SignerSelection[] = [
    { kind: SIGNER_KIND.internal, userId: "u-1" },
    { kind: SIGNER_KIND.external, email: "Out@Example.ORG", name: "Out" },
  ];

  it("matches an address however it was typed", () => {
    expect(alreadyChosen(signers, "out@example.org")).toBe(true);
    expect(alreadyChosen(signers, "  OUT@EXAMPLE.org ")).toBe(true);
  });

  it("does not match an address that is not on the list", () => {
    expect(alreadyChosen(signers, "other@example.org")).toBe(false);
  });
});
