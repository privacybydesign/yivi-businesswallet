import { useCallback, useEffect, useRef, useState } from "react";
import { useTranslation } from "react-i18next";
import * as React from "react";
import * as pdfjs from "pdfjs-dist";
import type {
  PageViewport,
  PDFDocumentLoadingTask,
  PDFDocumentProxy,
} from "pdfjs-dist";
import type { RenderParameters } from "pdfjs-dist/types/src/display/api";
import workerUrl from "pdfjs-dist/build/pdf.worker.min.mjs?url";
import { PageRenderer } from "../lib/pdf-page-render";
import { PLACEMENT_KIND } from "../api/signing";
import type { Placement, PlacementKind } from "../api/signing";
import {
  MIN_PLACEMENT_SIZE,
  NUDGE_STEP,
  NUDGE_STEP_LARGE,
  countParaphs,
  defaultSize,
  findPlacement,
  moveBy,
  paraphOnEveryPage,
  placeAt,
  placementsOnPage,
  resizeTo,
  signerAccent,
  withPlacement,
  withoutParaphs,
  withoutPlacement,
} from "../lib/placement";
import type { PageBox } from "../lib/placement";
import { Button, Icon, Input } from "../ui";

// pdf.js runs its parser in a worker; Vite resolves the shipped module to a URL.
pdfjs.GlobalWorkerOptions.workerSrc = workerUrl;

// The rendered page is capped at this CSS width so a page is legible without the
// editor taking over the form. The pixel-ratio multiplier is applied to the canvas
// backing store only, so the overlay coordinates stay in CSS pixels.
const MAX_PAGE_WIDTH = 620;
const MAX_PIXEL_RATIO = 2;

// pdf.js types both viewport conversions as `any[]`; each is always an [x, y] pair.
function pointOf(values: unknown): { x: number; y: number } {
  const [x, y] = values as [number, number];
  return { x, y };
}

const LABEL = "text-ink-soft text-[12px] font-semibold";
// The house focus treatment for a control that is not one of the ui/ primitives:
// the same ring Input uses, so a placed mark shows focus like every other target.
const FOCUS_RING =
  "outline-none focus-visible:border-ink focus-visible:ring-ink/20 focus-visible:ring-3";

// SignerLabel is one signee in the placement editor. Their position in the list is
// what the accent colour is derived from, so the editor and the form's own signer
// list colour the same person the same way.
export interface SignerLabel {
  name: string;
}

interface PlacementEditorProps {
  file: File;
  signers: SignerLabel[];
  placements: Placement[][];
  onChange: (signerIndex: number, placements: Placement[]) => void;
}

