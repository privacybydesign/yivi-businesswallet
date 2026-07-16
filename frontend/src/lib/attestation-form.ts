import { ApiError } from "../api/http";

// Shared field styling for the attestation forms, mirroring qerds-compose.
export const FIELD_LABEL = "text-ink-soft text-[12px] font-semibold";
const CONTROL =
  "rounded-yivi bg-surface text-ink w-full border px-3 text-[13.5px] outline-none transition-colors focus:ring-3";
const CONTROL_OK = "border-line-strong focus:border-ink focus:ring-ink/10";
const CONTROL_ERR = "border-error focus:border-error focus:ring-error/10";

export function control(hasError: boolean): string {
  return [CONTROL, hasError ? CONTROL_ERR : CONTROL_OK].join(" ");
}

// Reads the machine-readable `code` from an ApiError body (e.g. the backend's
// `unknown_attribute` / `missing_attribute` validation codes), if present.
export function apiErrorCode(error: unknown): string | null {
  if (
    error instanceof ApiError &&
    error.body &&
    typeof error.body === "object" &&
    "code" in error.body
  ) {
    const { code } = error.body;
    return typeof code === "string" ? code : null;
  }
  return null;
}
