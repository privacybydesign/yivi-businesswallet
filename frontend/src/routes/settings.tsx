import { useState } from "react";
import { useParams } from "react-router";
import { useTranslation } from "react-i18next";
import { useOrganizationQuery } from "../api/organization.queries";
import { accessMessage } from "../lib/access-message";
import { Button, Card, TopBar } from "../ui";
import { DepartmentSettings } from "./department-settings";
import * as React from "react";

const TABS = [
  { key: "org", labelKey: "settings.tabOrg" },
  { key: "wallets", labelKey: "settings.tabWallets" },
] as const;

const EYEBROW =
  "text-muted font-mono text-[11px] font-medium tracking-[0.06em] uppercase";
const CONTROL =
  "rounded-yivi border-line-strong bg-surface text-ink h-9 w-full border px-3 text-[13.5px] outline-none transition-colors focus:border-ink focus:ring-ink/10 focus:ring-3";

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
            <Card className="max-w-2xl p-7">
              <h2 className="text-[16px] font-semibold">
                {t("settings.orgProfile")}
              </h2>
              <div className="mt-4 grid grid-cols-[180px_1fr] items-center gap-x-5 gap-y-3.5">
                <span className={EYEBROW}>{t("settings.name")}</span>
                <input className={CONTROL} defaultValue={org.data.name} />
                <span className={EYEBROW}>{t("common.slug")}</span>
                <input
                  className={`${CONTROL} font-mono`}
                  defaultValue={org.data.slug}
                  readOnly
                />
              </div>
              <div className="mt-5 flex gap-2">
                <Button>{t("settings.save")}</Button>
                <Button variant="ghost">{t("settings.discard")}</Button>
              </div>
            </Card>

            <DepartmentSettings slug={slug} />
          </div>
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
