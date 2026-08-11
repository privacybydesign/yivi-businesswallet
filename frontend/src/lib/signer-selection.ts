import { SIGNER_KIND } from "../api/signing";
import type { SignerSelection } from "../api/signing";

// Helpers for the create-request form's ordered signer list, which mixes internal
// members with external signees. The backend stays the authority on who may sign
// (an active member, a usable address, nobody twice); these only decide what the
// form lets you add and how a chosen row is keyed.

// isEmailish is a shape check for the "add external signee" button: local@domain.tld
// with no whitespace. It deliberately does not try to be RFC 5322 — the backend
// refuses an unusable address, and a stricter guess here would reject real ones.
export function isEmailish(value: string): boolean {
  const trimmed = value.trim();
  if (trimmed === "" || /\s/.test(trimmed)) return false;
  const at = trimmed.indexOf("@");
  if (at <= 0 || at !== trimmed.lastIndexOf("@")) return false;
  const domain = trimmed.slice(at + 1);
  return (
    domain.includes(".") && !domain.startsWith(".") && !domain.endsWith(".")
  );
}

// signerKey is a chosen signer's stable React key: an external signee has no user
// id, so their address is what identifies the row.
export function signerKey(signer: SignerSelection): string {
  return signer.kind === SIGNER_KIND.internal
    ? `member:${signer.userId}`
    : `external:${signer.email.toLowerCase()}`;
}

// alreadyChosen reports whether an address is already on the list, so the same
// external signee cannot be added twice (the backend would refuse the whole request).
export function alreadyChosen(
  signers: SignerSelection[],
  email: string,
): boolean {
  const wanted = email.trim().toLowerCase();
  return signers.some(
    (s) => s.kind === SIGNER_KIND.external && s.email.toLowerCase() === wanted,
  );
}
