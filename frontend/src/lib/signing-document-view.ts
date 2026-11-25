// The arithmetic of the document viewer a signee reads their document in
// (routes/signing-document-viewer), apart from the canvas so it can be tested.

// A page is never drawn larger than this: a wide column would otherwise blow a
// page up past the size it reads comfortably at.
const MAX_SCALE = 1.5;

// fitScale is the zoom at which a page of pageWidth fills a column of
// columnWidth, capped at MAX_SCALE. A page or column with no width yet draws at
// its own size rather than at zero.
export function fitScale(pageWidth: number, columnWidth: number): number {
  if (pageWidth <= 0 || columnWidth <= 0) return 1;
  return Math.min(columnWidth / pageWidth, MAX_SCALE);
}

// clampPage keeps a page number inside a document of pageCount pages.
export function clampPage(page: number, pageCount: number): number {
  if (pageCount < 1) return 1;
  return Math.min(Math.max(page, 1), pageCount);
}
