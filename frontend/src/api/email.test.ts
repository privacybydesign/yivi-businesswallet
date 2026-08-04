import { describe, expect, it } from "vitest";
import {
  SMTP_AUTH_MECHANISMS,
  emailSettingsSchema,
  isSmtpAuthMechanism,
  smtpAuthMechanismOptions,
} from "./email";

function settingsResponse(overrides: Record<string, unknown> = {}) {
  return {
    configured: true,
    host: "smtp.office365.com",
    port: 587,
    username: "",
    authMechanism: "xoauth2",
    tenantId: "tenant-1",
    clientId: "client-1",
    fromName: "Acme",
    fromAddress: "no-reply@acme.example",
    enabled: true,
    hasPassword: false,
    hasClientSecret: true,
    ...overrides,
  };
}

describe("emailSettingsSchema", () => {
  it("parses a Microsoft 365 configuration", () => {
    const parsed = emailSettingsSchema.parse(settingsResponse());
    expect(parsed.authMechanism).toBe("xoauth2");
    expect(parsed.hasClientSecret).toBe(true);
  });

  // authMechanism is deliberately not a closed enum: a mechanism the backend
  // gains before this frontend learns it would otherwise fail the whole
  // settings document, taking the screen down rather than hiding one option.
  it("accepts a mechanism this frontend does not know", () => {
    const parsed = emailSettingsSchema.parse(
      settingsResponse({ authMechanism: "cram-md5" }),
    );
    expect(parsed.authMechanism).toBe("cram-md5");
  });

  it("still requires the fields the screen renders", () => {
    const withoutHost: Record<string, unknown> = settingsResponse();
    delete withoutHost.host;
    expect(() => emailSettingsSchema.parse(withoutHost)).toThrow();
  });
});

describe("smtpAuthMechanismOptions", () => {
  it("offers the known mechanisms", () => {
    expect(smtpAuthMechanismOptions("plain")).toEqual([
      ...SMTP_AUTH_MECHANISMS,
    ]);
    expect(smtpAuthMechanismOptions("")).toEqual([...SMTP_AUTH_MECHANISMS]);
  });

  // Dropping the stored value would leave the selector on a mechanism the org
  // never chose, and saving the form would then rewrite its configuration.
  it("keeps an unknown stored mechanism as an option", () => {
    expect(smtpAuthMechanismOptions("cram-md5")).toEqual([
      ...SMTP_AUTH_MECHANISMS,
      "cram-md5",
    ]);
  });
});

describe("isSmtpAuthMechanism", () => {
  it("recognises the mechanisms the backend can speak", () => {
    expect(isSmtpAuthMechanism("plain")).toBe(true);
    expect(isSmtpAuthMechanism("xoauth2")).toBe(true);
    expect(isSmtpAuthMechanism("cram-md5")).toBe(false);
  });
});
