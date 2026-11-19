import type { TFunction } from "i18next";
import { errorCode } from "./api-error";

export interface ReidentifyErrorContent {
  title: string;
  body: string;
}

// Maps a re-identification failure onto copy that says what to do about it: a
// dead link, the wrong wallet, a name that no longer matches, or a credential
// the organization considers too old (its freshness policy). Mirrors
// lib/invite-error.ts, which does the same job for the accept flow.
export function reidentifyError(
  error: unknown,
  t: TFunction,
): ReidentifyErrorContent {
  switch (errorCode(error)) {
    case "reidentify_link_not_found":
      return {
        title: t("reidentify.errors.linkTitle"),
        body: t("reidentify.errors.linkBody"),
      };
    case "email_mismatch":
      return {
        title: t("reidentify.errors.emailTitle"),
        body: t("reidentify.errors.emailBody"),
      };
    case "name_mismatch":
      return {
        title: t("reidentify.errors.nameTitle"),
        body: t("reidentify.errors.nameBody"),
      };
    case "credential_too_old":
      return {
        title: t("reidentify.errors.staleTitle"),
        body: t("reidentify.errors.staleBody"),
      };
    default:
      return {
        title: t("reidentify.errors.genericTitle"),
        body: t("reidentify.errors.genericBody", {
          message: error instanceof Error ? error.message : "",
        }),
      };
  }
}
