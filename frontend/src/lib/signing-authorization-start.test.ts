import { describe, expect, it } from "vitest";
import { authorizePath } from "./signing-authorization-start";

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
