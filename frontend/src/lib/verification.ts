import type { Verification, VerificationStatus } from "../api/verifications";

// Tones for the status and verdict tags of a verification session.
export type VerificationTone = "default" | "green" | "amber" | "red";

// parseClaimList turns the template form's free text ("vergunningnummer, markt")
// into the claim names the backend expects: split on commas or newlines,
// trimmed, empties dropped, duplicates removed in first-seen order.
export function parseClaimList(raw: string): string[] {
  const seen = new Set<string>();
  const claims: string[] = [];
  for (const part of raw.split(/[,\n]/)) {
    const claim = part.trim();
    if (claim === "" || seen.has(claim)) {
      continue;
    }
    seen.add(claim);
    claims.push(claim);
  }
  return claims;
}

// statusTone colours the session status: waiting is amber, expired is neutral,
// answered takes the verdict's colour.
export function statusTone(
  status: VerificationStatus,
  valid: boolean | undefined,
): VerificationTone {
  switch (status) {
    case "pending":
      return "amber";
    case "expired":
      return "default";
    case "completed":
      return valid ? "green" : "red";
  }
}

// verdictTone is the result card's colour: green only when every check passed.
export function verdictTone(
  session: Pick<Verification, "status" | "valid">,
): VerificationTone {
  if (session.status !== "completed") {
    return "default";
  }
  return session.valid ? "green" : "red";
}
