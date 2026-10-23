import { useParams, useSearchParams } from "react-router";
import { useTranslation } from "react-i18next";
import { useOrganizationQuery } from "../api/organization.queries";
import { accessMessage } from "../lib/access-message";
import {
  COMMUNICATION_SECTIONS,
  COMMUNICATION_TAB,
  SETTINGS_TABS,
  resolveSettingsLocation,
  settingsTabParam,
} from "../lib/settings-tabs";
import type { SettingsLocation, SettingsTab } from "../lib/settings-tabs";
import type { OrganizationDetail } from "../api/organization";
import { Card, TopBar } from "../ui";
import { EmailSettingsPanel } from "./email-settings";
import { EmailTemplatesPanel } from "./email-templates";
import { IdentitySettingsPanel } from "./identity-settings";
import { ScreeningSettingsPanel } from "./screening-settings";
import { IssuerSettingsPanel } from "./issuer-settings";
import { MandateSettings } from "./mandate-settings";
import { NotificationsSettingsPanel } from "./notifications-settings";
import { CscSettingsPanel } from "./csc-settings";
import { OrgProfileSettings } from "./org-profile-settings";
import { PostguardApiKeyCard } from "./postguard-api-key";
import { PostguardEncryptionKeyCard } from "./postguard-encryption-key";
import { PostguardNotificationsCard } from "./postguard-notifications";
import { BrandingPanel } from "./proofing-branding";
import { PrivacySettingsPanel } from "./settings-privacy";
import { ProvisioningSettingsPanel } from "./provisioning-settings";
import { SlackSettingsPanel } from "./slack-settings";
import { TeamsSettingsPanel } from "./teams-settings";
import { ThemeSettingsPanel } from "./theme-settings";
import { WscaWalletPanel } from "./wsca-wallet-settings";
import * as React from "react";

export default function Settings(): React.JSX.Element {
  const { t } = useTranslation();
  const { orgSlug } = useParams();
  // Guaranteed by the ":orgSlug" route segment this component mounts under.
  const slug = orgSlug!;
  const org = useOrganizationQuery(slug);
  const isAdmin = org.data?.role === "admin";
  const [searchParams, setSearchParams] = useSearchParams();
  // Both nav levels are addressable via ?tab=<key> so links can deep-link to a
  // specific panel (e.g. ?tab=wallets for WSCA activation, ?tab=slack for the
  // Slack panel under Communication); see lib/settings-tabs.ts.
  const { tab, section } = resolveSettingsLocation(searchParams.get("tab"));

  const goTo = (next: SettingsLocation): void => {
    setSearchParams(
      (prev) => {
        const params = new URLSearchParams(prev);
        const value = settingsTabParam(next);
        if (value === null) params.delete("tab");
        else params.set("tab", value);
        return params;
      },
      { replace: true },
    );
  };

  return (
    <>
      <TopBar title={t("settings.title")} subtitle={t("settings.subtitle")} />

      <div className="border-line bg-surface flex gap-1 overflow-x-auto border-b px-4 sm:px-8">
        {SETTINGS_TABS.map((item) => {
          const active = tab === item.key;
          return (
            <button
              key={item.key}
              type="button"
              onClick={() => goTo({ tab: item.key, section })}
              className={[
                "h-11 shrink-0 border-b-2 px-3.5 text-[13.5px] whitespace-nowrap transition-colors",
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

      <div className="p-4 sm:p-8">
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
        ) : tab === COMMUNICATION_TAB ? (
          <div className="flex flex-col gap-6">
            <div
              role="group"
              aria-label={t("settings.tabCommunication")}
              className="bg-surface-3 rounded-yivi inline-flex w-fit flex-wrap gap-1 p-[3px]"
            >
              {COMMUNICATION_SECTIONS.map((item) => (
                <button
                  key={item.key}
                  type="button"
                  onClick={() =>
                    goTo({ tab: COMMUNICATION_TAB, section: item.key })
                  }
                  aria-pressed={section === item.key}
                  className={[
                    "h-[26px] cursor-pointer rounded-md px-2.5 text-[12.5px] font-semibold transition-colors",
                    section === item.key
                      ? "bg-surface text-ink shadow-sm"
                      : "text-ink-soft hover:text-ink",
                  ].join(" ")}
                >
                  {t(item.labelKey)}
                </button>
              ))}
            </div>
            {section === "notifications" ? (
              <NotificationsSettingsPanel slug={slug} />
            ) : section === "email" ? (
              <EmailSettingsPanel slug={slug} />
            ) : section === "mailTemplates" ? (
              <EmailTemplatesPanel slug={slug} />
            ) : section === "slack" ? (
              <SlackSettingsPanel slug={slug} />
            ) : section === "msteams" ? (
              <TeamsSettingsPanel slug={slug} />
            ) : null}
          </div>
        ) : tab === "postguard" ? (
          <div className="flex max-w-2xl flex-col gap-6">
            <PostguardEncryptionKeyCard slug={slug} isAdmin={isAdmin} />
            <PostguardApiKeyCard slug={slug} isAdmin={isAdmin} />
            <PostguardNotificationsCard slug={slug} isAdmin={isAdmin} />
          </div>
        ) : (
          <SettingsPanel tab={tab} slug={slug} org={org.data} />
        )}
      </div>
    </>
  );
}

// The panel of a settings tab that holds one panel, as an admin sees it.
function SettingsPanel({
  tab,
  slug,
  org,
}: {
  tab: SettingsTab;
  slug: string;
  org: OrganizationDetail;
}): React.JSX.Element {
  switch (tab) {
    case "org":
      return <OrgProfileSettings org={org} />;
    case "branding":
      return <BrandingPanel slug={slug} orgName={org.name} />;
    case "privacy":
      return <PrivacySettingsPanel slug={slug} />;
    case "mandates":
      return <MandateSettings slug={slug} />;
    case "identity":
      return <IdentitySettingsPanel slug={slug} />;
    case "screening":
      return <ScreeningSettingsPanel slug={slug} />;
    case "theme":
      return <ThemeSettingsPanel slug={slug} />;
    case "issuer":
      return <IssuerSettingsPanel slug={slug} />;
    case "provisioning":
      return <ProvisioningSettingsPanel slug={slug} />;
    case "signing":
      return <CscSettingsPanel slug={slug} />;
    default:
      return <WscaWalletPanel slug={slug} />;
  }
}
