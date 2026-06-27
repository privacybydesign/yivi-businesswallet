import { NavLink, useMatches } from "react-router";
import { useTranslation } from "react-i18next";
import type { IconName } from "./icon";
import type { Me } from "../api/auth";
import type { Organization } from "../api/organization";
import { useOrganizationQuery } from "../api/organization.queries";
import { personInitials, shortName } from "../lib/name";
import { Icon } from "./icon";
import { Avatar } from "./avatar";
import { Logo } from "./logo";
import { OrgSwitcher } from "./org-switcher";
import * as React from "react";

type NavLabelKey =
  | "nav.dashboard"
  | "nav.members"
  | "nav.settings"
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
    { to: `/${slug}/settings`, labelKey: "nav.settings", icon: "settings" },
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
  me: Me;
  onLogout: () => void;
  loggingOut: boolean;
  organizations: Organization[];
  organizationsPending: boolean;
}

export function Sidebar({
  me,
  onLogout,
  loggingOut,
  organizations,
  organizationsPending,
}: SidebarProps): React.JSX.Element {
  const { t } = useTranslation();
  const matches = useMatches();

  // The org slug (if any) is on a descendant route match, not on this layout.
  const activeSlug = matches.find(
    (match) => (match.params as { orgSlug?: string }).orgSlug !== undefined,
  )?.params.orgSlug;
  const navItems = activeSlug ? orgNavItems(activeSlug) : ADMIN_NAV_ITEMS;

  // Platform admins outrank any single org; otherwise show the membership role
  // for the org currently in the URL.
  const activeOrg = useOrganizationQuery(activeSlug ?? "");
  const roleLabel = me.isPlatformAdmin
    ? t("nav.platformAdmin")
    : activeOrg.data?.role;

  return (
    <aside className="border-line bg-surface sticky top-0 flex h-screen w-58 shrink-0 flex-col border-r">
      <div className="border-line border-b px-4 pt-4.5 pb-3.5">
        <Logo />
      </div>

      <OrgSwitcher
        organizations={organizations}
        isPending={organizationsPending}
        isPlatformAdmin={me.isPlatformAdmin}
      />

      <nav className="flex-1 overflow-y-auto px-2.5 py-1.5">
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
        <Avatar initials={personInitials(me)} />
        <div className="min-w-0 flex-1">
          <div className="text-ink truncate text-[12.5px] font-semibold">
            {shortName(me)}
          </div>
          <div className="text-ink-soft text-[10.5px] capitalize">
            {roleLabel}
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
      </div>
    </aside>
  );
}
