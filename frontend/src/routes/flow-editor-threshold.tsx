import { useEffect, useRef, useState } from "react";
import { useTranslation } from "react-i18next";
import * as React from "react";
import {
  FACE_MATCH_MAX,
  FACE_MATCH_MIN,
  FACE_MATCH_RECOMMENDED,
  FACE_MATCH_STEP,
  assuranceLevelLabel,
  faceMatchFloor,
  faceMatchValue,
  formatFaceMatch,
} from "../lib/identity-proofing";
import type { ProofingFlowDraft } from "../lib/identity-proofing";
import { Icon } from "../ui";
import { HINT } from "./flow-editor-parts";

const INPUT_ID = "flow-face-threshold";
const HINT_ID = `${INPUT_ID}-hint`;
const NOTE_ID = `${INPUT_ID}-note`;
const TIP_ID = `${INPUT_ID}-tip`;
// The thumb's width in px, as .threshold-range draws it in index.css.
const THUMB_SIZE = 20;
const LOCK_SIZE = 13;
const WARNING_SIZE = 20;
const MIDPOINT = 0.5;
const SCALE = [
  FACE_MATCH_MIN,
  MIDPOINT,
  FACE_MATCH_RECOMMENDED,
  FACE_MATCH_MAX,
];
// The warning's tooltip shows on hover and focus (CSS), stays when the button
// is tapped (touch has neither), and goes on Escape, wherever focus is, until
// the pointer or focus leaves.
type TipState = "auto" | "pinned" | "dismissed";
const TIP_VISIBILITY: Record<TipState, string> = {
  auto: "hidden group-hover:block group-focus-within:block",
  pinned: "block",
  dismissed: "hidden",
};
const TRACK = "absolute top-[11px] h-1.5";
const HATCH =
  "bg-[repeating-linear-gradient(45deg,var(--color-line-strong)_0_4px,var(--color-line)_4px_8px)]";

// Where a value sits along the track: the native thumb's centre travels from
// half a thumb in to half a thumb short of the far end.
function along(value: number): string {
  return `calc(${THUMB_SIZE / 2}px + (100% - ${THUMB_SIZE}px) * ${value})`;
}

