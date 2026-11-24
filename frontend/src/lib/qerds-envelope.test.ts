import { describe, expect, it } from "vitest";
import { envelopeMessage } from "./qerds-envelope";

describe("envelopeMessage", () => {
  it("reads the message of a revocation notice", () => {
    const body = JSON.stringify({
      type: "eaa-credential-revoked/v1",
      credentialName: "LEI",
      reason: "Registration lapsed",
      message:
        "Yivi B.V. revoked the credential it issued to your organization (LEI). Reason: Registration lapsed",
    });
    expect(envelopeMessage(body)).toBe(
      "Yivi B.V. revoked the credential it issued to your organization (LEI). Reason: Registration lapsed",
    );
  });

  it("leaves other bodies alone", () => {
    expect(envelopeMessage("Hello, see attached.")).toBeNull();
    expect(envelopeMessage('{"type":"invoice","message":"x"}')).toBeNull();
    expect(
      envelopeMessage('{"type":"eaa-credential-offer-declined/v1"}'),
    ).toBeNull();
    expect(envelopeMessage("42")).toBeNull();
  });
});
