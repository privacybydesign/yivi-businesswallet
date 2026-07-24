/**
 * Client-side "unread" tracking for the QERDS inbox.
 *
 * Inbound delivery is automatic (background poll worker / webhook push on the
 * backend, see `.ai/features/qerds.md`), so a message can land while nobody is
 * looking at the inbox. We flag those arrivals as unread until the user opens
 * them. Read state is per-browser: it is a lightweight arrival signal, not an
 * org-wide read receipt, so it lives in localStorage rather than the backend.
 *
 * The pure helpers below (`unreadInboundIds`, `pruneSeen`) hold the logic and
 * are unit-tested; the localStorage wrappers are thin I/O.
 */

const STORAGE_PREFIX = "ybw.qerdsSeenInbound.";

function storageKey(slug: string): string {
  return STORAGE_PREFIX + slug;
}

// Inbound message ids that have arrived but are not yet marked seen.
export function unreadInboundIds(
  inboundIds: readonly string[],
  seen: readonly string[],
): string[] {
  const seenSet = new Set(seen);
  return inboundIds.filter((id) => !seenSet.has(id));
}

// Drops seen ids that are no longer in the inbox, so the stored set stays
// bounded by the current inbox size instead of growing forever.
export function pruneSeen(
  seen: readonly string[],
  existingIds: readonly string[],
): string[] {
  const existing = new Set(existingIds);
  return seen.filter((id) => existing.has(id));
}

// Loads the seen-id set for an org. Returns null when nothing is stored yet, so
// the caller can tell a first visit (baseline the whole inbox as already seen,
// no phantom "unread" burst) apart from an org whose inbox has been fully read.
export function loadSeenIds(slug: string): string[] | null {
  try {
    const raw = window.localStorage.getItem(storageKey(slug));
    if (raw === null) return null;
    const parsed: unknown = JSON.parse(raw);
    return Array.isArray(parsed)
      ? parsed.filter((v): v is string => typeof v === "string")
      : [];
  } catch {
    return null;
  }
}

export function saveSeenIds(slug: string, ids: readonly string[]): void {
  try {
    window.localStorage.setItem(storageKey(slug), JSON.stringify(ids));
  } catch {
    // Storage can be unavailable (private mode, quota). The unread badge is a
    // convenience signal, so a failed write is non-fatal.
  }
}
