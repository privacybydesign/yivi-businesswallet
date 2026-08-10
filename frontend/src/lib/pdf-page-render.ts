// pdf.js refuses to draw two pages onto one canvas at the same time: a live render
// task holds the canvas in a module-level set, and a second render() against it
// throws "Cannot use the same canvas during multiple render() operations". Only
// cancelling the first task (or letting it finish) gives the canvas back, and
// cancel() does that synchronously.
//
// Paging through a document to place a paraph on every page is exactly the sequence
// that hits it, so the lifecycle lives here rather than inline in the editor: one
// task at a time, cancelled before the next one starts.

// The part of pdf.js's RenderTask this needs. It is restated rather than imported so
// the rule can be tested against a stand-in that enforces the same thing.
export interface PageRenderTask {
  promise: Promise<unknown>;
  cancel: () => void;
}

export interface RenderablePage<Parameters> {
  render: (parameters: Parameters) => PageRenderTask;
}

// PageRenderer owns the one render a canvas may have in flight.
export class PageRenderer<Parameters> {
  private task: PageRenderTask | null = null;

  // draw cancels whatever is still drawing and starts this render. It resolves true
  // when the page finished drawing and false when it was cancelled — by a later
  // draw(), or by stop(). A render that fails for any other reason still rejects:
  // that is a page the requester cannot place a mark on, and the editor says so.
  async draw(
    page: RenderablePage<Parameters>,
    parameters: Parameters,
  ): Promise<boolean> {
    this.stop();
    const task = page.render(parameters);
    this.task = task;
    try {
      await task.promise;
    } catch (error) {
      // Ours to swallow only if this task is no longer the current one, which is
      // true exactly when stop() or a later draw() replaced it.
      if (this.task !== task) return false;
      this.task = null;
      throw error;
    }
    if (this.task !== task) return false;
    this.task = null;
    return true;
  }

  // stop releases the canvas. Calling it when nothing is drawing does nothing.
  stop(): void {
    const task = this.task;
    this.task = null;
    task?.cancel();
  }
}
