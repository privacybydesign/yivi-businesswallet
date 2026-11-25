import { describe, expect, it } from "vitest";
import { ApiError } from "../api/http";
import { walletNotActive } from "./api-error";

describe("walletNotActive", () => {
  it("recognises the refusal of a suspended or revoked wallet", () => {
    const refused = new ApiError(409, "Conflict", "/api/v1/orgs/acme/members", {
      error: "this wallet is suspended or revoked",
      code: "wallet_not_active",
    });
    expect(walletNotActive(refused)).toBe(true);
  });

  it("ignores any other error", () => {
    const other = new ApiError(409, "Conflict", "/api/v1/orgs/acme/members", {
      error: "already a member",
      code: "already_member",
    });
    expect(walletNotActive(other)).toBe(false);
    expect(walletNotActive(new Error("network"))).toBe(false);
  });
});
