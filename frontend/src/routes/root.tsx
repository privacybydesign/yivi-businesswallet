import { useCallback, useEffect, useMemo, useState } from "react";
import { Outlet, useMatches } from "react-router";
import { useLogoutMutation, useMeQuery } from "../api/auth.queries";
import {
  useMyOrganizationsQuery,
  useOrganizationsQuery,
} from "../api/organization.queries";
import { useOrgThemeQuery } from "../api/theme.queries";
import { setStoredOrgSlug } from "../lib/active-org";
import { applyOrgTheme, cacheOrgTheme, clearOrgTheme } from "../lib/theme";
import { BrandProvider, MobileNavContext, Sidebar } from "../ui";
import * as React from "react";

export default function Root(): React.JSX.Element | null {
  const { data: me } = useMeQuery();
  const logout = useLogoutMutation();
  const matches = useMatches();

  // Mobile-only: the sidebar collapses into an off-canvas drawer below `lg`.
  const [navOpen, setNavOpen] = useState(false);
  const closeNav = useCallback(() => setNavOpen(false), []);
  const openNav = useCallback(() => setNavOpen(true), []);
  const mobileNav = useMemo(() => ({ openNav }), [openNav]);

  // Escape closes the open drawer.
  useEffect(() => {
    if (!navOpen) {
      return;
    }
    const onKeyDown = (event: KeyboardEvent): void => {
      if (event.key === "Escape") {
        setNavOpen(false);
      }
    };
    document.addEventListener("keydown", onKeyDown);
    return () => document.removeEventListener("keydown", onKeyDown);
  }, [navOpen]);

  const isPlatformAdmin = me?.isPlatformAdmin ?? false;

  const allOrgs = useOrganizationsQuery(me != null && isPlatformAdmin);
  const myOrgs = useMyOrganizationsQuery(me != null && !isPlatformAdmin);
  const orgsQuery = isPlatformAdmin ? allOrgs : myOrgs;
  const organizations = orgsQuery.data;

  const activeSlug = matches.find(
    (match) => (match.params as { orgSlug?: string }).orgSlug !== undefined,
  )?.params.orgSlug;

  // Remember the last org the user actually has access to, so a bare visit to
  // "/" can route them back here. Never memorize a slug they can't access.
  useEffect(() => {
    if (activeSlug && organizations?.some((org) => org.slug === activeSlug)) {
      setStoredOrgSlug(activeSlug);
    }
  }, [activeSlug, organizations]);

  // Apply the active org's branding at runtime, mapping its colours onto the
  // design tokens; fall back to the default look when no org is in the URL.
  const theme = useOrgThemeQuery(activeSlug ?? "");
  const orgTheme = theme.data;
  useEffect(() => {
    if (!activeSlug) {
      clearOrgTheme();
      return;
    }
    applyOrgTheme(orgTheme);
    // Cache the resolved palette so the next full reload of this org paints its
    // branding before React hydrates (see index.html), avoiding the FOUC.
    cacheOrgTheme(activeSlug, orgTheme);
  }, [activeSlug, orgTheme]);
  useEffect(() => clearOrgTheme, []);

  const activeOrg = organizations?.find((org) => org.slug === activeSlug);
  const brand = useMemo(
    () => ({ logoUri: orgTheme?.logoUri || undefined, name: activeOrg?.name }),
    [orgTheme?.logoUri, activeOrg?.name],
  );

  // ProtectedRoute guarantees an authenticated user before Root mounts; this
  // narrows the nullable query type instead of re-deriving it defensively.
  if (me == null) {
    return null;
  }

  return (
    <MobileNavContext.Provider value={mobileNav}>
      <BrandProvider value={brand}>
        <div className="flex min-h-screen">
          {navOpen && (
            <div
              className="fixed inset-0 z-40 bg-black/40 lg:hidden"
              aria-hidden="true"
              onClick={closeNav}
            />
          )}
          <Sidebar
            me={me}
            onLogout={() => logout.mutate()}
            loggingOut={logout.isPending}
            organizations={organizations ?? []}
            organizationsPending={orgsQuery.isPending}
            brandLogoUri={orgTheme?.logoUri || undefined}
            brandName={activeOrg?.name}
            open={navOpen}
            onNavigate={closeNav}
          />
          <main className="min-w-0 flex-1">
            <Outlet />
          </main>
        </div>
      </BrandProvider>
    </MobileNavContext.Provider>
  );
}
