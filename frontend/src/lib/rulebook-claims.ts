// Claims for a WE BUILD rulebook schema are edited as JSON: the rulebooks nest
// objects and arrays that a field per attribute cannot hold. The backend
// validates them against the rulebook's JSON schema on issue.

const JSON_INDENT = 2;

export type ParsedClaims =
  | { ok: true; claims: Record<string, unknown> }
  | { ok: false; reason: "invalidJson" | "notAnObject" };

export function formatClaims(claims: Record<string, unknown>): string {
  return JSON.stringify(claims, null, JSON_INDENT);
}

export function parseClaims(text: string): ParsedClaims {
  let value: unknown;
  try {
    value = JSON.parse(text);
  } catch {
    return { ok: false, reason: "invalidJson" };
  }
  if (typeof value !== "object" || value === null || Array.isArray(value)) {
    return { ok: false, reason: "notAnObject" };
  }
  return { ok: true, claims: value as Record<string, unknown> };
}

// A one-line rendering of any claim value, for summaries.
export function claimSummary(value: unknown): string {
  if (value === undefined || value === null || value === "") {
    return "—";
  }
  return typeof value === "string" ? value : JSON.stringify(value);
}

// Keys a claim array's items by their content, numbering repeats, since claim
// values carry no id of their own.
export function keyedItems(items: unknown[]): { key: string; item: unknown }[] {
  const seen = new Map<string, number>();
  return items.map((item) => {
    const content = JSON.stringify(item) ?? "";
    const count = (seen.get(content) ?? 0) + 1;
    seen.set(content, count);
    return { key: `${content}#${count}`, item };
  });
}
