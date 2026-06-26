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
    <div className="sticky top-0 z-10 border-b border-line bg-surface px-8 pb-[18px] pt-[22px]">
      <div className="flex items-end justify-between gap-5">
        <div>
          <h1 className="text-[26px] font-bold leading-[1.15] tracking-[-0.01em]">
            {title}
          </h1>
          {subtitle && (
            <div className="mt-1 text-[12.5px] text-ink-soft">{subtitle}</div>
          )}
        </div>
        {actions && <div className="flex gap-2">{actions}</div>}
      </div>
    </div>
  );
}
