import type { TFunction } from "i18next";
import type { Mandate, MandateAuthority } from "../api/mandates";

// The two tiers of Recital 18, mirroring backend/internal/organization/mandate.go.
// Full strictly contains administrative, which is what bounds a delegation.
export const MANDATE_TYPES = ["full", "administrative"] as const;

// How far a mandate reaches. Only an org-wide mandate carries org-wide
// administrative authority. Narrowing to a single resource domain is not in the
// backend yet, so the grant form does not offer it.
export const MANDATE_SCOPES = ["organization", "department"] as const;

// Derived by the backend on every read, never stored.
export const MANDATE_STATUSES = [
  "pending",
  "active",
  "revoked",
  "expired",
] as const;

export type MandateType = (typeof MANDATE_TYPES)[number];
export type MandateScope = (typeof MANDATE_SCOPES)[number];
export type MandateStatus = (typeof MANDATE_STATUSES)[number];

// Mirrors maxRevocationReasonLength in backend/internal/organization/mandates.go,
// which answers 400 for anything longer.
export const MAX_REVOCATION_REASON_LENGTH = 500;

// The statuses a mandate can still be revoked from: one that is already revoked
// or expired ended on its own date, and the backend answers 409.
const REVOCABLE: readonly string[] = ["pending", "active"];

export function mandateIsRevocable(mandate: Mandate): boolean {
  return REVOCABLE.includes(mandate.status);
}

// Explicit literal keys keep the strongly-typed t() happy (no dynamic keys), and
// let the backend/frontend parity test assert each value the API can serve is
// named. Mirrors lib/representation.ts.
const TYPE_KEYS = {
  full: "mandates.types.full",
  administrative: "mandates.types.administrative",
} as const;

// The grant form's per-tier explanation. Kept beside TYPE_KEYS and looked up the
// same way so a tier the backend gains cannot be described by another tier's hint.
const TYPE_HINT_KEYS = {
  full: "mandates.typeHints.full",
  administrative: "mandates.typeHints.administrative",
} as const;

const SCOPE_KEYS = {
  organization: "mandates.scopes.organization",
  department: "mandates.scopes.department",
} as const;

const STATUS_KEYS = {
  pending: "mandates.statuses.pending",
  active: "mandates.statuses.active",
  revoked: "mandates.statuses.revoked",
  expired: "mandates.statuses.expired",
} as const;

// Each label falls back to the raw value: a tier or status the backend gains
// before this file does should still read as itself rather than as blank.
export function mandateTypeLabel(type: string, t: TFunction): string {
  const key = TYPE_KEYS[type as MandateType];
  return key === undefined ? type : t(key);
}

// No hint at all for a tier this file does not know: an unexplained option beats
// one explained as something it is not.
export function mandateTypeHint(
  type: string,
  t: TFunction,
): string | undefined {
  const key = TYPE_HINT_KEYS[type as MandateType];
  return key === undefined ? undefined : t(key);
}

export function mandateScopeLabel(mandate: Mandate, t: TFunction): string {
  if (mandate.scope === "department") {
    return mandate.scopeDepartmentName ?? t(SCOPE_KEYS.department);
  }
  const key = SCOPE_KEYS[mandate.scope as MandateScope];
  return key === undefined ? mandate.scope : t(key);
}

export function mandateStatusLabel(status: string, t: TFunction): string {
  const key = STATUS_KEYS[status as MandateStatus];
  return key === undefined ? status : t(key);
}

type StatusTone = "green" | "amber" | "red" | "default";

const STATUS_TONES: Record<MandateStatus, StatusTone> = {
  active: "green",
  pending: "amber",
  revoked: "red",
  expired: "default",
};

export function mandateStatusTone(status: string): StatusTone {
  return STATUS_TONES[status as MandateStatus] ?? "default";
}

export interface MandateRow {
  mandate: Mandate;
  // How far down a delegation chain the mandate sits: 0 for a root grant, 1 for
  // one cut from that, and so on.
  depth: number;
}

