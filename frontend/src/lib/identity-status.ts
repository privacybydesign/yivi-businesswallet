import type { TFunction } from "i18next";

// The re-identification status a member row or detail card shows. The backend
// derives it (internal/organization/identity_status.go) and the value travels
// as a plain string, so an unknown status renders neutrally rather than
// breaking the screen.
export type IdentityStatusTone = "default" | "green" | "amber" | "red" | "blue";

const TONES: Record<string, IdentityStatusTone> = {
  verified: "green",
  due_soon: "amber",
  overdue: "red",
  requested: "blue",
  never: "default",
};

export function identityStatusTone(status: string): IdentityStatusTone {
  return TONES[status] ?? "default";
}

// identityStatusLabel is the badge text. An unknown status falls back to the raw
// value: a status the backend adds before the frontend knows it should read as
// itself, not as "verified".
export function identityStatusLabel(status: string, t: TFunction): string {
  switch (status) {
    case "verified":
      return t("members.identity.verified");
    case "due_soon":
      return t("members.identity.dueSoon");
    case "overdue":
      return t("members.identity.overdue");
    case "requested":
      return t("members.identity.requested");
    case "never":
      return t("members.identity.never");
    default:
      return status;
  }
}

// identityStatusHint is the badge's title attribute: the date behind the status,
// so a list row explains itself on hover without a second column. Empty when the
// status carries no date to show.
export function identityStatusHint(
  member: {
    identityStatus: string;
    identityDueAt: string | null;
    identityRequestedAt: string | null;
    identityVerifiedAt: string | null;
  },
  t: TFunction,
  formatDate: (iso: string) => string,
): string {
  switch (member.identityStatus) {
    case "requested":
      return member.identityRequestedAt
        ? t("members.identity.requestedOn", {
            date: formatDate(member.identityRequestedAt),
          })
        : "";
    case "due_soon":
    case "overdue":
      return member.identityDueAt
        ? t("members.identity.dueOn", {
            date: formatDate(member.identityDueAt),
          })
        : "";
    case "verified":
      return member.identityVerifiedAt
        ? t("members.verifiedOn", {
            date: formatDate(member.identityVerifiedAt),
          })
        : "";
    case "never":
      return t("members.identity.neverHint");
    default:
      return "";
  }
}

// requestableIdentity reports whether asking this member to re-identify makes
// sense: an active member who is not already being asked. It is what enables
// the single and bulk "Request identification" actions.
export function requestableIdentity(member: {
  status: string;
  identityStatus: string;
}): boolean {
  return member.status === "active" && member.identityStatus !== "requested";
}

// memberTypeLabel maps the backend's member_type onto its copy, falling back to
// the raw value so a type this build does not know still reads as itself.
export function memberTypeLabel(memberType: string, t: TFunction): string {
  switch (memberType) {
    case "employee":
      return t("members.memberType.employee");
    case "external":
      return t("members.memberType.external");
    default:
      return memberType;
  }
}

// The statuses worth interrupting a member with the in-app banner. "never" is
// deliberately absent: it is the state every membership starts in until a
// policy exists, so a banner on it would page everybody the day the feature
// ships.
const BANNER_STATUSES = ["requested", "due_soon", "overdue"];

export function needsIdentityBanner(
  identity: { status: string } | undefined,
): boolean {
  return identity !== undefined && BANNER_STATUSES.includes(identity.status);
}
