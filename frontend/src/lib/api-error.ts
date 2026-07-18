import { ApiError } from "../api/http";

// errorCode reads the machine-readable `code` field from a structured API error
// body (the backend returns `{ error, code }`), or null when the value isn't a
// structured ApiError. Shared by the callers that branch on specific codes.
export function errorCode(error: unknown): string | null {
  if (
    error instanceof ApiError &&
    typeof error.body === "object" &&
    error.body !== null &&
    "code" in error.body &&
    typeof error.body.code === "string"
  ) {
    return error.body.code;
  }
  return null;
}
