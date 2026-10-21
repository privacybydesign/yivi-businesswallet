import * as React from "react";
import { Icon } from "../ui";

const CHEVRON_SIZE = 12;
const LOCK_SIZE = 13;
const CHECKBOX = "mt-0.5 h-[18px] w-[18px] flex-none accent-ink";
const SELECT_CLASS =
  "rounded-yivi border-line-strong bg-surface text-ink focus:border-ink focus:ring-ink/10 h-9 w-full appearance-none border pr-10 pl-4 text-[13.5px] transition-colors outline-none focus:ring-3";

export const HINT = "text-ink-soft text-[12.5px]";
export const ERROR = "text-error text-[12.5px]";

// A select with its chevron inset from the right edge.
export function ChevronSelect({
  id,
  value,
  onChange,
  children,
}: {
  id: string;
  value: string;
  onChange: (value: string) => void;
  children: React.ReactNode;
}): React.JSX.Element {
  return (
    <div className="relative w-full">
      <select
        id={id}
        className={SELECT_CLASS}
        value={value}
        onChange={(event) => onChange(event.target.value)}
      >
        {children}
      </select>
      <Icon
        name="chevron_down"
        size={CHEVRON_SIZE}
        className="pointer-events-none absolute top-3 right-4"
      />
    </div>
  );
}

// Why an option cannot be changed: shown under it, and as its tooltip.
export interface OptionLock {
  note: string;
  tooltip?: string;
}

// A checkbox with a label, a hint and, when something else fixes it, the
// reason it is locked.
export function OptionRow({
  id,
  checked,
  disabled,
  label,
  hint,
  lock,
  onChange,
}: {
  id: string;
  checked: boolean;
  disabled?: boolean;
  label: string;
  hint?: string;
  lock?: OptionLock;
  onChange?: (checked: boolean) => void;
}): React.JSX.Element {
  const hintId = `${id}-hint`;
  const lockId = `${id}-lock`;
  const locked = lock !== undefined;
  const off = (disabled ?? false) || locked;

  return (
    <div className="flex items-start gap-3" title={lock?.tooltip}>
      <input
        id={id}
        type="checkbox"
        className={CHECKBOX}
        checked={checked}
        disabled={off}
        aria-describedby={
          [hint && hintId, locked && lockId].filter(Boolean).join(" ") ||
          undefined
        }
        onChange={(event) => onChange?.(event.target.checked)}
      />
      <label
        htmlFor={id}
        className={`flex flex-col gap-0.5 ${off ? "cursor-default" : "cursor-pointer"} ${off && !checked ? "opacity-50" : ""}`}
      >
        <span className="text-[14px] font-semibold">{label}</span>
        {hint && (
          <span id={hintId} className="text-ink-soft text-[13px] leading-5">
            {hint}
          </span>
        )}
        {lock && (
          <span
            id={lockId}
            className="text-ink-soft inline-flex items-center gap-1 text-[12.5px]"
          >
            <Icon name="lock" size={LOCK_SIZE} />
            {lock.note}
          </span>
        )}
      </label>
    </div>
  );
}

// A section of the advanced settings, titled with the question it answers.
export function Section({
  title,
  hint,
  children,
}: {
  title: string;
  hint: string;
  children: React.ReactNode;
}): React.JSX.Element {
  return (
    <section className="border-line flex flex-col gap-4 border-t px-6 py-5">
      <div className="flex flex-col gap-1">
        <h3 className="font-display text-[15px] font-semibold">{title}</h3>
        <p className="text-ink-soft text-[13px]">{hint}</p>
      </div>
      {children}
    </section>
  );
}
