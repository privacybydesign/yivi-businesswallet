import type {
  CredentialType,
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

// typeKey of a row whose type is typed by hand: a credential from an issuer
// outside the catalogue (e.g. the KVK registration). A catalogue key always
// contains "::", so it never collides. An empty typeKey is a row nobody chose a
// type for yet.
export const OTHER_TYPE = "other";

// One credential row of the request form. A catalogue row names its type by
// typeKey (catalogKey) and asks for the attributes in picked; an OTHER_TYPE
// row carries the vct and claim names as typed.
export interface CredentialRow {
  typeKey: string;
  picked: string[];
  vct: string;
  claims: string;
}

export const EMPTY_ROW: CredentialRow = {
  typeKey: "",
  picked: [],
  vct: "",
  claims: "",
};

// catalogKey identifies a catalogue entry. Two issuers may design the same
// vct, so the issuer is part of it.
export function catalogKey(type: CredentialType): string {
  return `${type.issuer}::${type.vct}`;
}

export function findType(
  catalog: CredentialType[],
  typeKey: string,
): CredentialType | undefined {
  return catalog.find((type) => catalogKey(type) === typeKey);
}

// The credential a row asks for, or undefined when its catalogue entry is gone.
export function rowCredential(
  row: CredentialRow,
  catalog: CredentialType[],
): { vct: string; claims: string[] } | undefined {
  if (row.typeKey === OTHER_TYPE) {
    return { vct: row.vct.trim(), claims: parseClaimList(row.claims) };
  }
  const type = findType(catalog, row.typeKey);
  if (!type) return undefined;
  const keys = type.attributes.map((a) => a.key);
  // Catalogue order, not the order the boxes were ticked.
  return { vct: type.vct, claims: keys.filter((k) => row.picked.includes(k)) };
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
  catalog: CredentialType[],
): CredentialRequestErrors {
  const errors: CredentialRequestErrors = { rows: [] };
  const to = recipient.trim();
  if (to === "") errors.recipient = "recipientRequired";
  else if (!ADDRESS_PATTERN.test(to)) errors.recipient = "recipientInvalid";

  errors.rows = rows.map((row) => {
    const credential = rowCredential(row, catalog);
    if (!credential || credential.vct === "") return "vctRequired";
    if (WHITESPACE.test(credential.vct)) return "vctInvalid";
    if (credential.claims.some((c) => WHITESPACE.test(c))) {
      return "claimInvalid";
    }
    if (credential.claims.length > MAX_CLAIMS) return "tooManyClaims";
    return undefined;
  });
  return errors;
}

export function hasErrors(errors: CredentialRequestErrors): boolean {
  return (
    errors.recipient !== undefined || errors.rows.some((e) => e !== undefined)
  );
}

// buildSendPayload assumes validateCredentialRequest passed: a row whose
// catalogue entry is gone is dropped rather than sent without a type.
export function buildSendPayload(
  from: string,
  recipient: string,
  rows: CredentialRow[],
  catalog: CredentialType[],
): SendCredentialRequest {
  return {
    ...(from !== "" ? { from } : {}),
    recipient: recipient.trim(),
    credentials: rows.flatMap((row) => {
      const credential = rowCredential(row, catalog);
      return credential ? [credential] : [];
    }),
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
