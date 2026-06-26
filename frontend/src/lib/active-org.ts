/**
 * The active organization lives in the URL (the `:orgSlug` segment) so links
 * are shareable. localStorage only remembers the *last* org a user looked at,
 * so a bare visit to `/` can route them back where they were.
 */

const STORAGE_KEY = "ybw.activeOrgSlug";

export function getStoredOrgSlug(): string | null {
  try {
    return window.localStorage.getItem(STORAGE_KEY);
  } catch {
    return null;
  }
}

export function setStoredOrgSlug(slug: string): void {
  try {
    window.localStorage.setItem(STORAGE_KEY, slug);
  } catch {
    // Storage can be unavailable (private mode, quota) — the URL still drives
    // the active org, so a failed write is non-fatal.
  }
}
