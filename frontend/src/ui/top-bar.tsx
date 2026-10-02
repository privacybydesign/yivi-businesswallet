import type { ReactNode } from "react";
import { useTranslation } from "react-i18next";
import { Breadcrumbs } from "./breadcrumb";
import { Icon } from "./icon";
import { LanguageSwitcher } from "./language-switcher";
import { useMobileNav } from "./mobile-nav";

interface TopBarProps {
  title: string;
  subtitle?: string;
  actions?: ReactNode;
  // Shown before the title block, e.g. the avatar of the record it is about.
  leading?: ReactNode;
  // Shown beside the title, e.g. status tags.
  badges?: ReactNode;
}

export function TopBar({
  title,
  subtitle,
  actions,
  leading,
  badges,
}: TopBarProps): React.JSX.Element {
  const { t } = useTranslation();
  const nav = useMobileNav();
  return (
    <div className="border-topbar-line bg-topbar text-topbar-fg z-10 border-b px-4 pt-[22px] pb-[18px] sm:sticky sm:top-0 sm:px-8">
      {/* On phones the actions drop to their own wrapping row below the title
          (order + basis-full); from sm up they sit between title and switcher,
          and wrap below the title once it would shrink under basis-48. */}
      <div className="flex flex-wrap items-end justify-between gap-x-5 gap-y-3">
        <div className="flex min-w-0 grow basis-48 items-start gap-3">
          {nav && (
            <button
              type="button"
              onClick={nav.openNav}
              aria-label={t("nav.openMenu")}
              className="text-topbar-fg-soft hover:text-topbar-fg -ml-1 shrink-0 pt-0.5 transition-colors lg:hidden"
            >
              <Icon name="menu" size={22} />
            </button>
          )}
          {leading && (
            // The title already names the record; on phones the avatar would
            // only squeeze it.
            <div className="hidden shrink-0 self-end sm:block">{leading}</div>
          )}
          <div className="min-w-0">
            <Breadcrumbs />
            <div className="flex flex-wrap items-center gap-x-3 gap-y-1">
              <h1 className="text-[22px] leading-[1.15] font-bold tracking-[-0.01em] sm:text-[26px]">
                {title}
              </h1>
              {badges}
            </div>
            {subtitle && (
              <div className="text-topbar-fg-soft mt-1 text-[12.5px]">
                {subtitle}
              </div>
            )}
          </div>
        </div>
        {actions && (
          <div className="order-3 flex basis-full flex-wrap gap-2 sm:order-2 sm:basis-auto">
            {actions}
          </div>
        )}
        <div className="order-2 shrink-0 sm:order-3">
          <LanguageSwitcher />
        </div>
      </div>
    </div>
  );
}
