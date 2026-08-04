// Status, sectioning and filtering for the held-credential (Wallet) view.
//
// The backend serves the two facts the holder engine stores — the credential's own
// expiry and whether its last observed Token Status List bit read something other
// than valid — and the view turns those into the badge it shows. "Expiring soon" is
// a presentation window, not a state the engine knows, so it is derived here.

import type { TFunction } from "i18next";
import type { HeldAttestation, HeldSource } from "../api/attestations";
import { credentialDisplayName } from "./credential-display";

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

export const HELD_SOURCE_FILTERS = [
  "",
  "qerds",
  "openid4vci",
  "bootstrap",
] as const;

export type HeldSourceFilter = (typeof HELD_SOURCE_FILTERS)[number];

// The validity fields the status derives from — a subset of HeldAttestation, so
// the detail view (which carries the same two fields) can badge a credential too.
export interface HeldValidity {
  expiresAt?: string;
  revoked: boolean;
}

// heldStatus derives the badge for one held credential. Revoked outranks expiry:
// a revoked credential is unusable whatever its exp claim says. An unparseable or
// absent expiry means "does not expire", which is what a credential with no exp
// claim (and a row the engine knows no expiry for) is.
export function heldStatus(credential: HeldValidity, now: Date): HeldStatus {
  if (credential.revoked) {
    return "revoked";
  }
  if (!credential.expiresAt) {
    return "valid";
  }
  const expiresAt = Date.parse(credential.expiresAt);
  if (Number.isNaN(expiresAt)) {
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

// heldSearchText is what the search box matches on: the name shown on the card,
// the credential type and the issuer — the three things a card puts on screen.
function heldSearchText(credential: HeldAttestation): string {
  const name = credential.displayName || credentialDisplayName(credential.vct);
  return `${name}\n${credential.vct}\n${credential.issuer}`.toLowerCase();
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

function heldMatchesSource(
  source: HeldSource,
  filter: HeldSourceFilter,
): boolean {
  return filter === "" || source === filter;
}

export interface HeldFilters {
  query: string;
  status: HeldStatusFilter;
  source: HeldSourceFilter;
}

// A held credential paired with the status the view badges it with, so a card
// does not re-derive it.
export interface HeldCredentialWithStatus {
  credential: HeldAttestation;
  status: HeldStatus;
}

// heldSections applies the search and filters, then splits what is left into the
// two sections the view stacks: what needs attention (revoked, expired, expiring
// soon) and what is simply valid. Both keep the backend's order (most recently
// received first).
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
      !heldMatchesStatus(status, filters.status) ||
      !heldMatchesSource(credential.source, filters.source)
    ) {
      continue;
    }
    (heldNeedsAttention(status) ? attention : valid).push({
      credential,
      status,
    });
  }
  return { attention, valid };
}
