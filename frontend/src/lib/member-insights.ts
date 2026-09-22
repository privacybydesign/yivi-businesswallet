// The order the dashboard lists identity and screening statuses in: from
// "fine" to "needs attention", so the stacked bar reads left to right as the
// share of the org that is in order.
export const IDENTITY_STATUS_ORDER = [
  "verified",
  "due_soon",
  "requested",
  "overdue",
  "never",
] as const;

export const SCREENING_STATUS_ORDER = [
  "valid",
  "expiring",
  "recheck_required",
  "requested",
  "rejected",
  "expired",
  "none",
  "not_required",
] as const;

// Statuses that mean "this member has no current, verified identity": the
// dashboard's "not identified" headline. due_soon is still identified.
const NOT_IDENTIFIED = ["never", "requested", "overdue"];

// Statuses that mean "this member's VOG needs something from them": the
// dashboard's screening headline. expiring is still valid today, and
// not_required is nobody's problem.
const SCREENING_ATTENTION = [
  "none",
  "requested",
  "rejected",
  "expired",
  "recheck_required",
];

function sum(
  counts: Record<string, number>,
  statuses: readonly string[],
): number {
  return statuses.reduce((total, status) => total + (counts[status] ?? 0), 0);
}

export function notIdentifiedCount(identity: Record<string, number>): number {
  return sum(identity, NOT_IDENTIFIED);
}

export function screeningAttentionCount(
  screening: Record<string, number>,
): number {
  return sum(screening, SCREENING_ATTENTION);
}

// screeningApplies is false when the policy requires a VOG of nobody: every
// member is not_required and the screening bar would only say so.
export function screeningApplies(screening: Record<string, number>): boolean {
  return Object.entries(screening).some(
    ([status, count]) => status !== "not_required" && count > 0,
  );
}

export interface StatusSegment {
  status: string;
  count: number;
  // Share of the total, 0..100, for the segment's width.
  share: number;
}

// statusSegments turns a count map into the non-empty segments of a stacked
// bar, in the given order, with any status the frontend does not know appended
// so a new backend status is still drawn rather than dropped.
export function statusSegments(
  counts: Record<string, number>,
  order: readonly string[],
): StatusSegment[] {
  const total = Object.values(counts).reduce((a, b) => a + b, 0);
  if (total === 0) return [];
  const known = new Set<string>(order);
  const statuses = [
    ...order,
    ...Object.keys(counts)
      .filter((status) => !known.has(status))
      .sort(),
  ];
  return statuses
    .filter((status) => (counts[status] ?? 0) > 0)
    .map((status) => ({
      status,
      count: counts[status] ?? 0,
      share: ((counts[status] ?? 0) / total) * 100,
    }));
}
