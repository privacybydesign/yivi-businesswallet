import { describe, expect, it } from "vitest";
import { credentialOfferStatusTone } from "./qerds";

describe("credentialOfferStatusTone", () => {
  it("reads an accepted offer as success", () => {
    expect(credentialOfferStatusTone("accepted")).toBe("green");
  });

  it("reads a declined offer as an error", () => {
    expect(credentialOfferStatusTone("declined")).toBe("red");
  });

  it("reads pending, accepting and an unqueued offer (no status) as amber", () => {
    expect(credentialOfferStatusTone("pending")).toBe("amber");
    expect(credentialOfferStatusTone("accepting")).toBe("amber");
    expect(credentialOfferStatusTone("")).toBe("amber");
  });
});
