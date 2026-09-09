import { describe, expect, it } from "vitest";
import { ApiError } from "../api/http";
import {
  parseInvocation,
  startErrorKind,
  transactionErrorKind,
} from "./openid4vp-invocation";

function apiError(status: number, code?: string): ApiError {
  return new ApiError(
    status,
    "",
    "/api/v1/openid4vp/start",
    code ? { error: "e", code } : undefined,
  );
}

describe("parseInvocation", () => {
  it("forwards the verifier's parameters verbatim", () => {
    expect(
      parseInvocation(
        "?client_id=x509_san_dns%3Averifier.test&request_uri=https%3A%2F%2Fverifier.test%2Freq&request_uri_method=get",
      ),
    ).toEqual({
      clientId: "x509_san_dns:verifier.test",
      requestUri: "https://verifier.test/req",
      requestUriMethod: "get",
    });
  });

  // The backend rejects a by-value `request`; the frontend still forwards it so
  // the rejection is the backend's explicit one, not a silent drop.
  it("forwards a by-value request for the backend to reject", () => {
    expect(
      parseInvocation("?client_id=x509_san_dns%3Av&request=a.b.c"),
    ).toEqual({ clientId: "x509_san_dns:v", request: "a.b.c" });
  });

  it("returns null without a client_id", () => {
    expect(parseInvocation("")).toBeNull();
    expect(parseInvocation("?request_uri=https%3A%2F%2Fv%2Freq")).toBeNull();
    expect(parseInvocation("?client_id=")).toBeNull();
  });
});

describe("startErrorKind", () => {
  it.each([
    ["invalid_request", "invalidRequest"],
    ["invalid_request_uri_method", "invalidRequestUriMethod"],
    ["invalid_request_object", "invalidRequestObject"],
    ["request_uri_unreachable", "requestUriUnreachable"],
    ["request_object_validation_unavailable", "validationUnavailable"],
  ])("maps %s", (code, kind) => {
    expect(startErrorKind(apiError(400, code))).toBe(kind);
  });

  it("falls back to failed for unknown codes and non-API errors", () => {
    expect(startErrorKind(apiError(500, "internal_error"))).toBe("failed");
    expect(startErrorKind(new Error("network"))).toBe("failed");
  });
});

describe("transactionErrorKind", () => {
  it("maps the transaction-page statuses", () => {
    expect(transactionErrorKind(apiError(403, "forbidden"))).toBe(
      "otherSession",
    );
    expect(transactionErrorKind(apiError(409, "transaction_not_pending"))).toBe(
      "alreadyHandled",
    );
    expect(transactionErrorKind(apiError(404, "transaction_not_found"))).toBe(
      "notFound",
    );
    expect(transactionErrorKind(apiError(502, "presentation_failed"))).toBe(
      "presentationFailed",
    );
    expect(transactionErrorKind(apiError(422, "no_matching_credential"))).toBe(
      "noMatchingCredential",
    );
    expect(transactionErrorKind(apiError(500))).toBe("failed");
    expect(transactionErrorKind(new Error("x"))).toBe("failed");
  });
});
