import type { TFunction } from "i18next";

// The VOG screening status a member row or detail card shows. The backend
// derives it (internal/organization/screening_status.go) and the value travels
// as a plain string, so an unknown status renders neutrally rather than
// breaking the screen.
export type ScreeningStatusTone =
  | "default"
  | "green"
  | "amber"
  | "red"
  | "blue";

const TONES: Record<string, ScreeningStatusTone> = {
  valid: "green",
  expiring: "amber",
  expired: "red",
  rejected: "red",
  recheck_required: "amber",
  requested: "blue",
  none: "default",
  not_required: "default",
};

export function screeningStatusTone(status: string): ScreeningStatusTone {
  return TONES[status] ?? "default";
}

export function screeningStatusLabel(status: string, t: TFunction): string {
  switch (status) {
    case "valid":
      return t("members.vog.valid");
    case "expiring":
      return t("members.vog.expiring");
    case "expired":
      return t("members.vog.expired");
    case "rejected":
      return t("members.vog.rejected");
    case "recheck_required":
      return t("members.vog.recheckRequired");
    case "requested":
      return t("members.vog.requested");
    case "none":
      return t("members.vog.none");
    case "not_required":
      return t("members.vog.notRequired");
    default:
      return status;
  }
}

export function screeningStatusHint(
  member: {
    vogStatus: string;
    vogValidUntil: string | null;
    vogRequestedAt: string | null;
  },
  t: TFunction,
  formatDate: (iso: string) => string,
): string {
  switch (member.vogStatus) {
    case "requested":
      return member.vogRequestedAt
        ? t("members.vog.requestedOn", {
            date: formatDate(member.vogRequestedAt),
          })
        : "";
    case "valid":
    case "expiring":
      return member.vogValidUntil
        ? t("members.vog.validUntil", {
            date: formatDate(member.vogValidUntil),
          })
        : "";
    case "expired":
      return member.vogValidUntil
        ? t("members.vog.expiredOn", {
            date: formatDate(member.vogValidUntil),
          })
        : "";
    default:
      return "";
  }
}

// screeningResultTone / screeningResultLabel are for one screening attempt's
// raw result (member_screenings.result: valid/rejected/mismatch/
// insufficient_scope), a different vocabulary from the member-level derived
// status above - used by the VOG history table, never by the status tag.
export function screeningResultTone(result: string): ScreeningStatusTone {
  return result === "valid" ? "green" : "red";
}

export function screeningResultLabel(result: string, t: TFunction): string {
  switch (result) {
    case "valid":
      return t("members.vog.valid");
    case "mismatch":
      return t("memberDetail.vogHistory.resultMismatch");
    case "insufficient_scope":
      return t("memberDetail.vogHistory.resultInsufficientScope");
    case "rejected":
      return t("members.vog.rejected");
    default:
      return result;
  }
}

// requestableVog reports whether asking this member to submit a VOG makes
// sense: an active member for whom screening is required, who is not already
// being asked.
export function requestableVog(member: {
  status: string;
  vogStatus: string;
}): boolean {
  return (
    member.status === "active" &&
    member.vogStatus !== "requested" &&
    member.vogStatus !== "not_required"
  );
}

// The statuses worth interrupting a member with the in-app banner. "none" is
// included (unlike identity's "never"): a VOG is only ever "required" once an
// org configures it, so there is no mass-banner-on-launch risk the way there is
// for re-identification, which every existing member starts in "never".
const BANNER_STATUSES = [
  "requested",
  "none",
  "expiring",
  "expired",
  "rejected",
  "recheck_required",
];

export function needsVogBanner(vog: { status: string } | undefined): boolean {
  return vog !== undefined && BANNER_STATUSES.includes(vog.status);
}
