import type { TFunction } from "i18next";
import { errorCode } from "./api-error";

export interface InviteErrorContent {
  title: string;
  body: string;
}

type InviteErrorKind =
  | "nameMismatch"
  | "emailMismatch"
  | "alreadyMember"
  | "identityRejected"
  | "disclosureFailed"
  | "expired"
  | "notFound"
  | "failed";

function kindOf(error: unknown): InviteErrorKind {
  switch (errorCode(error)) {
    case "name_mismatch":
      return "nameMismatch";
    case "email_mismatch":
      return "emailMismatch";
    case "already_member":
      return "alreadyMember";
    case "identity_rejected":
      return "identityRejected";
    case "disclosure_failed":
      return "disclosureFailed";
    case "invitation_expired":
      return "expired";
    case "invitation_not_found":
      return "notFound";
    default:
      return "failed";
  }
}

// Maps an invitation failure to a user-facing title + body, shared by the
// invitation link page and the post-login accept flow.
export function inviteError(error: unknown, t: TFunction): InviteErrorContent {
  switch (kindOf(error)) {
    case "nameMismatch":
      return {
        title: t("inviteAccept.errors.nameMismatch.title"),
        body: t("inviteAccept.errors.nameMismatch.body"),
      };
    case "emailMismatch":
      return {
        title: t("inviteAccept.errors.emailMismatch.title"),
        body: t("inviteAccept.errors.emailMismatch.body"),
      };
    case "alreadyMember":
      return {
        title: t("inviteAccept.errors.alreadyMember.title"),
        body: t("inviteAccept.errors.alreadyMember.body"),
      };
    case "identityRejected":
      return {
        title: t("inviteAccept.errors.identityRejected.title"),
        body: t("inviteAccept.errors.identityRejected.body"),
      };
    case "disclosureFailed":
      return {
        title: t("inviteAccept.errors.disclosureFailed.title"),
        body: t("inviteAccept.errors.disclosureFailed.body"),
      };
    case "expired":
      return {
        title: t("inviteAccept.errors.expired.title"),
        body: t("inviteAccept.errors.expired.body"),
      };
    case "notFound":
      return {
        title: t("inviteAccept.errors.notFound.title"),
        body: t("inviteAccept.errors.notFound.body"),
      };
    case "failed":
      return {
        title: t("inviteAccept.errors.failed.title"),
        body: t("inviteAccept.errors.failed.body"),
      };
  }
}
