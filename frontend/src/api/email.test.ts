import { describe, expect, it } from "vitest";
import {
  SMTP_AUTH_MECHANISMS,
  emailSettingsBody,
  emailSettingsSchema,
  isSmtpAuthMechanism,
  smtpAuthMechanismOptions,
} from "./email";
import type { EmailSettingsForm } from "./email";

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

function settingsForm(overrides: Partial<EmailSettingsForm> = {}) {
  return {
    host: "smtp.office365.com",
    port: 587,
    username: "",
    password: "",
    authMechanism: "xoauth2",
    tenantId: "tenant-1",
    clientId: "client-1",
    clientSecret: "",
    fromName: "Acme",
    fromAddress: "no-reply@acme.example",
    enabled: true,
    ...overrides,
  } satisfies EmailSettingsForm;
}

describe("emailSettingsBody", () => {
  // The form unmounts the input for whichever secret the selected mechanism does
  // not use, so there is no way to blank it by hand. Leaving it stored would keep
  // the org's unused credential encrypted in its row indefinitely after it had
  // stopped being used; "" is what clears a stored secret, where null keeps it.
  it("clears the app registration when the mechanism is not xoauth2", () => {
    const body = emailSettingsBody(
      settingsForm({ authMechanism: "plain", password: "hunter2" }),
    );
    expect(body.tenantId).toBe("");
    expect(body.clientId).toBe("");
    expect(body.clientSecret).toBe("");
    expect(body.password).toBe("hunter2");
  });

  it("clears the smtp password when the mechanism is xoauth2", () => {
    const body = emailSettingsBody(
      settingsForm({ authMechanism: "xoauth2", password: "hunter2" }),
    );
    expect(body.password).toBe("");
    expect(body.tenantId).toBe("tenant-1");
    expect(body.clientId).toBe("client-1");
  });

  // A blank secret field means the admin did not touch it, which has to keep the
  // stored one rather than clear it — the two are different requests.
  it("keeps a stored secret the admin left blank", () => {
    expect(emailSettingsBody(settingsForm()).clientSecret).toBeNull();
    expect(
      emailSettingsBody(settingsForm({ authMechanism: "plain" })).password,
    ).toBeNull();
  });

  it("sends a typed secret so it replaces the stored one", () => {
    const body = emailSettingsBody(
      settingsForm({ clientSecret: "new-secret" }),
    );
    expect(body.clientSecret).toBe("new-secret");
  });

  // Plain authenticates with the username; XOAUTH2 uses it to override the
  // mailbox the token submits as. Neither direction clears it.
  it("keeps the username under both mechanisms", () => {
    for (const authMechanism of SMTP_AUTH_MECHANISMS) {
      const body = emailSettingsBody(
        settingsForm({ authMechanism, username: " sender@acme.example " }),
      );
      expect(body.username).toBe("sender@acme.example");
    }
  });

  // An unknown stored mechanism is not xoauth2, so it takes the plain branch
  // rather than being rewritten into one of the two this list knows.
  it("keeps an unknown mechanism and treats it as password auth", () => {
    const body = emailSettingsBody(settingsForm({ authMechanism: "cram-md5" }));
    expect(body.authMechanism).toBe("cram-md5");
    expect(body.clientSecret).toBe("");
  });

  it("trims the text fields", () => {
    const body = emailSettingsBody(
      settingsForm({
        host: "  smtp.office365.com  ",
        tenantId: "  tenant-1  ",
        clientId: "  client-1  ",
        fromName: "  Acme  ",
        fromAddress: "  no-reply@acme.example  ",
      }),
    );
    expect(body.host).toBe("smtp.office365.com");
    expect(body.tenantId).toBe("tenant-1");
    expect(body.clientId).toBe("client-1");
    expect(body.fromName).toBe("Acme");
    expect(body.fromAddress).toBe("no-reply@acme.example");
  });
});
