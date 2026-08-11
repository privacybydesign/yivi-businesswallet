import { describe, expect, it } from "vitest";
import { PageRenderer } from "./pdf-page-render";
import type { PageRenderTask } from "./pdf-page-render";

// A stand-in for pdf.js's page.render(). It enforces the one rule that matters here,
// the same way pdfjs-dist does: a canvas a live task is drawing to may not be handed
// to a second render() until that task is cancelled or has finished. A renderer that
// forgets to cancel therefore fails here the way it fails in the browser.
function fakePdf() {
  const inUse = new Set<object>();
  const page = {
    render({ canvas }: { canvas: object }): PageRenderTask {
      if (inUse.has(canvas)) {
        throw new Error(
          "Cannot use the same canvas during multiple render() operations.",
        );
      }
      inUse.add(canvas);
      let settle: (() => void) | null = null;
      let fail: ((error: Error) => void) | null = null;
      const promise = new Promise<void>((resolve, reject) => {
        settle = resolve;
        fail = reject;
      });
      const task: PageRenderTask = {
        promise,
        cancel() {
          inUse.delete(canvas);
          fail?.(new Error("Rendering cancelled"));
        },
      };
      tasks.push({
        finish() {
          inUse.delete(canvas);
          settle?.();
        },
        fail(error: Error) {
          inUse.delete(canvas);
          fail?.(error);
        },
      });
      return task;
    },
  };
  const tasks: { finish: () => void; fail: (error: Error) => void }[] = [];
  return { page, tasks, inUse };
}

const canvas = {};

describe("PageRenderer", () => {
  it("cancels a render still in flight before starting the next one", async () => {
    const { page, tasks } = fakePdf();
    const renderer = new PageRenderer<{ canvas: object }>();

    const first = renderer.draw(page, { canvas });
    // Paging while the first page is still rasterising: this is the call that threw.
    const second = renderer.draw(page, { canvas });

    await expect(first).resolves.toBe(false);
    tasks[1].finish();
    await expect(second).resolves.toBe(true);
  });

  it("releases the canvas when the editor leaves the page", async () => {
    const { page, tasks, inUse } = fakePdf();
    const renderer = new PageRenderer<{ canvas: object }>();

    const drawing = renderer.draw(page, { canvas });
    renderer.stop();

    await expect(drawing).resolves.toBe(false);
    expect(inUse.has(canvas)).toBe(false);
    expect(tasks).toHaveLength(1);
  });

  it("reports a render that genuinely failed", async () => {
    const { page, tasks } = fakePdf();
    const renderer = new PageRenderer<{ canvas: object }>();

    const drawing = renderer.draw(page, { canvas });
    tasks[0].fail(new Error("broken page"));

    await expect(drawing).rejects.toThrow("broken page");
  });

  it("draws again after a render finished", async () => {
    const { page, tasks } = fakePdf();
    const renderer = new PageRenderer<{ canvas: object }>();

    const first = renderer.draw(page, { canvas });
    tasks[0].finish();
    await expect(first).resolves.toBe(true);

    const second = renderer.draw(page, { canvas });
    tasks[1].finish();
    await expect(second).resolves.toBe(true);
  });
});
