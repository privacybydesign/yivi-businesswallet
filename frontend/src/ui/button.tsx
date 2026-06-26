import type { ButtonHTMLAttributes, ReactNode } from "react";
import type { IconName } from "./icon";
import { Icon } from "./icon";

type ButtonVariant = "primary" | "secondary" | "ghost" | "danger";
type ButtonSize = "sm" | "md" | "lg";

const VARIANT_CLASSES: Record<ButtonVariant, string> = {
  primary: "bg-primary text-primary-fg hover:bg-primary-hover",
  secondary: "bg-surface text-ink border border-line-strong hover:bg-surface-3",
  ghost: "bg-transparent text-ink hover:bg-surface-3",
  danger: "bg-transparent text-error border border-error hover:bg-error-bg",
};

const SIZE_CLASSES: Record<ButtonSize, string> = {
  sm: "h-7 px-2.5 text-[12.5px]",
  md: "h-9 px-3.5 text-[13.5px]",
  lg: "h-11 px-[18px] text-[15px]",
};

const ICON_SIZE = 16;

interface ButtonProps extends ButtonHTMLAttributes<HTMLButtonElement> {
  variant?: ButtonVariant;
  size?: ButtonSize;
  icon?: IconName;
  children?: ReactNode;
}

export function Button({
  variant = "primary",
  size = "md",
  icon,
  children,
  className,
  type = "button",
  ...rest
}: ButtonProps): React.JSX.Element {
  return (
    <button
      type={type}
      className={[
        "inline-flex items-center justify-center gap-2 rounded-yivi font-display font-semibold whitespace-nowrap",
        "cursor-pointer transition-colors duration-150 disabled:cursor-not-allowed disabled:opacity-50",
        VARIANT_CLASSES[variant],
        SIZE_CLASSES[size],
        className ?? "",
      ].join(" ")}
      {...rest}
    >
      {icon && <Icon name={icon} size={ICON_SIZE} />}
      {children}
    </button>
  );
}
