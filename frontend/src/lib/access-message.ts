import type { TFunction } from "i18next";
import { ApiError } from "../api/http";

const FORBIDDEN_STATUS = 403;
const NOT_FOUND_STATUS = 404;

// Maps an org-access error to a user-facing message; falls back to the raw
// error text for anything that isn't a recognised access failure.
export function accessMessage(error: Error, t: TFunction): string {
  if (error instanceof ApiError && error.status === FORBIDDEN_STATUS) {
    return t("access.notMember");
  }
  if (error instanceof ApiError && error.status === NOT_FOUND_STATUS) {
    return t("access.notExist");
  }
  return error.message;
}
