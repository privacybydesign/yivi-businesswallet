import { Navigate } from "react-router";
import { useTranslation } from "react-i18next";
import { useLogoutMutation, useMeQuery } from "../api/auth.queries";
import { useMyOrganizationsQuery } from "../api/organization.queries";
import { getStoredOrgSlug } from "../lib/active-org";
import { Button, Card, Logo } from "../ui";
import * as React from "react";

/**
 * Decides where a bare visit to "/" lands. The active org lives in the URL, so
 * this only resolves an initial slug — platform admins go to the global admin
 * area; members go to their last-used (or first) org.
 */
export default function RootRedirect(): React.JSX.Element | null {
  const { t } = useTranslation();
  const { data: me, isPending: mePending } = useMeQuery();
  const isPlatformAdmin = me?.isPlatformAdmin ?? false;
  const myOrgs = useMyOrganizationsQuery(me != null && !isPlatformAdmin);
  const logout = useLogoutMutation();

  if (mePending) {
    return null;
  }
  if (me == null) {
    return <Navigate to="/login" replace />;
  }
  if (isPlatformAdmin) {
    return <Navigate to="/admin" replace />;
  }
  if (myOrgs.isPending) {
    return null;
  }

  const orgs = myOrgs.data ?? [];
  if (orgs.length === 0) {
    return (
      <div className="bg-surface-2 flex min-h-screen items-center justify-center p-6">
        <Card className="w-full max-w-md p-8 text-center">
          <div className="flex justify-center">
            <Logo />
          </div>
          <h1 className="mt-6 text-[20px] font-bold">{t("rootEmpty.title")}</h1>
          <p className="text-ink-soft mt-2 text-[14px]">
            {t("rootEmpty.body")}
          </p>
          <div className="mt-6 flex justify-center">
            <Button
              variant="secondary"
              icon="logout"
              onClick={() => logout.mutate()}
              disabled={logout.isPending}
            >
              {t("rootEmpty.logOut")}
            </Button>
          </div>
        </Card>
      </div>
    );
  }

  const stored = getStoredOrgSlug();
  const target = orgs.find((org) => org.slug === stored) ?? orgs[0];
  return <Navigate to={`/${target.slug}`} replace />;
}
