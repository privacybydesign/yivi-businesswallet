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
        <span className="font-mono text-[11px] font-medium uppercase tracking-[0.08em] text-muted">
          {label}
        </span>
        {icon && (
          <span className="text-muted">
            <Icon name={icon} size={ICON_SIZE} />
          </span>
        )}
      </div>
      <div className="mt-1.5 font-display text-[26px] font-bold tracking-[-0.01em] text-ink">
        {value}
      </div>
      {hint && <div className="mt-0.5 text-[12px] text-muted">{hint}</div>}
    </Card>
  );
}
