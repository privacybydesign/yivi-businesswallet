import { describe, expect, it } from "vitest";
import { credentialDisplayName } from "./credential-display";

describe("credentialDisplayName", () => {
  it("maps a known issuer-URL VCT to its curated name", () => {
    expect(
      credentialDisplayName(
        "https://veramo-issuer.openid4vc.staging.yivi.app/vct/nl-yivi-supplier",
      ),
    ).toBe("Approved supplier");
  });

  it("maps a known bare VCT to its curated name", () => {
    expect(credentialDisplayName("nl.kvk.registration")).toBe(
      "KVK registration",
    );
  });

  it("recognises a known final segment regardless of issuer host", () => {
    expect(
      credentialDisplayName(
        "https://other-issuer.example/vct/nl-yivi-supplier",
      ),
    ).toBe("Approved supplier");
  });

  it("humanises an unknown issuer-URL VCT", () => {
    expect(
      credentialDisplayName("https://issuer.example/vct/some-new-credential"),
    ).toBe("Some new credential");
  });

  it("humanises an unknown dotted VCT", () => {
    expect(credentialDisplayName("com.example.membership")).toBe(
      "Com example membership",
    );
  });

  it("returns the input unchanged when empty", () => {
    expect(credentialDisplayName("")).toBe("");
  });
});
