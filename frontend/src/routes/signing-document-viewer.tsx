import { useEffect, useRef, useState } from "react";
import { useTranslation } from "react-i18next";
import * as pdfjs from "pdfjs-dist";
import type { PDFDocumentLoadingTask, PDFDocumentProxy } from "pdfjs-dist";
import type { RenderParameters } from "pdfjs-dist/types/src/display/api";
import workerUrl from "pdfjs-dist/build/pdf.worker.min.mjs?url";
import { PageRenderer } from "../lib/pdf-page-render";
import { clampPage, fitScale } from "../lib/signing-document-view";
import { Button } from "../ui";

pdfjs.GlobalWorkerOptions.workerSrc = workerUrl;

// A sharper canvas than this costs memory on a phone and shows nothing more.
const MAX_PIXEL_RATIO = 2;

type ViewerStatus = "loading" | "ready" | "error";

interface SigningDocumentViewerProps {
  // Fetches the PDF. It is called once for the life of the component: remount
  // (a key) to show another document.
  load: (signal: AbortSignal) => Promise<Blob>;
  // The document's name, for the viewer's accessible label.
  filename: string;
}

// SigningDocumentViewer shows a signee the document they are asked to sign, on
// the page they sign it from, one page at a time at the width of its column. It
// is the reading half of the signing flow: the buttons that sign or decline sit
// beside it and belong to the page that mounts it.
export function SigningDocumentViewer({
  load,
  filename,
}: Readonly<SigningDocumentViewerProps>): React.JSX.Element {
  const { t } = useTranslation();
  const frameRef = useRef<HTMLDivElement>(null);
  const canvasRef = useRef<HTMLCanvasElement>(null);
  const documentRef = useRef<PDFDocumentProxy | null>(null);
  // destroy() lives on the loading task and is what tears the worker down.
  const taskRef = useRef<PDFDocumentLoadingTask | null>(null);
  // One render at a time on this canvas: see lib/pdf-page-render.
  const rendererRef = useRef<PageRenderer<RenderParameters> | null>(null);
  // The loader as first given: a caller's inline arrow is a new function on
  // every render and must not refetch the document.
  const loadRef = useRef(load);

  const [status, setStatus] = useState<ViewerStatus>("loading");
  const [pageCount, setPageCount] = useState(0);
  const [page, setPage] = useState(1);
  const [width, setWidth] = useState(0);

  // The column's width decides the zoom, and changes when the window does.
  useEffect(() => {
    const frame = frameRef.current;
    if (frame == null) return;
    const observer = new ResizeObserver((entries) => {
      setWidth(Math.floor(entries[0].contentRect.width));
    });
    observer.observe(frame);
    return () => observer.disconnect();
  }, []);

  useEffect(() => {
    const controller = new AbortController();
    let cancelled = false;
    void (async () => {
      try {
        const blob = await loadRef.current(controller.signal);
        // pdf.js detaches the buffer it is handed, so it gets its own copy.
        const bytes = new Uint8Array(await blob.arrayBuffer());
        if (cancelled) return;
        const task = pdfjs.getDocument({ data: bytes });
        taskRef.current = task;
        const doc = await task.promise;
        if (cancelled) {
          void task.destroy();
          return;
        }
        documentRef.current = doc;
        setPageCount(doc.numPages);
        setStatus("ready");
      } catch {
        if (!cancelled) setStatus("error");
      }
    })();
    return () => {
      cancelled = true;
      controller.abort();
      void taskRef.current?.destroy();
      taskRef.current = null;
      documentRef.current = null;
    };
  }, []);

  useEffect(() => {
    const doc = documentRef.current;
    const canvas = canvasRef.current;
    if (doc == null || canvas == null || pageCount === 0 || width === 0) return;
    const renderer = (rendererRef.current ??=
      new PageRenderer<RenderParameters>());
    // A run abandoned while awaiting getPage must not resize the canvas under
    // the run that replaced it.
    let cancelled = false;
    void (async () => {
      try {
        const pdfPage = await doc.getPage(page);
        const unscaled = pdfPage.getViewport({ scale: 1 });
        const viewport = pdfPage.getViewport({
          scale: fitScale(unscaled.width, width),
        });
        if (cancelled) return;
        const ratio = Math.min(window.devicePixelRatio || 1, MAX_PIXEL_RATIO);
        canvas.width = Math.floor(viewport.width * ratio);
        canvas.height = Math.floor(viewport.height * ratio);
        canvas.style.width = `${viewport.width}px`;
        canvas.style.height = `${viewport.height}px`;
        const context = canvas.getContext("2d");
        if (context == null) return;
        context.setTransform(ratio, 0, 0, ratio, 0, 0);
        await renderer.draw(pdfPage, {
          canvas,
          canvasContext: context,
          viewport,
        });
      } catch {
        if (!cancelled) setStatus("error");
      }
    })();
    return () => {
      cancelled = true;
      renderer.stop();
    };
  }, [page, pageCount, width]);

  const go = (next: number): void => setPage(clampPage(next, pageCount));

  return (
    <section
      aria-label={t("signing.viewer.label", { filename })}
      className="flex min-w-0 flex-col gap-3"
    >
      <div
        ref={frameRef}
        className="border-line bg-surface-2 rounded-card max-h-[70vh] min-h-48 overflow-auto border"
      >
        {status === "loading" && (
          <p className="text-ink-soft p-6 text-center text-[13px]">
            {t("signing.viewer.loading")}
          </p>
        )}
        {status === "error" && (
          <p role="alert" className="text-error p-6 text-center text-[13px]">
            {t("signing.viewer.error")}
          </p>
        )}
        {/* The canvas is always mounted: the draw effect needs it to exist. */}
        <canvas
          ref={canvasRef}
          className={status === "ready" ? "mx-auto block" : "hidden"}
        />
      </div>
      {status === "ready" && (
        <div className="flex items-center justify-between gap-3">
          <Button
            type="button"
            variant="secondary"
            onClick={() => go(page - 1)}
            disabled={page <= 1}
          >
            {t("signing.viewer.previous")}
          </Button>
          <p className="text-ink-soft text-[13px]" aria-live="polite">
            {t("signing.viewer.page", { page, total: pageCount })}
          </p>
          <Button
            type="button"
            variant="secondary"
            onClick={() => go(page + 1)}
            disabled={page >= pageCount}
          >
            {t("signing.viewer.next")}
          </Button>
        </div>
      )}
    </section>
  );
}
