import { describe, expect, it } from "vitest";
import { PLACEMENT_KIND, SIGNER_KIND } from "../api/signing";
import type { Placement } from "../api/signing";
import {
  DEFAULT_PARAPH_SIZE,
  DEFAULT_SIGNATURE_SIZE,
  MIN_PLACEMENT_SIZE,
  defaultSize,
  findPlacement,
  moveBy,
  paraphOnEveryPage,
  placeAt,
  placementsIncomplete,
  placementsOnPage,
  resizeTo,
  signerAccent,
  SIGNER_ACCENTS,
  withPlacement,
  withoutParaphs,
  withoutPlacement,
} from "./placement";

// A4 in points, and a page whose crop box does not start at the origin — the second
// is what catches an implementation that assumes a page starts at 0,0.
const a4 = { minX: 0, minY: 0, maxX: 595, maxY: 842 };
const cropped = { minX: 20, minY: 30, maxX: 300, maxY: 400 };

function paraph(page: number, x = 500, y = 40): Placement {
  return { kind: PLACEMENT_KIND.paraph, page, x, y, width: 48, height: 28 };
}

describe("placeAt", () => {
  it("centres the mark on the point", () => {
    const got = placeAt(
      PLACEMENT_KIND.signature,
      2,
      { x: 300, y: 400 },
      DEFAULT_SIGNATURE_SIZE,
      a4,
    );
    expect(got).toEqual({
      kind: PLACEMENT_KIND.signature,
      page: 2,
      x: 300 - DEFAULT_SIGNATURE_SIZE.width / 2,
      y: 400 - DEFAULT_SIGNATURE_SIZE.height / 2,
      width: DEFAULT_SIGNATURE_SIZE.width,
      height: DEFAULT_SIGNATURE_SIZE.height,
    });
  });

  it("keeps a mark placed near an edge fully on the page", () => {
    const got = placeAt(
      PLACEMENT_KIND.paraph,
      1,
      { x: 594, y: 1 },
      DEFAULT_PARAPH_SIZE,
      a4,
    );
    expect(got.x + got.width).toBeLessThanOrEqual(a4.maxX);
    expect(got.y).toBe(0);
  });

  it("respects a page whose box does not start at the origin", () => {
    const got = placeAt(
      PLACEMENT_KIND.paraph,
      1,
      { x: 0, y: 0 },
      DEFAULT_PARAPH_SIZE,
      cropped,
    );
    expect(got.x).toBe(cropped.minX);
    expect(got.y).toBe(cropped.minY);
  });

  it("shrinks a mark that is larger than the page", () => {
    const got = placeAt(
      PLACEMENT_KIND.signature,
      1,
      { x: 100, y: 100 },
      { width: 5000, height: 5000 },
      cropped,
    );
    expect(got.width).toBe(cropped.maxX - cropped.minX);
    expect(got.height).toBe(cropped.maxY - cropped.minY);
  });
});

describe("moveBy", () => {
  it("shifts the mark", () => {
    const got = moveBy(paraph(1, 100, 100), 10, -20, a4);
    expect([got.x, got.y]).toEqual([110, 80]);
  });

  it("stops at the page edge instead of going over it", () => {
    const got = moveBy(paraph(1, 100, 100), 10_000, -10_000, a4);
    expect(got.x).toBe(a4.maxX - got.width);
    expect(got.y).toBe(a4.minY);
  });
});

describe("resizeTo", () => {
  it("holds the floor the backend also enforces", () => {
    const got = resizeTo(paraph(1), { width: 1, height: 1 }, a4);
    expect(got.width).toBe(MIN_PLACEMENT_SIZE);
    expect(got.height).toBe(MIN_PLACEMENT_SIZE);
  });

  it("pulls a mark back onto the page when it grows past the edge", () => {
    const got = resizeTo(paraph(1, 560, 40), { width: 200, height: 28 }, a4);
    expect(got.x + got.width).toBeLessThanOrEqual(a4.maxX);
  });
});

