import type { TFunction } from "i18next";

// The most requests one list read returns: the backend's maxListedRequests
// (backend/internal/proofing/proofing.go). A list that long may have left some
// out, so a count taken from it is a lower bound.
export const MAX_LISTED_REQUESTS = 200;

// Whether a list read may have left requests out.
export function listIsCapped(list: readonly unknown[]): boolean {
  return list.length >= MAX_LISTED_REQUESTS;
}

// A count taken from a list read, as the page shows it: "200+" when the list
// may have left some out, so it never reads as the whole number.
export function listedCount(
  count: number,
  capped: boolean,
  t: TFunction,
): string {
  return capped
    ? t("identityProofing.listedCountAtLeast", { count })
    : String(count);
}
