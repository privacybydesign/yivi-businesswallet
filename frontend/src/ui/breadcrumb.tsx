import { Link, useMatches } from "react-router";
import { useTranslation } from "react-i18next";
import { useQueryClient } from "@tanstack/react-query";
import type { QueryClient } from "@tanstack/react-query";
import type { TFunction } from "i18next";
import { Icon } from "./icon";
import * as React from "react";

export interface BreadcrumbItem {
  label: string;
  // Omit `to` for the current page (the last item); parents link to their route.
  to?: string;
}

// A route opts into the breadcrumb trail by exposing `handle: { crumb }`.
// `crumb` is a function so it can resolve i18n keys and dynamic data (e.g. the
// org name from the query cache) at render time — see router.tsx.
export interface CrumbContext {
  params: Record<string, string | undefined>;
  t: TFunction;
  queryClient: QueryClient;
}

export interface RouteHandle {
  crumb?: (ctx: CrumbContext) => string;
}

const SEPARATOR_SIZE = 12;

function Breadcrumb({ items }: { items: BreadcrumbItem[] }): React.JSX.Element {
  const { t } = useTranslation();
  return (
    <nav aria-label={t("nav.breadcrumb")}>
      <ol className="flex items-center gap-1.5 text-[12px]">
        {items.map((item, index) => {
          const isLast = index === items.length - 1;
          return (
            <li key={item.label} className="flex items-center gap-1.5">
              {item.to && !isLast ? (
                <Link
                  to={item.to}
                  className="text-muted hover:text-ink transition-colors"
                >
                  {item.label}
                </Link>
              ) : (
                <span
                  className={
                    isLast ? "text-ink-soft font-medium" : "text-muted"
                  }
                  aria-current={isLast ? "page" : undefined}
                >
                  {item.label}
                </span>
              )}
              {!isLast && (
                <Icon
                  name="chevron_right"
                  size={SEPARATOR_SIZE}
                  className="text-muted"
                />
              )}
            </li>
          );
        })}
      </ol>
    </nav>
  );
}

// Builds the trail automatically from the matched route chain. Each match that
// declares `handle.crumb` contributes one item; its pathname is the link.
export function Breadcrumbs(): React.JSX.Element | null {
  const matches = useMatches();
  const { t } = useTranslation();
  const queryClient = useQueryClient();

  // Crumbs may read from the query cache (e.g. the org name); re-render when it
  // changes so a name that resolves after first paint replaces its fallback.
  const [, bumpVersion] = React.useReducer((n: number): number => n + 1, 0);
  React.useEffect(
    () => queryClient.getQueryCache().subscribe(bumpVersion),
    [queryClient],
  );

  const items: BreadcrumbItem[] = matches.flatMap((match) => {
    const handle = match.handle as RouteHandle | undefined;
    if (!handle?.crumb) {
      return [];
    }
    return [
      {
        label: handle.crumb({ params: match.params, t, queryClient }),
        to: match.pathname,
      },
    ];
  });

  if (items.length === 0) {
    return null;
  }

  // The last crumb is the current page: render it as plain text, not a link.
  const trail = items.map((item, index) =>
    index === items.length - 1 ? { label: item.label } : item,
  );

  return (
    <div className="mb-1.5">
      <Breadcrumb items={trail} />
    </div>
  );
}
