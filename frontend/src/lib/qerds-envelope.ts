// The QERDS bodies the wallet sends machine to machine (a declined offer, a
// revoked credential) are JSON envelopes that carry a human-readable `message`.
const ENVELOPE_TYPE_PREFIX = "eaa-credential-";

// envelopeMessage returns the readable message of a wallet envelope, or null
// for any other body, which is then shown as it is.
export function envelopeMessage(body: string): string | null {
  let parsed: unknown;
  try {
    parsed = JSON.parse(body);
  } catch {
    return null;
  }
  if (typeof parsed !== "object" || parsed === null) {
    return null;
  }
  const { type, message } = parsed as Record<string, unknown>;
  if (
    typeof type !== "string" ||
    !type.startsWith(ENVELOPE_TYPE_PREFIX) ||
    typeof message !== "string" ||
    message === ""
  ) {
    return null;
  }
  return message;
}
