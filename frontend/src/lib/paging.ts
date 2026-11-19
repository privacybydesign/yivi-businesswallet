// Drawing every row of a busy org's list took seconds.
export const PAGE_SIZE = 50;

export function pageCount(total: number, size = PAGE_SIZE): number {
  return Math.max(1, Math.ceil(total / size));
}

// The page holding the item at index; the first for one not in the list (-1).
export function pageOfIndex(index: number, size = PAGE_SIZE): number {
  return index < 0 ? 0 : Math.floor(index / size);
}

// A list that shrank (a filter, a refresh) shows its last page, not none.
export function clampPage(
  page: number,
  total: number,
  size = PAGE_SIZE,
): number {
  return Math.min(Math.max(page, 0), pageCount(total, size) - 1);
}

export function pageSlice<T>(
  items: readonly T[],
  page: number,
  size = PAGE_SIZE,
): T[] {
  const at = clampPage(page, items.length, size);
  return items.slice(at * size, (at + 1) * size);
}
