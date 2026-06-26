import type { ReactNode } from "react";

type TagTone = "default" | "green" | "amber" | "red" | "blue";

const TONE_CLASSES: Record<TagTone, string> = {
  default: "bg-surface-3 text-ink-soft",
  green: "bg-success-bg text-success",
  amber: "bg-warning-bg text-warning-fg",
  red: "bg-error-bg text-error",
  blue: "bg-highlight text-link",
};

interface TagProps {
  tone?: TagTone;
  dot?: boolean;
  children: ReactNode;
}

export function Tag({
  tone = "default",
  dot = false,
  children,
}: TagProps): React.JSX.Element {
  return (
    <span
      className={[
        "inline-flex items-center gap-1.5 h-[22px] px-2 rounded-full text-[11.5px] font-semibold",
        TONE_CLASSES[tone],
      ].join(" ")}
    >
      {dot && (
        <span
          className="w-1.5 h-1.5 rounded-full bg-current"
          aria-hidden="true"
        />
      )}
      {children}
    </span>
  );
}