function childrenByParent(mandates: Mandate[]): Map<string, Mandate[]> {
  const children = new Map<string, Mandate[]>();
  for (const mandate of mandates) {
    const parent = mandate.parentMandateId;
    if (parent === null) {
      continue;
    }
    const siblings = children.get(parent);
    if (siblings) {
      siblings.push(mandate);
    } else {
      children.set(parent, [mandate]);
    }
  }
  return children;
}

// mandateLineage orders the register into its delegation chains: every mandate
// followed by what was cut from it, depth-first, so a chain reads top-down and a
// cascade is legible from the register rather than only from the audit log.
//
// Every mandate comes out exactly once. A row whose parent is not in the list is
// shown as a root instead of being dropped, because the register is the audit
// surface and a mandate that vanished from it would hide a grant.
export function mandateLineage(mandates: Mandate[]): MandateRow[] {
  const children = childrenByParent(mandates);
  const known = new Set(mandates.map((mandate) => mandate.id));
  const rows: MandateRow[] = [];
  const placed = new Set<string>();

  const walk = (mandate: Mandate, depth: number): void => {
    if (placed.has(mandate.id)) {
      return;
    }
    placed.add(mandate.id);
    rows.push({ mandate, depth });
    for (const child of children.get(mandate.id) ?? []) {
      walk(child, depth + 1);
    }
  };

  for (const mandate of mandates) {
    if (
      mandate.parentMandateId === null ||
      !known.has(mandate.parentMandateId)
    ) {
      walk(mandate, 0);
    }
  }
  // Nothing the backend serves can form a cycle (a parent always predates its
  // child), but a chain the walk above could not enter still has to be listed.
  for (const mandate of mandates) {
    walk(mandate, 0);
  }
  return rows;
}

// mandateCascade lists the mandates that revoking `id` would end as well: its
// descendants down the delegation chain, minus the ones that already ended. The
// backend walks the whole subtree but only touches rows that are neither revoked
// nor expired, so counting an ended descendant would overstate the warning.
export function mandateCascade(mandates: Mandate[], id: string): Mandate[] {
  const children = childrenByParent(mandates);
  const reached: Mandate[] = [];
  const seen = new Set<string>([id]);
  const queue = [id];

  while (queue.length > 0) {
    const next = queue.shift();
    if (next === undefined) {
      break;
    }
    for (const child of children.get(next) ?? []) {
      if (seen.has(child.id)) {
        continue;
      }
      seen.add(child.id);
      queue.push(child.id);
      if (mandateIsRevocable(child)) {
        reached.push(child);
      }
    }
  }
  return reached;
}

// Why the grant flow is or is not offered.
export type MandateGrantAvailability =
  | "available"
  | "jointAuthority"
  | "noAuthority";

// mandateGrantAvailability decides whether to offer the grant and revoke flows.
// It follows the backend's RequireMandateAuthority gate, with one hold of its
// own: a director registered `jointly` cannot bind the company alone, and the
// backend does not yet honour that column, so offering them a grant flow would
// record a grant no single director could make. Holding a full mandate of their
// own is a basis that does not depend on the registration, so it lifts the hold.
export function mandateGrantAvailability(
  authority: MandateAuthority,
): MandateGrantAvailability {
  if (!authority.mayGrant) {
    return "noAuthority";
  }
  if (authority.jointAuthority && !authority.fullMandate) {
    return "jointAuthority";
  }
  return "available";
}

// mandateWindowIsEmpty reports a validity window with nothing left in it, which
// the backend answers 400 for. An unset start means now, because validateGrant
// fills valid_from in — so an end in the past closes the window even with no
// start named, and checking only the both-set case round-trips into prose this
// screen cannot translate.
export function mandateWindowIsEmpty(
  from: string | undefined,
  until: string | undefined,
  now: string,
): boolean {
  return until !== undefined && until <= (from ?? now);
}

// isoFromLocalInput turns a datetime-local field's value into the RFC 3339
// instant the API takes. Empty stays empty: the backend's own defaults (now, and
// open-ended) are what an unset field means.
export function isoFromLocalInput(value: string): string | undefined {
  if (value.trim() === "") {
    return undefined;
  }
  const date = new Date(value);
  return Number.isNaN(date.getTime()) ? undefined : date.toISOString();
}
