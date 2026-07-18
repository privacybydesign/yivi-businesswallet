import * as React from "react";
import { Icon } from "./icon";

const CHECK_ICON_SIZE = 15;

interface StepperProps {
  // Already-translated step labels, in order.
  steps: string[];
  // Zero-based index of the active step. A value past the last index marks every
  // step complete (e.g. a final confirmation screen).
  current: number;
}

// Stepper renders a horizontal progress indicator: a numbered circle per step,
// connected by lines, with the label beneath each circle. Presentational only —
// it takes already-translated labels (translation happens at the route level).
export function Stepper({ steps, current }: StepperProps): React.JSX.Element {
  return (
    <ol className="flex items-start">
      {steps.map((label, index) => {
        const done = index < current;
        const active = index === current;
        return (
          <li key={label} className="flex flex-1 flex-col items-center">
            <div className="flex w-full items-center">
              <span
                className={[
                  "h-0.5 flex-1 rounded-full",
                  index === 0
                    ? "invisible"
                    : index <= current
                      ? "bg-primary"
                      : "bg-line-strong",
                ].join(" ")}
              />
              <span
                className={[
                  "flex h-8 w-8 shrink-0 items-center justify-center rounded-full text-[13px] font-semibold",
                  done
                    ? "bg-primary text-primary-fg"
                    : active
                      ? "border-primary text-primary border-2"
                      : "border-line-strong text-muted border",
                ].join(" ")}
              >
                {done ? (
                  <Icon name="valid" size={CHECK_ICON_SIZE} />
                ) : (
                  index + 1
                )}
              </span>
              <span
                className={[
                  "h-0.5 flex-1 rounded-full",
                  index === steps.length - 1
                    ? "invisible"
                    : index < current
                      ? "bg-primary"
                      : "bg-line-strong",
                ].join(" ")}
              />
            </div>
            <span
              className={[
                "mt-2 px-1 text-center text-[12px] leading-tight",
                active
                  ? "text-ink font-semibold"
                  : done
                    ? "text-ink"
                    : "text-muted",
              ].join(" ")}
            >
              {label}
            </span>
          </li>
        );
      })}
    </ol>
  );
}
