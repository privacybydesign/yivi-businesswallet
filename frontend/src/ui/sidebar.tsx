import { NavLink, useMatches } from "react-router";
import { useTranslation } from "react-i18next";
import type { IconName } from "./icon";
import type { Me } from "../api/auth";
import type { Organization } from "../api/organization";
import { useOrganizationQuery } from "../api/organization.queries";
import { personInitials, fullName } from "../lib/name";
import { Icon } from "./icon";
import { Avatar } from "./avatar";
import { Logo } from "./logo";
import { LanguageSwitcher } from "./language-switcher";
import { OrgSwitcher } from "./org-switcher";
import * as React from "react";

type NavLabelKey =
  | "nav.dashboard"
  | "nav.members"
  | "nav.qerds"
  | "nav.attestations"
  | "nav.postguard"
  | "nav.auditLog"
  | "nav.settings"
  | "nav.adminDashboard"
  | "nav.allOrganizations"
  | "nav.identityReviews";

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
    { to: `/${slug}/qerds`, labelKey: "nav.qerds", icon: "email" },
    {
      to: `/${slug}/attestations`,
      labelKey: "nav.attestations",
      icon: "valid",
    },
    { to: `/${slug}/postguard`, labelKey: "nav.postguard", icon: "lock" },
    { to: `/${slug}/audit-log`, labelKey: "nav.auditLog", icon: "time" },
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
  {
    to: "/admin/identity-reviews",
    labelKey: "nav.identityReviews",
    icon: "warning",
  },
];

const NAV_ICON_SIZE = 16;

interface SidebarProps {
  me: Me;
  onLogout: () => void;
  loggingOut: boolean;
  organizations: Organization[];
  organizationsPending: boolean;
  // Whether the mobile drawer is open. Ignored at `lg` and up, where the
  // sidebar is always visible.
  open: boolean;
  // Called when a nav link is followed, so Root can close the mobile drawer.
  onNavigate: () => void;
  // The active org's custom logo, shown in place of the Yivi wordmark.
  brandLogoUri?: string;
  brandName?: string;
}

export function Sidebar({
  me,
  onLogout,
  loggingOut,
  organizations,
  organizationsPending,
  open,
  onNavigate,
  brandLogoUri,
  brandName,
}: SidebarProps): React.JSX.Element {
  const { t } = useTranslation();
  const matches = useMatches();

  // The org slug (if any) is on a descendant route match, not on this layout.
  const activeSlug = matches.find(
    (match) => (match.params as { orgSlug?: string }).orgSlug !== undefined,
  )?.params.orgSlug;
  const navItems = activeSlug
    ? orgNavItems(activeSlug)
    : me.isPlatformAdmin
      ? ADMIN_NAV_ITEMS
      : [];

  // Platform admins outrank any single org; otherwise show the membership role
  // for the org currently in the URL.
  const activeOrg = useOrganizationQuery(activeSlug ?? "");
  const roleLabel = me.isPlatformAdmin
    ? t("nav.platformAdmin")
    : activeOrg.data?.role;

  return (
    <aside
      className={[
        // Off-canvas drawer on small screens; a static column from `lg` up.
        "border-line bg-surface fixed inset-y-0 left-0 z-50 flex h-screen w-58 shrink-0 flex-col border-r transition-transform duration-200",
        open ? "translate-x-0" : "invisible -translate-x-full",
        "lg:visible lg:sticky lg:top-0 lg:z-auto lg:translate-x-0",
      ].join(" ")}
    >
      <div className="border-line border-b px-4 pt-4.5 pb-3.5">
        <Logo src={brandLogoUri} alt={brandName} />
      </div>

      <OrgSwitcher
        organizations={organizations}
        isPending={organizationsPending}
        isPlatformAdmin={me.isPlatformAdmin}
        onNavigate={onNavigate}
      />

      <nav className="flex-1 overflow-y-auto px-2.5 py-1.5">
        {navItems.map((item) => (
          <NavLink
            key={item.to}
            to={item.to}
            end={item.end}
            onClick={onNavigate}
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

      <div className="border-line flex justify-end border-t px-3.5 py-2.5">
        <LanguageSwitcher />
      </div>

      <div className="border-line flex items-center gap-2.5 border-t px-3.5 py-2.5">
        <Avatar initials={personInitials(me)} />
        <div className="min-w-0 flex-1">
          <div className="text-ink truncate text-[12.5px] font-semibold">
            {fullName(me)}
          </div>
          <div className="text-ink-soft text-[10.5px] capitalize">
            {roleLabel}
          </div>
        </div>
        <button
          type="button"
          onClick={onLogout}
          disabled={loggingOut}
          aria-label={t("common.logOut")}
          className="text-ink-soft hover:text-ink transition-colors disabled:opacity-50"
        >
          <Icon name="logout" size={NAV_ICON_SIZE} />
        </button>
      </div>
    </aside>
  );
}
