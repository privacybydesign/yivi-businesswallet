import { PLACEMENT_KIND } from "../api/signing";
import type { Placement, PlacementKind, SignerSelection } from "../api/signing";

// Geometry for the signature/paraph placement editor. Everything here works in PDF
// user-space points with the origin at the page's bottom-left — the same space the
// backend stores and pdfsign draws in — so the conversion from viewer pixels happens
// exactly once, in the component that owns the pdf.js viewport.

// Default sizes, in points, for a freshly placed mark: a signature block wide enough
// for a name, a paraph box about the size of handwritten initials.
export const DEFAULT_SIGNATURE_SIZE = { width: 180, height: 56 } as const;
export const DEFAULT_PARAPH_SIZE = { width: 48, height: 28 } as const;

// MIN_PLACEMENT_SIZE mirrors the backend's floor (internal/signing/placement.go): a
// smaller rectangle is refused there, so the editor must not offer one.
export const MIN_PLACEMENT_SIZE = 8;

// Nudge step for moving a selected mark with the arrow keys, and the larger step
// Shift takes. Both are the single-pointer-free way to position a mark.
export const NUDGE_STEP = 4;
export const NUDGE_STEP_LARGE = 20;

// PageBox is one page's visible box in PDF points. It is not always anchored at the
// origin: a page with a crop box starts wherever that box starts.
export interface PageBox {
  minX: number;
  minY: number;
  maxX: number;
  maxY: number;
}

export interface Size {
  width: number;
  height: number;
}

export function defaultSize(kind: PlacementKind): Size {
  return kind === PLACEMENT_KIND.signature
    ? { ...DEFAULT_SIGNATURE_SIZE }
    : { ...DEFAULT_PARAPH_SIZE };
}

function clampBetween(value: number, low: number, high: number): number {
  return Math.min(Math.max(value, low), high);
}

// fitSize shrinks a size that does not fit the page, keeping it above the floor the
// backend enforces. A page smaller than that floor cannot hold a mark at all, which
// readGeometry already refuses server-side.
function fitSize(size: Size, box: PageBox): Size {
  return {
    width: clampBetween(size.width, MIN_PLACEMENT_SIZE, box.maxX - box.minX),
    height: clampBetween(size.height, MIN_PLACEMENT_SIZE, box.maxY - box.minY),
  };
}

// placeAt returns a mark of the given size centred on a point, moved inside the page.
// Clamping is right here rather than at submit time because a mark half off the page
// is refused by the backend, and the requester should see where it actually landed.
export function placeAt(
  kind: PlacementKind,
  page: number,
  centre: { x: number; y: number },
  size: Size,
  box: PageBox,
): Placement {
  const fitted = fitSize(size, box);
  return {
    kind,
    page,
    x: clampBetween(
      centre.x - fitted.width / 2,
      box.minX,
      box.maxX - fitted.width,
    ),
    y: clampBetween(
      centre.y - fitted.height / 2,
      box.minY,
      box.maxY - fitted.height,
    ),
    width: fitted.width,
    height: fitted.height,
  };
}

// moveBy shifts a mark and keeps it on the page.
export function moveBy(
  placement: Placement,
  dx: number,
  dy: number,
  box: PageBox,
): Placement {
  return {
    ...placement,
    x: clampBetween(placement.x + dx, box.minX, box.maxX - placement.width),
    y: clampBetween(placement.y + dy, box.minY, box.maxY - placement.height),
  };
}

// resizeTo re-sizes a mark around its lower-left corner, keeping it on the page.
export function resizeTo(
  placement: Placement,
  size: Size,
  box: PageBox,
): Placement {
  const fitted = fitSize(size, box);
  return {
    ...placement,
    width: fitted.width,
    height: fitted.height,
    x: clampBetween(placement.x, box.minX, box.maxX - fitted.width),
    y: clampBetween(placement.y, box.minY, box.maxY - fitted.height),
  };
}

// samePlacement identifies the slot a mark occupies: a signer has one signature block
// wherever it sits, and one paraph per page. Placing a second one replaces the first
// rather than adding a mark the backend would refuse.
function samePlacement(a: Placement, b: Placement): boolean {
  if (a.kind !== b.kind) return false;
  return a.kind === PLACEMENT_KIND.signature ? true : a.page === b.page;
}

export function withPlacement(
  placements: Placement[],
  placement: Placement,
): Placement[] {
  const next = placements.filter((p) => !samePlacement(p, placement));
  next.push(placement);
  return sortPlacements(next);
}

