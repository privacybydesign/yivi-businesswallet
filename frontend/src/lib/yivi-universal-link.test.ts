import { describe, expect, it } from "vitest";
import { yiviUniversalLink } from "./yivi-universal-link";

describe("yiviUniversalLink", () => {
  it("carries the OpenID4VP request over to the Yivi universal link", () => {
    expect(
      yiviUniversalLink(
        "openid4vp://?client_id=x509_san_dns%3Averifier&request_uri=https%3A%2F%2Fv.example%2Fr",
      ),
    ).toBe(
      "https://open.yivi.app/-/openid4vp?client_id=x509_san_dns%3Averifier&request_uri=https%3A%2F%2Fv.example%2Fr",
    );
  });
});
