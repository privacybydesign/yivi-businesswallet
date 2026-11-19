import { describe, expect, it } from "vitest";
import { loginPathFor, safeReturnTo } from "./return-to";

describe("safeReturnTo", () => {
  it("accepts the inbound OpenID4VP transaction route", () => {
    expect(safeReturnTo("/openid4vp/abc_DEF-123")).toBe(
      "/openid4vp/abc_DEF-123",
    );
  });

  it.each([
    ["absolute URL", "https://evil.example/openid4vp/abc"],
    ["protocol-relative", "//evil.example/openid4vp/abc"],
    ["other route", "/acme/settings"],
    ["trailing segment", "/openid4vp/abc/extra"],
    ["query string", "/openid4vp/abc?x=1"],
    ["verifier params instead of the id", "/openid4vp?client_id=x"],
    ["bare route", "/openid4vp"],
    ["encoded slash", "/openid4vp/abc%2F..%2F"],
    ["empty", ""],
  ])("falls back to the root for %s", (_name, raw) => {
    expect(safeReturnTo(raw)).toBe("/");
  });

  it("falls back to the root when absent", () => {
    expect(safeReturnTo(null)).toBe("/");
    expect(safeReturnTo(undefined)).toBe("/");
  });
});

describe("loginPathFor", () => {
  it("round-trips through safeReturnTo", () => {
    const path = loginPathFor("abc_DEF-123");
    const returnTo = new URL(path, "http://localhost").searchParams.get(
      "returnTo",
    );
    expect(safeReturnTo(returnTo)).toBe("/openid4vp/abc_DEF-123");
  });
});
