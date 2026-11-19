import { describe, expect, it } from "vitest";
import { CSC_PROVIDER_KIND_BUILT_IN } from "../api/csc";
import {
  canEnableCsc,
  cscSettingsInput,
  initialCscBaseUrl,
  isBuiltInCscKind,
} from "./csc-settings";

const REMOTE_KIND = "custom";
const REMOTE_URL = "https://qtsp.example.org";

describe("isBuiltInCscKind", () => {
  it("is true for the built-in service only", () => {
    expect(isBuiltInCscKind(CSC_PROVIDER_KIND_BUILT_IN)).toBe(true);
    expect(isBuiltInCscKind("sample")).toBe(false);
    expect(isBuiltInCscKind(REMOTE_KIND)).toBe(false);
  });
});

describe("canEnableCsc", () => {
  it("lets the built-in service be enabled without a base URL", () => {
    expect(canEnableCsc(CSC_PROVIDER_KIND_BUILT_IN, "")).toBe(true);
  });

  it("makes a remote provider wait for a base URL", () => {
    expect(canEnableCsc(REMOTE_KIND, "  ")).toBe(false);
    expect(canEnableCsc(REMOTE_KIND, REMOTE_URL)).toBe(true);
  });
});

describe("initialCscBaseUrl", () => {
  it("drops the deployment's own URL the built-in service reads back as", () => {
    expect(
      initialCscBaseUrl(CSC_PROVIDER_KIND_BUILT_IN, "https://wallet.example"),
    ).toBe("");
  });

  it("keeps a remote provider's URL", () => {
    expect(initialCscBaseUrl(REMOTE_KIND, REMOTE_URL)).toBe(REMOTE_URL);
  });
});

describe("cscSettingsInput", () => {
  it("sends no endpoint, client or secret for the built-in service", () => {
    const input = cscSettingsInput({
      enabled: true,
      providerKind: CSC_PROVIDER_KIND_BUILT_IN,
      baseUrl: REMOTE_URL,
      clientId: "left-over",
      clientSecret: "left-over",
    });
    expect(input).toEqual({
      enabled: true,
      providerKind: CSC_PROVIDER_KIND_BUILT_IN,
      baseUrl: "",
      clientId: "",
    });
    expect("clientSecret" in input).toBe(false);
  });

  it("lets the built-in service be switched off", () => {
    expect(
      cscSettingsInput({
        enabled: false,
        providerKind: CSC_PROVIDER_KIND_BUILT_IN,
        baseUrl: "",
        clientId: "",
        clientSecret: "",
      }).enabled,
    ).toBe(false);
  });

  it("trims a remote provider's fields and keeps a stored secret when none is typed", () => {
    expect(
      cscSettingsInput({
        enabled: true,
        providerKind: REMOTE_KIND,
        baseUrl: ` ${REMOTE_URL} `,
        clientId: " wallet ",
        clientSecret: "  ",
      }),
    ).toEqual({
      enabled: true,
      providerKind: REMOTE_KIND,
      baseUrl: REMOTE_URL,
      clientId: "wallet",
      clientSecret: null,
    });
  });

  it("replaces the secret with a typed one", () => {
    expect(
      cscSettingsInput({
        enabled: true,
        providerKind: REMOTE_KIND,
        baseUrl: REMOTE_URL,
        clientId: "wallet",
        clientSecret: " s3cret ",
      }).clientSecret,
    ).toBe("s3cret");
  });

  it("does not enable a remote provider that has no base URL", () => {
    expect(
      cscSettingsInput({
        enabled: true,
        providerKind: REMOTE_KIND,
        baseUrl: "",
        clientId: "",
        clientSecret: "",
      }).enabled,
    ).toBe(false);
  });
});
