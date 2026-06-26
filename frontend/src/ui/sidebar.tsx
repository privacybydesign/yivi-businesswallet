import { NavLink, useMatches } from "react-router";
import { useTranslation } from "react-i18next";
import type { IconName } from "./icon";
import type { Organization } from "../api/organization";
import { Icon } from "./icon";
import { Avatar } from "./avatar";
import { Logo } from "./logo";
import { OrgSwitcher } from "./org-switcher";
import * as React from "react";

type NavLabelKey =
  | "nav.dashboard"
  | "nav.members"
  | "nav.adminDashboard"
  | "nav.allOrganizations";

interface NavItem {
  to: string;
  labelKey: NavLabelKey;
  icon: IconName;
  end?: boolean;
}

function orgNavItems(slug: string): NavItem[] {
  return [
    { to: `/${slug}`, labelKey: "nav.dashboard", icon: "view", end: true },
    { to: `/${slug}/members`, labelKey: "nav.members", icon: "personal" },
  ];
}

const ADMIN_NAV_ITEMS: NavItem[] = [
  { to: "/admin", labelKey: "nav.adminDashboard", icon: "view", end: true },
  {
    to: "/admin/organizations",
    labelKey: "nav.allOrganizations",
    icon: "personal",
  },
];

const NAV_ICON_SIZE = 16;

interface SidebarProps {
  email: string | null;
  onLogout: () => void;
  loggingOut: boolean;
  organizations: Organization[];
  organizationsPending: boolean;
  isPlatformAdmin: boolean;
}

export function Sidebar({
  email,
  onLogout,
  loggingOut,
  organizations,
  organizationsPending,
  isPlatformAdmin,
}: SidebarProps): React.JSX.Element {
  const { t } = useTranslation();
  const matches = useMatches();

  // The org slug (if any) is on a descendant route match, not on this layout.
  const activeSlug = matches.find(
    (match) => (match.params as { orgSlug?: string }).orgSlug !== undefined,
  )?.params.orgSlug;
  const navItems = activeSlug ? orgNavItems(activeSlug) : ADMIN_NAV_ITEMS;

  return (
    <aside className="border-line bg-surface sticky top-0 flex h-screen w-58 shrink-0 flex-col border-r">
      <div className="border-line border-b px-4 pt-4.5 pb-3.5">
        <Logo />
      </div>

      {email && (
        <OrgSwitcher
          organizations={organizations}
          isPending={organizationsPending}
          isPlatformAdmin={isPlatformAdmin}
        />
      )}

      <nav className="flex-1 overflow-y-auto px-2.5 py-2">
        {navItems.map((item) => (
          <NavLink
            key={item.to}
            to={item.to}
            end={item.end}
            className={({ isActive }) =>
              [
                "relative flex h-8.5 items-center gap-2.5 rounded-md px-2.5 text-[13.5px] transition-colors",
                isActive
                  ? "bg-surface-3 text-ink font-semibold"
                  : "text-ink-soft hover:bg-surface-3 hover:text-ink font-medium",
              ].join(" ")
            }
          >
            {({ isActive }) => (
              <>
                {isActive && (
                  <span className="bg-primary absolute left-0 h-4.5 w-0.75 rounded-r-[3px]" />
                )}
                <Icon name={item.icon} size={NAV_ICON_SIZE} />
                {t(item.labelKey)}
              </>
            )}
          </NavLink>
        ))}
      </nav>

      <div className="border-line flex items-center gap-2.5 border-t px-3.5 py-2.5">
        {email ? (
          <>
            <Avatar name={email.split("@")[0] ?? email} tone="violet" />
            <div className="min-w-0 flex-1">
              <div className="text-ink truncate text-[12.5px] font-semibold">
                {email}
              </div>
              <div className="text-ink-soft font-mono text-[10.5px]">
                {t("nav.admin")}
              </div>
            </div>
            <button
              type="button"
              onClick={onLogout}
              disabled={loggingOut}
              aria-label={t("nav.logOut")}
              className="text-ink-soft hover:text-ink transition-colors disabled:opacity-50"
            >
              <Icon name="logout" size={NAV_ICON_SIZE} />
            </button>
          </>
        ) : (
          <NavLink
            to="/login"
            className="text-ink-soft hover:text-ink flex items-center gap-2 text-[13px] font-semibold"
          >
            <Icon name="personal" size={NAV_ICON_SIZE} />
            {t("nav.logIn")}
          </NavLink>
        )}
      </div>
    </aside>
  );
}
