// Status, sectioning and filtering for the held-credential (Wallet) view.
//
// The backend serves the two facts the holder engine stores — the credential's own
// expiry and whether its last observed Token Status List bit read something other
// than valid — and the view turns those into the badge it shows. "Expiring soon" is
// a presentation window, not a state the engine knows, so it is derived here.

import type { TFunction } from "i18next";
import type { HeldAttestation } from "../api/attestations";
import { credentialDisplayName } from "./credential-display";
import { fullName } from "./name";

// How long before a credential expires it moves to "Needs attention", so the org
// has time to have it re-issued before it stops working.
export const EXPIRING_SOON_DAYS = 30;

const MS_PER_DAY = 86_400_000;

export type HeldStatus = "valid" | "expiringSoon" | "expired" | "revoked";

// The filters the toolbar offers: "" is no filter. "attention" groups everything
// the "Needs attention" section holds, which is what someone scanning for work
// actually wants; the individual states are there to narrow further.
export const HELD_STATUS_FILTERS = [
  "",
  "attention",
  "revoked",
  "expired",
  "expiringSoon",
  "valid",
] as const;

export type HeldStatusFilter = (typeof HELD_STATUS_FILTERS)[number];

// The validity fields the status derives from — a subset of HeldAttestation, so
// the detail view (which carries the same two fields) can badge a credential too.
export interface HeldValidity {
  expiresAt?: string;
  revoked: boolean;
}

// heldExpiryAt reads a credential's expiry as a timestamp, or null when there is
// none the view can use: absent, or a value that does not parse. Both mean "does
// not expire" here, so the badge and the copy read one date the same way.
export function heldExpiryAt(credential: HeldValidity): number | null {
  if (!credential.expiresAt) {
    return null;
  }
  const expiresAt = Date.parse(credential.expiresAt);
  return Number.isNaN(expiresAt) ? null : expiresAt;
}

// heldExpiryIsPast reports whether the expiry has already passed, which is what the
// expiry copy picks its tense from. It is deliberately not derived from the badge:
// revoked outranks expiry, so a revoked credential whose exp claim is months past
// still badges "revoked", and a tense read off that badge would call the date
// upcoming. Null (no usable expiry) is not past — there is no date to phrase.
export function heldExpiryIsPast(credential: HeldValidity, now: Date): boolean {
  const expiresAt = heldExpiryAt(credential);
  return expiresAt !== null && expiresAt <= now.getTime();
}

// heldStatus derives the badge for one held credential. Revoked outranks expiry:
// a revoked credential is unusable whatever its exp claim says. An unparseable or
// absent expiry means "does not expire", which is what a credential with no exp
// claim (and a row the engine knows no expiry for) is.
export function heldStatus(credential: HeldValidity, now: Date): HeldStatus {
  if (credential.revoked) {
    return "revoked";
  }
  const expiresAt = heldExpiryAt(credential);
  if (expiresAt === null) {
    return "valid";
  }
  const remaining = expiresAt - now.getTime();
  if (remaining <= 0) {
    return "expired";
  }
  return remaining <= EXPIRING_SOON_DAYS * MS_PER_DAY
    ? "expiringSoon"
    : "valid";
}

// heldNeedsAttention reports whether a status belongs in the "Needs attention"
// section: the credential is revoked, has expired, or is about to.
export function heldNeedsAttention(status: HeldStatus): boolean {
  return status !== "valid";
}

// The Tag tones each status is badged with (mirrors ui/tag.tsx). Expired is neutral
// rather than red: the credential is spent, not rejected — which is how the issued
// ledger tones "expired" too.
export const HELD_STATUS_TONES: Record<
  HeldStatus,
  "default" | "green" | "amber" | "red"
> = {
  valid: "green",
  expiringSoon: "amber",
  expired: "default",
  revoked: "red",
};

export function heldStatusLabel(status: HeldStatus, t: TFunction): string {
  switch (status) {
    case "valid":
      return t("attestations.held.status.valid");
    case "expiringSoon":
      return t("attestations.held.status.expiringSoon");
    case "expired":
      return t("attestations.held.status.expired");
    case "revoked":
      return t("attestations.held.status.revoked");
  }
}

// heldSourceLabel names how a credential arrived. A source the frontend has no name
// for yet renders as its raw identifier rather than a blank.
export function heldSourceLabel(source: string, t: TFunction): string {
  switch (source) {
    case "qerds":
      return t("attestations.held.sources.qerds");
    case "openid4vci":
      return t("attestations.held.sources.openid4vci");
    case "bootstrap":
      return t("attestations.held.sources.bootstrap");
    default:
      return source;
  }
}

// heldSearchText is what the search box matches on: the name shown on the card, the
// credential type and the issuer — the things a card puts on screen. Both the
// translated issuer name and the raw identifier are included so a search matches
// whichever the card is showing (issuerName falls back to issuer server-side).
function heldSearchText(credential: HeldAttestation): string {
  const name = credential.displayName || credentialDisplayName(credential.vct);
  return `${name}\n${credential.vct}\n${credential.issuerName}\n${credential.issuer}`.toLowerCase();
}

// heldMatchesQuery reports whether a credential matches a search term. Terms are
// matched independently so "kvk 2026" finds a KVK credential from a 2026 issuer
// URL regardless of the order they were typed in. An empty query matches
// everything.
export function heldMatchesQuery(
  credential: HeldAttestation,
  query: string,
): boolean {
  const terms = query.toLowerCase().split(/\s+/).filter(Boolean);
  if (terms.length === 0) {
    return true;
  }
  const haystack = heldSearchText(credential);
  return terms.every((term) => haystack.includes(term));
}

