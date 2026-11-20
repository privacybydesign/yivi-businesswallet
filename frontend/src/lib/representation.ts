import type { TFunction } from "i18next";
import type { WalletEnrollment } from "../api/wallet";

// Explicit literal keys keep the strongly-typed t() happy (no dynamic keys).
const KIND_KEYS = {
  bestuurder: "enroll.kind.bestuurder",
  gevolmachtigde: "enroll.kind.gevolmachtigde",
  overig: "enroll.kind.overig",
} as const;

const AUTHORITY_KEYS = {
  sole: "enroll.authority.sole",
  jointly: "enroll.authority.jointly",
  beperkt: "enroll.authority.beperkt",
  volledig: "enroll.authority.volledig",
} as const;

// representationLabel formats the KVK representation into a human string, e.g.
// "Authorised representative (gevolmachtigde) · beperkt (limited authority)".
export function representationLabel(
  result: WalletEnrollment,
  t: TFunction,
): string {
  const kindKey =
    KIND_KEYS[result.representationKind as keyof typeof KIND_KEYS];
  if (kindKey === undefined) {
    return "";
  }
  const authKey =
    AUTHORITY_KEYS[
      result.representationAuthority as keyof typeof AUTHORITY_KEYS
    ];
  return authKey === undefined ? t(kindKey) : `${t(kindKey)} · ${t(authKey)}`;
}

// registeredMessage is what the confirmation says under the title: the role the
// register stated, or, where it stated none, how the wallet starts.
export function registeredMessage(
  result: WalletEnrollment,
  roleKey: "register.yourRole" | "enroll.yourRole",
  t: TFunction,
): string {
  const role = representationLabel(result, t);
  return role === "" ? t("register.startsInTestMode") : t(roleKey, { role });
}

// statedCompanyError is the message for a refused KVK number or company name,
// null for any other error code.
export function statedCompanyError(
  code: string | null,
  t: TFunction,
): string | null {
  if (code === "unknown_kvk") {
    return t("register.unknownKvk");
  }
  if (code === "legal_name_required") {
    return t("register.companyNameRequired");
  }
  return null;
}