// PlacementEditor renders the uploaded PDF and lets the requester put each signee's
// signature block and paraphs on it. Coordinates leave here in PDF points, which is
// what the backend validates and what pdfsign draws with — the viewport is the only
// thing that knows the zoom and the page rotation, and it does not outlive this
// component.
export function PlacementEditor({
  file,
  signers,
  placements,
  onChange,
}: PlacementEditorProps): React.JSX.Element {
  const { t } = useTranslation();
  const canvasRef = useRef<HTMLCanvasElement>(null);
  const documentRef = useRef<PDFDocumentProxy | null>(null);
  // destroy() lives on the loading task, not on the document, and it is what tears
  // the worker down — a file replaced mid-render must not leave one running.
  const taskRef = useRef<PDFDocumentLoadingTask | null>(null);
  // One render at a time on this canvas: see lib/pdf-page-render.
  const rendererRef = useRef<PageRenderer<RenderParameters> | null>(null);

  const [boxes, setBoxes] = useState<PageBox[]>([]);
  const [page, setPage] = useState(1);
  const [signerIndex, setSignerIndex] = useState(0);
  const [kind, setKind] = useState<PlacementKind>(PLACEMENT_KIND.signature);
  const [status, setStatus] = useState<"loading" | "ready" | "error">(
    "loading",
  );
  // The viewport of the page on screen is state, not a ref: the marks are positioned
  // through it while rendering, so a render that happens before it is set has to be
  // followed by another one.
  const [viewport, setViewport] = useState<PageViewport | null>(null);

  // The signee whose marks are being placed can fall off the end when a signer is
  // removed from the form.
  const activeSigner = Math.min(signerIndex, Math.max(signers.length - 1, 0));
  const activePlacements = placements[activeSigner] ?? [];

  // Loading the document: pdf.js detaches the buffer it is handed, so the file is
  // read into a copy. A file that is not a PDF is reported here rather than at submit.
  useEffect(() => {
    let cancelled = false;
    void (async () => {
      try {
        const bytes = new Uint8Array(await file.arrayBuffer());
        const task = pdfjs.getDocument({ data: bytes });
        taskRef.current = task;
        const doc = await task.promise;
        if (cancelled) {
          void task.destroy();
          return;
        }
        const pageBoxes: PageBox[] = [];
        for (let number = 1; number <= doc.numPages; number++) {
          const view: number[] = (await doc.getPage(number)).view;
          pageBoxes.push({
            minX: view[0],
            minY: view[1],
            maxX: view[2],
            maxY: view[3],
          });
        }
        if (cancelled) {
          void task.destroy();
          return;
        }
        documentRef.current = doc;
        setBoxes(pageBoxes);
        setStatus("ready");
      } catch {
        if (!cancelled) setStatus("error");
      }
    })();
    return () => {
      cancelled = true;
      void taskRef.current?.destroy();
      taskRef.current = null;
      documentRef.current = null;
    };
  }, [file]);

  // Drawing the current page. The viewport is kept for the lifetime of the rendered
  // page: it is what converts between overlay pixels and PDF points.
  useEffect(() => {
    const doc = documentRef.current;
    const canvas = canvasRef.current;
    if (doc == null || canvas == null || boxes.length === 0) return;
    const renderer = (rendererRef.current ??=
      new PageRenderer<RenderParameters>());
    // The effect that replaced this one runs its own body first, so a run abandoned
    // while awaiting getPage must not go on to resize the canvas under it.
    let cancelled = false;
    void (async () => {
      try {
        const pdfPage = await doc.getPage(page);
        const unscaled = pdfPage.getViewport({ scale: 1 });
        const scale = Math.min(MAX_PAGE_WIDTH / unscaled.width, 1);
        const viewport = pdfPage.getViewport({ scale });
        if (cancelled) return;
        const ratio = Math.min(window.devicePixelRatio || 1, MAX_PIXEL_RATIO);
        canvas.width = Math.floor(viewport.width * ratio);
        canvas.height = Math.floor(viewport.height * ratio);
        canvas.style.width = `${viewport.width}px`;
        canvas.style.height = `${viewport.height}px`;
        const context = canvas.getContext("2d");
        if (context == null) return;
        context.setTransform(ratio, 0, 0, ratio, 0, 0);
        setViewport(viewport);
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
  }, [page, boxes]);

  const box = boxes[page - 1];

  // toPdf / toCss are the only two conversions in the feature. Both go through the
  // viewport, so a rotated or cropped page needs no special case anywhere else.
  const toPdf = useCallback(
    (x: number, y: number): { x: number; y: number } => {
      if (viewport == null) return { x: 0, y: 0 };
      return pointOf(viewport.convertToPdfPoint(x, y));
    },
    [viewport],
  );

  const toCss = useCallback(
    (
      placement: Placement,
    ): { left: number; top: number; width: number; height: number } => {
      if (viewport == null) return { left: 0, top: 0, width: 0, height: 0 };
      const first = pointOf(
        viewport.convertToViewportPoint(placement.x, placement.y),
      );
      const second = pointOf(
        viewport.convertToViewportPoint(
          placement.x + placement.width,
          placement.y + placement.height,
        ),
      );
      return {
        left: Math.min(first.x, second.x),
        top: Math.min(first.y, second.y),
        width: Math.abs(second.x - first.x),
        height: Math.abs(second.y - first.y),
      };
    },
    [viewport],
  );

  // pdfDelta turns a movement in overlay pixels into one in PDF points, which is what
  // makes both dragging and the arrow keys behave the same way on a rotated page.
  const pdfDelta = useCallback(
    (dx: number, dy: number): { dx: number; dy: number } => {
      const from = toPdf(0, 0);
      const to = toPdf(dx, dy);
      return { dx: to.x - from.x, dy: to.y - from.y };
    },
    [toPdf],
  );

  const update = (next: Placement[]): void => onChange(activeSigner, next);

  const placeCentre = (centre: { x: number; y: number }): void => {
    if (box == null) return;
    const existing = findPlacement(activePlacements, kind, page);
    const size = existing ?? defaultSize(kind);
    update(
      withPlacement(activePlacements, placeAt(kind, page, centre, size, box)),
    );
  };

  const onOverlayClick = (event: React.MouseEvent<HTMLDivElement>): void => {
    const rect = event.currentTarget.getBoundingClientRect();
    placeCentre(toPdf(event.clientX - rect.left, event.clientY - rect.top));
  };

  // Dragging a placed mark. Pointer capture keeps the move going when the pointer
  // leaves the box; the arrow keys and "place on this page" are the equivalents that
  // need no dragging at all (WCAG 2.2 2.5.7).
  const onPointerDown = (
    event: React.PointerEvent<HTMLButtonElement>,
    placement: Placement,
  ): void => {
    if (box == null || event.button !== 0) return;
    const startX = event.clientX;
    const startY = event.clientY;
    const origin = placement;
    let moved = false;
    const target = event.currentTarget;
    target.setPointerCapture(event.pointerId);
    const onMove = (move: PointerEvent): void => {
      const dx = move.clientX - startX;
      const dy = move.clientY - startY;
      if (!moved && Math.abs(dx) + Math.abs(dy) < 2) return;
      moved = true;
      const delta = pdfDelta(dx, dy);
      update(
        withPlacement(
          activePlacements,
          moveBy(origin, delta.dx, delta.dy, box),
        ),
      );
    };
    const onUp = (): void => {
      target.removeEventListener("pointermove", onMove);
      target.removeEventListener("pointerup", onUp);
      target.removeEventListener("pointercancel", onUp);
    };
    target.addEventListener("pointermove", onMove);
    target.addEventListener("pointerup", onUp);
    target.addEventListener("pointercancel", onUp);
  };

  const onBoxKeyDown = (
    event: React.KeyboardEvent<HTMLButtonElement>,
    placement: Placement,
  ): void => {
    const step = event.shiftKey ? NUDGE_STEP_LARGE : NUDGE_STEP;
    const moves: Record<string, [number, number]> = {
      ArrowLeft: [-step, 0],
      ArrowRight: [step, 0],
      ArrowUp: [0, -step],
      ArrowDown: [0, step],
    };
    const move = moves[event.key];
    if (move == null || box == null) return;
    event.preventDefault();
    const delta = pdfDelta(move[0], move[1]);
    update(
      withPlacement(
        activePlacements,
        moveBy(placement, delta.dx, delta.dy, box),
      ),
    );
  };

  const selected = findPlacement(activePlacements, kind, page);
  const pageCount = boxes.length;
  const activeName = signers[activeSigner]?.name ?? "";
  const marksOnActivePage = signers.reduce(
    (total, _signer, index) =>
      total + placementsOnPage(placements[index] ?? [], page).length,
    0,
  );

  if (status === "error") {
    return (
      <p className="text-error p-6 text-[13px]">
        {t("signing.placement.loadError")}
      </p>
    );
  }

  // A signee's marks in the rail: whether they have a signature block, and how many
  // paraphs — the two facts a requester scans for while placing.
  const hasSignature = (index: number): boolean =>
    findPlacement(placements[index] ?? [], PLACEMENT_KIND.signature, 1) !==
    undefined;
  const paraphCount = (index: number): number =>
    countParaphs(placements[index] ?? []);

  return (
    <div className="border-line flex min-h-0 flex-col md:h-[640px] md:flex-row">
      {/* Left — the document */}
      <div className="bg-surface-2 flex min-h-0 min-w-0 flex-1 flex-col">
        <div className="border-line bg-surface flex items-center gap-3 border-b px-5 py-2.5">
          <Button
            variant="secondary"
            size="sm"
            iconOnly
            icon="chevron_left"
            onClick={() => setPage((p) => Math.max(p - 1, 1))}
            disabled={page <= 1}
            aria-label={t("signing.placement.previousPage")}
          />
          <span className="text-ink-soft font-mono text-[11.5px]">
            {t("signing.placement.pageOf", { page, count: pageCount })}
          </span>
          <Button
            variant="secondary"
            size="sm"
            iconOnly
            icon="chevron_right"
            onClick={() => setPage((p) => Math.min(p + 1, pageCount))}
            disabled={page >= pageCount}
            aria-label={t("signing.placement.nextPage")}
          />
          <div className="flex-1" />
          <span className="text-muted font-mono text-[11px]">
            {t("signing.placement.onThisPage", { count: marksOnActivePage })}
          </span>
        </div>

        {/* The page and its marks. The canvas is always mounted — the draw effect
            needs canvasRef to exist to produce the viewport, and the viewport is
            what mounts the marks overlay, so gating the canvas on the viewport
            would deadlock the two (the page would load forever). The loading text
            is an overlay shown until the first page is drawn. */}
        <div className="relative flex min-h-0 flex-1 justify-center overflow-auto p-6">
          {/* Whose marks are being placed — a floating cue over the page. */}
          {activeName !== "" && (
            <div className="border-line-strong bg-surface shadow-card absolute top-4 left-1/2 z-10 flex -translate-x-1/2 items-center gap-2 rounded-full border py-1 pr-1 pl-3 whitespace-nowrap">
              <span className="text-ink-soft font-mono text-[11px]">
                {t("signing.placement.signerLegend")}
              </span>
              <span className="text-link bg-highlight inline-flex items-center gap-1.5 rounded-full px-2.5 py-1 text-[12px] font-semibold">
                <span
                  className={`h-1.5 w-1.5 rounded-full ${signerAccent(activeSigner).chip}`}
                  aria-hidden="true"
                />
                <span className="max-w-[16ch] truncate">{activeName}</span>
              </span>
              <span className="bg-line h-4 w-px" aria-hidden="true" />
              {[PLACEMENT_KIND.signature, PLACEMENT_KIND.paraph].map(
                (option) => (
                  <button
                    key={option}
                    type="button"
                    aria-pressed={kind === option}
                    onClick={() => setKind(option)}
                    className={[
                      "rounded-full px-2.5 py-1 text-[12px] transition-colors",
                      FOCUS_RING,
                      kind === option
                        ? "bg-ink font-semibold text-white"
                        : "text-ink-soft hover:bg-surface-3 font-medium",
                    ].join(" ")}
                  >
                    {option === PLACEMENT_KIND.signature
                      ? t("signing.placement.kindSignature")
                      : t("signing.placement.kindParaph")}
                  </button>
                ),
              )}
            </div>
          )}

          <div className="relative h-fit">
            <canvas
              ref={canvasRef}
              className="border-line shadow-card block border bg-white"
              role="img"
              aria-label={t("signing.placement.pageOf", {
                page,
                count: pageCount,
              })}
            />
            {(status === "loading" || viewport == null) && (
              <p className="text-ink-soft absolute inset-0 flex items-center justify-center text-[13px]">
                {t("common.loading")}
              </p>
            )}
            {status === "ready" && viewport != null && (
              <>
                {/* The click surface. Placing without a pointer goes through "place
                    in the middle" in the rail, and moving through the arrow keys. */}
                <div
                  className="absolute inset-0 cursor-crosshair"
                  onClick={onOverlayClick}
                  role="presentation"
                />
                {signers.map((signer, index) =>
                  placementsOnPage(placements[index] ?? [], page).map(
                    (placement) => {
                      const tone = signerAccent(index);
                      const css = toCss(placement);
                      const isActive = index === activeSigner;
                      return (
                        <button
                          key={`${index}-${placement.kind}-${placement.page}`}
                          type="button"
                          onPointerDown={(event) => {
                            setSignerIndex(index);
                            setKind(placement.kind);
                            if (isActive) onPointerDown(event, placement);
                          }}
                          onKeyDown={(event) =>
                            isActive && onBoxKeyDown(event, placement)
                          }
                          aria-label={t("signing.placement.markLabel", {
                            name: signer.name,
                            kind:
                              placement.kind === PLACEMENT_KIND.signature
                                ? t("signing.placement.kindSignature")
                                : t("signing.placement.kindParaph"),
                            page: placement.page,
                            x: Math.round(placement.x),
                            y: Math.round(placement.y),
                          })}
                          className={[
                            "absolute flex items-center justify-center overflow-hidden border-2 text-[10px] font-semibold",
                            FOCUS_RING,
                            tone.box,
                            tone.text,
                            isActive
                              ? "cursor-move"
                              : "cursor-pointer opacity-70",
                          ].join(" ")}
                          style={{
                            left: css.left,
                            top: css.top,
                            width: css.width,
                            height: css.height,
                          }}
                        >
                          <span className="truncate px-1">{signer.name}</span>
                        </button>
                      );
                    },
                  ),
                )}
              </>
            )}
          </div>
        </div>

        <div className="border-line bg-surface border-t px-5 py-2.5">
          <span className="text-ink-soft text-[12px]">
            {activeName !== ""
              ? t("signing.placement.clickToPlace", { name: activeName })
              : t("signing.placement.keyboardHint")}
          </span>
        </div>
      </div>

      {/* Right — the signee rail */}
      <div className="border-line bg-surface flex min-h-0 w-full flex-col border-t md:w-[380px] md:shrink-0 md:border-t-0 md:border-l">
        <div className="min-h-0 flex-1 overflow-y-auto p-5">
          <h3 className="font-display text-ink text-[16px] font-semibold">
            {t("signing.placement.railTitle")}
          </h3>
          <p className="text-ink-soft mt-2 text-[12.5px] leading-relaxed text-pretty">
            {t("signing.placement.railHint")}
          </p>

          <div className="mt-4 flex flex-col gap-2">
            {signers.map((signer, index) => {
              const tone = signerAccent(index);
              const active = index === activeSigner;
              const paraphs = paraphCount(index);
              const marks = (placements[index] ?? []).length;
              return (
                <div
                  key={`${signer.name}-${index}`}
                  className={[
                    "rounded-yivi border",
                    active ? "border-ink shadow-card border-2" : "border-line",
                  ].join(" ")}
                >
                  <button
                    type="button"
                    aria-pressed={active}
                    onClick={() => setSignerIndex(index)}
                    className={`flex w-full items-center gap-2.5 px-3 py-2.5 text-left ${FOCUS_RING} rounded-yivi`}
                  >
                    <span
                      className={`h-2.5 w-2.5 shrink-0 rounded-full ${tone.chip}`}
                      aria-hidden="true"
                    />
                    <span className="min-w-0 flex-1">
                      <span className="font-display text-ink block truncate text-[13px] font-semibold">
                        {signer.name}
                      </span>
                      <span className="text-ink-soft block font-mono text-[10.5px]">
                        {t("signing.placement.markCount", { count: marks })}
                      </span>
                    </span>
                    {marks === 0 ? (
                      <span className="bg-warning-bg text-warning-fg inline-flex h-[19px] items-center rounded-full px-2 text-[10.5px] font-semibold">
                        {t("signing.placement.noMark")}
                      </span>
                    ) : hasSignature(index) ? (
                      <Icon
                        name="valid"
                        size={14}
                        className="text-success opacity-70"
                      />
                    ) : null}
                  </button>

                  {active && (
                    <div className="border-line border-t p-3">
                      {/* Signature / initials segmented control */}
                      <div className="bg-surface-3 rounded-yivi flex gap-0.5 p-0.5">
                        {[PLACEMENT_KIND.signature, PLACEMENT_KIND.paraph].map(
                          (option) => (
                            <button
                              key={option}
                              type="button"
                              aria-pressed={kind === option}
                              onClick={() => setKind(option)}
                              className={[
                                "h-[30px] flex-1 rounded-[6px] text-[12.5px] transition-colors",
                                FOCUS_RING,
                                kind === option
                                  ? "bg-surface text-ink shadow-card font-semibold"
                                  : "text-ink-soft font-medium",
                              ].join(" ")}
                            >
                              {option === PLACEMENT_KIND.signature
                                ? t("signing.placement.kindSignature")
                                : t("signing.placement.kindParaph")}
                            </button>
                          ),
                        )}
                      </div>

                      <div className="mt-3 flex flex-wrap items-center gap-2">
                        <Button
                          variant="secondary"
                          size="sm"
                          onClick={() => {
                            if (box == null) return;
                            placeCentre({
                              x: (box.minX + box.maxX) / 2,
                              y: (box.minY + box.maxY) / 2,
                            });
                          }}
                          disabled={box == null}
                        >
                          {t("signing.placement.placeOnPage")}
                        </Button>
                        {selected && (
                          <Button
                            variant="ghost"
                            size="sm"
                            onClick={() =>
                              update(
                                withoutPlacement(activePlacements, kind, page),
                              )
                            }
                          >
                            {t("signing.placement.removeMark")}
                          </Button>
                        )}
                      </div>

                      {/* Initials scope */}
                      {kind === PLACEMENT_KIND.paraph && (
                        <div className="border-highlight-border bg-highlight rounded-yivi mt-3 border p-3">
                          <div className="text-ink text-[12.5px] font-semibold">
                            {t("signing.placement.applyInitials")}
                          </div>
                          <div className="mt-2 flex flex-col gap-2">
                            <Button
                              variant="secondary"
                              size="sm"
                              onClick={() => {
                                if (selected == null) return;
                                update(
                                  withPlacement(
                                    withoutParaphs(activePlacements),
                                    selected,
                                  ),
                                );
                              }}
                              disabled={selected == null}
                            >
                              {t("signing.placement.thisPageOnly")}
                            </Button>
                            <Button
                              variant="secondary"
                              size="sm"
                              onClick={() => {
                                if (box == null) return;
                                // Expand the current paraph across every page, or
                                // seed one in the page centre when none is placed
                                // yet, so "every page" always does something.
                                const size = defaultSize(PLACEMENT_KIND.paraph);
                                const rect = selected ?? {
                                  width: size.width,
                                  height: size.height,
                                  x: (box.minX + box.maxX) / 2 - size.width / 2,
                                  y:
                                    (box.minY + box.maxY) / 2 - size.height / 2,
                                };
                                update(
                                  paraphOnEveryPage(
                                    activePlacements,
                                    rect,
                                    boxes,
                                  ),
                                );
                              }}
                              disabled={box == null || pageCount < 2}
                            >
                              {t("signing.placement.everyPageCount", {
                                count: pageCount,
                              })}
                            </Button>
                            {paraphs > 0 && (
                              <Button
                                variant="ghost"
                                size="sm"
                                onClick={() =>
                                  update(withoutParaphs(activePlacements))
                                }
                              >
                                {t("signing.placement.removeParaphs")}
                              </Button>
                            )}
                          </div>
                        </div>
                      )}

                      {/* Size of the selected mark */}
                      {selected && (
                        <div className="mt-3 flex items-end gap-3">
                          <label className="flex flex-col gap-1">
                            <span className={LABEL}>
                              {t("signing.placement.widthLabel")}
                            </span>
                            <Input
                              type="number"
                              className="w-20"
                              min={MIN_PLACEMENT_SIZE}
                              value={Math.round(selected.width)}
                              onChange={(e) => {
                                if (box == null) return;
                                update(
                                  withPlacement(
                                    activePlacements,
                                    resizeTo(
                                      selected,
                                      {
                                        width: Number(e.target.value),
                                        height: selected.height,
                                      },
                                      box,
                                    ),
                                  ),
                                );
                              }}
                            />
                          </label>
                          <label className="flex flex-col gap-1">
                            <span className={LABEL}>
                              {t("signing.placement.heightLabel")}
                            </span>
                            <Input
                              type="number"
                              className="w-20"
                              min={MIN_PLACEMENT_SIZE}
                              value={Math.round(selected.height)}
                              onChange={(e) => {
                                if (box == null) return;
                                update(
                                  withPlacement(
                                    activePlacements,
                                    resizeTo(
                                      selected,
                                      {
                                        width: selected.width,
                                        height: Number(e.target.value),
                                      },
                                      box,
                                    ),
                                  ),
                                );
                              }}
                            />
                          </label>
                        </div>
                      )}

                      <p className="text-muted mt-3 text-[11px]">
                        {t("signing.placement.keyboardHint")}
                      </p>
                    </div>
                  )}
                </div>
              );
            })}
          </div>
        </div>
      </div>
    </div>
  );
}
