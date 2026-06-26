import { useEffect } from "react";
import { Outlet, useMatches } from "react-router";
import { useLogoutMutation, useMeQuery } from "../api/auth.queries";
import {
  useMyOrganizationsQuery,
  useOrganizationsQuery,
} from "../api/organization.queries";
import { setStoredOrgSlug } from "../lib/active-org";
import { Sidebar } from "../ui";
import * as React from "react";

export default function Root(): React.JSX.Element {
  const { data: me, isPending } = useMeQuery();
  const logout = useLogoutMutation();
  const matches = useMatches();

  const email = !isPending && me != null ? me.email : null;
  const isPlatformAdmin = me?.isPlatformAdmin ?? false;

  const allOrgs = useOrganizationsQuery(email != null && isPlatformAdmin);
  const myOrgs = useMyOrganizationsQuery(email != null && !isPlatformAdmin);
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

  return (
    <div className="flex min-h-screen">
      <Sidebar
        email={email}
        onLogout={() => logout.mutate()}
        loggingOut={logout.isPending}
        organizations={organizations ?? []}
        organizationsPending={orgsQuery.isPending}
        isPlatformAdmin={isPlatformAdmin}
      />
      <main className="min-w-0 flex-1">
        <Outlet />
      </main>
    </div>
  );
}
