import type { SelectHTMLAttributes } from "react";
import { Icon } from "./icon";

const CHEVRON_SIZE = 12;

type SelectState = "ok" | "error";
type SelectSize = "default" | "compact";

const STATE_CLASSES: Record<SelectState, string> = {
  ok: "border-line-strong focus:border-ink focus:ring-ink/10",
  error: "border-error focus:border-error focus:ring-error/10",
};

const SIZE_CLASSES: Record<SelectSize, string> = {
  default: "h-9",
  compact: "h-8",
};

interface SelectProps extends Omit<
  SelectHTMLAttributes<HTMLSelectElement>,
  "className" | "size"
> {
  state?: SelectState;
  size?: SelectSize;
  // Lays out the field, not the control: width, max-width and margin. Replaces
  // the default full width.
  className?: string;
}

// A native select with its chevron inset like its text: the value starts 16 px
// from the left and stops 40 px from the right (16 inset + 12 chevron + 12
// gap), so a long value is cut off before the chevron.
export function Select({
  state = "ok",
  size = "default",
  className = "w-full",
  children,
  ...rest
}: SelectProps): React.JSX.Element {
  return (
    <div className={`relative ${className}`}>
      <select
        className={[
          "peer rounded-yivi bg-surface text-ink w-full appearance-none border",
          "pr-10 pl-4 text-[13.5px] text-ellipsis transition-colors outline-none",
          "focus:ring-3 disabled:opacity-60",
          STATE_CLASSES[state],
          SIZE_CLASSES[size],
        ].join(" ")}
        {...rest}
      >
        {children}
      </select>
      <Icon
        name="chevron_down"
        size={CHEVRON_SIZE}
        className="text-ink pointer-events-none absolute top-1/2 right-4 -translate-y-1/2 peer-disabled:opacity-60"
      />
    </div>
  );
}
