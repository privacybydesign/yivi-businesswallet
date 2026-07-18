import { ApiError } from "../api/http";
import { errorCode } from "./api-error";

// A sign-in claim can fail in ways that need distinct, actionable messaging:
// - notRegistered: the disclosure succeeded but the email has no account and no
//   pending invitations (backend 403 `user_not_invited`) — route to register or
//   ask an admin, not a dead-end retry.
// - credentialRejected: the disclosed credential can't be used to sign in
//   (backend 422).
// - failed: any other or unexpected error.
export type ClaimErrorKind = "notRegistered" | "credentialRejected" | "failed";

const NOT_INVITED_STATUS = 403;
const NOT_INVITED_CODE = "user_not_invited";
const CREDENTIAL_REJECTED_STATUS = 422;

// claimErrorKind classifies a failed sign-in claim so the login screen can show
// the matching copy and next step. Pure and dependency-free so it can be unit
// tested without rendering.
export function claimErrorKind(error: unknown): ClaimErrorKind {
  if (error instanceof ApiError) {
    if (
      error.status === NOT_INVITED_STATUS &&
      errorCode(error) === NOT_INVITED_CODE
    ) {
      return "notRegistered";
    }
    if (error.status === CREDENTIAL_REJECTED_STATUS) {
      return "credentialRejected";
    }
  }
  return "failed";
}
