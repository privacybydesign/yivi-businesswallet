import * as React from "react";
import { Icon } from "./icon";
import type { IconName } from "./icon";

export interface DataDisclosureItem {
  icon: IconName;
  // The attribute being shared (already translated).
  label: string;
  // Why it is shared / what it is used for (already translated).
  detail: string;
}

interface DataDisclosureProps {
  items: DataDisclosureItem[];
}

// DataDisclosure lists the data attributes a wallet discloses during a flow, each
// with a short explanation. Presentational only — the route supplies translated
// strings. Used to make identity read-out transparent before the user scans.
export function DataDisclosure({
  items,
}: DataDisclosureProps): React.JSX.Element {
  return (
    <ul className="flex flex-col gap-3">
      {items.map((item) => (
        <li key={item.label} className="flex items-start gap-3">
          <span className="bg-highlight text-link rounded-yivi-sm flex h-8 w-8 shrink-0 items-center justify-center">
            <Icon name={item.icon} size={16} />
          </span>
          <div className="min-w-0">
            <div className="text-ink text-[13.5px] font-semibold">
              {item.label}
            </div>
            <div className="text-ink-soft text-[12.5px] leading-snug">
              {item.detail}
            </div>
          </div>
        </li>
      ))}
    </ul>
  );
}
