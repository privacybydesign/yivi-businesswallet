import { useId } from "react";
import type { ReactNode } from "react";
import { Card } from "./card";

interface TaskSectionProps {
  title: string;
  // How many items the section holds, beside the title: a number, or a
  // label such as "200+".
  count?: number | string;
  hint?: string;
  // Beside the title, e.g. the link to the whole list.
  action?: ReactNode;
  children?: ReactNode;
}

// A to-do section: a titled card whose rows are TaskRows, or a TaskEmpty.
export function TaskSection({
  title,
  count,
  hint,
  action,
  children,
}: Readonly<TaskSectionProps>): React.JSX.Element {
  const headingId = useId();
  return (
    <Card role="region" aria-labelledby={headingId}>
      <div className="flex flex-wrap items-center justify-between gap-x-4 gap-y-2 px-5 py-[18px]">
        <div className="flex flex-col gap-1">
          <h2
            id={headingId}
            className="font-display text-ink text-[18px] font-semibold"
          >
            {title}
            {count !== undefined && (
              <span className="text-ink-soft ml-2 font-normal">{count}</span>
            )}
          </h2>
          {hint && <span className="text-ink-soft text-[13px]">{hint}</span>}
        </div>
        {action}
      </div>
      {children}
    </Card>
  );
}

interface TaskRowProps {
  title: ReactNode;
  detail: string;
  // At the row's end: its action, or when it was sent.
  aside?: ReactNode;
}

export function TaskRow({
  title,
  detail,
  aside,
}: Readonly<TaskRowProps>): React.JSX.Element {
  return (
    <li className="border-line flex flex-wrap items-center justify-between gap-3 border-t px-5 py-4">
      <div className="flex min-w-0 flex-col gap-0.5">
        <span className="text-ink font-semibold">{title}</span>
        <span className="text-ink-soft text-[13px]">{detail}</span>
      </div>
      {aside}
    </li>
  );
}

// What a section says when it holds nothing.
export function TaskEmpty({
  children,
}: Readonly<{
  children: ReactNode;
}>): React.JSX.Element {
  return (
    <p className="border-line text-ink-soft border-t px-5 pt-3.5 pb-[18px] text-[14px]">
      {children}
    </p>
  );
}
