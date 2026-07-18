import { describe, it, expect } from "vitest";
import { ApiError } from "../api/http";
import { claimErrorKind } from "./login-error";

function apiError(status: number, body: unknown): ApiError {
  return new ApiError(status, "", "/api/v1/auth/session/x/claim", body);
}

describe("claimErrorKind", () => {
  it("maps 403 user_not_invited to notRegistered", () => {
    // Regression: this case used to fall through to the generic failure.
    const error = apiError(403, {
      error: "not invited",
      code: "user_not_invited",
    });
    expect(claimErrorKind(error)).toBe("notRegistered");
  });

  it("maps 422 to credentialRejected", () => {
    const error = apiError(422, {
      error: "rejected",
      code: "disclosure_invalid",
    });
    expect(claimErrorKind(error)).toBe("credentialRejected");
  });

  it("treats a 403 with a different code as a generic failure", () => {
    const error = apiError(403, { error: "nope", code: "forbidden" });
    expect(claimErrorKind(error)).toBe("failed");
  });

  it("treats a non-ApiError as a generic failure", () => {
    expect(claimErrorKind(new Error("network"))).toBe("failed");
  });
});
