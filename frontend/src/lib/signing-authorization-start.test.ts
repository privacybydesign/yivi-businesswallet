import { describe, expect, it } from "vitest";
import {
  authorizePath,
  callbackPath,
  ceremonyEnd,
} from "./signing-authorization-start";

describe("authorizePath", () => {
  it("keeps the path and query and drops the origin", () => {
    expect(
      authorizePath(
        "https://wallet.example/api/v1/yivi-csc/oauth2/authorize?scope=service&state=a%2Bb",
      ),
    ).toBe("/api/v1/yivi-csc/oauth2/authorize?scope=service&state=a%2Bb");
  });

  it("refuses what is not an absolute URL", () => {
    expect(() => authorizePath("/api/v1/yivi-csc/oauth2/authorize")).toThrow();
  });
});

describe("callbackPath", () => {
  it("is the path and query of the ceremony's callback", () => {
    expect(
      callbackPath(
        "https://wallet.example/api/v1/signing/callback?code=c&state=s",
      ),
    ).toBe("/api/v1/signing/callback?code=c&state=s");
  });

  it("is nothing for a redirect that goes anywhere else", () => {
    expect(
      callbackPath("https://qtsp.example/oauth2/done?code=c"),
    ).toBeUndefined();
  });
});

describe("ceremonyEnd", () => {
  it("reads a linked certificate", () => {
    expect(ceremonyEnd("https://wallet.example/acme/signing?link=ok")).toEqual({
      linked: true,
      requestId: undefined,
    });
  });

  it("reads a link that was not completed", () => {
    expect(
      ceremonyEnd("https://wallet.example/acme/signing?link=failed").linked,
    ).toBe(false);
  });

  it("reads the request a signature was for", () => {
    expect(ceremonyEnd("https://wallet.example/sign/tok?request=abc")).toEqual({
      linked: undefined,
      requestId: "abc",
    });
  });

  it("reads nothing from a ceremony the service no longer knew", () => {
    expect(ceremonyEnd("https://wallet.example")).toEqual({
      linked: undefined,
      requestId: undefined,
    });
  });
});
