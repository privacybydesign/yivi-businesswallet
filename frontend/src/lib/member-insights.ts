// Statuses that mean "this member has no current, verified identity": the
// dashboard's "not identified" to-do. due_soon is still identified.
const NOT_IDENTIFIED = ["never", "requested", "overdue"];

// Statuses that mean "this member's VOG needs something from them": the
// dashboard's VOG to-do. expiring is still valid today, and
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
