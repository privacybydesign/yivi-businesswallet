import type { ReactNode } from "react";
import { useTranslation } from "react-i18next";
import { useBrand } from "./brand";
import { Breadcrumbs } from "./breadcrumb";
import { Icon } from "./icon";
import { Logo } from "./logo";
import { useMobileNav } from "./mobile-nav";

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
  const { t } = useTranslation();
  const nav = useMobileNav();
  const brand = useBrand();
  return (
    <div className="border-line bg-surface sticky top-0 z-10 border-b px-4 pt-[22px] pb-[18px] sm:px-8">
      <div className="flex items-end justify-between gap-5">
        <div className="flex min-w-0 items-start gap-3">
          {nav && (
            <button
              type="button"
              onClick={nav.openNav}
              aria-label={t("nav.openMenu")}
              className="text-ink-soft hover:text-ink -ml-1 shrink-0 pt-0.5 transition-colors lg:hidden"
            >
              <Icon name="menu" size={22} />
            </button>
          )}
          <div className="min-w-0">
            <Breadcrumbs />
            <h1 className="text-[22px] leading-[1.15] font-bold tracking-[-0.01em] sm:text-[26px]">
              {title}
            </h1>
            {subtitle && (
              <div className="text-ink-soft mt-1 text-[12.5px]">{subtitle}</div>
            )}
          </div>
        </div>
        <div className="flex shrink-0 items-center gap-4">
          {actions && <div className="flex shrink-0 gap-2">{actions}</div>}
          {brand.logoUri && (
            <Logo
              src={brand.logoUri}
              alt={brand.name || t("common.orgLogoAlt")}
            />
          )}
        </div>
      </div>
    </div>
  );
}