export function withoutPlacement(
  placements: Placement[],
  kind: PlacementKind,
  page: number,
): Placement[] {
  return placements.filter(
    (p) =>
      p.kind !== kind || (kind === PLACEMENT_KIND.paraph && p.page !== page),
  );
}

// paraphOnEveryPage copies one paraph rectangle onto every page, which is the
// shorthand a document with initials on each page needs. The expansion happens here,
// so what reaches the API is always one placement per page.
export function paraphOnEveryPage(
  placements: Placement[],
  rect: Size & { x: number; y: number },
  boxes: PageBox[],
): Placement[] {
  const kept = placements.filter((p) => p.kind !== PLACEMENT_KIND.paraph);
  const paraphs = boxes.map((box, index) =>
    placeAt(
      PLACEMENT_KIND.paraph,
      index + 1,
      { x: rect.x + rect.width / 2, y: rect.y + rect.height / 2 },
      { width: rect.width, height: rect.height },
      box,
    ),
  );
  return sortPlacements([...kept, ...paraphs]);
}

// alignParaphs keeps every one of a signer's paraphs at the same rectangle: initials
// sit in the same spot on each page they occupy, so moving or resizing one moves them
// all rather than making the requester reposition page by page. The set of pages a
// paraph lives on is chosen elsewhere (place / every page); pages without one are left
// untouched. `rect` is the new geometry of the mark being dragged or resized.
export function alignParaphs(
  placements: Placement[],
  rect: Size & { x: number; y: number },
  boxes: PageBox[],
): Placement[] {
  const centre = { x: rect.x + rect.width / 2, y: rect.y + rect.height / 2 };
  const size = { width: rect.width, height: rect.height };
  return sortPlacements(
    placements.map((p) => {
      const box = boxes[p.page - 1];
      return p.kind === PLACEMENT_KIND.paraph && box != null
        ? placeAt(PLACEMENT_KIND.paraph, p.page, centre, size, box)
        : p;
    }),
  );
}

export function withoutParaphs(placements: Placement[]): Placement[] {
  return placements.filter((p) => p.kind !== PLACEMENT_KIND.paraph);
}

// sortPlacements keeps a signer's marks in a stable, readable order: the signature
// block first, then the paraphs by page.
function sortPlacements(placements: Placement[]): Placement[] {
  return [...placements].sort((a, b) => {
    if (a.kind !== b.kind) return a.kind === PLACEMENT_KIND.signature ? -1 : 1;
    return a.page - b.page;
  });
}

export function findPlacement(
  placements: Placement[],
  kind: PlacementKind,
  page: number,
): Placement | undefined {
  return placements.find((p) =>
    kind === PLACEMENT_KIND.signature
      ? p.kind === kind
      : p.kind === kind && p.page === page,
  );
}

export function placementsOnPage(
  placements: Placement[],
  page: number,
): Placement[] {
  return placements.filter((p) => p.page === page);
}

export function countParaphs(placements: Placement[]): number {
  return placements.filter((p) => p.kind === PLACEMENT_KIND.paraph).length;
}

// signerAccents cycles the house tokens rather than introducing a palette of its own:
// there is no "signee colour" in the design system, and a mark also carries the
// signer's name, so colour is a second cue and never the only one.
export const SIGNER_ACCENTS = [
  { box: "border-brand bg-brand/10", chip: "bg-brand", text: "text-brand" },
  { box: "border-link bg-link/10", chip: "bg-link", text: "text-link" },
  {
    box: "border-success bg-success/10",
    chip: "bg-success",
    text: "text-success",
  },
  {
    box: "border-warning-fg bg-warning-fg/10",
    chip: "bg-warning-fg",
    text: "text-warning-fg",
  },
  { box: "border-ink bg-ink/10", chip: "bg-ink", text: "text-ink" },
] as const;

export function signerAccent(index: number): (typeof SIGNER_ACCENTS)[number] {
  return SIGNER_ACCENTS[index % SIGNER_ACCENTS.length];
}

// placementsIncomplete names the signers who still need a signature block. Placement
// is optional as a whole — a request with no marks at all signs invisibly, as it did
// before placement existed — but a request where only some signatures are visible is a
// half-finished one, so it is refused before it is created.
export function placementsIncomplete(
  signers: (SignerSelection & { placements: Placement[] })[],
): number[] {
  const anyPlaced = signers.some((s) => s.placements.length > 0);
  if (!anyPlaced) return [];
  return signers
    .map((s, index) =>
      findPlacement(s.placements, PLACEMENT_KIND.signature, 1) === undefined
        ? index
        : -1,
    )
    .filter((index) => index >= 0);
}
