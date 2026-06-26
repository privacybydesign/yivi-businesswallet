import type { IconName } from "./icon";
import { Icon } from "./icon";
import { Card } from "./card";

const ICON_SIZE = 14;

interface StatProps {
  label: string;
  value: React.ReactNode;
  hint?: string;
  icon?: IconName;
}

export function Stat({
  label,
  value,
  hint,
  icon,
}: StatProps): React.JSX.Element {
  return (
    <Card className="px-4 py-3.5">
      <div className="flex items-center justify-between">
        <span className="text-muted font-mono text-[11px] font-medium tracking-[0.08em] uppercase">
          {label}
        </span>
        {icon && (
          <span className="text-muted">
            <Icon name={icon} size={ICON_SIZE} />
          </span>
        )}
      </div>
      <div className="font-display text-ink mt-1.5 text-[26px] font-bold tracking-[-0.01em]">
        {value}
      </div>
      {hint && <div className="text-muted mt-0.5 text-[12px]">{hint}</div>}
    </Card>
  );
}
