import { readFileSync } from "node:fs";
import { describe, expect, it } from "vitest";
import * as pdfjs from "pdfjs-dist/legacy/build/pdf.mjs";
import { PLACEMENT_KIND } from "../api/signing";
import { placeAt } from "./placement";
import type { PageBox } from "./placement";

// The whole feature rests on one assumption: a rectangle the requester drew on a
// rendered page, converted to PDF points through the pdf.js viewport, is the same
// rectangle when it is converted back — and stays so at another zoom and on a rotated
// page. If that is wrong, every signature lands somewhere nobody chose, and no other
// test in the suite would notice. So it is checked against a real document, the same
// one the backend's own placement tests sign.
//
// This is a DOM-free test: getViewport and the two conversions are arithmetic on the
// page box, so the legacy build renders nothing and needs no canvas.

const SAMPLE = "../backend/internal/signing/testdata/sample.pdf";

function pointOf(values: unknown): { x: number; y: number } {
  const [x, y] = values as [number, number];
  return { x, y };
}

async function samplePage(rotation: number, scale: number) {
  const bytes = new Uint8Array(readFileSync(SAMPLE));
  const doc = await pdfjs.getDocument({ data: bytes }).promise;
  const page = await doc.getPage(1);
  return { page, viewport: page.getViewport({ scale, rotation }) };
}

describe("viewport round trip", () => {
  for (const rotation of [0, 90, 180, 270]) {
    for (const scale of [1, 1.75]) {
      it(`survives rotation ${rotation} at scale ${scale}`, async () => {
        const { page, viewport } = await samplePage(rotation, scale);
        const view: number[] = page.view;
        const box: PageBox = {
          minX: view[0],
          minY: view[1],
          maxX: view[2],
          maxY: view[3],
        };

        // A box drawn around a point a third of the way into the rendered page.
        const centre = pointOf(
          viewport.convertToPdfPoint(viewport.width / 3, viewport.height / 3),
        );
        const placement = placeAt(
          PLACEMENT_KIND.signature,
          1,
          centre,
          { width: 120, height: 40 },
          box,
        );

        // Back to the rendered page: the two corners must bracket the same pixels the
        // rectangle covers, whichever way the page is turned.
        const first = pointOf(
          viewport.convertToViewportPoint(placement.x, placement.y),
        );
        const second = pointOf(
          viewport.convertToViewportPoint(
            placement.x + placement.width,
            placement.y + placement.height,
          ),
        );
        const width = Math.abs(second.x - first.x);
        const height = Math.abs(second.y - first.y);
        // A rotated page swaps the two, which is exactly what the caller wants: the
        // box keeps its size on the page, not on the screen.
        const expected =
          rotation % 180 === 0
            ? [120 * scale, 40 * scale]
            : [40 * scale, 120 * scale];
        expect(width).toBeCloseTo(expected[0], 4);
        expect(height).toBeCloseTo(expected[1], 4);

        // And the rectangle really is inside the page, which is what the backend
        // re-checks before it stores anything.
        expect(placement.x).toBeGreaterThanOrEqual(box.minX);
        expect(placement.y).toBeGreaterThanOrEqual(box.minY);
        expect(placement.x + placement.width).toBeLessThanOrEqual(box.maxX);
        expect(placement.y + placement.height).toBeLessThanOrEqual(box.maxY);
      });
    }
  }
});
