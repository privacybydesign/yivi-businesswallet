// NO_DATE_OF_BIRTH is the backend's code for a VOG check it cannot run because
// the member has no date of birth on file (organization.ErrVogNoDateOfBirth).
const NO_DATE_OF_BIRTH = "no_date_of_birth";

// needsIdentityFirst decides whether the VOG page must ask the member to
// identify before (or together with) a VOG check: the org detail says so
// up-front (needsIdentity), or a just-attempted check was refused for that
// reason - the latter covers a stale org detail.
export function needsIdentityFirst(
  vog: { needsIdentity: boolean } | null | undefined,
  errorCodes: readonly (string | null)[],
): boolean {
  if (vog?.needsIdentity) return true;
  return errorCodes.includes(NO_DATE_OF_BIRTH);
}

// IDENTITY_REJECTION_CODES are the re-identification codes the identity half
// of a combined disclosure can fail with; they get re-identification copy
// (lib/reidentify-error.ts) rather than the generic VOG failure line.
const IDENTITY_REJECTION_CODES: readonly string[] = [
  "email_mismatch",
  "name_mismatch",
  "credential_too_old",
  "disclosure_failed",
];

export function isIdentityRejection(code: string | null): boolean {
  return code !== null && IDENTITY_REJECTION_CODES.includes(code);
}
