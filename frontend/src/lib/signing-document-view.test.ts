import { describe, expect, it } from "vitest";
import { clampPage, fitScale } from "./signing-document-view";

describe("fitScale", () => {
  it("shrinks a page to the width of a narrow column", () => {
    expect(fitScale(600, 300)).toBe(0.5);
  });

  it("does not enlarge a page past the cap in a wide column", () => {
    expect(fitScale(600, 3000)).toBe(1.5);
  });

  it("draws at the page's own size before either width is known", () => {
    expect(fitScale(0, 300)).toBe(1);
    expect(fitScale(600, 0)).toBe(1);
  });
});

describe("clampPage", () => {
  it("keeps a page inside the document", () => {
    expect(clampPage(0, 3)).toBe(1);
    expect(clampPage(2, 3)).toBe(2);
    expect(clampPage(4, 3)).toBe(3);
  });

  it("answers the first page for an empty document", () => {
    expect(clampPage(5, 0)).toBe(1);
  });
});
