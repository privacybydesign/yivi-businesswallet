import { describe, expect, it } from "vitest";
import { clampPage, pageCount, pageOfIndex, pageSlice } from "./paging";

const items = Array.from({ length: 120 }, (_, i) => i);

describe("paging", () => {
  it("counts at least one page", () => {
    expect(pageCount(0, 50)).toBe(1);
    expect(pageCount(50, 50)).toBe(1);
    expect(pageCount(120, 50)).toBe(3);
  });

  it("finds the page an item is on, the first for one not listed", () => {
    expect(pageOfIndex(49, 50)).toBe(0);
    expect(pageOfIndex(50, 50)).toBe(1);
    expect(pageOfIndex(-1, 50)).toBe(0);
  });

  it("slices a page, clamped to the pages there are", () => {
    expect(pageSlice(items, 1, 50)).toEqual(items.slice(50, 100));
    expect(pageSlice(items, 2, 50)).toEqual(items.slice(100));
    expect(pageSlice(items, 9, 50)).toEqual(items.slice(100));
    expect(pageSlice([], 3, 50)).toEqual([]);
  });
});

describe("clampPage", () => {
  it("keeps a page within the pages there are", () => {
    expect(clampPage(5, 120, 50)).toBe(2);
    expect(clampPage(-1, 120, 50)).toBe(0);
    expect(clampPage(1, 0, 50)).toBe(0);
  });
});
