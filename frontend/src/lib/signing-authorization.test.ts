import { describe, expect, it } from "vitest";
import { ApiError, ApiValidationError } from "../api/http";
import type { YiviCscAuthorization } from "../api/yivi-csc";
import {
  authorizationAutoRedirect,
  authorizationPollContinues,
  authorizationView,
} from "./signing-authorization";

const REDIRECT_URL = "https://wallet.example/api/v1/yivi-csc/return?code=abc";
const URL_READ = "/api/v1/yivi-csc/authorizations/abc";
const SECONDS_LEFT = 120;

const pending: YiviCscAuthorization = {
  status: "pending",
  purpose: "sign",
  description: "contract.pdf",
  walletLink: "openid4vp://?client_id=x",
  expiresAt: "2026-11-19T12:05:00Z",
};
const approved: YiviCscAuthorization = {
  status: "approved",
  purpose: "sign",
  expiresAt: pending.expiresAt,
  redirectUrl: REDIRECT_URL,
};

function denied(reason: string): YiviCscAuthorization {
  return {
    status: "denied",
    purpose: "sign",
    expiresAt: pending.expiresAt,
    reason,
    redirectUrl: REDIRECT_URL,
  };
}

function apiError(status: number): ApiError {
  return new ApiError(status, "", URL_READ, null);
}

describe("authorizationAutoRedirect", () => {
  it("follows the redirect on approval and after the signer's own cancel", () => {
    expect(authorizationAutoRedirect(approved)).toBe(REDIRECT_URL);
    expect(authorizationAutoRedirect(denied("cancelled"))).toBe(REDIRECT_URL);
  });

  it("waits for the signer to read why a denial happened", () => {
    expect(authorizationAutoRedirect(denied("identity_mismatch"))).toBe(
      undefined,
    );
    expect(authorizationAutoRedirect(denied("identity_incomplete"))).toBe(
      undefined,
    );
  });

  it("goes nowhere while pending or unread", () => {
    expect(authorizationAutoRedirect(pending)).toBe(undefined);
    expect(authorizationAutoRedirect(undefined)).toBe(undefined);
  });
});

describe("authorizationView", () => {
  it("loads until the first read", () => {
    expect(
      authorizationView({
        authorization: undefined,
        error: null,
        secondsLeft: 0,
      }),
    ).toBe("loading");
  });

  it("shows the QR code while pending", () => {
    expect(
      authorizationView({
        authorization: pending,
        error: null,
        secondsLeft: SECONDS_LEFT,
      }),
    ).toBe("pending");
  });

  it("keeps the QR code through a poll that will be retried", () => {
    expect(
      authorizationView({
        authorization: pending,
        error: apiError(503),
        secondsLeft: SECONDS_LEFT,
      }),
    ).toBe("pending");
  });

  it("is expired for an unknown id, also once it had been read", () => {
    expect(
      authorizationView({
        authorization: undefined,
        error: apiError(404),
        secondsLeft: 0,
      }),
    ).toBe("expired");
    expect(
      authorizationView({
        authorization: pending,
        error: apiError(404),
        secondsLeft: SECONDS_LEFT,
      }),
    ).toBe("expired");
  });

  it("is expired once a pending authorization ran out of time", () => {
    expect(
      authorizationView({
        authorization: pending,
        error: null,
        secondsLeft: 0,
      }),
    ).toBe("expired");
  });

  it("is an error when the authorization cannot be read", () => {
    expect(
      authorizationView({
        authorization: undefined,
        error: apiError(500),
        secondsLeft: 0,
      }),
    ).toBe("error");
    expect(
      authorizationView({
        authorization: pending,
        error: new ApiValidationError(URL_READ, []),
        secondsLeft: SECONDS_LEFT,
      }),
    ).toBe("error");
  });

  it("leaves on approval and on the signer's own cancel", () => {
    expect(
      authorizationView({
        authorization: approved,
        error: null,
        secondsLeft: 0,
      }),
    ).toBe("leaving");
    expect(
      authorizationView({
        authorization: denied("cancelled"),
        error: null,
        secondsLeft: 0,
      }),
    ).toBe("leaving");
  });

  it("stays on a denial the signer has to read, even past its expiry", () => {
    expect(
      authorizationView({
        authorization: denied("identity_mismatch"),
        error: null,
        secondsLeft: 0,
      }),
    ).toBe("denied");
  });

  it("stays on a decided authorization that names no redirect", () => {
    expect(
      authorizationView({
        authorization: { ...approved, redirectUrl: undefined },
        error: null,
        secondsLeft: 0,
      }),
    ).toBe("denied");
  });
});

describe("authorizationPollContinues", () => {
  it("polls only while pending", () => {
    expect(authorizationPollContinues(pending, null)).toBe(true);
    expect(authorizationPollContinues(approved, null)).toBe(false);
    expect(authorizationPollContinues(denied("cancelled"), null)).toBe(false);
    expect(authorizationPollContinues(undefined, null)).toBe(false);
  });

  it("polls on through a server error, and stops on one that is final", () => {
    expect(authorizationPollContinues(pending, apiError(502))).toBe(true);
    expect(authorizationPollContinues(pending, apiError(404))).toBe(false);
  });
});