describe("withPlacement", () => {
  it("replaces the one signature block wherever it was", () => {
    const first = placeAt(
      PLACEMENT_KIND.signature,
      1,
      { x: 100, y: 100 },
      defaultSize(PLACEMENT_KIND.signature),
      a4,
    );
    const second = placeAt(
      PLACEMENT_KIND.signature,
      3,
      { x: 200, y: 200 },
      defaultSize(PLACEMENT_KIND.signature),
      a4,
    );
    const got = withPlacement(withPlacement([], first), second);
    expect(got).toEqual([second]);
  });

  it("keeps one paraph per page and replaces the one on that page", () => {
    const got = withPlacement(
      withPlacement([paraph(1)], paraph(2)),
      paraph(1, 100, 100),
    );
    expect(got.map((p) => p.page)).toEqual([1, 2]);
    expect(findPlacement(got, PLACEMENT_KIND.paraph, 1)?.x).toBe(100);
  });

  it("orders the signature block before the paraphs", () => {
    const block = placeAt(
      PLACEMENT_KIND.signature,
      3,
      { x: 100, y: 100 },
      defaultSize(PLACEMENT_KIND.signature),
      a4,
    );
    const got = withPlacement(withPlacement([paraph(2)], paraph(1)), block);
    expect(got.map((p) => p.kind)).toEqual([
      PLACEMENT_KIND.signature,
      PLACEMENT_KIND.paraph,
      PLACEMENT_KIND.paraph,
    ]);
  });
});

describe("withoutPlacement", () => {
  it("removes only the paraph on the named page", () => {
    const got = withoutPlacement(
      [paraph(1), paraph(2)],
      PLACEMENT_KIND.paraph,
      1,
    );
    expect(got.map((p) => p.page)).toEqual([2]);
  });
});

describe("paraphOnEveryPage", () => {
  const boxes = [a4, a4, cropped];

  it("puts one paraph on every page and leaves the signature block alone", () => {
    const block = placeAt(
      PLACEMENT_KIND.signature,
      1,
      { x: 100, y: 100 },
      defaultSize(PLACEMENT_KIND.signature),
      a4,
    );
    const got = paraphOnEveryPage(withPlacement([], block), paraph(1), boxes);
    expect(
      got.filter((p) => p.kind === PLACEMENT_KIND.paraph).map((p) => p.page),
    ).toEqual([1, 2, 3]);
    expect(findPlacement(got, PLACEMENT_KIND.signature, 1)).toEqual(block);
  });

  it("fits the copy to a page that is smaller than the one it was drawn on", () => {
    const got = paraphOnEveryPage([], paraph(1, 500, 40), boxes);
    const onCropped = got.find((p) => p.page === 3);
    expect(onCropped).toBeDefined();
    expect(onCropped!.x + onCropped!.width).toBeLessThanOrEqual(cropped.maxX);
  });

  it("replaces any paraphs that were already there", () => {
    const got = paraphOnEveryPage([paraph(2, 10, 10)], paraph(1), boxes);
    expect(got.filter((p) => p.page === 2)).toHaveLength(1);
  });
});

describe("withoutParaphs", () => {
  it("keeps the signature block", () => {
    const block = placeAt(
      PLACEMENT_KIND.signature,
      1,
      { x: 100, y: 100 },
      defaultSize(PLACEMENT_KIND.signature),
      a4,
    );
    const got = withoutParaphs([block, paraph(1), paraph(2)]);
    expect(got).toEqual([block]);
  });
});

describe("placementsOnPage", () => {
  it("returns the marks of that page only", () => {
    expect(placementsOnPage([paraph(1), paraph(2)], 2)).toHaveLength(1);
  });
});

describe("signerAccent", () => {
  it("cycles the house accents so a sixth signer is still coloured", () => {
    expect(signerAccent(0)).toBe(SIGNER_ACCENTS[0]);
    expect(signerAccent(SIGNER_ACCENTS.length)).toBe(SIGNER_ACCENTS[0]);
  });
});

describe("placementsIncomplete", () => {
  const block = placeAt(
    PLACEMENT_KIND.signature,
    1,
    { x: 100, y: 100 },
    defaultSize(PLACEMENT_KIND.signature),
    a4,
  );

  it("accepts a request where nobody placed anything", () => {
    expect(
      placementsIncomplete([
        { kind: SIGNER_KIND.internal, userId: "u-1", placements: [] },
        { kind: SIGNER_KIND.internal, userId: "u-2", placements: [] },
      ]),
    ).toEqual([]);
  });

  it("names the signers with no signature block once anyone has one", () => {
    expect(
      placementsIncomplete([
        { kind: SIGNER_KIND.internal, userId: "u-1", placements: [block] },
        { kind: SIGNER_KIND.internal, userId: "u-2", placements: [] },
        { kind: SIGNER_KIND.internal, userId: "u-3", placements: [paraph(1)] },
      ]),
    ).toEqual([1, 2]);
  });

  it("accepts a request where everyone has a block", () => {
    expect(
      placementsIncomplete([
        { kind: SIGNER_KIND.internal, userId: "u-1", placements: [block] },
        {
          kind: SIGNER_KIND.internal,
          userId: "u-2",
          placements: [block, paraph(1)],
        },
      ]),
    ).toEqual([]);
  });
});
