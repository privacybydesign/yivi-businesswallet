import type { ButtonHTMLAttributes, ReactNode } from "react";
import type { ButtonSize, ButtonVariant } from "./button-class";
import { buttonClassName } from "./button-class";
import type { IconName } from "./icon";
import { Icon } from "./icon";

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
}: Readonly<ButtonProps>): React.JSX.Element {
  return (
    <button
      type={type}
      disabled={disabled || loading}
      aria-busy={loading || undefined}
      className={`${buttonClassName(variant, size, iconOnly)} ${className ?? ""}`}
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
