import type { HTMLAttributes, ReactNode } from "react";
import { Icon } from "./icon";

const TH_CLASS =
  "border-line bg-surface-2 text-muted border-b px-3.5 py-2.5 text-left font-mono text-[11px] font-medium tracking-[0.06em] uppercase";
const TD_CLASS = "border-line border-b px-3.5 py-3";

export type SortDir = "asc" | "desc";

export function Table({
  children,
  className,
}: {
  children: ReactNode;
  className?: string;
}): React.JSX.Element {
  return (
    // Wide tables scroll horizontally within their container on small screens
    // instead of forcing the whole page to overflow.
    <div className="overflow-x-auto">
      <table
        className={[
          "min-w-full border-collapse text-[13.5px]",
          className ?? "",
        ].join(" ")}
      >
        {children}
      </table>
    </div>
  );
}

function Head({ children }: { children: ReactNode }): React.JSX.Element {
  return (
    <thead>
      <tr>{children}</tr>
    </thead>
  );
}

interface HeaderCellProps {
  children?: ReactNode;
  className?: string;
  srOnly?: boolean;
  // When onSort is set the header becomes a sort toggle. sortDir is the active
  // direction when this is the sorted column, or null when sortable but inactive.
  sortDir?: SortDir | null;
  onSort?: () => void;
}

function HeaderCell({
  children,
  className,
  srOnly,
  sortDir,
  onSort,
}: HeaderCellProps): React.JSX.Element {
  const classes = [TH_CLASS, className ?? ""].join(" ");

  if (!onSort) {
    return (
      <th className={classes}>
        {srOnly ? <span className="sr-only">{children}</span> : children}
      </th>
    );
  }

  const active = sortDir != null;
  return (
    <th
      className={classes}
      aria-sort={
        active ? (sortDir === "asc" ? "ascending" : "descending") : "none"
      }
    >
      <button
        type="button"
        onClick={onSort}
        className="hover:text-ink -mx-1 inline-flex cursor-pointer items-center gap-1 rounded px-1 py-0.5 uppercase"
      >
        {children}
        <Icon
          name={sortDir === "desc" ? "chevron_down" : "chevron_up"}
          size={12}
          className={active ? "text-ink" : "text-muted opacity-40"}
        />
      </button>
    </th>
  );
}

function Body({ children }: { children: ReactNode }): React.JSX.Element {
  return <tbody>{children}</tbody>;
}

function Row({
  children,
  className,
  ...rest
}: HTMLAttributes<HTMLTableRowElement>): React.JSX.Element {
  return (
    <tr className={className} {...rest}>
      {children}
    </tr>
  );
}

function Cell({
  children,
  className,
  ...rest
}: HTMLAttributes<HTMLTableCellElement>): React.JSX.Element {
  return (
    <td className={[TD_CLASS, className ?? ""].join(" ")} {...rest}>
      {children}
    </td>
  );
}

// A single full-width row for loading and empty states; carries no row border
// so it reads as a message rather than a data row.
function State({
  colSpan,
  children,
}: {
  colSpan: number;
  children: ReactNode;
}): React.JSX.Element {
  return (
    <tr>
      <td className="text-ink-soft px-3.5 py-3" colSpan={colSpan}>
        {children}
      </td>
    </tr>
  );
}

Table.Head = Head;
Table.HeaderCell = HeaderCell;
Table.Body = Body;
Table.Row = Row;
Table.Cell = Cell;
Table.State = State;
