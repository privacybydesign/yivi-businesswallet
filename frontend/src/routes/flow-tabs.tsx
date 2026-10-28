import * as React from "react";

function flowTabId(idPrefix: string, tab: string): string {
  return `${idPrefix}-${tab}`;
}

// A flow page's row of tabs: a tablist with arrow keys moving between them.
// panelId names the tabpanel each one controls.
export function FlowTabs<T extends string>({
  idPrefix,
  label,
  tabs,
  active,
  onSelect,
  panelId,
}: Readonly<{
  idPrefix: string;
  label: string;
  tabs: readonly { key: T; label: React.ReactNode }[];
  active: T;
  onSelect: (tab: T) => void;
  panelId: (tab: T) => string;
}>): React.JSX.Element {
  // On the focused tab, as the tablist itself takes no focus.
  function onKeyDown(e: React.KeyboardEvent): void {
    if (e.key !== "ArrowRight" && e.key !== "ArrowLeft") return;
    e.preventDefault();
    const i = tabs.findIndex((tab) => tab.key === active);
    const delta = e.key === "ArrowRight" ? 1 : -1;
    const next = tabs[(i + delta + tabs.length) % tabs.length].key;
    onSelect(next);
    document.getElementById(flowTabId(idPrefix, next))?.focus();
  }

  return (
    <div
      role="tablist"
      aria-label={label}
      className="border-line bg-surface flex gap-1 overflow-x-auto border-b px-4 sm:px-8"
    >
      {tabs.map((tab) => {
        const selected = active === tab.key;
        return (
          <button
            key={tab.key}
            id={flowTabId(idPrefix, tab.key)}
            type="button"
            role="tab"
            aria-selected={selected}
            aria-controls={panelId(tab.key)}
            tabIndex={selected ? 0 : -1}
            onClick={() => onSelect(tab.key)}
            onKeyDown={onKeyDown}
            className={[
              "flex h-11 items-center gap-2 border-b-2 px-3.5 text-[13.5px] whitespace-nowrap transition-colors",
              selected
                ? "border-primary text-ink font-semibold"
                : "text-ink-soft hover:text-ink border-transparent font-medium",
            ].join(" ")}
          >
            {tab.label}
          </button>
        );
      })}
    </div>
  );
}

// The panel a tab controls, labelled by it.
export function FlowTabPanel({
  idPrefix,
  tab,
  id,
  hidden,
  className,
  children,
}: Readonly<{
  idPrefix: string;
  tab: string;
  id: string;
  hidden?: boolean;
  className: string;
  children: React.ReactNode;
}>): React.JSX.Element {
  return (
    <div
      id={id}
      role="tabpanel"
      aria-labelledby={flowTabId(idPrefix, tab)}
      hidden={hidden}
      className={className}
    >
      {children}
    </div>
  );
}
