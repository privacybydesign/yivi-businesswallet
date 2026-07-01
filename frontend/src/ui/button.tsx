import type { ButtonHTMLAttributes, ReactNode } from "react";
import type { IconName } from "./icon";
import { Icon } from "./icon";

type ButtonVariant =
  | "primary"
  | "secondary"
  | "ghost"
  | "danger"
  | "dangerGhost";
type ButtonSize = "sm" | "md" | "lg";

const VARIANT_CLASSES: Record<ButtonVariant, string> = {
  primary: "bg-primary text-primary-fg hover:bg-primary-hover",
  secondary: "bg-surface text-ink border border-line-strong hover:bg-surface-3",
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

const ICON_SIZE = 16;

interface ButtonProps extends ButtonHTMLAttributes<HTMLButtonElement> {
  variant?: ButtonVariant;
  size?: ButtonSize;
  icon?: IconName;
  iconOnly?: boolean;
  loading?: boolean;
  children?: ReactNode;
}

export function Button({
  variant = "primary",
  size = "md",
  icon,
  iconOnly = false,
  loading = false,
  children,
  className,
  type = "button",
  disabled,
  ...rest
}: ButtonProps): React.JSX.Element {
  return (
    <button
      type={type}
      disabled={disabled || loading}
      aria-busy={loading || undefined}
      className={[
        "rounded-yivi font-display inline-flex items-center justify-center gap-2 font-semibold whitespace-nowrap",
        "cursor-pointer transition-colors duration-150 disabled:cursor-not-allowed disabled:opacity-50",
        VARIANT_CLASSES[variant],
        iconOnly ? ICON_ONLY_SIZE_CLASSES[size] : SIZE_CLASSES[size],
        className ?? "",
      ].join(" ")}
      {...rest}
    >
      {loading ? (
        <span
          aria-hidden="true"
          className="h-4 w-4 animate-spin rounded-full border-2 border-current border-t-transparent"
        />
      ) : (
        icon && <Icon name={icon} size={ICON_SIZE} />
      )}
      {children}
    </button>
  );
}
