import type { ReactNode } from "react";

interface TopBarProps {
  title: string;
  subtitle?: string;
  actions?: ReactNode;
}

export function TopBar({
  title,
  subtitle,
  actions,
}: TopBarProps): React.JSX.Element {
  return (
    <div className="border-line bg-surface sticky top-0 z-10 border-b px-8 pt-[22px] pb-[18px]">
      <div className="flex items-end justify-between gap-5">
        <div>
          <h1 className="text-[26px] leading-[1.15] font-bold tracking-[-0.01em]">
            {title}
          </h1>
          {subtitle && (
            <div className="text-ink-soft mt-1 text-[12.5px]">{subtitle}</div>
          )}
        </div>
        {actions && <div className="flex gap-2">{actions}</div>}
      </div>
    </div>
  );
}