// The face match threshold slider under Checks: the range runs from the
// level's floor, the part under it drawn hatched and out of reach.
export function FaceMatchThreshold({
  draft,
  update,
}: Readonly<{
  draft: ProofingFlowDraft;
  update: (patch: Partial<ProofingFlowDraft>) => void;
}>): React.JSX.Element {
  const { t, i18n } = useTranslation();
  const [tip, setTip] = useState<TipState>("auto");
  const tipGroup = useRef<HTMLSpanElement>(null);
  const floor = faceMatchFloor(draft.assuranceLevel);
  const floored = floor > FACE_MATCH_MIN;
  const value = faceMatchValue(draft);
  const low = value < FACE_MATCH_RECOMMENDED;
  const warned = low && !floored;

  // Hover opens the tooltip while focus may be on the slider or another field,
  // so Escape is heard on the document, and counts only while the tooltip shows.
  // Leaving re-arms it through the native mouseleave: React's rides on
  // mouseout, which Chrome sometimes drops.
  useEffect(() => {
    const group = tipGroup.current;
    if (!warned || group === null) {
      return;
    }

    const dismiss = (event: KeyboardEvent): void => {
      if (event.key !== "Escape") {
        return;
      }

      const shown = group.matches(":hover, :focus-within");
      setTip((current) =>
        shown || current === "pinned" ? "dismissed" : current,
      );
    };
    const rearm = (): void => {
      setTip((current) => (current === "dismissed" ? "auto" : current));
    };

    document.addEventListener("keydown", dismiss);
    group.addEventListener("mouseleave", rearm);
    return () => {
      document.removeEventListener("keydown", dismiss);
      group.removeEventListener("mouseleave", rearm);
    };
  }, [warned]);

  const show = (number: number): string =>
    formatFaceMatch(number, i18n.language);
  const min = show(FACE_MATCH_RECOMMENDED);
  // The range starts where the floor does, so the native thumb lands on the
  // value's place along the whole scale.
  const start = `calc((100% - ${THUMB_SIZE}px) * ${floor})`;
  const describedBy = [HINT_ID, floored ? NOTE_ID : null, low ? TIP_ID : null]
    .filter(Boolean)
    .join(" ");

  return (
    <div className="flex max-w-lg flex-col gap-2.5 pt-1">
      <div className="flex flex-col gap-0.5">
        <label htmlFor={INPUT_ID} className="text-[14px] font-semibold">
          {t("identityProofingFlows.editor.checks.threshold.label")}
        </label>
        <span id={HINT_ID} className="text-ink-soft text-[13px]">
          {t("identityProofingFlows.editor.checks.threshold.hint")}
        </span>
      </div>
      <div className="relative grid grid-cols-1 items-center gap-x-4 gap-y-1 sm:grid-cols-[minmax(0,1fr)_auto]">
        <div className="relative h-7">
          <div
            aria-hidden="true"
            className={`${TRACK} bg-line inset-x-0 rounded-full`}
          />
          <div
            aria-hidden="true"
            className={`${TRACK} left-0 rounded-full ${low ? "bg-warning-fg" : "bg-ink"}`}
            style={{ width: along(value) }}
          />
          {floored && (
            <div
              aria-hidden="true"
              className={`${TRACK} ${HATCH} left-0 rounded-l-full`}
              style={{ width: along(floor) }}
            />
          )}
          {!floored && (
            <div
              aria-hidden="true"
              className="bg-ink-soft absolute top-1.5 h-4 w-0.5 -translate-x-1/2"
              style={{ left: along(FACE_MATCH_RECOMMENDED) }}
            />
          )}
          <input
            id={INPUT_ID}
            type="range"
            className="threshold-range absolute top-0"
            style={{ left: start, width: `calc(100% - ${start})` }}
            min={floor}
            max={FACE_MATCH_MAX}
            step={FACE_MATCH_STEP}
            value={value}
            data-warn={low || undefined}
            aria-describedby={describedBy}
            onChange={(event) =>
              update({ faceMatchThreshold: Number(event.target.value) })
            }
          />
        </div>
        <div
          aria-hidden="true"
          className="text-ink-soft relative h-[18px] text-[12px]"
        >
          {SCALE.map((mark) => (
            <span
              key={mark}
              className={`absolute -translate-x-1/2 ${mark === FACE_MATCH_RECOMMENDED ? "text-ink font-semibold" : ""}`}
              style={{ left: along(mark) }}
            >
              {show(mark)}
            </span>
          ))}
        </div>
        <div className="relative mt-1 flex items-center gap-1 sm:static sm:col-start-2 sm:row-start-1 sm:mt-0">
          <output
            htmlFor={INPUT_ID}
            aria-live="off"
            className={`rounded-yivi flex h-9 w-[52px] items-center justify-center border text-[13.5px] font-semibold ${
              low
                ? "border-warning bg-warning-bg text-warning-fg"
                : "border-line-strong"
            }`}
          >
            {show(value)}
          </output>
          {!floored && (
            <span
              ref={tipGroup}
              className="group flex size-9 items-center justify-center"
            >
              {low && (
                <>
                  <button
                    type="button"
                    aria-label={t(
                      "identityProofingFlows.editor.checks.threshold.warningLabel",
                      { min },
                    )}
                    aria-describedby={TIP_ID}
                    className="text-warning-fg rounded-yivi flex size-11 flex-none items-center justify-center"
                    onClick={() =>
                      setTip((current) =>
                        current === "pinned" ? "auto" : "pinned",
                      )
                    }
                    onBlur={() => setTip("auto")}
                  >
                    <Icon name="warning" size={WARNING_SIZE} />
                  </button>
                  {/* The padding bridges the gap to the button, so the pointer
                      stays over the tooltip on its way there. */}
                  <span
                    role="tooltip"
                    id={TIP_ID}
                    className={`absolute right-0 bottom-full z-10 w-70 max-w-full pb-2 ${TIP_VISIBILITY[tip]}`}
                  >
                    <span className="bg-primary text-primary-fg rounded-yivi-sm shadow-card-lg block px-3 py-2 text-[13px] leading-[19px]">
                      {t(
                        "identityProofingFlows.editor.checks.threshold.warning",
                        {
                          min,
                        },
                      )}
                    </span>
                  </span>
                </>
              )}
            </span>
          )}
        </div>
      </div>
      {floored ? (
        <span
          id={NOTE_ID}
          className="text-ink-soft inline-flex items-center gap-1.5 text-[12.5px]"
        >
          <Icon name="lock" size={LOCK_SIZE} />
          {t("identityProofingFlows.editor.checks.threshold.floor", {
            min: show(floor),
            level: assuranceLevelLabel(draft.assuranceLevel, t),
          })}
        </span>
      ) : (
        <span className={HINT}>
          {t("identityProofingFlows.editor.checks.threshold.recommended", {
            min,
          })}
        </span>
      )}
    </div>
  );
}
