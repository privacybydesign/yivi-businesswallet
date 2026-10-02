import type {
  OutboundStatus,
  SendCredentialRequest,
} from "../api/credential-requests";

// Mirrors the backend's limits (openid4vprequester.normalizeCredentials); the
// server re-enforces them, these are for immediate feedback.
export const MAX_CREDENTIALS = 10;
export const MAX_CLAIMS = 50;

// Plausible address check only; the QERDS provider is the authority.
const ADDRESS_PATTERN = /^[^\s@]+@[^\s@]+\.[^\s@]+$/;
const WHITESPACE = /\s/;

// One credential row of the request form: a type and its claim names as typed.
export interface CredentialRow {
  vct: string;
  claims: string;
}

export type CredentialRequestError =
  | "recipientRequired"
  | "recipientInvalid"
  | "vctRequired"
  | "vctInvalid"
  | "claimInvalid"
  | "tooManyClaims";

export interface CredentialRequestErrors {
  recipient?: CredentialRequestError;
  // Per row, by index; absent when the row is fine.
  rows: (CredentialRequestError | undefined)[];
}

// Claim names separated by commas or newlines, trimmed, empty entries and
// duplicates dropped, order kept.
export function parseClaimList(raw: string): string[] {
  const out: string[] = [];
  for (const part of raw.split(/[,\n]/)) {
    const name = part.trim();
    if (name !== "" && !out.includes(name)) out.push(name);
  }
  return out;
}

export function validateCredentialRequest(
  recipient: string,
  rows: CredentialRow[],
): CredentialRequestErrors {
  const errors: CredentialRequestErrors = { rows: [] };
  const to = recipient.trim();
  if (to === "") errors.recipient = "recipientRequired";
  else if (!ADDRESS_PATTERN.test(to)) errors.recipient = "recipientInvalid";

  errors.rows = rows.map((row) => {
    const vct = row.vct.trim();
    if (vct === "") return "vctRequired";
    if (WHITESPACE.test(vct)) return "vctInvalid";
    const claims = parseClaimList(row.claims);
    if (claims.some((c) => WHITESPACE.test(c))) return "claimInvalid";
    if (claims.length > MAX_CLAIMS) return "tooManyClaims";
    return undefined;
  });
  return errors;
}

export function hasErrors(errors: CredentialRequestErrors): boolean {
  return (
    errors.recipient !== undefined || errors.rows.some((e) => e !== undefined)
  );
}

export function buildSendPayload(
  from: string,
  recipient: string,
  rows: CredentialRow[],
): SendCredentialRequest {
  return {
    ...(from !== "" ? { from } : {}),
    recipient: recipient.trim(),
    credentials: rows.map((row) => ({
      vct: row.vct.trim(),
      claims: parseClaimList(row.claims),
    })),
  };
}

export type StatusTone = "default" | "green" | "amber" | "red" | "blue";

export function outboundStatusTone(status: OutboundStatus): StatusTone {
  switch (status) {
    case "completed":
      return "green";
    case "sent":
      return "amber";
    case "failed":
      return "red";
    case "expired":
      return "default";
  }
}

// A disclosed claim value as text: strings verbatim, anything structured as
// JSON so nested claims stay readable.
export function claimValueText(value: unknown): string {
  if (typeof value === "string") return value;
  if (value === null || value === undefined) return "";
  if (typeof value === "number" || typeof value === "boolean") {
    return String(value);
  }
  return JSON.stringify(value);
}
