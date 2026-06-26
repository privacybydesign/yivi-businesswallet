import type { HTMLAttributes, ReactNode } from "react";

type CardVariant = "normal" | "highlight";

const VARIANT_CLASSES: Record<CardVariant, string> = {
  normal: "bg-surface border border-line",
  highlight: "bg-highlight border border-highlight-border",
};

interface CardProps extends HTMLAttributes<HTMLDivElement> {
  variant?: CardVariant;
  children?: ReactNode;
}

export function Card({
  variant = "normal",
  children,
  className,
  ...rest
}: CardProps): React.JSX.Element {
  return (
    <div
      className={[
        "rounded-yivi shadow-card",
        VARIANT_CLASSES[variant],
        className ?? "",
      ].join(" ")}
      {...rest}
    >
      {children}
    </div>
  );
}
