import { NavLink } from "react-router";
import { useTranslation } from "react-i18next";
import type { IconName } from "./icon";
import { Icon } from "./icon";
import { Avatar } from "./avatar";
import { Logo } from "./logo";
import * as React from "react";

interface NavItem {
  to: string;
  labelKey: "nav.dashboard" | "nav.organizations";
  icon: IconName;
  end?: boolean;
}

const NAV_ITEMS: NavItem[] = [
  { to: "/", labelKey: "nav.dashboard", icon: "view", end: true },
  { to: "/organizations", labelKey: "nav.organizations", icon: "personal" },
];

const NAV_ICON_SIZE = 16;

interface SidebarProps {
  email: string | null;
  onLogout: () => void;
  loggingOut: boolean;
}

export function Sidebar({
  email,
  onLogout,
  loggingOut,
}: SidebarProps): React.JSX.Element {
  const { t } = useTranslation();
  return (
    <aside className="sticky top-0 flex h-screen w-58 shrink-0 flex-col border-r border-line bg-surface">
      <div className="border-b border-line px-4 py-3.5">
        <Logo />
      </div>

      <nav className="flex-1 overflow-y-auto px-2.5 py-2">
        {NAV_ITEMS.map((item) => (
          <NavLink
            key={item.to}
            to={item.to}
            end={item.end}
            className={({ isActive }) =>
              [
                "relative flex h-8.5 items-center gap-2.5 rounded-md px-2.5 text-[13.5px] transition-colors",
                isActive
                  ? "bg-surface-3 font-semibold text-ink"
                  : "font-medium text-ink-soft hover:bg-surface-3 hover:text-ink",
              ].join(" ")
            }
          >
            {({ isActive }) => (
              <>
                {isActive && (
                  <span className="absolute left-0 h-4.5 w-0.75 rounded-r-[3px] bg-primary" />
                )}
                <Icon name={item.icon} size={NAV_ICON_SIZE} />
                {t(item.labelKey)}
              </>
            )}
          </NavLink>
        ))}
      </nav>

      <div className="flex items-center gap-2.5 border-t border-line px-3.5 py-2.5">
        {email ? (
          <>
            <Avatar name={email.split("@")[0] ?? email} tone="violet" />
            <div className="min-w-0 flex-1">
              <div className="truncate text-[12.5px] font-semibold text-ink">
                {email}
              </div>
              <div className="font-mono text-[10.5px] text-ink-soft">
                {t("nav.admin")}
              </div>
            </div>
            <button
              type="button"
              onClick={onLogout}
              disabled={loggingOut}
              aria-label={t("nav.logOut")}
              className="text-ink-soft transition-colors hover:text-ink disabled:opacity-50"
            >
              <Icon name="logout" size={NAV_ICON_SIZE} />
            </button>
          </>
        ) : (
          <NavLink
            to="/login"
            className="flex items-center gap-2 text-[13px] font-semibold text-ink-soft hover:text-ink"
          >
            <Icon name="personal" size={NAV_ICON_SIZE} />
            {t("nav.logIn")}
          </NavLink>
        )}
      </div>
    </aside>
  );
}
