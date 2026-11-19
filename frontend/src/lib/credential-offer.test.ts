import { readFileSync } from "node:fs";
import { fileURLToPath } from "node:url";
import { describe, expect, it } from "vitest";
import { credentialOfferSchema } from "../api/attestations";

// The backend is the source of truth for the inbound credential-offer queue
// (backend/internal/attestation/offer_store.go). Two things about that struct
// have to hold from this side, and neither is visible to the type system:
//
//  1. The offer deeplink must stay OFF the wire. It is a bearer token — anyone
//     who has it can redeem the credential — so CredentialOffer.Offer is tagged
//     `json:"-"` and the backend replays it server-side on accept. Dropping that
//     tag is a one-character change that would serve it to every org member, and
//     nothing else in the stack would complain.
//  2. Every field the backend DOES serve is named in the zod schema the list
//     response is parsed through, so a field added there is not silently dropped
//     on the way to the UI.
const offerStoreGoPath = fileURLToPath(
  new URL(
    "../../../backend/internal/attestation/offer_store.go",
    import.meta.url,
  ),
);
const offerStoreSource = readFileSync(offerStoreGoPath, "utf8");

// The `type CredentialOffer struct { ... }` block, so a tag on another type
// cannot satisfy these assertions.
function credentialOfferStruct(): string {
  const start = offerStoreSource.indexOf("type CredentialOffer struct {");
  expect(start).toBeGreaterThanOrEqual(0);
  const end = offerStoreSource.indexOf("\n}", start);
  expect(end).toBeGreaterThan(start);
  return offerStoreSource.slice(start, end);
}

// Field name → json tag key, for every field of the struct that carries one.
function jsonTags(): Map<string, string> {
  const tags = new Map<string, string>();
  for (const line of credentialOfferStruct().split("\n")) {
    const match = /^\s*(\w+)\s+[^`]+`json:"([^",]+)/.exec(line);
    if (match) tags.set(match[1], match[2]);
  }
  return tags;
}

describe("credential offer backend/frontend parity", () => {
  it("reads the json tags off the Go struct", () => {
    expect(jsonTags().size).toBeGreaterThan(0);
  });

  it("keeps the offer deeplink out of the API response", () => {
    expect(jsonTags().get("Offer")).toBe("-");
  });

  it.each([...jsonTags()].filter(([, key]) => key !== "-"))(
    "parses the served field %s (%s)",
    (_field, key) => {
      expect(Object.keys(credentialOfferSchema.shape)).toContain(key);
    },
  );
});
