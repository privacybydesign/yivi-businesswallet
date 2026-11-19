import { ApiError } from "../api/http";
import { AUTHORIZATION_DENIAL, AUTHORIZATION_STATUS } from "../api/yivi-csc";
import type { YiviCscAuthorization } from "../api/yivi-csc";
import { pollFailedForGood } from "./identity-proofing";

const NOT_FOUND_STATUS = 404;

// What the authorization page shows:
// - loading: nothing read yet;
// - pending: the QR code, waiting for the signer in the Yivi app;
// - leaving: decided, and the browser is on its way to the redirect;
// - denied: refused for a reason the signer has to read before going on;
// - expired: the id is unknown or ran out, so there is nowhere to go;
// - error: the authorization cannot be read.
export type AuthorizationView =
  | "loading"
  | "pending"
  | "leaving"
  | "denied"
  | "expired"
  | "error";

// Where the browser goes without asking: on approval, and after the signer's
// own cancel, which needs no explaining. Any other denial waits for the
// signer to read why.
export function authorizationAutoRedirect(
  authorization: YiviCscAuthorization | undefined,
): string | undefined {
  if (authorization === undefined) return undefined;
  const { status, reason, redirectUrl } = authorization;
  if (status === AUTHORIZATION_STATUS.approved) return redirectUrl;
  if (
    status === AUTHORIZATION_STATUS.denied &&
    reason === AUTHORIZATION_DENIAL.cancelled
  ) {
    return redirectUrl;
  }
  return undefined;
}

export function authorizationView({
  authorization,
  error,
  secondsLeft,
}: {
  authorization: YiviCscAuthorization | undefined;
  error: Error | null;
  secondsLeft: number;
}): AuthorizationView {
  // The service forgets an authorization as it expires, so a poll that had
  // been answered before can still end in a 404.
  if (error instanceof ApiError && error.status === NOT_FOUND_STATUS) {
    return "expired";
  }
  if (authorization === undefined) {
    return error === null ? "loading" : "error";
  }
  if (authorization.status === AUTHORIZATION_STATUS.pending) {
    if (secondsLeft === 0) return "expired";
    // A poll that will not be retried leaves a QR code nothing watches.
    return pollFailedForGood(error) ? "error" : "pending";
  }
  if (authorizationAutoRedirect(authorization) !== undefined) return "leaving";
  return "denied";
}

// Whether the page keeps re-reading the authorization: only while the signer
// can still decide it, and the read has not failed for good.
export function authorizationPollContinues(
  authorization: YiviCscAuthorization | undefined,
  error: Error | null,
): boolean {
  return (
    authorization?.status === AUTHORIZATION_STATUS.pending &&
    !pollFailedForGood(error)
  );
}
