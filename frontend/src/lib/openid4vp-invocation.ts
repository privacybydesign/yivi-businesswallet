import { ApiError } from "../api/http";
import type { OpenID4VPInvocation } from "../api/openid4vp";
import { errorCode } from "./api-error";

// parseInvocation reads the OpenID4VP Authorization Request parameters a
// verifier put on GET /openid4vp. Nothing is validated here beyond "a client_id
// is present": which request forms are accepted (request_uri only, get/post) is
// the backend's decision, so its answer is what the user sees. Returns null when
// the URL cannot be an invocation at all.
export function parseInvocation(search: string): OpenID4VPInvocation | null {
  const params = new URLSearchParams(search);
  const clientId = params.get("client_id");
  if (!clientId) {
    return null;
  }
  const invocation: OpenID4VPInvocation = { clientId };
  const requestUri = params.get("request_uri");
  if (requestUri !== null) {
    invocation.requestUri = requestUri;
  }
  const requestUriMethod = params.get("request_uri_method");
  if (requestUriMethod !== null) {
    invocation.requestUriMethod = requestUriMethod;
  }
  const request = params.get("request");
  if (request !== null) {
    invocation.request = request;
  }
  return invocation;
}

// The backend's start errors, by stable code, so the page can show a specific
// explanation rather than a generic failure.
export type StartErrorKind =
  | "invalidRequest"
  | "invalidRequestUriMethod"
  | "invalidRequestObject"
  | "requestUriUnreachable"
  | "validationUnavailable"
  | "failed";

const START_ERROR_KINDS: Record<string, StartErrorKind> = {
  invalid_request: "invalidRequest",
  invalid_request_uri_method: "invalidRequestUriMethod",
  invalid_request_object: "invalidRequestObject",
  request_uri_unreachable: "requestUriUnreachable",
  request_object_validation_unavailable: "validationUnavailable",
};

export function startErrorKind(error: unknown): StartErrorKind {
  const code = errorCode(error);
  if (code !== null && code in START_ERROR_KINDS) {
    return START_ERROR_KINDS[code];
  }
  return "failed";
}

// The transaction-page failures that need their own copy: a transaction bound to
// another signed-in user (403), one already handled or expired (409), one the
// backend no longer knows (404), and a presentation that failed to deliver (502).
export type TransactionErrorKind =
  | "otherSession"
  | "alreadyHandled"
  | "notFound"
  | "noMatchingCredential"
  | "presentationFailed"
  | "failed";

const FORBIDDEN_STATUS = 403;
const NOT_FOUND_STATUS = 404;
const CONFLICT_STATUS = 409;
const PRESENTATION_FAILED_CODE = "presentation_failed";
const NO_MATCHING_CREDENTIAL_CODE = "no_matching_credential";

export function transactionErrorKind(error: unknown): TransactionErrorKind {
  if (!(error instanceof ApiError)) {
    return "failed";
  }
  switch (errorCode(error)) {
    case PRESENTATION_FAILED_CODE:
      return "presentationFailed";
    case NO_MATCHING_CREDENTIAL_CODE:
      return "noMatchingCredential";
  }
  switch (error.status) {
    case FORBIDDEN_STATUS:
      return "otherSession";
    case CONFLICT_STATUS:
      return "alreadyHandled";
    case NOT_FOUND_STATUS:
      return "notFound";
    default:
      return "failed";
  }
}