function heldMatchesStatus(
  status: HeldStatus,
  filter: HeldStatusFilter,
): boolean {
  if (filter === "") {
    return true;
  }
  if (filter === "attention") {
    return heldNeedsAttention(status);
  }
  return status === filter;
}

export interface HeldFilters {
  query: string;
  status: HeldStatusFilter;
}

// A held credential paired with the status the view badges it with, so a card
// does not re-derive it.
export interface HeldCredentialWithStatus {
  credential: HeldAttestation;
  status: HeldStatus;
}

// heldSections applies the search and filters, then splits what is left into the
// two sections the view stacks: what needs attention (revoked, then expired, then
// expiring soon) and what is simply valid. Within one state both keep the
// backend's order (most recently received first).
export function heldSections(
  credentials: HeldAttestation[],
  filters: HeldFilters,
  now: Date,
): {
  attention: HeldCredentialWithStatus[];
  valid: HeldCredentialWithStatus[];
} {
  const attention: HeldCredentialWithStatus[] = [];
  const valid: HeldCredentialWithStatus[] = [];
  for (const credential of credentials) {
    const status = heldStatus(credential, now);
    if (
      !heldMatchesQuery(credential, filters.query) ||
      !heldMatchesStatus(status, filters.status)
    ) {
      continue;
    }
    (heldNeedsAttention(status) ? attention : valid).push({
      credential,
      status,
    });
  }
  // The worst first: what can no longer be used, then what is about to stop.
  // The sort is stable, so the backend's order holds within one state.
  attention.sort((a, b) => ATTENTION_RANK[a.status] - ATTENTION_RANK[b.status]);
  return { attention, valid };
}

const ATTENTION_RANK: Record<HeldStatus, number> = {
  revoked: 0,
  expired: 1,
  expiringSoon: 2,
  valid: 3,
};

// The status chips of the Wallet view: "" is every credential.
export const HELD_CHIP_FILTERS = [
  "",
  "valid",
  "expiringSoon",
  "expired",
  "revoked",
] as const satisfies readonly HeldStatusFilter[];

// How many credentials each chip would show.
export function heldStatusCounts(
  credentials: readonly HeldValidity[],
  now: Date,
): Record<(typeof HELD_CHIP_FILTERS)[number], number> {
  const counts = { "": 0, valid: 0, expiringSoon: 0, expired: 0, revoked: 0 };
  for (const credential of credentials) {
    counts[""]++;
    counts[heldStatus(credential, now)]++;
  }
  return counts;
}

// Whole days until the credential expires, negative once it has (days since);
// null when it does not expire. A part of a day counts towards the nearer end,
// so "expires in 1 day" never reads for something that has hours left.
export function heldDaysToExpiry(
  credential: HeldValidity,
  now: Date,
): number | null {
  const expiresAt = heldExpiryAt(credential);
  if (expiresAt === null) {
    return null;
  }
  const days = (expiresAt - now.getTime()) / MS_PER_DAY;
  return days >= 0 ? Math.floor(days) : Math.ceil(days);
}

// One line of a held credential's history.
export interface HeldHistoryEntry {
  at: string;
  kind: "received" | "statusChanged" | "statusChecked" | "removed" | "other";
  action: string;
  actor?: string;
  sender?: string;
  revoked?: boolean;
}

interface HistoryEvent {
  occurredAt: string;
  action: string;
  metadata: Record<string, unknown>;
  actor: {
    preferredName?: string | null;
    givenNames: string;
    lastName: string;
  } | null;
  detailHidden?: boolean;
}

function afterField(event: HistoryEvent, key: string): unknown {
  const after = event.metadata.after;
  return typeof after === "object" && after !== null
    ? (after as Record<string, unknown>)[key]
    : undefined;
}

// heldHistory merges a credential's audit trail with what the credential itself
// records: its receipt (for one received before the trail recorded that) and
// its last status check, oldest first.
export function heldHistory(
  events: readonly HistoryEvent[],
  credential: { receivedAt: string; statusCheckedAt?: string },
): HeldHistoryEntry[] {
  const entries: HeldHistoryEntry[] = events.map((event) => {
    // Named as the audit log names its actors.
    const actor = event.actor
      ? fullName({
          ...event.actor,
          preferredName: event.actor.preferredName ?? null,
        })
      : undefined;
    switch (event.action) {
      case "attestation.held_received": {
        const sender = afterField(event, "sender");
        return {
          at: event.occurredAt,
          kind: "received",
          action: event.action,
          actor,
          sender:
            typeof sender === "string" && sender !== "" ? sender : undefined,
        };
      }
      case "attestation.held_status_changed":
        return {
          at: event.occurredAt,
          kind: "statusChanged",
          action: event.action,
          // Which way it moved is metadata, withheld from an ordinary member.
          revoked: event.detailHidden
            ? undefined
            : afterField(event, "revoked") === true,
        };
      case "attestation.held_deleted":
        return {
          at: event.occurredAt,
          kind: "removed",
          action: event.action,
          actor,
        };
      default:
        return {
          at: event.occurredAt,
          kind: "other",
          action: event.action,
          actor,
        };
    }
  });
  if (!entries.some((e) => e.kind === "received")) {
    entries.push({ at: credential.receivedAt, kind: "received", action: "" });
  }
  if (credential.statusCheckedAt) {
    entries.push({
      at: credential.statusCheckedAt,
      kind: "statusChecked",
      action: "",
    });
  }
  return entries.sort((a, b) => Date.parse(a.at) - Date.parse(b.at));
}

// The formats irmago stores an SD-JWT VC under.
const SD_JWT_FORMATS = new Set(["dc+sd-jwt", "vc+sd-jwt"]);

// A credential format as people know it; an unknown one shows as is.
export function heldFormatLabel(format: string): string {
  if (SD_JWT_FORMATS.has(format)) {
    return "SD-JWT VC";
  }
  return format || "—";
}
