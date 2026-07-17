import { useState } from "react";
import { useParams } from "react-router";
import { useTranslation } from "react-i18next";
import { useOrganizationQuery } from "../api/organization.queries";
import { accessMessage } from "../lib/access-message";
import { Card, TopBar } from "../ui";
import { DepartmentSettings } from "./department-settings";
import { EmailSettingsPanel } from "./email-settings";
import { IssuerSettingsPanel } from "./issuer-settings";
import { OrgProfileSettings } from "./org-profile-settings";
import * as React from "react";

const TABS = [
  { key: "org", labelKey: "settings.tabOrg" },
  { key: "email", labelKey: "settings.tabEmail" },
  { key: "issuer", labelKey: "settings.tabIssuer" },
  { key: "wallets", labelKey: "settings.tabWallets" },
] as const;

export default function Settings(): React.JSX.Element {
  const { t } = useTranslation();
  const { orgSlug } = useParams();
  // Guaranteed by the ":orgSlug" route segment this component mounts under.
  const slug = orgSlug!;
  const org = useOrganizationQuery(slug);
  const isAdmin = org.data?.role === "admin";
  const [tab, setTab] = useState("org");

  return (
    <>
      <TopBar title={t("settings.title")} subtitle={t("settings.subtitle")} />

      <div className="border-line bg-surface flex gap-1 border-b px-8">
        {TABS.map((item) => {
          const active = tab === item.key;
          return (
            <button
              key={item.key}
              type="button"
              onClick={() => setTab(item.key)}
              className={[
                "h-11 border-b-2 px-3.5 text-[13.5px] transition-colors",
                active
                  ? "border-primary text-ink font-semibold"
                  : "text-ink-soft hover:text-ink border-transparent font-medium",
              ].join(" ")}
            >
              {t(item.labelKey)}
            </button>
          );
        })}
      </div>

      <div className="p-8">
        {org.isError ? (
          <Card className="p-6">
            <p className="text-error text-[14px]">
              {accessMessage(org.error, t)}
            </p>
          </Card>
        ) : org.isPending ? (
          <Card className="p-6">
            <p className="text-ink-soft text-[14px]">{t("common.loading")}</p>
          </Card>
        ) : !isAdmin ? (
          <Card className="p-6">
            <p className="text-ink-soft text-[14px]">
              {t("settings.adminOnly")}
            </p>
          </Card>
        ) : tab === "org" ? (
          <div className="flex flex-col gap-6">
            <OrgProfileSettings org={org.data} />
            <DepartmentSettings slug={slug} />
          </div>
        ) : tab === "email" ? (
          <EmailSettingsPanel slug={slug} />
        ) : tab === "issuer" ? (
          <IssuerSettingsPanel slug={slug} />
        ) : (
          <Card className="p-6">
            <p className="text-ink-soft text-[14px]">
              {t("settings.walletsPlaceholder")}
            </p>
          </Card>
        )}
      </div>
    </>
  );
}
