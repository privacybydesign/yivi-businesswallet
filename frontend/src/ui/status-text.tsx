import type { ReactNode } from "react";

type StatusTextTone = "default" | "green" | "amber" | "red";

const TONE_CLASSES: Record<StatusTextTone, string> = {
  default: "font-normal",
  green: "text-success-fg font-semibold",
  amber: "text-warning-fg font-semibold",
  red: "text-error font-semibold",
};

interface StatusTextProps {
  tone?: StatusTextTone;
  title?: string;
  children: ReactNode;
}

// A status, version or count as plain text. It stays on one line: a result
// that wraps in a table cell reads as two.
export function StatusText({
  tone = "default",
  title,
  children,
}: Readonly<StatusTextProps>): React.JSX.Element {
  return (
    <span title={title} className={`whitespace-nowrap ${TONE_CLASSES[tone]}`}>
      {children}
    </span>
  );
}
