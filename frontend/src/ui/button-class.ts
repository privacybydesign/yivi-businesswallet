export type ButtonVariant =
  | "primary"
  | "secondary"
  | "ghost"
  | "danger"
  | "dangerGhost";
export type ButtonSize = "sm" | "md" | "lg";

const VARIANT_CLASSES: Record<ButtonVariant, string> = {
  primary: "bg-primary text-primary-fg hover:bg-primary-hover",
  secondary:
    "bg-surface text-ink border-(length:--yb-button-outline-width) border-(--yb-button-outline) hover:bg-surface-3",
  ghost: "bg-transparent text-ink hover:bg-surface-3",
  danger: "bg-transparent text-error border border-error hover:bg-error-bg",
  dangerGhost:
    "bg-transparent text-ink-soft hover:bg-error-bg hover:text-error focus-visible:bg-error-bg focus-visible:text-error",
};

const SIZE_CLASSES: Record<ButtonSize, string> = {
  sm: "h-7 px-2.5 text-[12.5px]",
  md: "h-9 px-3.5 text-[13.5px]",
  lg: "h-11 px-[18px] text-[15px]",
};

const ICON_ONLY_SIZE_CLASSES: Record<ButtonSize, string> = {
  sm: "h-7 w-7",
  md: "h-9 w-9",
  lg: "h-11 w-11",
};

// buttonClassName is a Button's look, for a link that must look like one.
export function buttonClassName(
  variant: ButtonVariant = "primary",
  size: ButtonSize = "md",
  iconOnly = false,
): string {
  return [
    "rounded-button font-button inline-flex items-center justify-center gap-2 font-(--yb-button-weight) whitespace-nowrap",
    "cursor-pointer transition-colors duration-150 disabled:cursor-not-allowed disabled:opacity-50",
    VARIANT_CLASSES[variant],
    iconOnly ? ICON_ONLY_SIZE_CLASSES[size] : SIZE_CLASSES[size],
  ].join(" ");
}
